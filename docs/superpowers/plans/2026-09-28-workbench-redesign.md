# Workbench Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Conductor workbench to match the "Conductor Mockups" design (workbench, wall, carousel, launch, share, join) and add the presence, structured-attention, activity and metadata plumbing the design needs. Runs/orchestration and keyboard turn-taking are out of scope.

**Architecture:** Phase A (Tasks 1–9) is client-only: new status tokens, a session-list sidebar that replaces the nav, a workbench page with an inspector, a wall with an answerable queue, a carousel film strip, and restyled dialogs. Quick replies reuse the existing terminal transports (a short-lived control connection), so no new HTTP routes. Phase B (Tasks 10–13) extends the wire protocol in the three places the repo rules require (`internal/proto`, `web/app/utils/protocol.ts`, `docs/protocol.md`): a viewer roster with names, attention `kind`/`options`, a per-session activity log with `lastAnswer`, and session metadata (branch, host user, per-link viewer counts). Everything that changes what a viewer sees lives in `internal/session.Local` so server and host behave the same.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/json`, `log/slog`), Nuxt 4.5.2 + @nuxt/ui 4.11.2 + Vue 3.5.43, xterm 6, vitest, vue-tsc.

**Spec:** `docs/features.md` (scope and decisions). Mockup reference: claude.ai/design project `57dbb4ce-bb1c-40be-aed4-96d5c57ce623`, file `Conductor Mockups.dc.html` (screens 1a workbench, 1b wall, 1d launch, 1e share, 1f join, 1g carousel; 1c runs is deferred).

## Global Constraints

- Protocol changes touch `internal/proto`, `web/app/utils/protocol.ts` and `docs/protocol.md` together. Every new message/field gets a size limit and a test.
- Stdlib first. The only new npm dependency allowed by this plan is `@fontsource-variable/jetbrains-mono` (bundled font, no runtime network). No new Go dependencies.
- Commands are argv arrays; never build a shell string from user input on the server. (The launch dialog renders a copyable command client-side from the catalog argv; that is display only.)
- Compare tokens with `share.Equal`; never log query strings.
- Theme is one system-wide dark/light setting. No per-route theme.
- No join preview: the join page never connects before the guest presses Join.
- Terracotta is the brand mark only. Needs-input is amber (`warning`), running is green (`success`), idle/exited is grey (`neutral`). Every status carries a text label, never colour alone.
- Keep per-connection bounds: names ≤ 40 chars, attention options ≤ 6 × 60 chars, activity ring ≤ 200 entries, roster ≤ MaxViewers.
- Do not commit `internal/web/dist` contents, `web/.nuxt`, `web/.output`. Commit `web/package-lock.json`.
- Run `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test` before claiming a task done. `make web-build` and `make build-go` at the end of each phase.

## Review Focus

1. **A view-role guest reaches a quick-reply control.** Expected: the reply box and option buttons are hidden or disabled for `view` roles; the server still rejects INPUT with `read_only`. Pinned in Task 6 (useQuickReply refuses without control) and Task 11 (QuickReplyBar hides buttons when `role !== 'control'`).
2. **Two people answer the same prompt.** Expected: the first input clears `needs_input`; the second client's buttons disable the moment the attention state changes, and a late click is a no-op. Pinned in Task 11 (QuickReplyBar disables when state leaves `needs_input`) and Task 12 (Local records exactly one `lastAnswer` per needs-input episode; test).
3. **A display name with control characters, emoji spam or 10 KB of text.** Expected: names are trimmed, control characters stripped, truncated to 40 runes, empty becomes "guest". Pinned in Task 10 (`session.CleanName` test).
4. **A Claude Code hook payload with an unknown `notification_type` or missing `tool_name`.** Expected: falls back to a free-text needs-input with no options; never panics. Pinned in Task 11 (`MapClaudeHook` tests).
5. **A hosted session whose host reconnects after the roster changed.** Expected: the roster is rebuilt from the host's Local on the next viewers broadcast; the server never keeps a stale name list. Pinned in Task 10 (roster is derived from `Hub` subscriptions on every broadcast; no separate cache).

---

## File Structure

**Go**

| File | Change |
|---|---|
| `internal/proto/control.go` | `Hello.Name`; `Viewers.List []ViewerInfo`; `Attention.Kind/Options`; new `CtlActivity` + `Activity` message |
| `internal/proto/hostmsg.go` | `ViewerJoin.LinkLabel`; `HostInfo.User`; `HostSession.Branch`; `HostAttentionMsg.Kind/Options` |
| `internal/session/hub.go` | `Subscription.Name/Since/lastInput`; `Hub.Roster()`; `Hub.CountByLink()` |
| `internal/session/local.go` | `AttachOptions` + `AttachWith`; roster broadcasts; `SetAttentionFull`; activity ring; `LastAnswer`; `LinkViewers` |
| `internal/session/attention.go` | `Attention.Kind/Options`; `CleanName`; option limits |
| `internal/session/activity.go` (new) | `ActivityEntry`, `activityRing` |
| `internal/session/info.go` | `Info.Branch/HostUser/LastAnswer`; `Driver.LinkViewers` |
| `internal/session/git.go` (new) | `GitBranch(dir) string` |
| `internal/signal/hosted.go` | `SetAttentionFull`; `LinkViewers`; register stores branch/host user |
| `internal/api/ws_viewer.go` | hello name → attach; link label lookup |
| `internal/api/attention.go` | accept `kind`/`options` |
| `internal/api/links.go` | `active` count per link; activity entries for create/revoke |
| `internal/api/sessions.go` | branch at launch |
| `internal/hostagent/agent.go`, `peer.go`, `localtty.go` | branch + user in register; link label on peers; named local terminal |
| `internal/notify/notify.go` | `Request.Kind/Options`; permission mapping |
| `docs/protocol.md` | rows for every change |

**Web**

| File | Change |
|---|---|
| `web/app/app.config.ts`, `assets/css/main.css`, `package.json` | status colour scales, JetBrains Mono |
| `web/app/utils/sessions.ts` (new, +test) | grouping, filtering, meta line, relative time |
| `web/app/utils/hostCommand.ts` (new, +test) | renders the `conductor host` command |
| `web/app/utils/protocol.ts` | mirrors of every proto change |
| `web/app/utils/transport/base.ts` | hello name, RTT measurement |
| `web/app/composables/useIdentity.ts` (new) | display name in localStorage |
| `web/app/composables/useQuickReply.ts` (new) | short-lived control connection to send input |
| `web/app/composables/useTerminalTransport.ts` | `name` in spec |
| `web/app/composables/useSessions.ts` | new fields on `SessionInfo`, `ShareLink.active` |
| `web/app/composables/useShortcuts.ts` | updated tables |
| `web/app/layouts/default.vue` | sidebar = session list + bottom nav |
| `web/app/components/SessionSidebar.vue` (new) | grouped list, filter, launch |
| `web/app/components/SessionAvatar.vue` (new) | agent initials chip |
| `web/app/components/ViewerAvatars.vue` (new) | stacked initials |
| `web/app/components/QuickReplyBar.vue` (new) | waiting bar with buttons / reply box |
| `web/app/components/SessionInspector.vue` (new) | People / Files / Activity tabs |
| `web/app/components/FileBrowser.vue` (new) | inline body extracted from `FileViewer.vue` |
| `web/app/components/FileViewer.vue` | thin slideover wrapper around `FileBrowser` |
| `web/app/components/WallQueue.vue` (new) | queue cards with reply |
| `web/app/components/CarouselStrip.vue` (new) | film strip |
| `web/app/components/AttentionBadge.vue`, `SessionTile.vue`, `TransportBadge.vue`, `SessionStatusBadge.vue` | colours, RTT |
| `web/app/components/LaunchSessionModal.vue`, `ShareLinksModal.vue` | redesign |
| `web/app/components/TerminalView.vue` | `sendInput`, roster/activity emits, font |
| `web/app/pages/index.vue` | redirect / empty state |
| `web/app/pages/sessions/[id].vue` | workbench |
| `web/app/pages/wall.vue`, `carousel.vue`, `join/[token].vue` | redesign |

---

## Phase A — client only

### Task 1: Status colour tokens and code font

**Files:**
- Modify: `web/app/assets/css/main.css`, `web/app/app.config.ts`, `web/package.json`, `web/app/components/AttentionBadge.vue`, `web/app/components/SessionTile.vue`, `web/app/utils/attention.ts` (favicon dot), `web/app/components/TerminalView.vue` (font), `web/app/layouts/default.vue` (wall badge colour)
- Test: `web/app/utils/attention.test.ts` (existing; favicon colour assertion)

**Interfaces:**
- Produces: Tailwind scales `signal-*` (amber) and `active-*` (green) and Nuxt UI aliases `warning: 'signal'`, `success: 'active'`. `AttentionBadge` uses `warning` for `needs_input`. CSS var `--font-mono` resolves to JetBrains Mono.

- [ ] **Step 1: Add the font dependency**

```bash
cd web && npm install @fontsource-variable/jetbrains-mono@5
```
Commit `web/package.json` and `web/package-lock.json`. Reason for the PR: bundled brand code font, no runtime network.

- [ ] **Step 2: Add scales and fonts to `main.css`**

Inside the existing `@theme static { ... }` block add:

```css
  /* Operational status: amber needs-input, green running. Never terracotta. */
  --color-signal-50: #fdf8ec;
  --color-signal-100: #f7e9c9;
  --color-signal-200: #f3dda6;
  --color-signal-300: #e8c475;
  --color-signal-400: #dba63e;
  --color-signal-500: #c98a1b;
  --color-signal-600: #a56f14;
  --color-signal-700: #805000;
  --color-signal-800: #6b4400;
  --color-signal-900: #5e3c00;
  --color-signal-950: #3a2500;
  --color-active-50: #eef7f1;
  --color-active-100: #e6efe7;
  --color-active-200: #c4dfcc;
  --color-active-300: #9bcaa9;
  --color-active-400: #6fae83;
  --color-active-500: #3f8f5f;
  --color-active-600: #31764d;
  --color-active-700: #245a3a;
  --color-active-800: #1e4a30;
  --color-active-900: #183b27;
  --color-active-950: #0e2418;
  --font-mono: 'JetBrains Mono Variable', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
```

At the top of `main.css`, after the `@import "@nuxt/ui";` line, add:

```css
@import "@fontsource-variable/jetbrains-mono";
```

- [ ] **Step 3: Alias the semantic colours in `app.config.ts`**

```ts
export default defineAppConfig({
  ui: {
    // Brand palette from docs/design/brand.md: forest for primary actions,
    // terracotta only as the restrained accent (never a status light).
    // Operational status uses its own scales: amber needs-input, green running.
    colors: {
      primary: 'forest',
      secondary: 'terracotta',
      neutral: 'zinc',
      warning: 'signal',
      success: 'active',
    },
  },
})
```

- [ ] **Step 4: Recolour the attention badge**

In `AttentionBadge.vue` replace the `color` computed and the chip:

```ts
const color = computed(() => (props.attention?.state === 'needs_input' ? 'warning' : props.attention?.state === 'done' ? 'success' : 'neutral'))
```
and `<UChip :show="attention?.state === 'needs_input'" color="warning" inset>`. Keep the pulse.

- [ ] **Step 5: Recolour the wall tile ring and the layout badge**

`SessionTile.vue`: `needsInput ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'`.
`layouts/default.vue`: the Wall badge `color: 'secondary'` becomes `color: 'warning'` (the whole nav is replaced in Task 3, but keep the build green now).

- [ ] **Step 6: Favicon dot becomes amber**

In `utils/attention.ts` `attentionFavicon`, change `ctx.fillStyle = '#d26b3f'` to `ctx.fillStyle = '#c98a1b'` and update the doc comment ("amber dot"). Add to `attention.test.ts`:

```ts
it('favicon alert uses the amber status colour, not terracotta', async () => {
  const src = await import('./attention')
  // Source-level guard: the drawing code must not reference the brand accent.
  const text = (await import('node:fs')).readFileSync(new URL('./attention.ts', import.meta.url), 'utf8')
  expect(text).toContain('#c98a1b')
  expect(text).not.toContain('#d26b3f')
  expect(typeof src.attentionFavicon).toBe('function')
})
```

- [ ] **Step 7: Terminal font**

In `TerminalView.vue` `onMounted`, set `fontFamily: '"JetBrains Mono Variable", ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace'` and, right after `term.open(host.value!)`:

```ts
  document.fonts?.ready.then(() => {
    if (!term) return
    if (props.fit === 'scale') scheduleScale()
    else scheduleResize()
  })
```

- [ ] **Step 8: Run checks and commit**

```bash
npm --prefix web test && npm --prefix web run typecheck
git add web/package.json web/package-lock.json web/app
git commit -m "web: amber/green status scales and bundled JetBrains Mono"
```

---

### Task 2: Session grouping and formatting utilities

**Files:**
- Create: `web/app/utils/sessions.ts`, `web/app/utils/sessions.test.ts`

**Interfaces:**
- Produces:
```ts
export type SessionGroupKey = 'needs' | 'running' | 'exited'
export interface SessionGroups { needs: SessionInfo[]; running: SessionInfo[]; exited: SessionInfo[] }
export function groupSessions(list: SessionInfo[]): SessionGroups
export function filterSessions(list: SessionInfo[], query: string): SessionInfo[]
export function agentInitials(agentId: string): string          // 'claude' → 'CC', 'codex' → 'CX', 'agy'|'antigravity' → 'AG', 'sh'|'bash'|'zsh' → '$_', else first two letters upper
export function sessionMeta(s: SessionInfo, now?: number): string // "~/src/api · hosted · 3 here" style
export function relativeTime(iso: string, now?: number): string  // "4m", "1h 4m", "38m ago" variants: relativeTime(iso, now, { suffix: true })
export function initials(name: string): string                   // 'Priya Shah' → 'PS'
```

- [ ] **Step 1: Write the failing tests**

```ts
import { describe, expect, it } from 'vitest'
import { agentInitials, filterSessions, groupSessions, initials, relativeTime, sessionMeta } from './sessions'
import type { SessionInfo } from '~/composables/useSessions'

function s(p: Partial<SessionInfo>): SessionInfo {
  return { id: 'x', name: 'n', kind: 'server', agentId: 'claude', command: ['claude'], cwd: '/srv', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: '2026-09-28T10:00:00Z', ...p }
}

describe('groupSessions', () => {
  it('puts needs_input first, then running, then ended, newest attention first', () => {
    const g = groupSessions([
      s({ id: 'a', status: 'running' }),
      s({ id: 'b', attention: { state: 'needs_input', since: '2026-09-28T10:05:00Z' } }),
      s({ id: 'c', status: 'exited' }),
      s({ id: 'd', attention: { state: 'needs_input', since: '2026-09-28T10:06:00Z' } }),
    ])
    expect(g.needs.map((x) => x.id)).toEqual(['d', 'b'])
    expect(g.running.map((x) => x.id)).toEqual(['a'])
    expect(g.exited.map((x) => x.id)).toEqual(['c'])
  })
  it('an ended session that still carries needs_input is exited, not needs', () => {
    const g = groupSessions([s({ id: 'a', status: 'stopped', attention: { state: 'needs_input' } })])
    expect(g.needs).toEqual([])
    expect(g.exited.length).toBe(1)
  })
})

describe('filterSessions', () => {
  const list = [s({ id: 'a', name: 'auth-refactor', cwd: '/home/jd/src/api', hostName: 'mac-jd' }), s({ id: 'b', name: 'flaky-e2e', cwd: '/srv/web' })]
  it('matches name, cwd, host and agent case-insensitively', () => {
    expect(filterSessions(list, 'AUTH').map((x) => x.id)).toEqual(['a'])
    expect(filterSessions(list, 'srv/web').map((x) => x.id)).toEqual(['b'])
    expect(filterSessions(list, 'mac-').map((x) => x.id)).toEqual(['a'])
    expect(filterSessions(list, 'claude').length).toBe(2)
  })
  it('empty query returns everything', () => {
    expect(filterSessions(list, '  ').length).toBe(2)
  })
})

describe('agentInitials', () => {
  it('maps known agents and falls back to two letters', () => {
    expect(agentInitials('claude')).toBe('CC')
    expect(agentInitials('claude-code')).toBe('CC')
    expect(agentInitials('codex')).toBe('CX')
    expect(agentInitials('agy')).toBe('AG')
    expect(agentInitials('bash')).toBe('$_')
    expect(agentInitials('aider')).toBe('AI')
    expect(agentInitials('')).toBe('?')
  })
})

describe('relativeTime', () => {
  const now = Date.parse('2026-09-28T12:00:00Z')
  it('formats short durations', () => {
    expect(relativeTime('2026-09-28T11:59:40Z', now)).toBe('20s')
    expect(relativeTime('2026-09-28T11:56:00Z', now)).toBe('4m')
    expect(relativeTime('2026-09-28T10:56:00Z', now)).toBe('1h 4m')
    expect(relativeTime('2026-09-26T10:56:00Z', now)).toBe('2d')
    expect(relativeTime('2026-09-28T11:22:00Z', now, { suffix: true })).toBe('38m ago')
  })
})

describe('sessionMeta', () => {
  it('shortens the home directory and names the host', () => {
    const now = Date.parse('2026-09-28T12:00:00Z')
    expect(sessionMeta(s({ cwd: '/home/jd/src/api', kind: 'hosted', hostName: 'mac-jd', viewers: 3, createdAt: '2026-09-28T11:38:00Z' }), now)).toBe('~/src/api · hosted · 3 here')
    expect(sessionMeta(s({ cwd: '/srv/web', kind: 'server', viewers: 0, createdAt: '2026-09-28T11:56:00Z' }), now)).toBe('/srv/web · server · 4m')
  })
})

describe('initials', () => {
  it('takes the first letter of the first two words', () => {
    expect(initials('Priya Shah')).toBe('PS')
    expect(initials('jd')).toBe('JD')
    expect(initials('')).toBe('?')
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `npm --prefix web test -- sessions`
Expected: FAIL, module `./sessions` not found.

- [ ] **Step 3: Implement `utils/sessions.ts`**

```ts
import type { SessionInfo } from '~/composables/useSessions'
import { isEnded } from './attention'

export type SessionGroupKey = 'needs' | 'running' | 'exited'
export interface SessionGroups { needs: SessionInfo[]; running: SessionInfo[]; exited: SessionInfo[] }

export function groupSessions(list: SessionInfo[]): SessionGroups {
  const g: SessionGroups = { needs: [], running: [], exited: [] }
  for (const s of list) {
    if (isEnded(s.status)) g.exited.push(s)
    else if (s.attention?.state === 'needs_input') g.needs.push(s)
    else g.running.push(s)
  }
  g.needs.sort((a, b) => (b.attention?.since ?? '').localeCompare(a.attention?.since ?? ''))
  g.running.sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  g.exited.sort((a, b) => (b.endedAt ?? b.createdAt).localeCompare(a.endedAt ?? a.createdAt))
  return g
}

export function filterSessions(list: SessionInfo[], query: string): SessionInfo[] {
  const q = query.trim().toLowerCase()
  if (!q) return list
  return list.filter((s) => [s.name, s.cwd, s.hostName ?? '', s.agentId, s.hostUser ?? '', s.branch ?? ''].some((v) => v.toLowerCase().includes(q)))
}

const AGENT_INITIALS: Record<string, string> = { claude: 'CC', 'claude-code': 'CC', codex: 'CX', agy: 'AG', antigravity: 'AG', sh: '$_', bash: '$_', zsh: '$_', fish: '$_' }

export function agentInitials(agentId: string): string {
  const id = agentId.trim().toLowerCase()
  if (!id) return '?'
  if (AGENT_INITIALS[id]) return AGENT_INITIALS[id]!
  return id.slice(0, 2).toUpperCase()
}

export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (!words.length) return '?'
  const letters = words.slice(0, 2).map((w) => w[0]!.toUpperCase())
  return letters.length === 1 ? words[0]!.slice(0, 2).toUpperCase() : letters.join('')
}

export function relativeTime(iso: string, now = Date.now(), opts: { suffix?: boolean } = {}): string {
  const sec = Math.max(0, Math.floor((now - Date.parse(iso)) / 1000))
  let out: string
  if (sec < 60) out = `${sec}s`
  else if (sec < 3600) out = `${Math.floor(sec / 60)}m`
  else if (sec < 86400) {
    const h = Math.floor(sec / 3600)
    const m = Math.floor((sec % 3600) / 60)
    out = m ? `${h}h ${m}m` : `${h}h`
  } else out = `${Math.floor(sec / 86400)}d`
  return opts.suffix ? `${out} ago` : out
}

/** Shortens /home/<user>/… and /Users/<user>/… to ~/… */
export function shortCwd(cwd: string): string {
  return cwd.replace(/^\/(?:home|Users)\/[^/]+/, '~')
}

export function sessionMeta(s: SessionInfo, now = Date.now()): string {
  const where = s.kind === 'hosted' ? 'hosted' : 'server'
  const tail = s.viewers > 0 ? `${s.viewers} here` : relativeTime(s.createdAt, now)
  return `${shortCwd(s.cwd)} · ${where} · ${tail}`
}
```

Note: `hostUser` and `branch` do not exist on `SessionInfo` until Task 13. Add them now as optional fields in `useSessions.ts` (`hostUser?: string`, `branch?: string`); the server simply omits them until Task 13.

- [ ] **Step 4: Run tests**

Run: `npm --prefix web test -- sessions` → PASS. Then `npm --prefix web run typecheck`.

- [ ] **Step 5: Commit**

```bash
git add web/app/utils/sessions.ts web/app/utils/sessions.test.ts web/app/composables/useSessions.ts
git commit -m "web: session grouping, filtering and meta formatting helpers"
```

---

### Task 3: Sidebar becomes the session list; index page redirects

**Files:**
- Create: `web/app/components/SessionSidebar.vue`, `web/app/components/SessionAvatar.vue`, `web/app/composables/useLaunchModal.ts` (`export function useLaunchModal() { return { open: useState<boolean>('launchOpen', () => false) } }`)
- Modify: `web/app/layouts/default.vue`, `web/app/pages/index.vue`, `web/app/composables/useShortcuts.ts`

**Interfaces:**
- `SessionAvatar` props: `{ agentId: string; size?: 'sm' | 'md'; solid?: boolean }` renders `agentInitials(agentId)` in a rounded square: solid forest when `solid`, sage otherwise; a dashed grey border for ended sessions is the caller's class.
- `SessionSidebar` has no props; reads `useAttention()`, `useRoute()`; emits nothing; opens `LaunchSessionModal` itself.
- Global shortcuts: `n` opens Launch, `/` focuses the filter, `g-w`, `g-c`, `g-a`, `meta_b`, `?`. `g-s` is removed (the list is always visible).

- [ ] **Step 1: `SessionAvatar.vue`**

```vue
<script setup lang="ts">
import { agentInitials } from '~/utils/sessions'
const props = withDefaults(defineProps<{ agentId: string; size?: 'sm' | 'md'; solid?: boolean }>(), { size: 'sm', solid: false })
const label = computed(() => agentInitials(props.agentId))
</script>
<template>
  <span
    class="grid place-items-center rounded-md font-mono font-semibold flex-none select-none"
    :class="[size === 'sm' ? 'size-6 text-[10px]' : 'size-7 text-[10.5px]', solid ? 'bg-primary text-inverted' : 'bg-elevated text-primary']"
    :title="agentId"
  >{{ label }}</span>
</template>
```

- [ ] **Step 2: `SessionSidebar.vue`**

Structure (all Nuxt UI parts):
- Top: `UButton` "Launch agent" (block, primary, trailing `UKbd` `N`), then `UInput` filter with leading `/` icon text and placeholder "Filter sessions, paths, people"; ref `filterInput` exposed so the layout shortcut can focus it.
- Body: three sections from `groupSessions(filterSessions(attention.sessions.value, query))`:
  - "NEEDS YOU · n" (uppercase tracking-wide text-xs, `text-warning`), each row: `SessionAvatar solid`, name (font-semibold), attention message line (text-xs, truncated), meta line (`sessionMeta`, font-mono text-[11px] text-muted), amber dot right. The row is a `NuxtLink :to="/sessions/${id}"`, `bg-elevated border border-default` when it is the active route, `hover:bg-elevated/60` otherwise.
  - "RUNNING · n": avatar, name (font-medium), meta line, green dot.
  - "EXITED · n": `opacity-75`, dashed avatar, "exit N · 38m ago" (`relativeTime(endedAt ?? createdAt, now, {suffix:true})`).
  - A 30-second `setInterval` bumps a `now` ref so relative times refresh.
- Empty state when there are no sessions at all: "No sessions yet." + hint.
- Bottom nav (inside the layout footer, not this component): see Step 3.

Row template for a needs-you entry:

```vue
<NuxtLink :to="`/sessions/${s.id}`" class="flex gap-2.5 rounded-md px-2.5 py-2 transition-colors" :class="active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60'">
  <SessionAvatar :agent-id="s.agentId" solid />
  <div class="min-w-0 flex-1 flex flex-col gap-0.5">
    <div class="flex items-center gap-1.5"><span class="truncate text-sm font-semibold">{{ s.name }}</span><span class="ml-auto size-2 rounded-full bg-warning flex-none" /></div>
    <span class="truncate text-xs">{{ s.attention?.message || 'Waiting for input' }}</span>
    <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
  </div>
</NuxtLink>
```

- [ ] **Step 3: Rewrite `layouts/default.vue`**

Keep `UDashboardGroup` / `UDashboardSidebar` (collapsible off, resizable, default size 18, min 14, max 26, hidden handling unchanged). Header: mark + "Conductor" + `UKbd ⌘B` hide button. Default slot: `<SessionSidebar ref="sidebarList" />`. Footer: a compact `UNavigationMenu` with Wall (badge `attention.count`, colour `warning`), Carousel, Agents, then the existing Alerts popover, Shortcuts, Admin token, Theme, Forget token as icon-only ghost buttons in a row with tooltips. Shortcuts:

```ts
defineShortcuts({
  meta_b: () => sidebar.toggle(),
  '?': () => shortcuts.show(),
  n: () => (launch.value = true),
  '/': () => sidebarList.value?.focusFilter(),
  'g-w': () => router.push('/wall'),
  'g-c': () => router.push('/carousel'),
  'g-a': () => router.push('/agents'),
})
```
`SessionSidebar` exposes `focusFilter()` and accepts `v-model:launch`; simpler: mount `LaunchSessionModal` in the layout with `launch` state from a new `useLaunchModal()` composable (`useState<boolean>('launchOpen')`) so both the sidebar button and the shortcut open it. `@launched` navigates to `/sessions/${s.id}` (server) or stays open in waiting mode (Task 8).

- [ ] **Step 4: `pages/index.vue` → redirect or empty state**

```vue
<script setup lang="ts">
import { groupSessions } from '~/utils/sessions'
useHead({ title: 'Sessions' })
const attention = useAttention()
const admin = useAdminToken()
const launch = useLaunchModal()
const target = computed(() => {
  const g = groupSessions(attention.sessions.value)
  return g.needs[0] ?? g.running[0] ?? null
})
watch(target, (t) => { if (t) navigateTo(`/sessions/${t.id}`, { replace: true }) }, { immediate: true })
onMounted(() => { if (!admin.hasToken.value) admin.needsToken.value = true; attention.start() })
</script>
<template>
  <UDashboardPanel id="home">
    <template #header><UDashboardNavbar title="Sessions"><template #leading><SidebarReveal /></template></UDashboardNavbar></template>
    <template #body>
      <div class="flex-1 flex flex-col items-center justify-center gap-3 text-muted p-8 text-center">
        <UIcon name="i-lucide-terminal" class="size-10" />
        <p class="text-sm">No active sessions. Launch an agent here or run <code>conductor host</code> from your machine.</p>
        <UButton label="Launch agent" icon="i-lucide-play" @click="launch.open.value = true" />
      </div>
    </template>
  </UDashboardPanel>
</template>
```
Delete the table, `UTable` import and thumbnail code.

- [ ] **Step 5: Update shortcut tables**

`GLOBAL_SHORTCUTS` rows: `['meta','B']` sidebar, `['?']`, `['N']` Launch agent, `['/']` Filter sessions, `['G','W']`, `['G','C']`, `['G','A']`. Remove `G S`.

- [ ] **Step 6: Typecheck, run the app, commit**

`npm --prefix web run typecheck`. Run `make run` and `make web-dev`, open `/`, confirm the sidebar lists sessions grouped and the index redirects to the first session. Commit: `web: session list sidebar replaces the nav; index redirects to the first session`.

---

### Task 4: Terminal input hook and RTT measurement

**Files:**
- Modify: `web/app/utils/transport/base.ts`, `web/app/utils/transport/types.ts`, `web/app/components/TerminalView.vue`, `web/app/components/TransportBadge.vue`
- Test: `web/app/utils/transport/rtt.test.ts` (new)

**Interfaces:**
- `TerminalTransport` gains `readonly rtt: Ref<number | null>` (ms, last ping/pong) — declared in `types.ts`.
- `BaseTransport.ping()` records `pingSentAt`; on `pong` with matching `ts` sets `rtt.value = Date.now() - msg.ts`.
- `TerminalView` exposes `sendInput(text: string): boolean` (false when not open) and emits `transport` with `{ kind, state, rtt }`.
- `TransportBadge` props gain `rtt?: number | null` and render "WebRTC direct · 14 ms" style labels: `webrtc` → "WebRTC direct", `relay` → "Relay", `ws` → "WebSocket".
- Pure helper for the test: `export function rttFromPong(sentTs: number, now: number): number | null` in `web/app/utils/transport/rtt.ts` — returns `null` for negative or > 60000.

- [ ] **Step 1: Test** `rtt.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { rttFromPong } from './rtt'
describe('rttFromPong', () => {
  it('returns the elapsed time and rejects nonsense', () => {
    expect(rttFromPong(1000, 1014)).toBe(14)
    expect(rttFromPong(1000, 999)).toBeNull()
    expect(rttFromPong(0, 70000)).toBeNull()
  })
})
```
Run → FAIL (module missing).

- [ ] **Step 2: Implement `rtt.ts`** (`const d = now - sentTs; return d < 0 || d > 60000 ? null : d`). In `base.ts` add `readonly rtt: Ref<number | null> = ref(null)`; in `handleControl` add `else if (msg.t === 'pong') { const r = rttFromPong(msg.ts, Date.now()); if (r !== null) this.rtt.value = r }`. Add `rtt` to the `TerminalTransport` interface in `types.ts`.

- [ ] **Step 3: TerminalView** — extend the `transport` emit payload with `rtt`; the watch becomes `watch([t.kind, t.state, t.rtt], ([kind, state, rtt]) => emit('transport', { kind, state, rtt }), { immediate: true })`. Add to `defineExpose`: `sendInput: (text: string) => { if (!transport || transport.state.value !== 'open') return false; transport.sendInput(encodeText(text)); return true }`. Lower the ping interval to 10 s so the badge is fresh.

- [ ] **Step 4: TransportBadge** — new `rtt` prop; label: state not open → existing labels; open → `${name}${rtt != null ? ` · ${rtt} ms` : ''}` with names above; colour `success` for webrtc, `neutral` for ws/relay; leading dot via `UChip`. Update the three callers (`sessions/[id].vue`, `wall.vue`, `join/[token].vue`) to pass `:rtt="transport.rtt"` and type the ref as `{ kind; state; rtt: number | null }`.

- [ ] **Step 5: Test, typecheck, commit** — `npm --prefix web test && npm --prefix web run typecheck`; commit `web: terminal sendInput hook and transport round-trip time`.

---

### Task 5: Workbench page with inspector and quick-reply bar

**Files:**
- Create: `web/app/components/FileBrowser.vue`, `web/app/components/SessionInspector.vue`, `web/app/components/QuickReplyBar.vue`, `web/app/components/ViewerAvatars.vue`
- Modify: `web/app/components/FileViewer.vue`, `web/app/pages/sessions/[id].vue`

**Interfaces:**
- `FileBrowser` = everything currently inside `FileViewer`'s slideover body (breadcrumbs, dir listing, highlighted file, image, url iframe). Props: `request`, `cwd?`, `rawUrl?`; models `target`, `url`. `FileViewer` keeps its models and renders `<USlideover><FileBrowser v-bind…/></USlideover>` so `wall.vue` and `join/[token].vue` are unchanged.
- `SessionInspector` props: `{ session: SessionInfo; role: Role; viewers: ViewerInfo[]; activity: ActivityEntry[]; request; rawUrl; links: ShareLink[] }`; models: `tab: 'people'|'files'|'activity'`, `target`, `url`; emits `newLink`, `revoke(linkId)`. Until Task 10/12 land, `viewers` is `[]` and the People tab shows "N here" from the count with a note; `activity` is `[]` with an empty state. Define the placeholder types now in `protocol.ts`:

```ts
export interface ViewerInfo { id: string; name: string; role: Role; link?: string; since: string; lastInputAt?: string }
export interface ActivityEntry { at: string; type: 'attention' | 'input' | 'join' | 'leave' | 'link' | 'status'; by?: string; byName?: string; message?: string }
```
- `QuickReplyBar` props: `{ attention: Attention; agentName: string; role: Role; busy?: boolean }`; emits `reply(text: string)` and `option(index: number)`. Phase A renders: amber dot, title "`{agentName}` is waiting on you", subtitle "Answer here or in the terminal. First reply wins; everyone sees who answered.", and a `UInput` + Send (Enter) that emits `reply`. Hidden entirely when `attention.state !== 'needs_input'`. The input is disabled with tooltip "View only" when `role !== 'control'`.
- `ViewerAvatars` props `{ viewers: ViewerInfo[]; max?: number }` renders stacked `initials(name)` circles (`-ml-2 ring-2 ring-default`), overflow "+n".

- [ ] **Step 1: Extract `FileBrowser.vue`** by moving the `<script setup>` and the body template out of `FileViewer.vue`; `FileViewer.vue` becomes:

```vue
<script setup lang="ts">
import type { FileResponse } from '~/utils/protocol'
export type { FileTarget } from './FileBrowser.vue'
import type { FileTarget } from './FileBrowser.vue'
defineProps<{ request: (path: string, stat?: boolean) => Promise<FileResponse>; cwd?: string; rawUrl?: (path: string) => string | null }>()
const open = defineModel<boolean>('open', { default: false })
const target = defineModel<FileTarget | null>('target', { default: null })
const url = defineModel<string | null>('url', { default: null })
watch([target, url], ([t, u]) => { if (t || u) open.value = true })
</script>
<template>
  <USlideover v-model:open="open" :ui="{ content: 'max-w-3xl' }">
    <template #content><FileBrowser v-model:target="target" v-model:url="url" :request="request" :cwd="cwd" :raw-url="rawUrl" class="h-full" /></template>
  </USlideover>
</template>
```
(Keep whatever open-on-target logic FileViewer already has; move the rest.) `FileBrowser` exports `FileTarget`.

- [ ] **Step 2: `QuickReplyBar.vue`**

```vue
<script setup lang="ts">
import type { Attention, Role } from '~/utils/protocol'
const props = defineProps<{ attention?: Attention; agentName: string; role: Role; busy?: boolean }>()
const emit = defineEmits<{ reply: [text: string]; option: [index: number] }>()
const text = ref('')
const show = computed(() => props.attention?.state === 'needs_input')
const canReply = computed(() => props.role === 'control' && !props.busy)
function send() { const t = text.value.trim(); if (!t || !canReply.value) return; emit('reply', t); text.value = '' }
</script>
<template>
  <div v-if="show" class="flex items-center gap-3 rounded-md border border-default bg-default px-3.5 py-3" data-quick-reply>
    <span class="size-2 rounded-full bg-warning flex-none" />
    <div class="min-w-0 flex flex-col gap-0.5">
      <span class="text-sm font-semibold">{{ agentName }} is waiting on you</span>
      <span class="text-xs text-muted truncate">{{ attention?.message || 'Answer here or in the terminal. First reply wins; everyone sees who answered.' }}</span>
    </div>
    <form class="ml-auto flex items-center gap-2 flex-none" @submit.prevent="send">
      <UInput v-model="text" size="sm" class="w-64" :placeholder="canReply ? `Reply to ${agentName}…` : 'View only'" :disabled="!canReply" />
      <UButton type="submit" size="sm" label="Send" trailing-icon="i-lucide-corner-down-left" :disabled="!canReply || !text.trim()" />
    </form>
  </div>
</template>
```
(Task 11 adds the option buttons before the form.)

- [ ] **Step 3: `SessionInspector.vue`** — `UTabs`-style header built from three `UButton variant="link"` with an underline for the active tab (mockup), body:
  - People: "HERE NOW · n" list of `viewers` (avatar circle, name, sub line "`role` · via `link`" or "Owner"), a `Control`/`View` badge; when `viewers` is empty show "`session.viewers` here" and "Names arrive once everyone reconnects." Then "LINKS" with "+ New link" (emits `newLink`), each link card: label, role badge, "Revoke" (emits), sub line "expires in …" (`relativeTime` to `expiresAt`) and, when `link.active` exists (Task 13), "· n using".
  - Files: `<FileBrowser>` inline with an "open path[:line]" input above it (moved from the navbar).
  - Activity: reverse-chronological list of `activity` entries: time (font-mono), by name, message. Empty state "Nothing yet."
- [ ] **Step 4: Rewrite `pages/sessions/[id].vue`**

Header (`UDashboardNavbar` with `:ui="{ root: 'h-14' }"`): leading `SidebarReveal`; title block = name + `AttentionBadge` on the first line, meta line under it in font-mono text-xs text-muted: `{agentLabel} · {kind === 'hosted' ? `hosted by ${hostUser ?? '?'} on ${hostName}` : 'server'} · {shortCwd(cwd)}{branch ? ` · ${branch}` : ''}`. Right: `TransportBadge` with rtt, `ViewerAvatars`, `UButton "Files"` (toggles inspector tab to files / shows inspector), `UButton "Share"` primary (opens `ShareLinksModal`), `UDropdownMenu` "⋯" with Signal items and Stop.

Body: `div.flex.flex-1.min-h-0` → main column (`p-3 gap-3`): `TerminalView` (flex-1) then `QuickReplyBar` (`@reply="terminal?.sendInput(text + '\r')"`); right column `SessionInspector` (w-[332px], `hidden xl:flex`, toggled by an `inspector` ref stored in localStorage key `conductor.inspector`). Attention state comes from the terminal's `attention` emit as today, merged with the live store entry (`attention.sessions` lookup by id) so the header matches the sidebar.

Remove the old `UAlert` "Agent is waiting for input"; the bar replaces it. Keep `ShareLinksModal` and drop `FileViewer` (inline browser instead).

- [ ] **Step 5: Typecheck, run, commit** — verify a needs-input session shows the bar and that typing a reply and pressing Enter delivers it to the PTY (run the `sh` agent from the test catalog: `read x` then reply). Commit `web: workbench page with inspector and reply bar`.

---

### Task 6: Quick-reply transport and the wall queue

**Files:**
- Create: `web/app/composables/useQuickReply.ts`, `web/app/components/WallQueue.vue`
- Modify: `web/app/pages/wall.vue`, `web/app/composables/useShortcuts.ts`

**Interfaces:**
- `useQuickReply()` returns `{ send(session: SessionInfo, text: string, opts?: { token?: string }): Promise<void>; sending: Ref<Set<string>> }`. Implementation: `create({ sessionId, token: opts.token ?? admin.token.value, kind: session.kind, forceRelay: true })`, `await t.connect({ cols: session.cols || 80, rows: session.rows || 24 })`, check `welcome.role === 'control'` else throw `Error('view only')`, `t.sendInput(encodeText(text))`, wait 150 ms, `t.close()`. Adds/removes `session.id` in `sending`. Errors are thrown to the caller (toast).
- `WallQueue` props `{ sessions: SessionInfo[]; answered: SessionInfo[] }` (answered is `[]` until Task 12); emits `reply(session, text)`, `option(session, index)`, `open(session)`. Renders the "QUEUE · n waiting" header with `J / K` kbd hint, one card per session (avatar, name, `relativeTime(attention.since)`, message, reply input "Reply to {agent}…" ↵), and "ANSWERED" list (Task 12 fills it).

- [ ] **Step 1: `useQuickReply.ts`**

```ts
import { encodeText } from '~/utils/protocol'
import type { SessionInfo } from './useSessions'

export function useQuickReply() {
  const { create } = useTerminalTransport()
  const admin = useAdminToken()
  const sending = useState<Set<string>>('quickReplySending', () => new Set())

  async function send(session: SessionInfo, text: string, opts: { token?: string } = {}): Promise<void> {
    if (sending.value.has(session.id)) return
    sending.value = new Set(sending.value).add(session.id)
    const t = create({ sessionId: session.id, token: opts.token ?? admin.token.value, kind: session.kind, forceRelay: true })
    try {
      const welcome = await t.connect({ cols: session.cols || 80, rows: session.rows || 24 })
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      t.sendInput(encodeText(text))
      await new Promise((r) => setTimeout(r, 150))
    } finally {
      t.close()
      const next = new Set(sending.value)
      next.delete(session.id)
      sending.value = next
    }
  }
  return { send, sending }
}
```
Note: a `control` attach applies the client's size (resize policy). Passing the session's own `cols/rows` keeps the PTY size unchanged.

- [ ] **Step 2: Wall header chips and queue**

`wall.vue` grid mode: navbar title "Wall"; trailing chips as a `UButtonGroup` of toggle buttons `All n` / `Needs you n` / `Running n` bound to `filter: 'all'|'needs'|'running'` (default all). Tiles = `active` filtered by chip. Right: `USwitch "Jump to input requests"` bound to a persisted `jump` setting (localStorage `conductor.wall.jump`; when on, entering focus on a newly waiting session is NOT automatic — the switch means the queue auto-scrolls the newest waiting card into view and plays through `useAttention` notifications; keep it simple: it toggles queue auto-scroll) and the fullscreen button. Body: `aside.w-[340px]` `WallQueue` (hidden when `waiting.length === 0` and `answered.length === 0`) + the existing grid.

Wire: `@reply="(s, text) => quick.send(s, text + '\r').catch(toastErr)"`, `@open="focusSession"`. J/K: `defineShortcuts({ j: () => queueSel++, k: () => queueSel-- })` with `queueSel` clamped; the selected card gets `ring-2 ring-primary` and Enter focuses its input. Add `['J'] next in queue`, `['K'] previous in queue` rows to `WALL_SHORTCUTS`.

- [ ] **Step 3: Typecheck, run, commit** — with two `sh` sessions blocked on `read`, confirm both appear in the queue and replying from the queue unblocks the right one without leaving the wall. Commit `web: wall queue with quick replies; chips filter tiles`.

---

### Task 7: Carousel film strip and rotation state

**Files:**
- Create: `web/app/components/CarouselStrip.vue`
- Modify: `web/app/pages/carousel.vue`

**Interfaces:**
- `CarouselStrip` props `{ sessions: SessionInfo[]; selected: number; progress: number /*0..1 of current interval*/; holdingId?: string }`; emits `select(index)`. Each cell: `SessionAvatar`, name, status dot (amber/green/grey), a 3 px bar at the bottom: full amber when the cell is the holding session, `progress` wide forest when it is the selected session and rotating, empty otherwise. Selected cell has `border-2 border-primary`.
- Progress: `carousel.vue` keeps `progress` from an `autoplay:timerset`/`select` timestamp: `lastTick = Date.now()` on `select`; a 250 ms interval computes `progress = rotating ? Math.min(1, (Date.now() - lastTick) / settings.intervalMs) : 0`.

- [ ] **Step 1: Header controls** — navbar: title "Carousel", `UBadge` `n / total` font-mono; right: a `UButtonGroup` `‹` `[state]` `›` where state is "Rotating" / "Holding" (follow mode on a waiting session) / "Paused" (space) / "Typing" (terminal focused), then "Every" `USelect` (5/10/20/30/60 s, mono labels "20s"), `USwitch` Auto-rotate, `USwitch` Follow input requests, fullscreen.
- [ ] **Step 2: Slide header** — avatar, name (text-base font-semibold), `AttentionBadge`, meta line (`agentLabel · hosted by … · cwd`); right: when holding show `UBadge color="warning" variant="subtle"` "Jumped here {relativeTime(jumpedAt)} ago. Stays until someone answers"; "Open page" button. Track `jumpedAt` when follow mode scrolls.
- [ ] **Step 3: Terminal frame** — wrap `TerminalView` in a div with `ring-[3px] ring-primary ring-offset-2 ring-offset-default` when the terminal has focus (`typing`), and an absolute top-right `UBadge` "Paused: terminal has focus" while typing.
- [ ] **Step 4: Strip + footer** — `CarouselStrip` under the slide; footer line: "Next up: **{next.name}**. It also needs input, so it goes before running sessions" when the next session in follow order needs input, else "Next up: **{next.name}**"; right: kbd hints `← → step`, `Space pause`, `Esc leave terminal`. Add `escape` shortcut that blurs the terminal (`(document.activeElement as HTMLElement)?.blur()`), and the row to `CAROUSEL_SHORTCUTS`.
- [ ] **Step 5: Typecheck, run, commit** — `web: carousel film strip, rotation state and next-up line`.

---

### Task 8: Launch dialog — catalog cards, "runs on", host command

**Files:**
- Create: `web/app/utils/hostCommand.ts`, `web/app/utils/hostCommand.test.ts`, `web/app/composables/useLaunchModal.ts`
- Modify: `web/app/components/LaunchSessionModal.vue`

**Interfaces:**
- `hostCommand({ server, token, name, argv, cwd? }): string` → `conductor host --server <server> --token <token> --name <shell-quoted name> [--cwd <q>] -- <argv…>` with POSIX single-quote quoting for any arg containing whitespace or quotes (`'` → `'\''`). Display only.
- `useLaunchModal()` → `{ open: Ref<boolean> }` (`useState('launchOpen')`).
- The modal emits `launched(session)` for server launches. For "My machine" it shows the command, "Copy", the hint text from the mockup, and a footer "Waiting for your machine…" that watches `useAttention().sessions` for a `hosted` session with `name === state.name` and `createdAt > openedAt`; when found it closes and navigates to it.

- [ ] **Step 1: Tests** `hostCommand.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { hostCommand, shellQuote } from './hostCommand'
describe('hostCommand', () => {
  it('renders flags then the argv after --', () => {
    expect(hostCommand({ server: 'https://conductor.acme.dev', token: 'tok', name: 'auth-refactor', argv: ['claude'] }))
      .toBe('conductor host --server https://conductor.acme.dev --token tok --name auth-refactor -- claude')
  })
  it('quotes names and args with spaces or quotes', () => {
    expect(shellQuote("it's")).toBe("'it'\\''s'")
    expect(hostCommand({ server: 'http://x', token: 't', name: 'my run', argv: ['claude', '--model', 'opus 4'] }))
      .toBe("conductor host --server http://x --token t --name 'my run' -- claude --model 'opus 4'")
  })
  it('includes --cwd when given and omits --name when empty', () => {
    expect(hostCommand({ server: 'http://x', token: 't', name: '', argv: ['sh'], cwd: '/srv/app' })).toBe('conductor host --server http://x --token t --cwd /srv/app -- sh')
  })
})
```
Run → FAIL.

- [ ] **Step 2: Implement**

```ts
export function shellQuote(s: string): string {
  if (s === '') return "''"
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s
  return `'${s.replace(/'/g, "'\\''")}'`
}
export function hostCommand(o: { server: string; token: string; name: string; argv: string[]; cwd?: string }): string {
  const parts = ['conductor', 'host', '--server', shellQuote(o.server), '--token', shellQuote(o.token)]
  if (o.name) parts.push('--name', shellQuote(o.name))
  if (o.cwd) parts.push('--cwd', shellQuote(o.cwd))
  parts.push('--', ...o.argv.map(shellQuote))
  return parts.join(' ')
}
```
Run → PASS.

- [ ] **Step 3: Modal redesign** — `UModal` title "Launch agent" with `UKbd Esc`. Body: a 4-column grid of agent cards (`SessionAvatar` md + name; selected = `border-primary bg-primary/5`), "Runs on" segmented control (`UTabs` or two `UButton`s in a `bg-elevated rounded-md p-0.5` wrapper) `Server` / `My machine`, "Name" input, then either (server) "Working directory" + "Extra arguments" (as today) and footer `Cancel` / `Launch`; or (my machine) "Run this in your terminal" dark code block with the command from `hostCommand({ server: location.origin (or httpBase), token: admin.token, name, argv: selected.command, cwd })`, a "Copy" link button, the hint paragraph, and a footer with the amber dot "Waiting for your machine…" + `Cancel`. The token note: "This is your admin token; keep the command private."
- [ ] **Step 4: Typecheck, run, commit** — start a host from the copied command and confirm the modal navigates to the new hosted session. Commit `web: launch dialog with catalog cards and a host command for your machine`.

---

### Task 9: Share dialog and Join page; display name

**Files:**
- Create: `web/app/composables/useIdentity.ts`
- Modify: `web/app/components/ShareLinksModal.vue`, `web/app/pages/join/[token].vue`, `web/app/composables/useTerminalTransport.ts` (accept `name` in the spec, unused until Task 10)

**Interfaces:**
- `useIdentity()` → `{ name: Ref<string>; set(name: string): void }`, localStorage key `conductor.displayName`, trimmed to 40 chars.
- `TransportSpec.name?: string` stored on the transport for the hello (wired in Task 10).

- [ ] **Step 1: `useIdentity.ts`** (same shape as `useAdminToken`).
- [ ] **Step 2: ShareLinksModal** — title "Share {sessionName}" (new prop `sessionName?`), subtitle from the mockup; role as two selectable cards (View: "Watch output, open files. Can't type." / Control: "Types into the agent, answers prompts."), Label + Expires (`Never`, `In 1 hour`, `In 2 hours`, `In 8 hours`, `In 24 hours`, `In 7 days`) side by side, "Create link". After creation: a `bg-elevated` box with the URL (mono, truncated middle), "Copy link" primary, and the terracotta-text note "Shown once. Copy it now; you can always make a new one." (`text-secondary`). Existing links list stays below (with `active` "n using" once Task 13 lands).
- [ ] **Step 3: Join page** — before joining show a centered card: mark, "Join {name}", sentence "A live {agent} session hosted by {hostUser ?? hostName} / on the server. You'll have **control**: your keystrokes reach the agent." or "…**view only**: you can watch and open files.", "Your name" input (prefilled from `useIdentity`), "Join session" button. No terminal until pressed. After Join: current header + terminal, with `create({ …, name: identity.name.value })`. Errors as today. Remove the `TerminalView` from the pre-join state entirely (no preview).
- [ ] **Step 4: Typecheck, run, commit** — `web: share dialog redesign; join page asks for a name and never previews`.

**Phase A gate:** `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test && make web-build && make build-go`. Run the binary and click through every screen.

---

## Phase B — protocol and server

### Task 10: Presence — named viewers roster

**Files:**
- Modify: `internal/proto/control.go`, `internal/proto/hostmsg.go`, `internal/session/attention.go` (CleanName), `internal/session/hub.go`, `internal/session/local.go`, `internal/api/ws_viewer.go`, `internal/hostagent/agent.go`, `internal/hostagent/peer.go`, `internal/hostagent/localtty.go`, `internal/signal/hosted.go`, `docs/protocol.md`, `web/app/utils/protocol.ts`, `web/app/utils/transport/base.ts`, `web/app/composables/useTerminalTransport.ts`, `web/app/components/TerminalView.vue`, pages that consume `viewers`
- Test: `internal/session/local_test.go`, `internal/session/attention_test.go`, `internal/api/api_test.go` (ws test), `internal/hostagent/peer_test.go`

**Interfaces (Go):**
```go
// proto
type Hello struct { T string; Proto int; Cols, Rows uint16; Client string; Name string `json:"name,omitempty"` }
type ViewerInfo struct {
    ID string `json:"id"`; Name string `json:"name"`; Role string `json:"role"`
    Link string `json:"link,omitempty"`; Since string `json:"since"`; LastInputAt string `json:"lastInputAt,omitempty"`
}
type Viewers struct { T string; Count int; List []ViewerInfo `json:"list,omitempty"` }
type ViewerJoin struct { …; LinkLabel string `json:"linkLabel,omitempty"` }
const MaxNameLen = 40

// session
func CleanName(s string) string // trims, strips control chars, cuts to 40 runes, "" → "guest"
type AttachOptions struct { ID string; Role Role; LinkID, LinkLabel, Name string; Cols, Rows uint16 }
func (s *Local) AttachWith(o AttachOptions, sink Sink) (*Subscription, error)
func (s *Local) Attach(id string, role Role, linkID string, cols, rows uint16, sink Sink) (*Subscription, error) // = AttachWith with Name ""
func (h *Hub) Roster() []proto.ViewerInfo  // sorted by Since
```
`Subscription` gains `Name, LinkLabel string; Since time.Time; lastInput atomic.Int64`.

- [ ] **Step 1: Failing tests**

`attention_test.go`:
```go
func TestCleanName(t *testing.T) {
	cases := map[string]string{"  Priya Shah ": "Priya Shah", "": "guest", "a\x00b\x1bc": "abc", strings.Repeat("x", 100): strings.Repeat("x", 40), "é🙂": "é🙂"}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
```
`local_test.go`:
```go
func TestViewersRosterCarriesNamesAndTyping(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	subA, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Priya", LinkLabel: "pairing", Cols: 80, Rows: 24}, a)
	s.AttachWith(AttachOptions{Role: RoleView, Cols: 80, Rows: 24}, b)
	b.waitFrames(t, 4) // welcome, ready, viewers(self), then viewers after A? order: A gets welcome/ready/viewers(1); B gets welcome/ready/viewers(2)
	var roster map[string]any
	for i := 0; i < b.count(); i++ {
		if m := decodeControl(t, b.frame(i)); m["t"] == proto.CtlViewers { roster = m }
	}
	list := roster["list"].([]any)
	if len(list) != 2 || roster["count"].(float64) != 2 { t.Fatalf("roster: %v", roster) }
	names := map[string]bool{}
	for _, v := range list { names[v.(map[string]any)["name"].(string)] = true }
	if !names["Priya"] || !names["guest"] { t.Fatalf("names: %v", names) }
	// Typing: input from A refreshes lastInputAt and rebroadcasts the roster.
	before := b.count()
	if err := s.Input(subA, []byte("x")); err != nil { t.Fatal(err) }
	b.waitFrames(t, before+1)
	m := decodeControl(t, b.frame(before))
	entry := m["list"].([]any)[0].(map[string]any)
	if entry["lastInputAt"] == nil && m["list"].([]any)[1].(map[string]any)["lastInputAt"] == nil { t.Fatal("expected lastInputAt after input") }
}
```
(Adapt frame indices to the chan sink helper in the file; the assertion that matters: a `viewers` message with `list` of length 2 carrying names, and one more `viewers` after input.)

- [ ] **Step 2: Run** `go test ./internal/session/ -run 'TestCleanName|TestViewersRoster'` → FAIL (undefined).

- [ ] **Step 3: Implement**

`attention.go`:
```go
// CleanName normalises a display name from a client: control characters are
// dropped, surrounding space trimmed, length capped at MaxNameLen runes.
func CleanName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f { continue }
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if n := []rune(out); len(n) > proto.MaxNameLen { out = string(n[:proto.MaxNameLen]) }
	if out == "" { return "guest" }
	return out
}
```
`hub.go`: add fields to `Subscription`; `newSubscription` sets `Since: time.Now().UTC()`; add

```go
// Roster describes every live subscription for the viewers message.
func (h *Hub) Roster() []proto.ViewerInfo {
	h.mu.RLock(); defer h.mu.RUnlock()
	out := make([]proto.ViewerInfo, 0, len(h.subs))
	for _, s := range h.subs {
		if s.Reason() != nil { continue }
		v := proto.ViewerInfo{ID: s.ID, Name: s.Name, Role: string(s.Role), Link: s.LinkLabel, Since: s.Since.Format(time.RFC3339)}
		if ms := s.lastInput.Load(); ms > 0 { v.LastInputAt = time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out
}
```
`local.go`: add `viewersFrame() []byte { r := s.hub.Roster(); return proto.MustControl(proto.Viewers{T: proto.CtlViewers, Count: len(r), List: r}) }` and use it in Attach/Detach instead of the count-only message. `AttachWith` replaces the body of `Attach`; `Attach` delegates. In `Input`, after a successful write:

```go
	now := time.Now().UnixMilli()
	prev := sub.lastInput.Swap(now)
	if now-prev > 2000 { // at most one roster refresh per 2 s per typist
		s.mu.Lock(); s.hub.Broadcast(s.viewersFrame()); s.mu.Unlock()
	}
```
`ws_viewer.go`: `local.AttachWith(session.AttachOptions{Role: role, LinkID: linkID, LinkLabel: s.linkLabel(linkID), Name: session.CleanName(hello.Name), Cols: hello.Cols, Rows: hello.Rows}, sink)` with `func (s *Server) linkLabel(id string) string { if l, ok := s.links.Get(id); ok { return l.Label }; return "" }`. For hosted: `AddViewer` unchanged, but `hs.AddViewer` sends `ViewerJoin{…, LinkLabel: s.linkLabel(linkID)}` — add a `linkLabel` parameter to `AddViewer(id, role, linkID, linkLabel string)` and store it on `signal.Viewer`.
`hostagent/peer.go`: `peer` gets `linkLabel`; `newPeer(a, id, role, linkID, linkLabel)`; `attach` uses `AttachWith(session.AttachOptions{ID: p.id, Role: p.role, LinkID: p.linkID, LinkLabel: p.linkLabel, Name: session.CleanName(hello.Name), Cols: hello.Cols, Rows: hello.Rows}, sink)`. `agent.handleControl` passes `m.LinkLabel`.
`localtty.go`: `AttachWith(AttachOptions{ID: "local-"+…, Role: RoleControl, Name: "host terminal", Cols: cols, Rows: rows}, …)`.
Size limit: `readHello` rejects `len(hello.Name) > 4*proto.MaxNameLen` bytes with a protocol error (before cleaning).

- [ ] **Step 4: Run** the session tests → PASS. `go test -race ./internal/... ` → PASS (fix any call sites the compiler finds).

- [ ] **Step 5: Docs** — `docs/protocol.md`: `hello` row: `proto:1, cols, rows, client, name?` (name ≤ 40 runes after cleaning; control chars stripped; empty → "guest"); `viewers` row: `count, list[{id,name,role,link?,since,lastInputAt?}]` (list is the full roster, re-sent on join, leave and at most every 2 s while someone types); host `viewer_join{viewerId, role, linkId?, linkLabel?}`.

- [ ] **Step 6: TypeScript** — `protocol.ts`: `ViewerInfo` (already added in Task 5) and `{ t: 'viewers'; count: number; list?: ViewerInfo[] }`. `base.ts` `helloFrame` includes `name` when set: constructor takes `opts?: { name?: string }` in both transports, `useTerminalTransport.create` passes `spec.name ?? useIdentity().name.value`. `TerminalView` emits `viewers` as `{ count, list }` (change the emit type; update callers). Workbench header `ViewerAvatars`, inspector People tab, sidebar "3 here", wall tile footer "3 here" and carousel badge read from the roster. Typing indicator: a 1 s ticker in `SessionInspector` marks a viewer "typing" when `Date.now() - Date.parse(lastInputAt) < 4000` (mockup: "Priya is typing…" line under the terminal too — render in the workbench under the terminal as `text-xs text-muted`).

- [ ] **Step 7: Full checks and commit** — `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test`. Commit `presence: named viewer roster in the viewers message`.

---

### Task 11: Structured attention — kind and options

**Files:**
- Modify: `internal/session/attention.go`, `internal/session/local.go`, `internal/signal/hosted.go`, `internal/proto/control.go`, `internal/proto/hostmsg.go`, `internal/api/attention.go`, `internal/api/ws_host.go`, `internal/hostagent/agent.go`, `internal/notify/notify.go`, `cmd/conductor` or `internal/cli/notify.go` (pass-through), `docs/protocol.md`, `web/app/utils/protocol.ts`, `web/app/components/QuickReplyBar.vue`, `web/app/components/WallQueue.vue`
- Test: `internal/notify/notify_test.go`, `internal/session/local_test.go`, `internal/api/api_test.go`

**Interfaces (Go):**
```go
// session.Attention gains
Kind    string   `json:"kind,omitempty"`    // "" | "permission" | "prompt" | "done"
Options []Option `json:"options,omitempty"` // ≤ MaxAttentionOptions
type Option struct { Label string `json:"label"`; Input string `json:"input"` } // Label ≤ 60 runes, Input ≤ 16 bytes
const MaxAttentionOptions = 6
func CleanOptions(in []Option) []Option
func (s *Local) SetAttentionFull(state AttentionState, message, source, kind string, options []Option)   // SetAttention calls it with "", nil
func (h *HostedSession) SetAttentionFull(state AttentionState, message, source, kind string, options []Option, forward bool)
// proto.Attention and proto.HostAttentionMsg gain Kind string, Options []AttentionOption{Label, Input}
// notify.Request gains Kind string `json:"kind,omitempty"`, Options []Option `json:"options,omitempty"`
```

Claude Code mapping in `MapClaudeHook`:
- `PermissionRequest`: state `needs_input`, kind `permission`, message `Allow <tool_name>?` (or "Allow this action?" when empty; if `Message` is present it wins), options `[{Yes,"1"},{Always for this session,"2"},{No…,"3"}]`.
- `Notification` with `notification_type == "permission_prompt"`: same as above with message from the payload.
- Other `Notification` needs-input: kind `prompt`, no options.
- `Stop`: state `done`, kind `done`.
Codex `agent-turn-complete`: kind `prompt`.
Option inputs are single digits with no Enter: Claude Code's permission dialog confirms on the digit key. **Verify this manually in the run app (see Verification) and adjust the `Input` strings in one place (`permissionOptions()` in notify.go) if a trailing `\r` is needed.**

- [ ] **Step 1: Tests** — `notify_test.go`:

```go
func TestMapClaudeHookPermissionOptions(t *testing.T) {
	req, ok := MapClaudeHook([]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`))
	if !ok || req.State != "needs_input" || req.Kind != "permission" { t.Fatalf("%+v %v", req, ok) }
	if req.Message != "Allow Bash?" { t.Fatalf("message %q", req.Message) }
	if len(req.Options) != 3 || req.Options[0].Input != "1" || req.Options[2].Input != "3" { t.Fatalf("options %+v", req.Options) }
}
func TestMapClaudeHookUnknownNotificationIsPlainPrompt(t *testing.T) {
	req, ok := MapClaudeHook([]byte(`{"hook_event_name":"Notification","notification_type":"something_new","message":"hi"}`))
	if !ok || req.Kind != "prompt" || len(req.Options) != 0 || req.Message != "hi" { t.Fatalf("%+v", req) }
}
func TestMapClaudeHookPermissionWithoutToolName(t *testing.T) {
	req, _ := MapClaudeHook([]byte(`{"hook_event_name":"PermissionRequest"}`))
	if req.Message != "Allow this action?" || len(req.Options) != 3 { t.Fatalf("%+v", req) }
}
```
`local_test.go`:
```go
func TestSetAttentionFullBroadcastsOptionsAndInputClears(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.Attach("", RoleControl, "", 80, 24, sink)
	sink.waitFrames(t, 3)
	n := sink.count()
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, "permission", []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}})
	sink.waitFrames(t, n+1)
	m := decodeControl(t, sink.frame(n))
	if m["kind"] != "permission" || len(m["options"].([]any)) != 2 { t.Fatalf("attention: %v", m) }
	if got := s.Info().Attention; got.Kind != "permission" || len(got.Options) != 2 { t.Fatalf("info: %+v", got) }
	_ = s.Input(sub, []byte("1"))
	deadline := time.Now().Add(time.Second)
	for s.Info().Attention.State != AttentionNone && time.Now().Before(deadline) { time.Sleep(5 * time.Millisecond) }
	if a := s.Info().Attention; a.State != AttentionNone || a.Kind != "" || a.Options != nil { t.Fatalf("not cleared: %+v", a) }
}
func TestCleanOptionsBounds(t *testing.T) {
	in := make([]Option, 10)
	for i := range in { in[i] = Option{Label: strings.Repeat("l", 100), Input: strings.Repeat("i", 40)} }
	out := CleanOptions(in)
	if len(out) != MaxAttentionOptions || len([]rune(out[0].Label)) != 60 || len(out[0].Input) != 16 { t.Fatalf("%+v", out) }
	if CleanOptions([]Option{{Label: "", Input: "1"}, {Label: "x", Input: ""}}) != nil { t.Fatal("empty label or input must be dropped") }
}
```
`api_test.go` (extend `TestAttentionAPI` or add): POST `/api/sessions/{id}/attention` with `{"state":"needs_input","kind":"permission","options":[{"label":"Yes","input":"1"}]}` → 200 and `GET /api/sessions/{id}` shows `attention.kind == "permission"`; POST with 7 options → still 200 but 6 stored; POST with `kind:"bogus"` → 400 `invalid_kind`.

- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** the fields, `CleanOptions` (drop entries with empty label/input, cut label to 60 runes, input to 16 bytes, cap 6), `SetAttentionFull` in `Local` (dedupe compares kind/options too; `Input` clearing passes `"", nil`), `HostedSession.SetAttentionFull`, proto structs, host message pass-through in `agent.onLocalChange`/`handleControl` and `ws_host.go`, API validation (`kind ∈ {"", permission, prompt, done}` else 400 `invalid_kind`), notify mapping with

```go
func permissionOptions() []Option {
	return []Option{{Label: "Yes", Input: "1"}, {Label: "Always for this session", Input: "2"}, {Label: "No, explain…", Input: "3"}}
}
```
and the `conductor notify` CLI forwarding `Kind`/`Options` in the JSON body (check `internal/cli/notify.go` builds the request from `notify.Request` — it marshals the struct, so the new fields flow automatically; confirm with a test that the body contains `"kind"`).

- [ ] **Step 4: Run** `go test -race ./internal/notify/ ./internal/session/ ./internal/api/` → PASS.
- [ ] **Step 5: Docs** — protocol.md `attention` row: `state, message?, source?, kind?, options?[{label,input}]` with limits; the Attention section explains kinds and that `options[].input` is exactly what a client sends as INPUT when the human picks it. API request body gains `kind`, `options`.
- [ ] **Step 6: Web** — `protocol.ts`: `AttentionKind = '' | 'permission' | 'prompt' | 'done'`, `AttentionOption { label: string; input: string }`, `Attention.kind?`, `Attention.options?`, control message updated. `QuickReplyBar`: when `attention.options?.length` render a `UButton` per option (first primary, others outline) with a leading `UKbd` `1..n`, emitting `option(index)`; buttons are `:disabled="!canReply"`; the text reply stays for kind `prompt`. Number keys 1–n trigger the buttons while the bar is visible and the terminal is not focused (`defineShortcuts` in the page guarded by `bar.visible`). Workbench: `@option="(i) => terminal?.sendInput(attention.options[i].input)"`. Wall queue cards: same buttons via `quick.send(s, option.input)`; text reply appends `'\r'`, option input does not. The bar/buttons disable immediately when the store's attention for that session leaves `needs_input` (watch on `attention.sessions`).
- [ ] **Step 7: Full checks; commit** `attention: structured kind and options; quick-reply buttons`.

---

### Task 12: Activity log and last answer

**Files:**
- Create: `internal/session/activity.go`, `internal/session/activity_test.go`
- Modify: `internal/session/local.go`, `internal/session/info.go`, `internal/proto/control.go`, `internal/api/links.go`, `docs/protocol.md`, `web/app/utils/protocol.ts`, `web/app/components/TerminalView.vue`, `web/app/components/SessionInspector.vue`, `web/app/components/WallQueue.vue`, `web/app/pages/wall.vue`, `web/app/pages/sessions/[id].vue`

**Interfaces (Go):**
```go
type ActivityEntry struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`              // attention | input | join | leave | link | status
	By      string    `json:"by,omitempty"`      // subscriber id
	ByName  string    `json:"byName,omitempty"`
	Message string    `json:"message,omitempty"` // ≤ MaxAttentionMessage
}
const MaxActivity = 200
type activityRing struct{ mu sync.Mutex; buf []ActivityEntry } // Add(e), Snapshot() []ActivityEntry (oldest first)
type Answer struct { By string `json:"by,omitempty"`; ByName string `json:"byName"`; At time.Time `json:"at"`; Message string `json:"message,omitempty"` }
// Info gains LastAnswer *Answer `json:"lastAnswer,omitempty"`
func (s *Local) Record(e ActivityEntry)                 // appends, broadcasts CtlActivity, no OnChange
// proto: CtlActivity = "activity"; type Activity struct { T string; At string; Type, By, ByName, Message string }
```
Local records: `join`/`leave` in Attach/Detach (ByName = sub.Name), `attention` on every SetAttentionFull with a non-empty state (Message = attention message), `input` exactly once when input clears `needs_input` (ByName = sub.Name, Message = the attention message that was answered) and sets `Info.LastAnswer`; `status` on markEnded. On attach, after `ready`, replay the last 50 entries as `activity` frames. The API records `link` entries ("link created: pairing (control)", "link revoked: pairing") on Local sessions only (`if l, ok := d.(*session.Local); ok { l.Record(...) }`).

- [ ] **Step 1: Tests** — `activity_test.go`: ring keeps the newest 200 and `Snapshot` is oldest-first. `local_test.go`:

```go
func TestInputDuringNeedsInputRecordsOneAnswer(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "Priya", Cols: 80, Rows: 24}, sink)
	s.SetAttention(AttentionNeedsInput, "Apply edit?", SourceAPI)
	_ = s.Input(sub, []byte("1"))
	_ = s.Input(sub, []byte("\r"))
	time.Sleep(20 * time.Millisecond)
	info := s.Info()
	if info.LastAnswer == nil || info.LastAnswer.ByName != "Priya" || info.LastAnswer.Message != "Apply edit?" { t.Fatalf("lastAnswer: %+v", info.LastAnswer) }
	answers := 0
	for _, e := range s.Activity() { if e.Type == "input" { answers++ } }
	if answers != 1 { t.Fatalf("expected exactly one input entry, got %d", answers) }
}
func TestActivityReplayOnAttach(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	for i := 0; i < 60; i++ { s.Record(ActivityEntry{Type: "link", Message: "x"}) }
	sink := newChanSink(false)
	s.Attach("", RoleView, "", 80, 24, sink)
	sink.waitFrames(t, 3+50)
	replayed := 0
	for i := 0; i < sink.count(); i++ { if m := decodeControl(t, sink.frame(i)); m["t"] == proto.CtlActivity { replayed++ } }
	if replayed < 50 || replayed > 52 { t.Fatalf("replayed %d", replayed) } // 50 replay + this join entry
}
```
- [ ] **Step 2: Run → FAIL. Step 3: Implement** as specified (`Activity()` returns the snapshot; `Record` broadcasts `proto.Activity` with `At` RFC3339Nano). Size: `Message` cleaned with `CleanMessage` (≤ 500).
- [ ] **Step 4: Run → PASS.** Docs: `activity` row (owner → client) `at, type, by?, byName?, message?`; the last 50 entries replay after `ready`; `Info.lastAnswer{by?, byName, at, message?}`.
- [ ] **Step 5: Web** — `protocol.ts` `{ t: 'activity'; … }` and `SessionInfo.lastAnswer?`. `TerminalView` emits `activity(entry)`; the workbench keeps a bounded array (200) for the inspector's Activity tab. Wall queue "ANSWERED": sessions with `lastAnswer`, newest first, rendered as "**{byName}** answered **{name}** · {relativeTime(at)}" (message in a tooltip), max 8. Sidebar needs-you rows unchanged.
- [ ] **Step 6: Full checks; commit** `activity: per-session log with replay and lastAnswer`.

---

### Task 13: Metadata — branch, host user, per-link usage

**Files:**
- Create: `internal/session/git.go`, `internal/session/git_test.go`
- Modify: `internal/session/info.go` (`Branch`, `HostUser`, `Driver.LinkViewers`), `internal/session/local.go` (`LinkViewers`), `internal/session/hub.go` (`CountByLink`), `internal/signal/hosted.go` (`LinkViewers`, store branch/user from register), `internal/signal/hub.go` (copy `reg.Session.Branch`, `reg.Host.User` into info), `internal/proto/hostmsg.go` (`HostInfo.User`, `HostSession.Branch`), `internal/hostagent/agent.go` (fill them; `os/user`), `internal/api/sessions.go` (branch at launch), `internal/api/links.go` (`active` per link), `docs/protocol.md`, `web/app/composables/useSessions.ts` (`ShareLink.active?`), pages showing meta

**Interfaces:**
```go
// GitBranch returns the current branch name for the repository containing
// dir ("" when dir is not in a repository, HEAD is detached, or on error).
// It reads .git/HEAD directly (a .git *file* points at a worktree gitdir).
func GitBranch(dir string) string
func (s *Local) LinkViewers() map[string]int         // live subscriptions per LinkID (empty id omitted)
func (h *HostedSession) LinkViewers() map[string]int // server-side viewers per LinkID
// Driver interface gains LinkViewers() map[string]int
// links list response: {"links":[{...link fields..., "active": n}]}
```

- [ ] **Step 1: Tests** — `git_test.go` creates a temp dir with `.git/HEAD` = `ref: refs/heads/feat/x\n` → `"feat/x"`; a nested subdir → same; a detached SHA → `""`; a `.git` file `gitdir: <path>` whose target has `HEAD` → resolved; no `.git` → `""`. `local_test.go`: two subs on link "L1", one on "" → `LinkViewers()["L1"] == 2`, no "" key. `api_test.go`: in `TestLinksAndJoin`, after opening a viewer WebSocket with the link token, `GET /api/sessions/{id}/links` shows `active == 1` for that link.
- [ ] **Step 2: Run → FAIL. Step 3: Implement**; `handleCreateSession` sets `info.Branch = session.GitBranch(cwd)`; hostagent sets `HostSession.Branch = session.GitBranch(dir)` and `HostInfo.User = currentUser()` (`user.Current().Username`, fallback `$USER`); `signal/hub.go` copies both into `hs.info` (also on resume); `handleListLinks` builds `[]map[string]any` merging `active` from `d.LinkViewers()`. Limits: branch ≤ 200 bytes (truncate), user ≤ 64.
- [ ] **Step 4: Run → PASS.** Docs: host register `host{name,version,user?}`, `session{…, branch?}`; `Info` fields `branch?`, `hostUser?`; links list `active`.
- [ ] **Step 5: Web** — header meta line uses `hostUser` and `branch`; inspector Links cards show "· n using"; ShareLinksModal list shows `active`. Sidebar filter already searches them.
- [ ] **Step 6: Full checks; commit** `metadata: git branch, host user and per-link viewer counts`.

---

### Task 14: Docs and final verification

**Files:**
- Modify: `docs/features.md` (move delivered items from Planned to a "Delivered" section with dates), `README.md` (quick start mentions the sidebar and `conductor host` command from the Launch dialog), `AGENTS.md` map row for `internal/session` (mention roster/activity) if wording changed.

- [ ] **Step 1:** Update the docs.
- [ ] **Step 2:** Full gate: `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test && make web-build && make build-go && python3 scripts/brand_assets.py --check`.
- [ ] **Step 3:** Manual run (see Verification). **Step 4:** Commit `docs: features delivered by the workbench redesign`.

---

## Verification (end to end)

1. `make build && CONDUCTOR_ADMIN_TOKEN=t ./bin/conductor serve --dev` and open `http://localhost:8080`.
2. Launch two `sh` sessions from the sidebar (`N`), run `read x` in each; both move to **Needs you** in the sidebar and the wall queue. Reply from the queue to one; only that one continues and appears under **Answered** with your name.
3. Open the workbench for the other; the reply bar shows; press Enter with text; the PTY receives it and the bar disappears within a second on every open tab.
4. Launch Claude Code (`claude`) on a machine with the `conductor notify` hook configured; trigger a permission prompt; the bar shows **Yes / Always for this session / No, explain…**; click **Yes** and confirm Claude proceeds. If it only highlights the option, change `permissionOptions()` inputs to `"1\r"` etc. and re-test.
5. Share the session with a `control` link; open it in a private window; enter a name; join. The owner's tab shows the name in the header avatars and the People tab, and "… is typing" while the guest types. Revoke the link: the guest is disconnected and the Activity tab shows "link revoked".
6. Launch → **My machine**: copy the command, run it on the same box with a `.git` checkout; the dialog closes on the new hosted session; its header shows `hosted by <user> on <host> · ~/… · <branch>`.
7. Carousel: with one waiting session, the strip's bar is amber on it, the header says **Holding**, the footer names it as next up.
8. Theme toggle flips the whole app including the wall; there is no per-page theme.
9. Fetch a share URL with `curl -s http://localhost:8080/join/<token>`: only the SPA shell comes back; the join API returns name, agent, host, role and no terminal content.
