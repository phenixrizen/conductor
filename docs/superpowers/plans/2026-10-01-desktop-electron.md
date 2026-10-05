# Desktop app (Electron shell) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Conductor as a standalone desktop app on macOS and Linux: an Electron shell that bundles the `conductor` binary, runs `conductor serve` on a loopback port with a generated admin token, and opens the embedded workbench in a window with the token handed over internally.

**Architecture:** Nothing moves out of Go. The shell (`desktop/`) spawns the bundled `conductor serve` with `--listen 127.0.0.1:0`-style port discovery, a per-install data directory under Electron's `userData`, and the user's home as the allowed root; it loads `http://127.0.0.1:<port>/` in a `BrowserWindow`. A preload script exposes `window.conductorDesktop` (token, open-externally, version) over a context-isolated bridge; the SPA detects it and skips the token prompt. Packaging is `electron-builder` with the Go binary as an extra resource built by the Makefile per platform. Windows is a client-only build until the PTY layer has a ConPTY backend (`creack/pty` has no Windows support).

**Tech Stack:** Electron (latest LTS at planning time), TypeScript, electron-builder; Go 1.26 binary unchanged except for one flag; Nuxt 4 SPA unchanged except for a desktop bridge composable.

**Spec:** `docs/features.md` § "Future features" (a new entry "Desktop app" to be added by Task 7); this plan is the design. Base branch: whatever Round 3 lands on (written on 2026-10-01 after `round2-crews-events` b8545c9).

## Global Constraints

- No new Go dependency. The only Go change is a `--listen` value of `127.0.0.1:0` meaning "pick a free port" plus a `conductor serve --print-listen` line on stdout so the shell learns the port; everything else stays as it is.
- The admin token never touches disk in plain text on the shell side beyond the OS keychain or Electron `safeStorage`; it is generated per install, stored encrypted, passed to the server through the environment (`CONDUCTOR_ADMIN_TOKEN`), and handed to the renderer only through the preload bridge. It is never put in the URL.
- `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true` on every window; the renderer never gets Node.
- External links (share URLs, docs, the join page opened from a link) open in the system browser through the bridge; the window never navigates away from the local server origin (`will-navigate` and `setWindowOpenHandler` enforce it).
- Commands the shell runs are argv arrays (`child_process.spawn` with an args array, `shell: false`).
- The shell must not change what a browser user sees: every web change is behind `isDesktop()` and the web build still works without the bridge.
- Do not commit `desktop/node_modules`, `desktop/dist` or `desktop/out`; commit `desktop/package-lock.json`.

## Review Focus

1. A second launch while the app runs must focus the existing window, not start a second server on another port (Task 2 tests the single-instance lock).
2. Quitting while agents run must stop the server and its PTY children without leaving orphans (Task 2 tests SIGTERM then SIGKILL after a bounded wait).
3. A share link clicked inside the app must open in the system browser with the token-less join URL, never navigate the app window (Task 4 tests `will-navigate` and the handler).
4. A corrupted or missing encrypted token must regenerate a token and start cleanly, never prompt the user for a token (Task 3 tests the fallback).
5. A port already in use at the chosen address must be retried, not reported as a crash (Task 1 tests `127.0.0.1:0` discovery).

---

### Task 1: Port discovery in the server

**Files:**
- Modify: `internal/cli/serve.go`, `internal/cli/serve_test.go`, `README.md` (one sentence under Configuration), `docs/protocol.md` (no change; HTTP API unaffected)

**Interfaces:**
- `conductor serve --listen 127.0.0.1:0 --print-listen` prints exactly one line `listen 127.0.0.1:<port>` to stdout once the listener is bound, then logs as usual. Without `--print-listen` the behaviour is unchanged. A `:0` port works with or without the flag.

- [ ] **Step 1: Failing test**

```go
func TestServePrintsTheBoundAddress(t *testing.T) {
	stdout := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, []string{"--listen", "127.0.0.1:0", "--print-listen", "--data-dir", t.TempDir()}, stdout, io.Discard)
	}()
	addr := waitForLine(t, stdout, "listen 127.0.0.1:")
	resp, err := http.Get("http://" + strings.TrimPrefix(addr, "listen ") + "/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health on %s: %v %v", addr, err, resp)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
```
`waitForLine` polls the buffer under a 5 s deadline. If `runServe` does not take `stdout`, adjust to the existing signature in `serve.go` (it takes a writer for the banner; check `serve_test.go` for the helper used by `TestServeWarns…`). Run: `go test -run TestServePrintsTheBoundAddress ./internal/cli/` → FAIL (unknown flag).

- [ ] **Step 2: Implement.** In `serve.go` add `printListen := fs.Bool("print-listen", false, "print the bound listen address on stdout once listening")`. Bind with `net.Listen("tcp", cfg.Listen)` before `http.Serve` (the server already does this or uses `ListenAndServe`; switch to an explicit listener), then if `*printListen` write `fmt.Fprintf(stdout, "listen %s\n", ln.Addr())`. Keep the existing `conductor serving` log line and make it use `ln.Addr()` too so `:0` logs the real port.

- [ ] **Step 3: Run** `go test -race -count=1 ./internal/cli/` → PASS. README: "`--listen 127.0.0.1:0` picks a free port; `--print-listen` prints it." Commit `serve: --print-listen and a free port with :0`.

---

### Task 2: The shell: process, window, lifecycle

**Files:**
- Create: `desktop/package.json`, `desktop/tsconfig.json`, `desktop/src/main.ts`, `desktop/src/server.ts`, `desktop/src/window.ts`, `desktop/src/preload.ts`, `desktop/test/server.test.ts`, `desktop/.gitignore`
- Modify: `.gitignore` (`desktop/node_modules`, `desktop/dist`, `desktop/out`), `Makefile` (`desktop-install`, `desktop-dev`)

**Interfaces:**
```ts
// server.ts
export interface ServerHandle { port: number; token: string; stop(): Promise<void> }
export async function startServer(opts: { binary: string; dataDir: string; home: string; token: string; log: (line: string) => void }): Promise<ServerHandle>
// spawns [binary, 'serve', '--listen', '127.0.0.1:0', '--print-listen'] with env CONDUCTOR_ADMIN_TOKEN, CONDUCTOR_DATA_DIR, CONDUCTOR_ALLOWED_ROOTS=home, CONDUCTOR_DEFAULT_CWD=home;
// resolves on the `listen ` line (10 s timeout → rejects); stop() sends SIGTERM, waits 5 s, then SIGKILL.
// preload.ts exposes window.conductorDesktop = { token: () => Promise<string>, openExternal: (url: string) => void, version: string }
```

- [ ] **Step 1: Failing test** (`desktop/test/server.test.ts`, vitest, with a fake binary script written to a temp dir that prints `listen 127.0.0.1:43210` and sleeps):

```ts
it('starts the server and learns the port', async () => {
  const h = await startServer({ binary: fakeBinary(), dataDir: tmp(), home: tmp(), token: 't', log: () => {} })
  expect(h.port).toBe(43210)
  await h.stop()
})
it('rejects when the binary never prints the address', async () => {
  await expect(startServer({ binary: fakeBinary({ silent: true }), dataDir: tmp(), home: tmp(), token: 't', log: () => {}, timeoutMs: 200 })).rejects.toThrow(/listen/)
})
it('stop kills a binary that ignores SIGTERM', async () => {
  const h = await startServer({ binary: fakeBinary({ ignoreTerm: true }), /* … */ })
  const t0 = Date.now(); await h.stop(); expect(Date.now() - t0).toBeLessThan(7000)
})
```
Run: `npm --prefix desktop test` → FAIL (module missing).

- [ ] **Step 2: Implement** `server.ts` as specified (`spawn(binary, args, { env, stdio: ['ignore', 'pipe', 'pipe'], shell: false })`, line-buffer stdout, forward stderr to `log`). `main.ts`: `app.requestSingleInstanceLock()` (quit if false; on `second-instance` focus the window), `app.whenReady` → token (Task 3) → `startServer` → `createWindow(port)` → on `before-quit` await `stop()`. `window.ts`: `new BrowserWindow({ webPreferences: { preload, contextIsolation: true, nodeIntegration: false, sandbox: true } })`, `loadURL('http://127.0.0.1:' + port + '/')`, `will-navigate` and `setWindowOpenHandler` deny anything not on that origin and call `shell.openExternal` instead. `preload.ts` with `contextBridge.exposeInMainWorld('conductorDesktop', …)` and `ipcRenderer.invoke('token')`.

- [ ] **Step 3: Run** vitest → PASS; `npm --prefix desktop run build` compiles. Commit `desktop: electron shell that runs the bundled server`.

---

### Task 3: Token generation and storage

**Files:**
- Create: `desktop/src/token.ts`, `desktop/test/token.test.ts`
- Modify: `desktop/src/main.ts`

**Interfaces:** `loadOrCreateToken(file: string, safe: { isEncryptionAvailable(): boolean; encryptString(s: string): Buffer; decryptString(b: Buffer): string }): Promise<string>` — 32 random bytes hex; stored with Electron `safeStorage` when available, else refused (the app shows an error dialog: no OS keychain); a missing, unreadable or undecryptable file regenerates the token and overwrites the file (an old server token is useless once the server restarts, so regeneration is safe); file mode 0600.

- [ ] **Step 1: Failing tests:** creates a 64-hex token and writes an encrypted file; returns the same token on the second call; a corrupted file yields a new token without throwing; `isEncryptionAvailable() === false` rejects with a clear message. Run → FAIL.
- [ ] **Step 2: Implement.** **Step 3:** PASS; wire into `main.ts`; commit `desktop: per-install admin token in safeStorage`.

---

### Task 4: The desktop bridge in the web app

**Files:**
- Create: `web/app/composables/useDesktop.ts`, `web/app/utils/desktop.ts` (+test)
- Modify: `web/app/composables/useAdminToken.ts`, `web/app/composables/useApi.ts` (if it reads the token), `web/app/components/ShareLinksModal.vue`, `web/app/pages/join/[token].vue`, `web/app/components/FileBrowser.vue` and every `window.open`/`target="_blank"` site (9 today) to go through `openExternal`, `web/app/layouts/default.vue` (hide the token entry and the server-URL hints when desktop)

**Interfaces:**
```ts
// utils/desktop.ts
export interface DesktopBridge { token(): Promise<string>; openExternal(url: string): void; version: string }
export function desktopBridge(w: Window = window): DesktopBridge | null   // window.conductorDesktop or null
export function openExternal(url: string, w?: Window): void              // bridge when present, else window.open(url, '_blank', 'noopener')
// composables/useDesktop.ts: isDesktop (computed), version
```
`useAdminToken` on desktop: on first use calls `bridge.token()` and sets it (never writes it to localStorage); `needsToken` stays false.

- [ ] **Step 1: Failing vitest** for `desktopBridge` (null without the global, the object with it) and `openExternal` (calls the bridge; falls back to `window.open` with `noopener`). Run → FAIL.
- [ ] **Step 2: Implement; replace the nine external-link sites; hide the token UI on desktop. Step 3:** typecheck, vitest → PASS; commit `web: desktop bridge for the token and external links`.

---

### Task 5: Packaging

**Files:**
- Create: `desktop/electron-builder.yml`, `desktop/build/icon.icns` and `icon.png` (from `scripts/brand_assets.py` output; add a target there if the sizes are missing), `desktop/README.md`
- Modify: `Makefile` (`desktop-binaries`: cross-build `bin/desktop/<os>-<arch>/conductor` for darwin/arm64, darwin/amd64, linux/amd64, linux/arm64 with `CGO_ENABLED=0 GOOS=… GOARCH=…`; `desktop-package`: runs `electron-builder` with `extraResources` pointing at the matching binary), `scripts/brand_assets.py` (icon sizes)

**Interfaces:** `electron-builder.yml` with `appId: dev.conductor.app`, `productName: Conductor`, `mac: { target: dmg, hardenedRuntime: true, entitlements for network client }`, `linux: { target: [AppImage, deb] }`, `extraResources: [{ from: ../bin/desktop/${os}-${arch}/conductor, to: conductor }]`; `main.ts` resolves the binary at `process.resourcesPath + '/conductor'` in production and `../bin/conductor` in dev.

- [ ] **Step 1:** `make desktop-binaries` builds all four binaries (test: `file bin/desktop/linux-amd64/conductor` says ELF; darwin ones Mach-O). **Step 2:** `make desktop-package` on Linux produces an AppImage that starts, prints the port in the shell log and serves `/api/health` (a smoke script under `desktop/test/smoke.sh`, skipped where Electron cannot run headless; `xvfb-run` when present). **Step 3:** commit `desktop: packaging for macOS and Linux`.

---

### Task 6: CI and release

**Files:**
- Modify: `.github/workflows/ci.yml` (a `desktop` job: `make desktop-binaries` on ubuntu, `npm --prefix desktop ci`, `npm --prefix desktop test`, `npm --prefix desktop run build`; packaging on a matrix of `macos-latest` and `ubuntu-latest` only on tags), `docs/architecture.md` (a "Desktop" paragraph)

- [ ] **Step 1:** the job runs green on a branch build without signing (unsigned artifacts uploaded as workflow artifacts). **Step 2:** document the secrets needed for signing and notarization (`CSC_LINK`, `CSC_KEY_PASSWORD`, `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID`) without adding them. Commit `ci: desktop build and tagged packaging`.

---

### Task 7: Docs and features

- `README.md`: a "Desktop app" section (what it bundles, where data lives (`userData/conductor.d`), that the allowed root is the home directory, how to point it at a remote server later, Windows status).
- `docs/features.md`: "Desktop app" moves from this plan into Delivered with the date; Windows (ConPTY backend) and auto-update stay under Future features.
- Commit `docs: desktop app`.

---

## Verification

1. `make desktop-binaries && make desktop-package` on Linux; run the AppImage: the workbench opens with no token prompt, launching a `shell` agent works, a share link opens in the system browser, quitting leaves no `conductor` process.
2. Second launch while running focuses the existing window.
3. Delete the token file while the app runs, relaunch: a new token, no prompt.
4. The web build without the bridge still shows the token prompt (`make web-build` and open in a browser).
