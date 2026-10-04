# Conductor desktop

The desktop app is an Electron shell around the `conductor` server: it
starts `conductor serve` on a free loopback port with an admin token minted
for the run, opens the embedded workbench in a window with that token handed
over through a context-isolated bridge, and stops the server when it quits.
Nothing moves out of Go: the shell only runs the binary, keeps its settings,
and shows its log.

## Layout

| Path | Responsibility |
|---|---|
| `src/main.ts` | the app's lifecycle: settings, the launcher, the supervisor, the window, tray and menu, quit |
| `src/server.ts` | `ServerSupervisor`: spawn, the handshake line, health polling, restart with backoff, stop by closing stdin |
| `src/handshake.ts` | the `--print-listen` line, read out of chunked output past a shell's noise |
| `src/launcher.ts`, `src/launcher-wsl.ts`, `src/wsl.ts` | where the binary runs: on this machine, or inside a WSL 2 distribution on Windows |
| `src/env.ts`, `src/shellPath.ts` | the server's environment: the login shell's PATH, the settings as `CONDUCTOR_*`, the token |
| `src/settings.ts` | `userData/settings.json`: data directory, allowed roots, default directory, yolo, reach, close to tray, the WSL distribution |
| `src/install.ts` | the stable copy of the binary the server runs from (the hook assets name it) |
| `src/window.ts`, `src/ipc.ts`, `src/preload.ts` | the window's fences, the bridge the workbench calls (`window.conductorDesktop`), the sender checks |
| `src/logs.ts`, `static/log.html` | rotating logs under `userData/logs` and the log window |
| `static/setup.html` | the Windows first-run screen: what WSL is for, Install WSL, Try again |
| `test/` | vitest: every module above, the supervisor against a fake server script, WSL against a fake `wsl.exe` runner |

## Running it

```bash
make build-go          # the server the shell runs in development (bin/conductor)
make desktop-install   # npm ci
make desktop-dev       # electron . against bin/conductor (or CONDUCTOR_DESKTOP_BIN)
make desktop-test      # tsc --noEmit and vitest
```

Development uses the checkout's binary as it is; a packaged app copies the
bundled binary to a stable path (`~/.local/share/conductor/bin/conductor`,
`~/Library/Application Support/Conductor/bin/conductor`) once per version
and runs it from there, so an AppImage mounted somewhere new each start
still has hook assets that name a binary that exists.

## The token

The admin token is 32 random bytes minted when the server starts and lives
in the shell's memory, the server's environment and the handshake line: it
is never written to disk. The workbench reads it from the bridge, never from
localStorage. "Open in browser" opens the system browser on the server's
address with the token in the URL fragment, which never reaches the server;
the workbench takes it and drops it from the address bar.

## Windows

There is no Windows build of the server: the installer bundles the Linux
binary and the shell runs it inside the user's WSL 2 distribution
(`wsl.exe -d <distro> --exec sh -lc …` through the login shell, so the
agents' PATH is theirs), after copying it into the distribution's home.
Without WSL 2 the app shows the setup screen. WSL's default NAT mode is
left as it is (mirrored networking is never asked for: it changes WSL for
Docker and every other tool). For WebRTC the app runs a UDP forwarder on
Windows (`src/udp-forwarder.ts`): the server inside the distribution puts
every WebRTC connection on one UDP port (7877, `CONDUCTOR_ICE_UDP_PORT`)
and advertises the Windows LAN address, the forwarder listens on that port
on Windows and carries each remote peer to the distribution's address on a
socket of its own, so ICE sees one NAT, the router's. The installer adds the
inbound firewall rule for the port when it has the right; Settings offers it
again (an elevated `netsh`, fixed arguments) when it is missing. The person
configures no Windows networking by hand, ever: no port proxy, no
`.wslconfig` change. What else must cross Hyper-V's NAT (the TLS listener,
the router mapping) is the app's to forward in a later round; until then
sharing from WSL goes through a switchyard or a paste invite.

**Invites.** The app registers the `conductor:` URL scheme (electron-builder
`protocols`, `setAsDefaultProtocolClient`). An invite,
`conductor://<switchyard>/join/<token>`, reaches a running app through
`second-instance` (Windows, Linux) or `open-url` (macOS), or starts it; the
app opens its own workbench at `/join/<token>?server=https://<switchyard>`,
which signals to the switchyard from the app's page (`src/invite.ts` has
the parser, the same rules as the web's `utils/invite.ts`).

## What stays by hand

The packaged app on each platform (dmg, deb, rpm, AppImage, nsis) and the
WSL path on a real Windows machine: `docs/features.md` lists the checklist.
