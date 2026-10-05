# Round 4 Implementation Plan: crews in view, prompts that run, interactive tiles, yolo, resume, e2e

## How to work this plan

This plan builds Round 4 of `docs/features.md` on the branch `round2-crews-events`, from HEAD `2c9c220` (at that HEAD this file, `docs/round4/` and the Round 4 section of `docs/features.md` are uncommitted). Each task below carries its code, its tests, its checks and its commit in full: follow it as written. Line numbers are of `2c9c220` (for `docs/features.md`, of the working tree with its Round 4 section); where they have moved, the quoted text is the anchor.

- Read `AGENTS.md` first. Its rules hold for every task; "Rules and fixed values" below repeats the ones this round leans on and adds the values the plan fixes.
- Do the tasks in order, 1 to 10. Where two can run in parallel, the task's **Order** line says so (and which to merge first).
- For each task: write the failing test first and see it fail, make it pass, run the task's listed checks (its `Run:` and `Expected:` lines), then review your own diff against the task's **Done when** list, and commit with the `git add` and `git commit` the task gives. Steps carry checkboxes (`- [ ]`) to track progress.
- Before finishing the round, run the full gate from `AGENTS.md`: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`, and the new `make test-e2e` (Task 9). A check that cannot run (no Chromium, no network) is a stated limitation, not a pass.
- Any headless check runs against a test server of its own: `HOME=<fresh dir> CONDUCTOR_DATA_DIR=<that dir>/.conductor CONDUCTOR_PUBLIC_URL=http://127.0.0.1:<port> bin/conductor serve --config conductor.example.json --listen 127.0.0.1:<port>`, stopped with `kill <pid>` (never `pkill -f`). Always set all three variables: a server started without them takes the checkout's `./conductor.d`, the developer's own server's data directory, which a test must never touch. "Headless checks" under File Structure has the full recipe.
- `codex exec` works non-interactively on this machine if a second opinion helps: `codex exec --sandbox read-only "<question>" < /dev/null` for questions (always `< /dev/null`, or it waits on stdin). Optional.

**Goal:** Round 4 of `docs/features.md`: a crew's typed prompts, handoffs, broadcasts and a person's replies run without anyone pressing Enter (and never answer a trust question); run state reaches every page through the existing event stream, with each crew's runs on the Crews page and the sidebar grouped by run; wall and crew-view tiles are live terminals that fill their width and never reset a session to 80×24; a global yolo switch with per-agent recipes; an ended session needs nothing; agent sessions are named and resumable; a Playwright suite drives the example crews with a stub agent.

**Architecture:** One serialised, cancellable submission routine in `internal/session` (`Local.Submit`) types every automatic line and every reply as a bracketed paste (when the program has that mode on) and, 250 ms later, a carriage return of its own; it holds no lock across the pause, re-checks that no question came up before the Enter, never re-sends, stamps the member prompted just before the Enter (a callback), and, for Claude Code, presses Enter once more when no `working` report follows within 3 s. The session follows the program's bracketed-paste mode from its output, does not count title-only output (Codex's spinner) as output, and watches the screen text for the agent's trust question (a per-agent pattern on the catalog), which holds the engine's prompt and is answered only by a person's Enter. The run engine derives a run state on every read and reports changes no session event carries through `OnRunChange`, which the server turns into a bounded `run` event on `/api/events`; the browser's one live store keeps the runs. Tiles fit their pane and size the PTY like the full view (latest controller wins); a hello of 0×0 follows the session's size, so scaled view-only tiles and quick replies never resize. Yolo is catalog data (`yolo {args, env}`) applied in `createLocalSession` as argv and filtered env, with two adapter hooks (Claude Code's settings key in its hooks file; Codex's per-launch trust override). Resume is catalog data too (`session {startArgs, resumeArgs, idPattern, …}`): an id chosen at launch or captured from the hook payloads `conductor notify` already maps, a new session launched with the resume arguments, a crew member resumed in its worktree.

**Tech Stack:** Go stdlib (`net/http` mux, `encoding/json` with `DisallowUnknownFields`, `regexp` RE2, `os/exec` argv only), Nuxt 4 + Nuxt UI 4 + xterm 6 (FitAddon), vitest, and one new dev dependency group: `@playwright/test` 1.44.1 with `@types/node` 22.20.5 for the e2e suite (a reason in the PR: the spec asks for an in-repo browser suite; 1.44.1 is the release whose `browsers.json` pins Chromium revision 1117, the one cached on this machine). No new Go dependency.

**Spec:** `docs/features.md` § "Round 4: crews in view, prompts that run, interactive tiles, yolo, e2e (planned 2026-10-01)" → "Decisions": runs on the Crews page; the sidebar groups by run; a typed prompt runs (with the trust-dialog rule and the terminal-report rule); tiles are interactive and fill; a global yolo flag; Playwright tests; an ended session needs nothing; agent sessions are named and resumable; broadcast goes to everyone by default. Where the code and the spec disagree the spec wins, and the task says so. Research the plan relies on: the five files under `docs/round4/` (Inputs, below).

## Inputs

- `docs/features.md` § "Round 4: crews in view, prompts that run, interactive tiles, yolo, e2e (planned 2026-10-01)": the spec. Its "Decisions" are what this plan builds; where the code and the spec disagree the spec wins, and the task says so.
- `docs/round4/prompt-investigation.md`: why a typed prompt waits for Enter (the text and its carriage return in one write, read as a paste by Codex every time and by Claude Code over 800 bytes or before its input is up), the fix verified live on Claude Code 2.1.287 and Codex 0.159.0 (a bracketed paste, then Enter 250 ms later), the readiness rules, the trust dialogs a typed Enter answered, and the cursor-position report that cleared needs input.
- `docs/round4/terminal-fill.md`: why wall and crew-view tiles leave 16–37 % of their width blank (a scaled 80×24 grid, and each control tile's hello resetting its session to 80×24), with the measurements and the proposed tile mode.
- `docs/round4/yolo-flags.md`: each built-in agent's yolo recipe, what it really turns off, its trust and first-run dialogs, and how each was verified (live, help, docs); Claude Code honours only the last `--settings`.
- `docs/round4/session-resume.md`: how each built-in agent names, reports and resumes its own session (launch arguments, the field to capture, resume argv, the working-directory rule, the id's shape), the live checks behind them, and Codex's hidden title thread.
- `docs/round4/spec-review-codex.md`: Codex's critique of the spec (feasibility problems, gaps, risks and a task breakdown) that shaped the serialised submission, the run events, the `(0, 0)` hello and the recipe inheritance.

## Decisions already taken

Judgement calls made while planning, which the tasks build on. Change one only with a reason, and then every task that leans on it.

- **An agent at rest reports `done`, not needs input** (Task 4, Step 8). A crew types handoffs and broadcasts only into a member that is not waiting on a prompt, so with Codex reporting needs input after every turn (and Claude Code's `idle_prompt` a minute after its turn) a handoff to an idle Codex member would wait for a person forever and a broadcast would skip it. Both now report `done`, as Claude Code's `Stop` does; a real question (a permission request, Codex's bell, Claude Code's `permission_prompt`) still reports needs input. Codex's hidden title thread, whose turn would now count as done, maps to nothing.
- **Resume** (Task 5). A member of a stopped run is refused (`409 run_stopped`): reopening a stopped run would swap the run's context under goroutines that read it. An ended session is kept, and can be resumed, while it is listed (`exitedRetention`, 10 minutes by default); a crew member while its run is kept. opencode, omp, amp, aider and dsh have no resume recipe this round and are relaunched plainly (pi's recipe chooses its id at launch; capturing pi's id through its plugin is deferred). Hosted sessions answer `400 hosted_session`. A store of resume records beyond that is deferred (Task 10 lists it).
- **The trust-dialog patterns** are the words Claude Code 2.1.287 ("Quick safety check: Is this a project you created or one you trust?") and Codex 0.159.0 ("Trust this folder?") drew on screen in the investigation's captures, not the investigation's paraphrase ("Do you trust the files in this folder?" was not on Claude Code 2.1.287's screen). They must be checked again against the screen whenever either CLI updates.
- **The trust question is answered only by a write with Enter in it** (Task 2): an arrow key that moves the dialog's selection does not release the hold.
- **The live check against the real CLIs** is Task 4's last step (Step 11) and has not been run. Task 9's live spec has not been run either.
- **The plan's code has been run.** The Go of Tasks 1–5 was applied to a copy of the repository at `2c9c220`, task by task with each task's checks: every task builds and passes, and at the end `gofmt -l` is empty, `go vet ./...` is clean and `go test -race -count=1 ./...` passes in every package. The 14 `internal/crew` tests that expected one write of `"text\r"` pass again through two helpers alone (`typed`, `waitTyped`), and the done-before-prompt tests stay meaningful (`TestADoneAsThePromptIsTypedCounts` waits for the Enter, `TestADoneBeforeTheEnterDoesNotCount` is new). The web of Tasks 6–8 was applied on top of that, cumulatively (with Task 1's `FOLLOW_SIZE`): `npx vitest run` passes (240 tests), `npx nuxt typecheck` is clean, re-applying every edit of the three tasks reproduces the tested files, and the three headless checks (`tiles-check.js`, `runs-check.js`, `yolo-resume-check.js`) pass against a binary built from that state. Task 9's stub and harness ran against HEAD's binary (6 tests pass; its UI tests need Tasks 6–7's hooks).
- Smaller calls, each argued in its task: the yolo recipe's environment is not masked in `Redacted` (the recipes carry mode switches, not secrets) and `Member.Yolo` is not built (Task 3); `crew.RepoRoots` works on git before 2.31 (Task 3; this machine has 2.25.1, which has no `--path-format=absolute`); a starting member whose session waits counts as needing input (Task 7, where the spec wins over `memberStatus`).

## Rules and fixed values

Copied from the spec and `AGENTS.md`, with the values this plan fixes; every task follows them.

**Plan-fixed values.**

| Value | Where | Number |
|---|---|---|
| Pause between a submission's text and its Enter | `session.SubmitPause` (Options.SubmitPause) | 250 ms |
| Wait for an agent that confirms a prompt before one more Enter | `session.ConfirmWait` | 3 s (Claude Code only: `Adapter.ConfirmsSubmit` with the hook signal) |
| Quiet before the trust watcher looks | `patternQuiet` (as the signal pattern) | 500 ms |
| Screen text the trust watcher keeps | `maxScreenTail` | 1024 bytes |
| A terminal report Input looks at | `maxReport` | 256 bytes |
| Text of a submission | `session.MaxSubmitText` | `proto.MaxInput` − 12 = 32 756 bytes |
| Text of a `submit` control message | `proto.MaxSubmit` | 4096 bytes |
| A broadcast's submissions, a `submit` message's | `broadcastTimeout`, `submitTimeout` | 15 s, 10 s |
| A run event's data | `maxRunEvent` | 128 bytes |
| Client coalescing of run events | `RUN_EVENT_WINDOW_MS` (`web/app/utils/runs.ts`) | 250 ms; more than 8 runs pending → one `GET /api/runs` |
| Tile fit debounce, tile font, tile scrollback | `FIT_DEBOUNCE_MS`, `TerminalView` tile mode | 100 ms, 11 px, 0 |
| Yolo recipe bounds | `catalog.validateYolo` | 16 args of ≤ 4096 bytes; 16 env entries, names `^[A-Za-z_][A-Za-z0-9_]*$` ≤ 128 bytes, never `CONDUCTOR_*`, values ≤ 4096 bytes |
| Agent session id | `session.MaxAgentSessionID`, `agentSessionShape` | ≤ 128 bytes, `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`, and the agent's `idPattern` |
| Session recipe bounds | `catalog.validateSession` | 16 args each way, `{id}` a whole argument once; `idPattern` anchored, ≤ 200 bytes, never matching `""` or a dash-led id |
| E2E stub merge wait, poll | `STUB_MERGE_WAIT_S` | 60 s, 0.5 s |
| E2E test timeout, server start wait | `playwright.config.ts`, global setup | 120 s a test, 20 s |
| E2E ports | global setup | 18400–18499 |
| Playwright | `web/package.json` | `@playwright/test` 1.44.1 (Chromium 1117), `@types/node` 22.20.5 |

**Rules.**

- Protocol changes touch three places together: `internal/proto`, `web/app/utils/protocol.ts` and `docs/protocol.md`. Every new frame or message gets a size limit and a test. Here: the hello rule (Task 1: `proto.HelloSize`, `FOLLOW_SIZE`, the hello row), the `submit` message (Task 2: `proto.Submit`/`MaxSubmit`, the transport's `submit`, the row), the `run` SSE event (Task 4: the event, `maxRunEvent`, the `GET /api/events` row; its TS handling is Task 7).
- `(0, 0)` in a hello means "follow the current size"; any other pair is two values in 1–500 as today (a pair out of range is followed, as before); zero stays invalid in a `resize` message; an older client's nonzero hello keeps its meaning. Hello sizing never grants control: the role comes from the token on both transports.
- Commands are argv arrays. Never build a shell string from user input. Recipes are `[]string` placed as whole arguments; `{id}` is only ever a whole argument; ids are validated against the shape and the agent's pattern before they reach argv; Codex's trust override is one `-c` argument whose paths are TOML strings (`tomlString`).
- The yolo recipe's environment goes through the same allowlisted environment as the agent's own (`pty.BuildEnv`'s `set`), never the privileged inject map, and cannot name `CONDUCTOR_*`.
- Server session working directories go through `resolveCwd`; file reads go through `session.ResolvePath`. A resume runs in the session's stored directory, through `resolveCwd` again.
- Compare tokens with `share.Equal`; store only hashes; never log query strings. A captured agent session id is logged at debug level only, and only that it was dropped.
- Keep per-connection bounds (read limits, queues, in-flight file requests). A `submit` message blocks only its own connection's read loop (WebSocket) or runs off the host's frame loop (host peer).
- Stdlib first; `encoding/json` with `DisallowUnknownFields`: every new request field is declared (`yolo` on sessions and crews, `agentSession`/`turn` on the attention route). `conductor notify` retries once without `agentSession`/`turn` when an older server refuses them as unknown.
- A person's own keystrokes stay raw (`Local.Input`); only Conductor's lines and the reply boxes go through `Submit`. A write that is only a terminal's automatic report (CPR, DSR, DA1/DA2, DECRPM, window reports, OSC colour replies, DCS replies, focus in/out; ≤ 256 bytes) answers nothing.
- A trust question is never typed into: while the agent's trust prompt shows, the engine holds the prompt (no readiness cap), the member needs input with the question's words, and the hold ends only on a person's Enter.
- An ended session needs nothing: its attention is cleared in the critical section that ends it (server and hosted sessions alike); the activity log keeps the history.
- Runs stay in memory (no persistence); `GET /api/runs` stays newest first; the engine keeps every active run and evicts idle ones past 100.
- No polling for runs anywhere: the crew view's 10 s re-read goes. (The live store's existing fallback poll while the event stream is down re-reads runs too; it is the stream's fallback, not a page's poll.)
- UI components come from Nuxt UI; the brand palette in `web/app/app.config.ts` and `docs/design/brand.md`. The junction mark is artwork, never a status light: the yolo badge is a warning-coloured `UBadge`, the run state badges use the existing status colours.
- An API route: handler in `internal/api`, auth via `requireAdmin` or `authenticate`, a test, the client call in `web/app/composables/useSessions.ts`. A control message: struct in `internal/proto/control.go`, dispatch in `ws_viewer.go` and `hostagent/peer.go`, the TypeScript side, handling in `transport/base.ts`, a row in `docs/protocol.md`.
- Hosted sessions (`conductor host`) are out of scope for yolo and resume this round (the resume route answers `400 hosted_session`).
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or `web/.output`. Commit `web/package-lock.json`. Never `git add -A`; never `pkill -f`.
- Checks before finishing: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`, and `make test-e2e`. A check that cannot run (no Chromium, no network) is a reported limitation, not a pass.

## Risks to verify first

The five inputs most likely to bite a person, each pinned to the task whose tests exercise it.

1. **A crew launched in a repository the agent has never trusted.** Claude Code draws "Quick safety check: Is this a project you created or one you trust?" (default "No, exit"), Codex "Trust this folder?" (default "Trust and continue", which saves the trust). Expected: nothing is typed into it, past the readiness cap too; the member and the run show needs input with the question's words; the run log says so once; an arrow key that moves the selection does not release the hold, the person's Enter does; the prompt is typed after the agent's own screen is up. Pinned in Task 2 (`TestATrustQuestionNeedsInput`, `TestATrustQuestionIsAnsweredByEnter`) and Task 4 (`TestATrustQuestionHoldsThePrompt`).
2. **A question that comes up in the 250 ms between a line and its Enter** (a permission dialog while a handoff or a broadcast is being typed). Expected: the Enter is left out, the line is never typed again, the run log (or the broadcast reply's `no_enter`) says so, and the question stays unanswered. Pinned in Task 2 (`TestSubmitLeavesTheEnterOutForAPromptRaisedDuringThePause`) and Task 4 (`TestAHandoffTypedWithoutItsEnterIsNotTypedAgain`).
3. **Opening the wall, the crew view or a view-only run link on a session a person sized at 148×57.** Expected: a control tile sizes it to the tile that last fitted it (never 80×24), the view-only page and a quick reply never resize it, and a view link's hello of any size changes nothing. Pinned in Task 1 (`TestAHelloOfZeroFollowsTheSessionsSize`, `TestAPeersHelloOfZeroFollowsTheSize`) and Task 6 (`tiles-check.js`).
4. **A hook payload whose session id is shaped like a flag** (`--dangerously-skip-permissions`), a path, or 129 bytes long. Expected: dropped (400 for the length), never stored, never in a resume's argv; an id is used only when it matches the shape and the agent's pattern. Pinned in Task 5 (`TestAReportedIDOfTheWrongShapeIsDropped`, `TestSessionRecipeValidation`).
5. **A yolo recipe that names `CONDUCTOR_NOTIFY_TOKEN`, or a crew with yolo off on a server started with `--yolo`.** Expected: the recipe is refused at save (`400 invalid_agent`); the crew's members launch without the recipe and without the badge; a launch with no `yolo` follows the server. Pinned in Task 3 (`TestYoloRecipeIsValidated`, `TestCreateSessionAppliesTheYoloRecipe`, `TestACrewsYoloIsTheRuns`).

---

## File Structure

| File | Responsibility | Tasks |
|---|---|---|
| `internal/proto/control.go` | `HelloSize`; `CtlSubmit`, `Submit`, `MaxSubmit` | 1, 2 |
| `internal/session/report.go` (new), `report_test.go` (new) | terminal reports that answer nothing; the ended-session and report tests | 1 |
| `internal/session/local.go`, `local_test.go` | ended clears attention; hello rule; Options (trust, pause, confirm, launched); the pump's title, paste and trust feeds; `answer`/`stampTyping`; `Type`/`TypeUnlessWaiting`/`write` removed | 1, 2, 4, 5 |
| `internal/session/submit.go` (new), `submit_test.go` (new) | `Submit`, `Submission`, `SubmitResult`, `SubmitLine`, paste mode, title-only output, the confirm wait | 2 |
| `internal/session/pattern.go` | `ScreenTail`, `NewScreenWatcher`, tracker `Reset` | 2 |
| `internal/session/attention.go`, `info.go` | `SourceTrust`; `Info.Yolo`, `AgentSession`, `ResumedFrom` | 2, 3, 5 |
| `internal/session/agentsession.go` (new) | `AgentSession`, id shape, `SetAgentSession`, `ReportAgentSession`, `Launched` | 5 |
| `internal/session/sessiontest/fakeproc_test.go` | through `Submit` | 4 |
| `internal/signal/hosted.go`, `signal_test.go` | a hosted session that ends needs nothing; `submit` from a view link refused on the relay | 1, 2 |
| `internal/api/ws_viewer.go`, `ws_e2e_test.go` | hello rule test; `submit` handling | 1, 2 |
| `internal/hostagent/peer.go`, `peer_test.go`, `hooks.go` | hello rule test; `submit` handling; `InjectFor`'s new argument | 1, 2, 3 |
| `internal/catalog/catalog.go`, `defaults.go`, `catalog_test.go` | `Yolo`, `TrustPrompt`, `SessionRecipe`: fields, bounds, inheritance; built-in recipes | 3, 5 |
| `internal/agents/adapter.go`, `registry.go`, `claude.go`, `codex.go`, tests | `YoloInject`, `TrustArgs`, `ConfirmsSubmit`; Claude's yolo settings files; Codex's trust override | 3 |
| `internal/config/config.go`, `config_test.go`; `internal/cli/serve.go`, `completion.go`, tests | `yolo`, `CONDUCTOR_YOLO`, `--yolo` | 3 |
| `internal/crew/run.go`, `handoff.go`, `crew.go`, `worktree.go`, tests (`prompt_test.go` new) | `LaunchSpec`, run yolo; prompts/handoffs through `Submit`; readiness with paste and trust; run state; `OnRunChange`; `RepoRoots`; `ResumeMember` | 3, 4, 5 |
| `internal/api/sessions.go`, `runs.go`, `crews.go`, `catalog.go`, `events.go`, `server.go`, `attention.go`, `resume.go` (new), tests (`yolo_test.go`, `resume_test.go` new) | the launch path (yolo, trust, recipes); crews' yolo; broadcast through `Submit`; run events; agent session capture; resume routes | 3, 4, 5 |
| `internal/notify/notify.go`, `mappers.go`, tests; `internal/cli/notify_test.go` | `agentSession`/`turn` in reports; Codex's title thread dropped | 5 |
| `web/app/utils/protocol.ts`, `transport/types.ts`, `transport/base.ts` | `FOLLOW_SIZE`; `submit` | 1, 2 |
| `web/app/composables/useQuickReply.ts`, `pages/sessions/[id].vue`, `pages/join/[token].vue`, `pages/wall.vue` | replies through `submit`; quick reply's 0×0 hello | 1, 2 |
| `web/app/components/TerminalView.vue`, `SessionTile.vue`, `JoinCrewGrid.vue`, `pages/wall.vue`, `pages/runs/[run].vue`, `utils/tile.ts` (new) | interactive tiles that fill | 6 |
| `web/app/utils/runs.ts` (new), `composables/useAttention.ts`, `utils/sidebar.ts`, `utils/crews.ts`, `components/SessionSidebar.vue`, `SidebarRail.vue`, `BroadcastBar.vue`, `layouts/default.vue`, `pages/crews/[[id]].vue`, `pages/runs/[run].vue` | runs in the live store; Crews page runs; sidebar grouping; broadcast selection | 7 |
| `web/app/components/LaunchSessionModal.vue`, `CrewEditor.vue`, `AddAgentSlideover.vue`, `YoloBadge.vue` (new), `ResumeButton.vue` (new), `utils/agentForm.ts`, `composables/useSessions.ts`, session header, tiles, sidebar | yolo and resume in the workbench | 8 |
| `web/e2e/` (new), `web/playwright.config.ts` (new), `web/package.json`, `web/package-lock.json`, `Makefile`, `.github/workflows/ci.yml`, `AGENTS.md` | the Playwright suite | 9 |
| `README.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/features.md`, `AGENTS.md`, `internal/catalog/defaults.go` (sites) | docs | 1, 2, 4 (their protocol rows), 10 |

Tasks run in this order. Where two tasks touch one file the later one names the hunks it changes and anchors on text the earlier one leaves; in parallel worktrees, Tasks 1–3 can start together (1 and 2 both edit `local.go`: Task 1 owns `markEnded`, `AttachWith` and the report line in `write`; Task 2 owns `Options`, the `Local` struct, `NewLocal`, `pump`, `firePattern`'s neighbourhood, `setAttention`'s broadcast and the rest of `write`; merge 1 first), Task 4 needs 2 and 3, Task 5 needs 3 and 4, Tasks 6–8 need 1, 2, 4 and 5's wire shapes, Task 9 needs everything, Task 10 documents everything. Line numbers are at `2c9c220`; where an earlier task of this plan moved a line, the quoted text is the anchor.

### Headless checks

Headless checks (Tasks 6–8) use `playwright-core` 1.44.1 (the release Task 9 pins, whose Chromium is revision 1117) installed in a directory outside the checkout, `$PW` below, which also holds the check scripts; neither is committed. Chromium is `~/.cache/ms-playwright/chromium-1117/chrome-linux/chrome` (`npx playwright-core install chromium`, run in `$PW`, fetches it where it is missing), launched with `args: ['--no-sandbox', '--use-gl=swiftshader']`; pages are opened with `waitUntil: 'commit'` then a selector. The scripts assert with `node:assert/strict`, print `PASS <name>` and exit 1 at the first failure. The test server is built with `make web-build && make build-go` and started with a home, a data directory and a public URL of its own (never the checkout's `./conductor.d`):

```bash
PW=/tmp/conductor-pw   # any directory outside the checkout: playwright-core and the check scripts
mkdir -p $PW
[ -d $PW/node_modules/playwright-core ] || npm --prefix $PW install --no-save --no-audit --no-fund playwright-core@1.44.1
T=$(mktemp -d $PW/server.XXXXXX)   # the server's fresh HOME; its data directory is $T/.conductor
HOME=$T CONDUCTOR_DATA_DIR=$T/.conductor CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8099 \
  bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 > $T/server.log 2>&1 &
echo $! > $T/server.pid
# … the step's checks …
kill $(cat $T/server.pid); rm -rf "$T"
```

Admin token `dev-admin-token-change-me`. Kill the test server by pid, never `pkill -f conductor`.

---

### Task 1: An ended session needs nothing, a terminal's report answers nothing, and the hello rule

**Order:** first. Tasks 1, 2 and 3 can start in parallel in separate worktrees; Tasks 1 and 2 both edit `internal/session/local.go` (this task owns `markEnded`, `AttachWith` and the report line in `write`), so merge this task first.

Three small server rules that the rest of the round leans on. (a) When a session's process exits or is stopped, its attention is cleared in the critical section that sets `exited`/`stopped` (today `markEnded` leaves it, so a killed agent keeps saying "needs input"); a hosted session's `HostStatus` does the same. (b) A write from a controller that is only a terminal's automatic report — xterm answering Codex's cursor-position query after every turn (`ESC[6n` → `ESC[<r>;<c>R`) — is still written but answers nothing (today it clears the needs-input Codex just raised, recorded as the viewer answering). (c) The hello rule, made explicit: two dimensions in 1–500 from a controller set the PTY (as today); `(0, 0)` follows the current size, as does any other pair (as `AttachWith` already behaves); the role never comes from the hello. The browser side gets `FOLLOW_SIZE`, which the quick reply uses now and the scaled tiles use in Task 6.

**Files:**
- Create: `internal/session/report.go`, `internal/session/report_test.go`
- Modify: `internal/session/local.go` — `markEnded` (`:180-218`), `AttachWith`'s resize condition (`:545`), `write` (`:678-680`, after the process write); `internal/session/local_test.go` (append); `internal/proto/control.go` (`:221-224`, after `ValidDimension`); `internal/signal/hosted.go` — `HostStatus` (`:616-626`); `internal/signal/signal_test.go` (append); `internal/api/ws_e2e_test.go` (append); `internal/hostagent/peer_test.go` (imports, append); `web/app/utils/protocol.ts` (`:14`, after `ProtoVersion`); `web/app/utils/transport/types.ts` (`:18`); `web/app/composables/useQuickReply.ts` (`:1`, `:27`); `docs/protocol.md` (`:36`, `:63-67`)

**Interfaces:**
- Consumes: `Local.setAttention`, `attentionMessage`, `Subscription`, test helpers `newLocal`, `newLocalWith`, `newChanSink`, `decodeControl`, `waitControl`, `inputEntries` (`internal/session`); `register` (`internal/signal`); `dialViewer`, `wsClient.hello/expectControl`, `e.createSession`, `e.local` (`internal/api`); `newPeer`, `peer.startRelay/handleFrame/close`, `agent` (`internal/hostagent`), `sessiontest.NewFakeProc`.
- Produces:
```go
// internal/proto/control.go
func HelloSize(cols, rows uint16) bool // both in 1–MaxTerminalDimension: a controller's hello sets the size
// internal/session/report.go
const maxReport = 256
var terminalReport *regexp.Regexp
func isTerminalReport(data []byte) bool
```
```ts
// web/app/utils/protocol.ts
export const FOLLOW_SIZE: { readonly cols: 0; readonly rows: 0 }
```

- [ ] **Step 1: Write the failing session tests** in a new file `internal/session/report_test.go` (Task 2's `submit_test.go` reuses its two helpers):

```go
package session

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// quiet is Options for a session under test: no log.
func quiet(o Options) Options {
	o.Log = slog.New(slog.DiscardHandler)
	return o
}

// nextWrite returns the next write to p, failing after 3 s.
func nextWrite(t *testing.T, p *fakeProc) string {
	t.Helper()
	select {
	case b := <-p.input:
		return string(b)
	case <-time.After(3 * time.Second):
		t.Fatal("nothing written within 3 s")
		return ""
	}
}

// A write that is only a terminal's automatic report reaches the program and
// answers nothing: xterm answering Codex's cursor position query does not
// clear the prompt Codex raised. Typing does.
func TestATerminalReportAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, err := s.Attach("", RoleControl, "", 0, 0, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAttention(AttentionNeedsInput, "READY", SourceAPI)
	for _, report := range []string{"\x1b[12;40R", "\x1b[?1;2c", "\x1b[>0;276;0c", "\x1b[0n", "\x1b[I", "\x1b[O",
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\", "\x1b]10;rgb:ffff/ffff/ffff\a", "\x1bP>|xterm.js(6.0.0)\x1b\\", "\x1b[12;40R\x1b[I"} {
		if err := s.Input(sub, []byte(report)); err != nil {
			t.Fatal(err)
		}
		if got := nextWrite(t, p); got != report {
			t.Fatalf("wrote %q", got)
		}
		if st := s.Info().Attention.State; st != AttentionNeedsInput {
			t.Fatalf("the report %q answered the prompt", report)
		}
	}
	if err := s.Input(sub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("typing did not answer: %q", st)
	}
	for in, want := range map[string]bool{"\x1b[A": false, "\x1b[12;40Ry": false, "y\x1b[12;40R": false, "": false,
		strings.Repeat("\x1b[1;1R", 50): false, strings.Repeat("\x1b[1;1R", 30): true} {
		if got := isTerminalReport([]byte(in)); got != want {
			t.Errorf("isTerminalReport(%q) = %v", in, got)
		}
	}
}

// An ended session needs nothing: the prompt it waited on goes as its status
// becomes exited, in one change, viewers are told, and the activity log keeps
// its history.
func TestAnEndedSessionNeedsNothing(t *testing.T) {
	var mu sync.Mutex
	var seen []Info
	s, p := newLocalWith(t, quiet(Options{OnChange: func(i Info) {
		mu.Lock()
		seen = append(seen, i)
		mu.Unlock()
	}}))
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 0, 0, sink); err != nil {
		t.Fatal(err)
	}
	s.SetAttention(AttentionNeedsInput, "Allow edit?", SourceAPI)
	p.exit()
	<-s.Ended()
	info := s.Info()
	if info.Status != StatusExited || info.Attention.State != AttentionNone || info.Attention.Message != "" {
		t.Fatalf("ended: %+v", info)
	}
	mu.Lock()
	for _, i := range seen {
		if i.Status.Ended() && i.Attention.State != AttentionNone {
			t.Fatalf("a change showed an ended session waiting: %+v", i)
		}
	}
	mu.Unlock()
	if m := waitControl(sink, func(m map[string]any) bool { return m["t"] == "attention" && m["state"] == "" }); m == nil {
		t.Fatal("viewers were not told the prompt went")
	}
	found := false
	for _, e := range s.Activity() {
		found = found || (e.Type == ActivityAttention && e.Message == "Allow edit?")
	}
	if !found {
		t.Fatal("the activity log lost the prompt")
	}
}
```

(Task 2 gives `quiet` a short submission pause.)

Append to `internal/session/local_test.go`:

```go
// A hello's size: a controller's two dimensions in range set the PTY's, as
// they always did; (0, 0) follows it, and so does an out-of-range pair; a
// viewer's never changes it. The welcome carries the size that holds.
func TestAHelloOfZeroFollowsTheSize(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	for _, tc := range []struct {
		role       Role
		cols, rows uint16
		resized    bool
	}{
		{RoleControl, 0, 0, false},
		{RoleControl, 0, 40, false},
		{RoleControl, 501, 40, false},
		{RoleView, 148, 57, false},
		{RoleControl, 148, 57, true},
		{RoleControl, 0, 0, false},
	} {
		sink := newChanSink(false)
		if _, err := s.Attach("", tc.role, "", tc.cols, tc.rows, sink); err != nil {
			t.Fatal(err)
		}
		sink.waitFrames(t, 1)
		w := decodeControl(t, sink.frame(0))
		select {
		case got := <-p.resize:
			if !tc.resized || got != [2]uint16{tc.cols, tc.rows} {
				t.Fatalf("%s %dx%d resized the PTY to %v", tc.role, tc.cols, tc.rows, got)
			}
		case <-time.After(20 * time.Millisecond):
			if tc.resized {
				t.Fatalf("%s %dx%d did not resize the PTY", tc.role, tc.cols, tc.rows)
			}
		}
		info := s.Info()
		if w["cols"] != float64(info.Cols) || w["rows"] != float64(info.Rows) {
			t.Fatalf("welcome %v, session %dx%d", w, info.Cols, info.Rows)
		}
	}
	if info := s.Info(); info.Cols != 148 || info.Rows != 57 {
		t.Fatalf("size %dx%d", info.Cols, info.Rows)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/session -run 'TerminalReport|EndedSession|HelloOfZero'`
Expected: FAIL to compile, `undefined: isTerminalReport`.

- [ ] **Step 3: The reports.** Create `internal/session/report.go`:

```go
package session

import "regexp"

// maxReport bounds what Input looks at to tell a terminal's automatic report
// from typing: a longer write is typing.
const maxReport = 256

// terminalReport matches what a terminal sends by itself, never a key press:
// the replies to a cursor position query (CPR, DECXCPR), a status report, the
// device attributes (DA1, DA2), a mode report (DECRPM), a window report, a
// colour query's reply (OSC 4, 10, 11, 12), a DCS reply (XTVERSION, DECRQSS),
// and the focus in and out reports. One write may carry several. A write of
// nothing else does not answer the prompt a session waits on (Input): xterm
// answers Codex's cursor position query after every turn. A mouse report is
// a person's click and is not one of them.
var terminalReport = regexp.MustCompile(`^(?:` +
	`\x1b\[\??\d{1,4};\d{1,4}R` + // CPR, DECXCPR
	`|\x1b\[[0-3]n` + // DSR status
	`|\x1b\[[?>][\d;]{0,64}c` + // DA1, DA2
	`|\x1b\[\??\d{1,5};\d{1,2}\$y` + // DECRPM
	`|\x1b\[\d{1,2}(?:;\d{1,5}){0,2}t` + // window reports
	`|\x1b\[[IO]` + // focus in, out
	`|\x1b\](?:4;\d{1,3}|1[0-2]);rgb:[0-9a-fA-F/]{1,64}(?:\x07|\x1b\\)` + // colour replies
	`|\x1bP[>!|0-9$+]{1,4}[^\x1b]{0,128}\x1b\\` + // DCS replies
	`)+$`)

// isTerminalReport reports whether data is only terminal reports.
func isTerminalReport(data []byte) bool {
	return len(data) > 0 && len(data) <= maxReport && data[0] == 0x1b && terminalReport.Match(data)
}
```

In `internal/session/local.go` `write` (`:678-680`), right after the process write:

```go
	if _, err := s.proc.Write(data); err != nil {
		return false, err
	}
```

insert:

```go
	if sub != nil && isTerminalReport(data) {
		// A terminal's own report (xterm answering a cursor position query)
		// is written and answers nothing; nor is it a person typing.
		return true, nil
	}
```

- [ ] **Step 4: An ended session needs nothing.** In `markEnded` (`:188-207`), replace

```go
	s.info.Status = status
	now := time.Now().UTC()
```

with

```go
	s.info.Status = status
	// An ended session needs nothing: what it waited on goes with its
	// process, in the critical section that ends it, so nothing ever sees an
	// ended session that still needs input. Its history stays in the activity
	// log.
	cleared := s.info.Attention.State != AttentionNone
	if cleared {
		s.info.Attention = Attention{}
	}
	now := time.Now().UTC()
```

and replace

```go
	frame := proto.MustControl(proto.Status{T: proto.CtlStatus, Status: string(status), ExitCode: exitCode})
	s.hub.Broadcast(frame)
	s.mu.Unlock()
```

with

```go
	if cleared {
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	frame := proto.MustControl(proto.Status{T: proto.CtlStatus, Status: string(status), ExitCode: exitCode})
	s.hub.Broadcast(frame)
	s.mu.Unlock()
```

- [ ] **Step 5: The hello rule.** In `internal/proto/control.go`, after `ValidDimension` (`:221-224`), add:

```go
// HelloSize reports whether a hello's size asks for a size: two dimensions in
// 1–MaxTerminalDimension do, and a controller's hello sets the session to it
// (latest controller wins). (0, 0) is the hello of a viewer that shows the
// session at the session's size, a scaled tile or a quick reply: it follows
// the current size and changes nothing, and so does any other pair (a zero
// with a nonzero, a dimension over 500), as such a hello always did. A resize
// message has no such case: there zero is invalid. The size never decides the
// role, which comes from the token on both transports.
func HelloSize(cols, rows uint16) bool {
	return ValidDimension(cols) && ValidDimension(rows)
}
```

In `AttachWith` (`internal/session/local.go:545`), replace

```go
	if role == RoleControl && proto.ValidDimension(cols) && proto.ValidDimension(rows) && !s.info.Status.Ended() {
```

with

```go
	// The hello's size: a controller's sets the PTY's, (0, 0) follows it
	// (proto.HelloSize). The role comes from the token, never from the hello.
	if role == RoleControl && proto.HelloSize(cols, rows) && !s.info.Status.Ended() {
```

- [ ] **Step 6: Run the session tests**

Run: `go test -race -count=1 ./internal/session`
Expected: PASS.

- [ ] **Step 7: A hosted session that ends needs nothing.** In `internal/signal/hosted.go` `HostStatus` (`:616-626`), replace

```go
	h.info.Status = status
	h.info.ExitCode = exitCode
	if status.Ended() && h.info.EndedAt == nil {
		now := time.Now().UTC()
		h.info.EndedAt = &now
	}
```

with

```go
	h.info.Status = status
	h.info.ExitCode = exitCode
	if status.Ended() {
		if h.info.EndedAt == nil {
			now := time.Now().UTC()
			h.info.EndedAt = &now
		}
		// An ended session needs nothing, whatever the host said before. A
		// host that only went away (host_disconnected) may come back: its
		// session keeps its state.
		h.info.Attention = session.Attention{}
	}
```

Append to `internal/signal/signal_test.go`:

```go
// A hosted session that ends needs nothing: the host's last attention goes
// with the status that ends it. A disconnected host's session keeps its
// state, since the host may come back.
func TestAnEndedHostedSessionNeedsNothing(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	if err := hs.SetAttention(session.AttentionNeedsInput, "Allow?", session.SourceAPI, false); err != nil {
		t.Fatal(err)
	}
	hs.HostDisconnected(conn)
	if st := hs.Info().Attention.State; st != session.AttentionNeedsInput {
		t.Fatalf("a disconnected host's session lost its prompt: %q", st)
	}
	code := 0
	hs.HostStatus(session.StatusExited, &code)
	if info := hs.Info(); info.Status != session.StatusExited || info.Attention.State != session.AttentionNone {
		t.Fatalf("ended: %+v", info)
	}
}
```

- [ ] **Step 8: The rule on both transports.** Append to `internal/api/ws_e2e_test.go`:

```go
// Over the WebSocket a hello of (0, 0) follows the session's size, as does a
// view link's hello of any size and an out-of-range pair; a controller's sets
// it, as before. The role comes from the token alone.
func TestAHelloOfZeroFollowsTheSessionsSize(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	ctl := dialViewer(t, e, id, adminToken)
	ctl.hello(148, 57)
	ctl.expectControl(proto.CtlWelcome)
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	for _, tc := range []struct {
		token      string
		cols, rows uint16
	}{
		{adminToken, 0, 0},
		{lo["token"].(string), 120, 40},
		{adminToken, 501, 40},
	} {
		c := dialViewer(t, e, id, tc.token)
		c.hello(tc.cols, tc.rows)
		if w := c.expectControl(proto.CtlWelcome); w["cols"] != float64(148) || w["rows"] != float64(57) {
			t.Fatalf("hello %dx%d: welcome %v", tc.cols, tc.rows, w)
		}
		if info := e.local(id).Info(); info.Cols != 148 || info.Rows != 57 {
			t.Fatalf("hello %dx%d resized the session to %dx%d", tc.cols, tc.rows, info.Cols, info.Rows)
		}
	}
}
```

In `internal/hostagent/peer_test.go`, add `"fmt"` to the imports (after `"encoding/json"`) and `"github.com/phenixrizen/conductor/internal/session/sessiontest"` (after `".../internal/session"`), then append:

```go
// A viewer's hello over the host's transports follows the same rule as on the
// server: (0, 0) follows the session's size, a view link's size is never
// applied, and a controller's two dimensions in range set it. The role is the
// one the server gave the peer, whatever the hello says.
func TestAPeersHelloOfZeroFollowsTheSize(t *testing.T) {
	proc := sessiontest.NewFakeProc()
	local := session.NewLocal(session.Info{ID: "s", Cols: 148, Rows: 57}, proc, session.Options{Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { proc.End(0) })
	a := &agent{opts: Options{}, local: local, peers: map[string]*peer{}, log: slog.New(slog.DiscardHandler), sendHook: func(any) {}}
	for i, tc := range []struct {
		role       session.Role
		cols, rows uint16
		want       [2]uint16
	}{
		{session.RoleControl, 0, 0, [2]uint16{0, 0}},
		{session.RoleView, 80, 24, [2]uint16{0, 0}},
		{session.RoleControl, 0, 24, [2]uint16{0, 0}},
		{session.RoleControl, 100, 30, [2]uint16{100, 30}},
	} {
		p := newPeer(a, fmt.Sprintf("%016x", i), tc.role, "", "")
		p.startRelay()
		p.handleFrame(proto.Frame{Type: proto.TypeControl, Payload: mustJSON(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: tc.cols, Rows: tc.rows})})
		if c, r := proc.Size(); [2]uint16{c, r} != tc.want {
			t.Fatalf("%s %dx%d: PTY %dx%d", tc.role, tc.cols, tc.rows, c, r)
		}
		p.close()
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
```

(The relay sink writes to a control connection that is not there, so the subscription closes right after it attaches; the size is applied in `AttachWith` before that, which is what the test reads. `FakeProc.Size` is 0×0 until a resize.)

- [ ] **Step 9: Run the packages**

Run: `go test -race -count=1 ./internal/session ./internal/signal ./internal/api ./internal/hostagent ./internal/proto`
Expected: PASS.

- [ ] **Step 10: The browser side.** In `web/app/utils/protocol.ts`, after `export const ProtoVersion = 1` (`:14`):

```ts
/**
 * A hello of 0 × 0 follows the session's size (proto.HelloSize): a scaled tile or a quick reply never resizes a
 * session. Any other size a controller sends sets it, latest controller wins (docs/protocol.md, Resize policy).
 */
export const FOLLOW_SIZE = { cols: 0, rows: 0 } as const
```

In `web/app/utils/transport/types.ts` (`:18`), document it on the interface:

```ts
  /** `hello` 0 × 0 (FOLLOW_SIZE) follows the session's size; a controller's other size sets it. */
  connect(hello: { cols: number; rows: number }): Promise<Welcome>
```

In `web/app/composables/useQuickReply.ts`, change the first import line to `import { encodeText, FOLLOW_SIZE } from '~/utils/protocol'`, the comment's last two lines (`:7-8`)

```ts
 * so no WebRTC negotiation). The attach uses the session's own size so the
 * PTY is not resized by the round trip.
```

to

```ts
 * so no WebRTC negotiation). The hello follows the session's size
 * (FOLLOW_SIZE), so the round trip never resizes it.
```

and `:27`

```ts
      const welcome = await t.connect({ cols: session.cols || 80, rows: session.rows || 24 })
```

to

```ts
      const welcome = await t.connect(FOLLOW_SIZE)
```

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: PASS.

- [ ] **Step 11: `docs/protocol.md`.** The `hello` row (`:36`) becomes:

```
| `hello` | `proto:1, cols, rows, client, name?` | Must be the first frame, within 5 s. `cols, rows`: a controller's two values in 1–500 set the session's size (see Resize policy); `0, 0` follows the session's size and changes nothing, as does any other pair out of range; a viewer's never changes it. The size never decides the role, which comes from the token. `name` is the display name other viewers see (≤ 160 bytes on the wire; cleaned to ≤ 40 runes, control characters stripped, empty → `guest`) |
```

and in "Resize policy" (`:63-67`) replace "Attaching as a controller applies the client's size immediately." with:

```
Attaching as a controller applies the client's size immediately, unless its
`hello` says `0, 0`: a viewer that shows the session scaled at the session's
own size (the view-only run tiles, a quick reply) follows the size and never
resets it. A terminal's automatic reports (a cursor position report, device
attributes, focus in and out, colour replies; at most 256 bytes, nothing else
in the frame) are written to the process like any INPUT but do not answer the
prompt the session waits on. When a session's process ends, its attention is
cleared in the same change that sets `exited` or `stopped`.
```

- [ ] **Step 12: Check and commit**

Run: `make lint && go test -race -count=1 ./internal/session ./internal/signal ./internal/api ./internal/hostagent ./internal/proto`
Expected: clean, PASS.

```bash
git add internal/session/report.go internal/session/report_test.go internal/session/local.go internal/session/local_test.go internal/proto/control.go internal/signal/hosted.go internal/signal/signal_test.go internal/api/ws_e2e_test.go internal/hostagent/peer_test.go web/app/utils/protocol.ts web/app/utils/transport/types.ts web/app/composables/useQuickReply.ts docs/protocol.md
git commit -m "session: an ended session needs nothing, a terminal's report answers nothing; a 0x0 hello follows the size"
```

**Done when:**

- `internal/session/report.go` exists: a controller's write that is only a terminal's automatic report (≤ 256 bytes: CPR, DSR, DA1/DA2, DECRPM, window reports, OSC colour replies, DCS replies, focus in/out) is still written but answers nothing; `report_test.go` covers it.
- `markEnded` clears the attention in the critical section that sets `exited`/`stopped`, and a hosted session's `HostStatus` does the same (`signal_test.go`).
- `proto.HelloSize` exists; in `AttachWith` only a controller's hello with two dimensions in 1–500 sets the PTY's size, and `(0, 0)` or any other pair follows it; the rule is tested on the WebSocket (`ws_e2e_test.go`) and on the host peer (`peer_test.go`).
- `FOLLOW_SIZE` is in `web/app/utils/protocol.ts`, documented on the transport interface, and the quick reply's hello uses it.
- `docs/protocol.md`'s `hello` row and "Resize policy" state the rule.
- `make lint` is clean, the five packages of Step 12 pass with `-race`, and `npm --prefix web run typecheck && npm --prefix web test` pass.
- Committed: "session: an ended session needs nothing, a terminal's report answers nothing; a 0x0 hello follows the size".

---

### Task 2: The submission routine and the `submit` message

**Order:** after Task 1, or in parallel with Tasks 1 and 3 in a separate worktree, merged after Task 1: in `internal/session/local.go` this task owns `Options`, the `Local` struct, `NewLocal`, `pump`, `firePattern`'s neighbourhood, `setAttention`'s broadcast and the rest of `write`. Its care points are concurrency across the pause, the lock order and the TUIs' behaviour.

The cause the investigation found: `Local.Type` writes the text and its carriage return in one write, and both TUIs read that as a paste — Codex's paste-burst detector turns the Enter into a newline on every prompt, Claude Code collapses a read over 800 bytes into a pasted block whose Enter is a line of it. The fix verified end to end on both CLIs: the text as a bracketed paste (when the program has that mode on), then the carriage return as a separate write 250 ms later. This task builds that as one routine, `Local.Submit`, with the guarantees the spec asks for — one submission at a time per session, no session lock across the pause, cancellable, a re-check before the Enter that no question came up (a question raised in the pause is never answered; the text is never re-sent), a callback just before the Enter (the engine stamps the member prompted there, Task 4), and, for an agent that reports taking a prompt (Claude Code's `UserPromptSubmit` hook), one more Enter when no report follows within 3 s. Around it, in the session: the program's bracketed-paste mode followed from its output (`BracketedPaste`, for the engine's readiness in Task 4), title-only output (Codex's spinner) not moving `LastOutputAt`, and the trust watcher: a per-agent pattern matched against the screen's text (escape sequences read as spaces), which raises needs_input with the question's words (source `trust`) and is answered only by a write with Enter in it. Then the `submit` control message, so the reply boxes go through the same routine: the quick reply, the session page's and the join page's reply bar, and the wall's.

`Type` and `TypeUnlessWaiting` stay in this task (the engine still calls them); Task 4 moves the engine and the broadcast onto `Submit` and removes them. A person's keystrokes (`Input`) stay raw.

**Files:**
- Create: `internal/session/submit.go`, `internal/session/submit_test.go`
- Modify: `internal/session/local.go` — imports, `Options` (`:63-69`), the `Local` struct (`:91-101`), `NewLocal` (`:120-142`), `pump` (`:148-161`), `markEnded` (the two lines Task 1 left and the watcher stop), after `firePattern` (`:318-320`), `setAttention`'s broadcast (`:422-423`), `write` (`:662-714` as Task 1 left it), `ErrTextTooLong` (`:623-625`); `internal/session/attention.go` (`:30-37`); `internal/session/pattern.go` (after `LineTracker.Last` `:176-182`, the `PatternWatcher` struct `:203`, `NewPatternWatcher` `:214-216`, before `Stop` `:258`); `internal/session/report_test.go` (`quiet`); `internal/proto/control.go` (`:10-15`, after `Resize` `:80-87`); `internal/api/ws_viewer.go` (imports, before `sendInputError` `:105`, `handleLocalControl` before `case proto.CtlHello:` `:156`); `internal/api/ws_e2e_test.go` (append); `internal/hostagent/peer.go` (imports, `handleFrame` after `case proto.CtlPing:` `:205-208`); `internal/signal/hosted.go` (`RelayToHost` `:457`), `signal_test.go` (`:69-72`); `web/app/utils/protocol.ts` (after `FOLLOW_SIZE`), `web/app/utils/transport/types.ts`, `web/app/utils/transport/base.ts` (after `sendInput` `:54-57`), `web/app/components/TerminalView.vue` (`:260-267`), `web/app/composables/useQuickReply.ts`, `web/app/pages/sessions/[id].vue` (`:93`, `:188-190`), `web/app/pages/join/[token].vue` (`:28`, `:134-136`), `web/app/pages/wall.vue` (`:127-129`); `docs/protocol.md` (the client → owner table `:34-39`)

**Interfaces:**
- Consumes: Task 1's `isTerminalReport`, `quiet`, `nextWrite`; `Local.setAttention`, `unlessWaiting`, `PatternWatcher`, `escScanner`, `Subscription.lastInput`, `CleanName`, `Record`.
- Produces:
```go
// internal/session/submit.go
const SubmitPause = 250 * time.Millisecond
const ConfirmWait = 3 * time.Second
const MaxSubmitText = proto.MaxInput - 12
type Submission struct {
	Text          string
	By            *Subscription // a controller's reply; nil for Conductor, recorded as ByName
	ByName        string
	UnlessWaiting bool   // handoffs, broadcasts: nothing while a prompt shows
	Confirm       bool   // a role prompt: one more Enter after ConfirmWait without a report (Options.ConfirmSubmit)
	BeforeEnter   func() // runs just before the Enter, outside the session's lock
}
type SubmitResult struct{ Typed, Entered, Reentered bool }
func (s *Local) Submit(ctx context.Context, sub Submission) (SubmitResult, error)
func (s *Local) BracketedPaste() (seen, on bool)
func SubmitLine(text string) string
// internal/session/local.go
type Options struct { …; TrustPattern *regexp.Regexp; SubmitPause time.Duration; ConfirmSubmit bool; ConfirmWait time.Duration }
var ErrTextTooLong error
// internal/session/attention.go
const SourceTrust = "trust"
// internal/session/pattern.go
type ScreenTail struct{ … }
func (t *ScreenTail) Write(chunk []byte); func (t *ScreenTail) Last() string; func (t *ScreenTail) Reset()
func (t *LineTracker) Reset()
func NewScreenWatcher(re *regexp.Regexp, quiet time.Duration, fire func(text string)) *PatternWatcher
func (w *PatternWatcher) Reset()
// internal/proto/control.go
const CtlSubmit = "submit"
type Submit struct{ T, Text string } // json t, text
const MaxSubmit = 4096
```
```ts
// web/app/utils/protocol.ts
export const MAX_SUBMIT = 4096
// web/app/utils/transport/types.ts (TerminalTransport), base.ts (BaseTransport)
submit(text: string): void
// web/app/components/TerminalView.vue defineExpose
submit(text: string): boolean
// web/app/composables/useQuickReply.ts
reply(session: SessionInfo, text: string, opts?: { token?: string }): Promise<void>
```

- [ ] **Step 1: Write the failing session tests.** In `internal/session/report_test.go` (Task 1), give `quiet` the short pause the submission tests use:

```go
// quiet is Options for a session under test: no log and, unless set, a short
// pause before a submission's Enter.
func quiet(o Options) Options {
	o.Log = slog.New(slog.DiscardHandler)
	if o.SubmitPause == 0 {
		o.SubmitPause = 30 * time.Millisecond
	}
	return o
}
```

Create `internal/session/submit_test.go`:

```go
package session

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noWrite fails when p is written to within d.
func noWrite(t *testing.T, p *fakeProc, d time.Duration) {
	t.Helper()
	select {
	case b := <-p.input:
		t.Fatalf("wrote %q", b)
	case <-time.After(d):
	}
}

// waitPaste waits until s has seen the program set bracketed paste to on.
func waitPaste(t *testing.T, s *Local, on bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if seen, now := s.BracketedPaste(); seen && now == on {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("bracketed paste never turned %v", on)
}

// The text and its Enter are two writes, the pause apart; the text is
// recorded once, by the name given.
func TestSubmitWritesTheTextThenItsEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{SubmitPause: 80 * time.Millisecond}))
	done := make(chan SubmitResult, 1)
	go func() {
		res, err := s.Submit(t.Context(), Submission{Text: "Own the plan.", ByName: "crew"})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	if got := nextWrite(t, p); got != "Own the plan." {
		t.Fatalf("text %q", got)
	}
	start := time.Now()
	if got := nextWrite(t, p); got != "\r" {
		t.Fatalf("enter %q", got)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Fatalf("the Enter came %v after the text", d)
	}
	if res := <-done; !res.Typed || !res.Entered || res.Reentered {
		t.Fatalf("result %+v", res)
	}
	if e := inputEntries(s); len(e) != 1 || e[0].ByName != "crew" || e[0].Message != "Own the plan." {
		t.Fatalf("input entries %+v", e)
	}
}

// While the program has bracketed paste on, the text goes in its markers,
// with no escape of its own: the text cannot end the paste early.
func TestSubmitPastesWhileTheProgramAsksForIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	p.outW.Write([]byte("\x1b[?20"))
	p.outW.Write([]byte("04h> ")) // split between two reads
	waitPaste(t, s, true)
	go s.Submit(t.Context(), Submission{Text: "fix \x1b[201~it\nnow", ByName: "crew"})
	if got := nextWrite(t, p); got != "\x1b[200~fix [201~it now\x1b[201~" {
		t.Fatalf("paste %q", got)
	}
	if got := nextWrite(t, p); got != "\r" {
		t.Fatalf("enter %q", got)
	}
	p.outW.Write([]byte("\x1b[?2004l"))
	waitPaste(t, s, false)
	go s.Submit(t.Context(), Submission{Text: "raw", ByName: "crew"})
	if got := nextWrite(t, p); got != "raw" {
		t.Fatalf("raw %q", got)
	}
	nextWrite(t, p)
}

// The Enter answers the prompt that showed when the submission began, as a
// person's typing does.
func TestSubmitAnswersThePromptItWasTypedInto(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	s.SetAttention(AttentionNeedsInput, "Name the branch?", SourceAPI)
	go s.Submit(t.Context(), Submission{Text: "main", ByName: "crew"})
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNeedsInput {
		t.Fatalf("the text alone answered the prompt: %q", st)
	}
	nextWrite(t, p)
	deadline := time.Now().Add(3 * time.Second)
	for s.Info().Attention.State != AttentionNone && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.ByName != "crew" || info.LastAnswer.Message != "Name the branch?" {
		t.Fatalf("after the Enter: %+v %+v", info.Attention, info.LastAnswer)
	}
}

// UnlessWaiting writes and records nothing while a prompt shows.
func TestSubmitUnlessWaitingLeavesAPromptAlone(t *testing.T) {
	var changes atomic.Int32
	s, p := newLocalWith(t, quiet(Options{OnChange: func(Info) { changes.Add(1) }}))
	s.SetAttention(AttentionNeedsInput, "Allow edit?", SourceAPI)
	before, logged := changes.Load(), len(s.Activity())
	res, err := s.Submit(t.Context(), Submission{Text: "Handoff from lead: go", ByName: "crew", UnlessWaiting: true})
	if err != nil || res != (SubmitResult{}) {
		t.Fatalf("waiting: %+v %v", res, err)
	}
	noWrite(t, p, 50*time.Millisecond)
	if info := s.Info(); info.Attention.State != AttentionNeedsInput || info.LastAnswer != nil || changes.Load() != before || len(s.Activity()) != logged {
		t.Fatalf("waiting: %+v", info)
	}
}

// A prompt raised during the pause is never answered: the Enter is left out,
// and the text waits in the program's input.
func TestSubmitLeavesTheEnterOutForAPromptRaisedDuringThePause(t *testing.T) {
	for _, unless := range []bool{false, true} {
		s, p := newLocalWith(t, quiet(Options{}))
		p.onWrite = func() { s.SetAttention(AttentionNeedsInput, "Allow write?", SourceAPI) }
		res, err := s.Submit(t.Context(), Submission{Text: "Handoff from lead: go", ByName: "crew", UnlessWaiting: unless})
		if err != nil || !res.Typed || res.Entered {
			t.Fatalf("unless %v: %+v %v", unless, res, err)
		}
		nextWrite(t, p)
		noWrite(t, p, 50*time.Millisecond)
		if info := s.Info(); info.Attention.Message != "Allow write?" || info.LastAnswer != nil {
			t.Fatalf("unless %v: the prompt raised in the pause was answered: %+v", unless, info)
		}
	}
}

// Two submissions never interleave: each text is followed by its own Enter.
func TestSubmitIsOneAtATime(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	var wg sync.WaitGroup
	for _, text := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Submit(t.Context(), Submission{Text: text, ByName: "crew"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for range 3 {
		if text, enter := nextWrite(t, p), nextWrite(t, p); text == "\r" || enter != "\r" {
			t.Fatalf("interleaved: %q then %q", text, enter)
		}
	}
}

// A submission cut short in its pause returns with the text typed and never
// writes the Enter.
func TestSubmitIsCancellable(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{SubmitPause: time.Second}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	var res SubmitResult
	go func() {
		var err error
		res, err = s.Submit(ctx, Submission{Text: "slow", ByName: "crew"})
		done <- err
	}()
	nextWrite(t, p)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || !res.Typed || res.Entered {
		t.Fatalf("%+v %v", res, err)
	}
	noWrite(t, p, 50*time.Millisecond)
}

// BeforeEnter runs after the text and before the Enter.
func TestSubmitCallsBeforeEnterJustBeforeTheEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	var order []string
	var mu sync.Mutex
	p.onWrite = func() {
		mu.Lock()
		order = append(order, "write")
		mu.Unlock()
	}
	go s.Submit(t.Context(), Submission{Text: "go", ByName: "crew", BeforeEnter: func() {
		mu.Lock()
		order = append(order, "before")
		mu.Unlock()
	}})
	nextWrite(t, p)
	nextWrite(t, p)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "write,before,write" {
		t.Fatalf("order %v", order)
	}
}

// An agent that reports taking a prompt and has not within ConfirmWait gets
// one more Enter; one that reports, or that shows a question, gets none.
func TestSubmitPressesEnterAgainWithoutConfirmation(t *testing.T) {
	opts := quiet(Options{ConfirmSubmit: true, ConfirmWait: 150 * time.Millisecond})
	s, p := newLocalWith(t, opts)
	res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true})
	if err != nil || !res.Entered || !res.Reentered {
		t.Fatalf("no confirmation: %+v %v", res, err)
	}
	for _, want := range []string{"Plan it.", "\r", "\r"} {
		if got := nextWrite(t, p); got != want {
			t.Fatalf("wrote %q, want %q", got, want)
		}
	}

	s, p = newLocalWith(t, opts)
	p.onWrite = func() {
		if len(p.input) == 1 { // the Enter: the agent takes the prompt
			go s.SetAttention(AttentionWorking, "", SourceAPI)
		}
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true}); err != nil || res.Reentered {
		t.Fatalf("confirmed: %+v %v", res, err)
	}

	s, p = newLocalWith(t, opts)
	p.onWrite = func() {
		if len(p.input) == 1 {
			go s.SetAttention(AttentionNeedsInput, "Allow?", SourceAPI)
		}
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true}); err != nil || res.Reentered {
		t.Fatalf("a question came up: %+v %v", res, err)
	}

	// Without ConfirmSubmit, or without Confirm, never.
	s, _ = newLocalWith(t, quiet(Options{ConfirmWait: 50 * time.Millisecond}))
	if res, _ := s.Submit(t.Context(), Submission{Text: "x", ByName: "crew", Confirm: true}); res.Reentered {
		t.Fatal("re-entered for an agent that does not confirm")
	}
	s, _ = newLocalWith(t, opts)
	if res, _ := s.Submit(t.Context(), Submission{Text: "x", ByName: "crew"}); res.Reentered {
		t.Fatal("re-entered a submission that did not ask")
	}
}

// A viewer cannot submit, a text over MaxSubmitText is refused before
// anything is written, and an ended session takes nothing.
func TestSubmitRefusals(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	viewer := &Subscription{ID: "v", Role: RoleView}
	if _, err := s.Submit(t.Context(), Submission{Text: "x", By: viewer}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: strings.Repeat("x", MaxSubmitText+1), ByName: "crew"}); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("long: %v", err)
	}
	noWrite(t, p, 20*time.Millisecond)
	go s.Submit(t.Context(), Submission{Text: strings.Repeat("x", MaxSubmitText), ByName: "crew"})
	if got := nextWrite(t, p); len(got) != MaxSubmitText {
		t.Fatalf("wrote %d bytes", len(got))
	}
	nextWrite(t, p)
	p.exit()
	<-s.Ended()
	if res, err := s.Submit(t.Context(), Submission{Text: "late", ByName: "crew"}); !errors.Is(err, ErrSessionEnded) || res.Typed {
		t.Fatalf("ended: %+v %v", res, err)
	}
}

func TestSubmitLine(t *testing.T) {
	for in, want := range map[string]string{
		"  a\nb\tc\r ":          "a b c",
		"bell\a and esc\x1b[2J": "bell and esc[2J",
		"bad \xff utf8":         "bad  utf8",
		"\x00":                  "",
	} {
		if got := SubmitLine(in); got != want {
			t.Errorf("SubmitLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// Output that only sets the window title does not move LastOutputAt.
func TestATitleUpdateIsNotOutput(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	p.outW.Write([]byte("\x1b]0;⠋ codex\a\x1b]2;⠙ codex\x1b\\"))
	time.Sleep(50 * time.Millisecond)
	if at := s.LastOutputAt(); !at.IsZero() {
		t.Fatalf("a title update moved LastOutputAt to %v", at)
	}
	p.outW.Write([]byte("\x1b]0;x\a> "))
	deadline := time.Now().Add(3 * time.Second)
	for s.LastOutputAt().IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.LastOutputAt().IsZero() {
		t.Fatal("text with a title update did not count")
	}
}

// The agent's trust question, drawn with cursor moves between its words,
// marks the session needs_input with its words once the screen is quiet;
// the first submission's Enter ends the watching.
func TestATrustQuestionNeedsInput(t *testing.T) {
	re := regexp.MustCompile(`Trust\s*this\s*folder\?`)
	s, p := newLocalWith(t, quiet(Options{TrustPattern: re}))
	p.outW.Write([]byte("\x1b[2;3HTrust\x1b[2;9Hthis\x1b[1Cfolder?\r\n\x1b[38;5;2m› 1. Trust and continue\x1b[0m"))
	p.outW.Write([]byte("\x1b]0;⠋\a")) // a title update does not restart the quiet
	deadline := time.Now().Add(3 * time.Second)
	for s.Info().Attention.State != AttentionNeedsInput && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	att := s.Info().Attention
	if att.State != AttentionNeedsInput || att.Source != SourceTrust || att.Message != "Trust this folder?" {
		t.Fatalf("attention %+v", att)
	}
}

func TestScreenTail(t *testing.T) {
	var st ScreenTail
	st.Write([]byte("\x1b[1;1HDo\x1b[1;4Hyou"))
	st.Write([]byte("\x1b[3"))
	st.Write([]byte("Ctrust\r\n\tit?"))
	if got := st.Last(); got != "Do you trust it?" {
		t.Fatalf("%q", got)
	}
	st.Write([]byte(strings.Repeat("x", 3000)))
	if got := st.Last(); len(got) != maxScreenTail {
		t.Fatalf("kept %d bytes", len(got))
	}
}

// A trust question is answered by Enter alone: an arrow key that moves its
// selection leaves it showing. Once answered, only the question drawn anew
// raises it again.
func TestATrustQuestionIsAnsweredByEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`)}))
	sub, err := s.Attach("", RoleControl, "", 0, 0, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	waitTrust := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for s.Info().Attention.Source != SourceTrust && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if s.Info().Attention.Source != SourceTrust {
			t.Fatalf("no trust question: %+v", s.Info().Attention)
		}
	}
	p.outW.Write([]byte("Trust this folder?\r\n› 1. Trust and continue"))
	waitTrust()
	s.Input(sub, []byte("\x1b[B"))
	nextWrite(t, p)
	if st := s.Info().Attention; st.State != AttentionNeedsInput || st.Source != SourceTrust {
		t.Fatalf("an arrow key answered the trust question: %+v", st)
	}
	s.Input(sub, []byte("\r"))
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("Enter did not answer: %q", st)
	}
	p.outW.Write([]byte("\x1b[2J> ")) // the agent's own screen: the old question does not count again
	time.Sleep(700 * time.Millisecond)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("the answered question came back: %+v", s.Info().Attention)
	}
	p.outW.Write([]byte("Trust this folder?"))
	waitTrust()
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/session -run 'Submit|TitleUpdate|TrustQuestion|ScreenTail'`
Expected: FAIL to compile (`undefined: Submission`, `SubmitPause`, `ScreenTail`, …).

- [ ] **Step 3: The routine.** Create `internal/session/submit.go`:

```go
package session

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Submitting a line. What Conductor types itself (a crew member's role
// prompt, a handoff, a broadcast) and what a person sends from a reply box
// reach the program the way a terminal sends a paste followed by a key press:
// the text, wrapped in the bracketed-paste markers while the program has that
// mode on, then, SubmitPause later, a carriage return written on its own. A
// TUI that reads the text and its carriage return in one read takes the
// carriage return as part of a paste (Codex's paste-burst detector turns it
// into a newline; Claude Code collapses a read of over 800 bytes into a
// pasted block); written apart, it is Enter. A person's own keystrokes (Input)
// stay raw.

// Defaults of the submission routine (Options.SubmitPause, Options.ConfirmWait).
const (
	// SubmitPause is the pause between the text and its Enter.
	SubmitPause = 250 * time.Millisecond
	// ConfirmWait is how long Submit waits, for a prompt whose agent reports
	// taking it (Options.ConfirmSubmit and Submission.Confirm), before it
	// presses Enter once more.
	ConfirmWait = 3 * time.Second
)

// The bracketed-paste markers a terminal wraps a paste in.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// MaxSubmitText bounds the text of a submission, in bytes: with the paste
// markers around it, it fits one INPUT frame.
const MaxSubmitText = proto.MaxInput - len(pasteStart) - len(pasteEnd)

// Submission is a line to submit (Local.Submit).
type Submission struct {
	// Text is the line. Submit makes each line break, carriage return and
	// tab a space and drops the other control characters, so that the text
	// can neither end the paste early nor press a key of its own. Empty, only
	// the Enter is written.
	Text string
	// By is the controller who sends it, from a reply box; nil for what
	// Conductor types itself, recorded as ByName (cleaned as a display name).
	By     *Subscription
	ByName string
	// UnlessWaiting writes nothing while the session waits on a prompt: for
	// what must never answer one (handoffs, broadcasts).
	UnlessWaiting bool
	// Confirm presses Enter once more when the session's agent reports
	// taking a prompt (Options.ConfirmSubmit) and has not done so within
	// ConfirmWait: a role prompt typed while the program was still starting
	// can sit in its input box. Never for a reply.
	Confirm bool
	// BeforeEnter, when set, runs just before the Enter is written, outside
	// the session's lock: the run engine stamps a member prompted there, so
	// that a done reported before the Enter never counts for the prompt.
	BeforeEnter func()
}

// SubmitResult says how far a submission went.
type SubmitResult struct {
	// Typed is set once the text was written.
	Typed bool
	// Entered is set once its Enter was written. Typed without Entered: a
	// prompt appeared during the pause, and the text waits in the program's
	// input without its Enter; nothing is ever written again for it.
	Entered bool
	// Reentered is set when a second Enter was written (Confirm).
	Reentered bool
}

// Submit types sub.Text and presses Enter, as two writes Options.SubmitPause
// apart, one submission at a time per session. It holds no lock of the
// session across the pause, and a person's keystrokes are not held back
// meanwhile.
//
//   - With UnlessWaiting it writes nothing while the session waits on a
//     prompt, and returns a zero result.
//   - The Enter answers the prompt that was showing when Submit began, as
//     typing does; a prompt raised during the pause (one with a new Since) is
//     never answered: the Enter is left out.
//   - It records one input entry: always, with the text, for what Conductor
//     types; for a controller's reply, the question it answered, when it
//     answered one.
//   - ctx ends the wait for the session's turn and the pause; an error after
//     the text was written comes with Typed set, and the text is never
//     written again.
//
// Errors: ErrReadOnly for a view-role By, ErrTextTooLong for a text over
// MaxSubmitText bytes once cleaned, ErrSessionEnded, ctx's error, or the
// process's write error.
func (s *Local) Submit(ctx context.Context, sub Submission) (SubmitResult, error) {
	var res SubmitResult
	if sub.By != nil && sub.By.Role != RoleControl {
		return res, ErrReadOnly
	}
	text := SubmitLine(sub.Text)
	if len(text) > MaxSubmitText {
		return res, ErrTextTooLong
	}
	select {
	case s.submitting <- struct{}{}:
	case <-ctx.Done():
		return res, ctx.Err()
	case <-s.ended:
		return res, ErrSessionEnded
	}
	defer func() { <-s.submitting }()

	s.mu.Lock()
	ended := s.info.Status.Ended()
	promptSince := s.info.Attention.Since
	showing := s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if sub.UnlessWaiting && showing {
		return res, nil
	}
	byName := CleanName(sub.ByName)
	if text != "" {
		data := text
		if s.paste.on() {
			data = pasteStart + text + pasteEnd
		}
		if _, err := s.proc.Write([]byte(data)); err != nil {
			return res, err
		}
		res.Typed = true
		if sub.By == nil {
			s.Record(ActivityEntry{Type: ActivityInput, ByName: byName, Message: text})
		}
		t := time.NewTimer(s.opts.SubmitPause)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return res, ctx.Err()
		case <-s.ended:
			t.Stop()
			return res, ErrSessionEnded
		}
	}
	s.mu.Lock()
	ended = s.info.Status.Ended()
	att := s.info.Attention
	s.mu.Unlock()
	if ended {
		return res, ErrSessionEnded
	}
	if att.State == AttentionNeedsInput && att.Since != promptSince {
		return res, nil
	}
	if sub.BeforeEnter != nil {
		sub.BeforeEnter()
	}
	enterAt := time.Now().UTC()
	if _, err := s.proc.Write([]byte{'\r'}); err != nil {
		return res, err
	}
	res.Entered = true
	if s.trust != nil {
		// A trust question comes before the first prompt, never after.
		s.trust.Stop()
	}
	if sub.By != nil {
		s.stampTyping(sub.By)
		s.answer(promptSince, sub.By.ID, sub.By.Name, true, true)
	} else {
		s.answer(promptSince, "", byName, false, true)
	}
	if sub.Confirm && s.opts.ConfirmSubmit && !s.confirmed(ctx, enterAt) {
		res.Reentered = s.enterAgain()
	}
	return res, nil
}

// confirmed waits up to Options.ConfirmWait for the agent to show that it
// took what was submitted at enterAt: an attention change stamped after it
// that typing did not make (Claude Code's UserPromptSubmit hook reports
// working). It returns true when ctx ends or the session ends meanwhile:
// nothing is pressed then.
func (s *Local) confirmed(ctx context.Context, enterAt time.Time) bool {
	deadline := time.NewTimer(s.opts.ConfirmWait)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		att := s.info.Attention
		changed := s.attnChanged
		s.mu.Unlock()
		if att.Since != nil && att.Since.After(enterAt) && att.Source != SourceInput {
			return true
		}
		select {
		case <-changed:
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return true
		case <-s.ended:
			return true
		}
	}
}

// enterAgain writes one more carriage return, unless the session has ended or
// waits on a prompt, which the Enter must not answer. It records nothing.
func (s *Local) enterAgain() bool {
	s.mu.Lock()
	skip := s.info.Status.Ended() || s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if skip {
		return false
	}
	if _, err := s.proc.Write([]byte{'\r'}); err != nil {
		return false
	}
	s.log.Info("pressed Enter again: the agent did not report taking the prompt", "after", s.opts.ConfirmWait)
	return true
}

// SubmitLine is the text Submit writes for text: valid UTF-8, each line
// break, carriage return and tab a space, every other control character (ESC
// among them, so the text cannot end a paste early) dropped, trimmed.
func SubmitLine(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "")))
}

// pasteMode follows the bracketed-paste mode a program sets in its output
// (ESC[?2004h on, ESC[?2004l off), as a terminal does, carrying the end of a
// chunk over to the next so a sequence split between two reads still counts.
// Only the pump writes it; anyone reads it.
type pasteMode struct {
	state atomic.Int32 // 0 never set, 1 on, 2 off
	tail  []byte       // the pump's alone
}

var (
	pasteOnSeq  = []byte("\x1b[?2004h")
	pasteOffSeq = []byte("\x1b[?2004l")
)

// feed takes a chunk of output. Only the pump calls it.
func (p *pasteMode) feed(chunk []byte) {
	data := append(p.tail, chunk...)
	on, off := bytes.LastIndex(data, pasteOnSeq), bytes.LastIndex(data, pasteOffSeq)
	switch {
	case on > off:
		p.state.Store(1)
	case off > on:
		p.state.Store(2)
	}
	n := min(len(data), len(pasteOnSeq)-1)
	p.tail = append(p.tail[:0], data[len(data)-n:]...)
}

func (p *pasteMode) on() bool { return p.state.Load() == 1 }

// BracketedPaste reports whether the program has turned bracketed paste on
// at least once (seen) and whether it is on now. A program that turned it on
// and then off again is between screens (Claude Code does while it starts):
// the run engine does not type into it then.
func (s *Local) BracketedPaste() (seen, on bool) {
	st := s.paste.state.Load()
	return st != 0, st == 1
}

// titleOnly matches output that only sets the window title (OSC 0 or 2), as
// an agent that animates a spinner in its title does while it waits (Codex
// with terminal_title set): such output does not move LastOutputAt, so the
// program is seen to be quiet. A title sequence cut between two reads is
// output like any other.
var titleOnly = regexp.MustCompile(`^(?:\x1b\][02];[^\x07\x1b]*(?:\x07|\x1b\\))+$`)
```

- [ ] **Step 4: The trust watcher's text.** In `internal/session/attention.go`, add to the sources (`:30-37`), after `SourceAdmin   = "admin"`:

```go
	SourceTrust   = "trust" // the agent's workspace-trust question is on the screen (Options.TrustPattern)
```

In `internal/session/pattern.go`:

1. After `LineTracker.backspace` and before `// Last returns the text of the last line` (`:174-175`), add:

```go
// Reset forgets the line, not the place in an escape sequence.
func (t *LineTracker) Reset() { t.line = t.line[:0] }

```

2. After `LineTracker.Last` (`:182`), before `// PatternWatcher follows a terminal stream`, add:

```go
// maxScreenTail bounds the text ScreenTail keeps.
const maxScreenTail = 1024

// ScreenTail keeps the end of a terminal stream's text for a pattern that
// spans what a TUI draws with cursor moves between its words (a dialog drawn
// cell by cell): each escape sequence and each control character reads as one
// space, a run of white space as one space, and only the last 1024 bytes are
// kept. It keeps its place in a sequence split between two writes.
type ScreenTail struct {
	esc escScanner
	buf []byte // grows to 2*maxScreenTail, then keeps its last maxScreenTail bytes
}

// Write adds a chunk of the stream.
func (t *ScreenTail) Write(chunk []byte) {
	for _, b := range chunk {
		if !t.esc.text(b) || b <= ' ' || b == 0x7f {
			if n := len(t.buf); n > 0 && t.buf[n-1] != ' ' {
				t.push(' ')
			}
			continue
		}
		t.push(b)
	}
}

func (t *ScreenTail) push(b byte) {
	if len(t.buf) >= 2*maxScreenTail {
		n := copy(t.buf, t.buf[len(t.buf)-maxScreenTail:])
		t.buf = t.buf[:n]
	}
	t.buf = append(t.buf, b)
}

// Last returns the text kept, at most its last 1024 bytes.
func (t *ScreenTail) Last() string {
	b := t.buf
	if len(b) > maxScreenTail {
		b = b[len(b)-maxScreenTail:]
	}
	return string(b)
}

// Reset forgets the text kept, not the place in an escape sequence.
func (t *ScreenTail) Reset() { t.buf = t.buf[:0] }

// textTracker is what a PatternWatcher matches: the last line (LineTracker)
// or the end of the screen's text (ScreenTail).
type textTracker interface {
	Write(chunk []byte)
	Last() string
	Reset()
}

```

3. In the `PatternWatcher` struct (`:203`), `tracker LineTracker` becomes `tracker textTracker`; `NewPatternWatcher` (`:214-216`) becomes:

```go
func NewPatternWatcher(re *regexp.Regexp, quiet time.Duration, fire func(line string)) *PatternWatcher {
	return &PatternWatcher{re: re, quiet: quiet, fire: fire, tracker: &LineTracker{}}
}

// NewScreenWatcher is NewPatternWatcher matching the end of the screen's text
// (ScreenTail) rather than its last line: fire gets that text.
func NewScreenWatcher(re *regexp.Regexp, quiet time.Duration, fire func(text string)) *PatternWatcher {
	return &PatternWatcher{re: re, quiet: quiet, fire: fire, tracker: &ScreenTail{}}
}
```

4. Before `// Stop ends the watching.` (`:258`), add:

```go
// Reset forgets the text fed so far: the next fire needs a match in what is
// fed from now on.
func (w *PatternWatcher) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tracker.Reset()
	w.checked = w.feeds
}

```

- [ ] **Step 5: The session's side.** In `internal/session/local.go`:

1. Imports: add `"bytes"` before `"context"`.

2. At the end of `Options` (after the `Pattern *regexp.Regexp` field, `:68`), add:

```go
	// TrustPattern, when set, matches the agent's workspace-trust question in
	// the text of its screen, each escape sequence read as a space
	// (ScreenTail), after patternQuiet without output other than title
	// updates. A match marks the session needs_input (source "trust", kind
	// "prompt", the matched words as the message) unless it waits already,
	// until the first submission's Enter: the run engine types no prompt
	// while it shows, and a person answers the question.
	TrustPattern *regexp.Regexp
	// SubmitPause is the pause between a submission's text and its Enter;
	// SubmitPause when zero.
	SubmitPause time.Duration
	// ConfirmSubmit says the agent reports taking a prompt (Claude Code's
	// UserPromptSubmit hook reports working): a submission that asks for it
	// (Submission.Confirm) and is not taken within ConfirmWait gets one more
	// Enter.
	ConfirmSubmit bool
	// ConfirmWait is that wait; ConfirmWait when zero.
	ConfirmWait time.Duration
```

3. In the `Local` struct, after `pattern        *PatternWatcher // nil without Options.Pattern` add `trust          *PatternWatcher // nil without Options.TrustPattern`, and replace its last field `ended chan struct{}` with:

```go
	ended chan struct{}

	// submitting holds a token while a submission runs (Submit): one at a
	// time per session.
	submitting chan struct{}
	// paste follows the program's bracketed-paste mode (Submit, BracketedPaste).
	paste pasteMode
	// attnChanged is closed and replaced, under mu, on every attention change:
	// Submit waits on it for the agent to take a prompt.
	attnChanged chan struct{}
```

4. In `NewLocal`, after the `StopGrace` default:

```go
	if opts.SubmitPause <= 0 {
		opts.SubmitPause = SubmitPause
	}
	if opts.ConfirmWait <= 0 {
		opts.ConfirmWait = ConfirmWait
	}
```

after `ended: make(chan struct{}),` in the literal:

```go

		submitting:  make(chan struct{}, 1),
		attnChanged: make(chan struct{}),
```

and after the `opts.Pattern` block:

```go
	if opts.TrustPattern != nil {
		s.trust = NewScreenWatcher(opts.TrustPattern, patternQuiet, s.fireTrust)
	}
```

5. In `pump` (`:148-161`), replace

```go
			s.mu.Lock()
			s.ring.Write(chunk)
			s.hub.Broadcast(proto.Encode(proto.TypeOutput, chunk))
			s.lastOutput = time.Now()
			s.mu.Unlock()
			s.scanOutput(chunk)
			if s.pattern != nil {
				s.pattern.Feed(chunk)
			}
```

with

```go
			// Output that only sets the window title (a spinner in it) does
			// not count as output for the quiet the run engine and the
			// trust watcher wait for.
			title := titleOnly.Match(chunk)
			s.mu.Lock()
			s.ring.Write(chunk)
			s.hub.Broadcast(proto.Encode(proto.TypeOutput, chunk))
			if !title {
				s.lastOutput = time.Now()
			}
			s.mu.Unlock()
			s.paste.feed(chunk)
			s.scanOutput(chunk)
			if s.pattern != nil {
				s.pattern.Feed(chunk)
			}
			if s.trust != nil && !title {
				s.trust.Feed(chunk)
			}
```

6. In `markEnded`, after the `s.pattern.Stop()` block add

```go
	if s.trust != nil {
		s.trust.Stop()
	}
```

and in the clear Task 1 added, `s.info.Attention = Attention{}` is followed by `s.signalAttention()`.

7. After `firePattern` (`:318-320`) add:

```go
// fireTrust is the trust watcher's fire: the agent's workspace-trust question
// is on the screen, text its last ScreenTail. The session needs input, with
// the question's words, unless it waits already.
func (s *Local) fireTrust(text string) {
	words := s.opts.TrustPattern.FindString(text)
	if words == "" {
		return
	}
	s.setAttention(AttentionNeedsInput, words, SourceTrust, KindPrompt, nil, unlessWaiting)
}

// signalAttention wakes whoever waits for an attention change (Submit). The
// caller holds s.mu.
func (s *Local) signalAttention() {
	close(s.attnChanged)
	s.attnChanged = make(chan struct{})
}
```

8. In `setAttention` (`:422-423`), `s.info.Attention = att` is followed by `s.signalAttention()`, before the broadcast.

9. Replace `ErrTextTooLong` (`:623-625`) with:

```go
// ErrTextTooLong refuses text longer than one INPUT frame carries: Type's
// with its line break over proto.MaxInput bytes, a submission's over
// MaxSubmitText once cleaned. Nothing was written.
var ErrTextTooLong = errors.New("session: text to type is longer than one input frame")
```

10. Replace `write` as Task 1 left it (from its comment `// write writes data to the process` to its closing brace) with the version that shares the answer with `Submit`, and add `stampTyping` and `answer` after it:

```go
// write writes data to the process, for sub, a controller's subscription, or,
// with sub nil, for Type as byName. It answers the needs_input prompt that was
// showing when it began (answer), and records an input entry when it does, or
// always for Type. A controller's write that is only a terminal's report
// answers nothing. With skipWhileWaiting it writes nothing while that prompt
// shows, and reports whether it wrote. (Task 4 replaces Type and this with
// Submit.)
func (s *Local) write(data []byte, sub *Subscription, byName string, skipWhileWaiting bool) (bool, error) {
	s.mu.Lock()
	ended := s.info.Status.Ended()
	// The prompt this input answers is the one on the screen as it is typed.
	// A process that reacts before Write returns (an echo that rings the bell)
	// may raise the next one meanwhile, and typing must not clear that one.
	// Each attention change gets a new Since, which tells the prompts apart.
	promptSince := s.info.Attention.Since
	showing := s.info.Attention.State == AttentionNeedsInput
	s.mu.Unlock()
	if ended {
		return false, ErrSessionEnded
	}
	if skipWhileWaiting && showing {
		return false, nil
	}
	if _, err := s.proc.Write(data); err != nil {
		return false, err
	}
	if sub == nil {
		s.Record(ActivityEntry{Type: ActivityInput, ByName: byName, Message: strings.TrimRight(string(data), "\r\n")})
		s.answer(promptSince, "", byName, false, true)
		return true, nil
	}
	if isTerminalReport(data) {
		// A terminal's own report (xterm answering a cursor position query)
		// is written and answers nothing; nor is it a person typing.
		return true, nil
	}
	s.stampTyping(sub)
	s.answer(promptSince, sub.ID, sub.Name, true, bytes.IndexByte(data, '\r') >= 0)
	return true, nil
}

// stampTyping marks sub as typing now and refreshes the roster at most every
// 2 s (presence).
func (s *Local) stampTyping(sub *Subscription) {
	now := time.Now().UnixMilli()
	if prev := sub.lastInput.Swap(now); now-prev > 2000 {
		s.mu.Lock()
		s.hub.Broadcast(s.viewersFrame())
		s.mu.Unlock()
	}
}

// answer settles the needs_input prompt a write answered: the one stamped
// promptSince, when it is still showing, is cleared and recorded as answered
// by by (a subscriber ID, "" for Conductor) and byName. A trust question is
// answered only by a write with Enter in it (enter): an arrow key that moves
// its selection leaves it showing, so that no prompt is typed into it. First
// reply wins: the check, the answer and the clear share one critical
// section, so two concurrent typists cannot both claim the prompt. With
// record, an input entry with the question records the answer.
func (s *Local) answer(promptSince *time.Time, by, byName string, record, enter bool) {
	s.mu.Lock()
	att := s.info.Attention
	waiting := att.State == AttentionNeedsInput && att.Since == promptSince && (enter || att.Source != SourceTrust)
	question := att.Message
	if waiting {
		s.info.LastAnswer = &Answer{By: by, ByName: byName, At: time.Now().UTC(), Message: question}
		s.info.Attention = Attention{State: AttentionNone, Source: SourceInput}
		s.signalAttention()
		s.hub.Broadcast(proto.MustControl(attentionMessage(s.info.Attention)))
	}
	s.mu.Unlock()
	if !waiting {
		return
	}
	if att.Source == SourceTrust && s.trust != nil {
		// What showed the question is behind: only a new one counts.
		s.trust.Reset()
	}
	if record {
		s.Record(ActivityEntry{Type: ActivityInput, By: by, ByName: byName, Message: question})
	}
	s.notifyChange()
}
```

(`Type`'s and `TypeUnlessWaiting`'s bodies stay; their doc comments say ErrTextTooLong as before.)

- [ ] **Step 6: Run the session tests**

Run: `go test -race -count=1 ./internal/session`
Expected: PASS, the tests of Task 1 and the existing `Type` tests included.

- [ ] **Step 7: The `submit` message.** In `internal/proto/control.go`, add `CtlSubmit  = "submit"` after `CtlFileGet = "file_get"` (client → owner), and after the `Resize` struct (`:80-87`):

```go
// Submit asks the owner to submit Text as a line, as a reply box does: the
// text, as a paste when the program asks for one, then Enter on its own after
// a pause (session.Local.Submit). Controllers only; Text is at most MaxSubmit
// bytes.
type Submit struct {
	T    string `json:"t"`
	Text string `json:"text"`
}

// MaxSubmit bounds the text of a submit message, in bytes: a reply box's line.
const MaxSubmit = 4096
```

In `internal/api/ws_viewer.go`, add `"time"` to the imports (after `"net/http"`), before `func (s *Server) sendInputError(` (`:105`):

```go
// submitTimeout bounds a submit message's submission: its turn, the pause
// and its Enter.
const submitTimeout = 10 * time.Second

```

and in `handleLocalControl`, before `case proto.CtlHello:` (`:156`):

```go
	case proto.CtlSubmit:
		var m proto.Submit
		if json.Unmarshal(payload, &m) != nil {
			return false
		}
		if len(m.Text) > proto.MaxSubmit {
			local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "submit text too long"))
			return true
		}
		// The submission goes on when the client goes: a reply box closes its
		// connection once it has sent, and a line cut in its pause would wait
		// without its Enter. The read loop waits for it, so what the client
		// sends next comes after it.
		ctx, cancel := context.WithTimeout(context.Background(), submitTimeout)
		_, err := local.Submit(ctx, session.Submission{Text: m.Text, By: sub})
		cancel()
		if err != nil {
			s.sendInputError(sub, local, err)
		}
```

(`sendInputError` maps `ErrReadOnly` to `read_only` and `ErrSessionEnded` to `session_ended`.)

In `internal/hostagent/peer.go`, add `"context"` before `"encoding/json"` and `"time"` after `"sync"` in the imports, and in `handleFrame` after the `case proto.CtlPing:` block (`:205-208`):

```go
		case proto.CtlSubmit:
			var m proto.Submit
			if json.Unmarshal(f.Payload, &m) != nil || len(m.Text) > proto.MaxSubmit {
				p.a.local.Send(sub, proto.NewError(proto.ErrCodeBadFrame, "bad submit message"))
				return
			}
			// Off the frame loop, which also carries the relay's other
			// viewers: the submission pauses before its Enter.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := p.a.local.Submit(ctx, session.Submission{Text: m.Text, By: sub}); err != nil {
					code := "input_failed"
					switch {
					case errors.Is(err, session.ErrReadOnly):
						code = proto.ErrCodeReadOnly
					case errors.Is(err, session.ErrSessionEnded):
						code = proto.ErrCodeSessionEnded
					}
					p.a.local.Send(sub, proto.NewError(code, err.Error()))
				}
			}()
```

In `internal/signal/hosted.go` `RelayToHost` (`:457`), a view-role viewer's `submit` is refused on the relay as its `resize` is:

```go
			if t, _ := proto.ParseHeader(inner.Payload); t == proto.CtlResize || t == proto.CtlSubmit {
```

and in `internal/signal/signal_test.go` `TestRegisterResumeAndRelayRules`, after the view-resize check (`:69-72`):

```go
	submit := proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "x"})
	if err := hs.RelayToHost(v, proto.Frame{Type: proto.TypeControl, Payload: submit[1:]}); !errors.Is(err, session.ErrReadOnly) {
		t.Fatalf("view submit: %v", err)
	}
```

Append to `internal/api/ws_e2e_test.go`:

```go
// A submit message types its text and then Enter, apart, for a controller;
// a view link's is refused, and an over-long one is a bad frame.
func TestSubmitMessageTypesALine(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	ctl := dialViewer(t, e, id, adminToken)
	ctl.hello(0, 0)
	ctl.expectControl(proto.CtlReady)
	ctl.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "hello there"}))
	ctl.expectOutput("hello there")
	if e.local(id).Info().Attention.State != "" {
		t.Fatal("attention changed")
	}
	ctl.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: strings.Repeat("x", proto.MaxSubmit+1)}))
	if m := ctl.expectControl(proto.CtlError); m["code"] != proto.ErrCodeBadFrame {
		t.Fatalf("long: %v", m)
	}
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	v := dialViewer(t, e, id, lo["token"].(string))
	v.hello(0, 0)
	v.expectControl(proto.CtlReady)
	v.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "nope"}))
	if m := v.expectControl(proto.CtlError); m["code"] != proto.ErrCodeReadOnly {
		t.Fatalf("view: %v", m)
	}
}
```

Run: `go test -race -count=1 ./internal/api ./internal/hostagent ./internal/signal ./internal/proto`
Expected: PASS.

- [ ] **Step 8: The reply boxes.** In `web/app/utils/protocol.ts`, after `FOLLOW_SIZE` (Task 1):

```ts
/** The longest line a `submit` control message carries, in bytes (proto.MaxSubmit). */
export const MAX_SUBMIT = 4096
```

In `web/app/utils/transport/types.ts`, after `sendInput(data: Uint8Array): void`:

```ts
  /**
   * Submits a line as a reply box does: the owner types it (as a paste when the program asks for one) and presses
   * Enter 250 ms later, so that a TUI takes the Enter as Enter. Controllers only; at most MAX_SUBMIT bytes.
   */
  submit(text: string): void
```

In `web/app/utils/transport/base.ts`, after `sendInput` (`:54-57`):

```ts
  submit(text: string): void {
    if (this.state.value !== 'open') return
    this.send(encodeControl({ t: 'submit', text }))
  }
```

In `web/app/components/TerminalView.vue`, after `sendInput` (`:260-265`):

```ts
/** Submits a line through the session's owner (a paste, then Enter); false when the transport is not open. */
function submit(text: string): boolean {
  if (!transport || transport.state.value !== 'open') return false
  transport.submit(text)
  return true
}
```

and the expose line (`:267`) becomes `defineExpose({ connect, disconnect, requestFile, sendInput, submit, focus: () => term?.focus(), scrollToBottom: () => term?.scrollToBottom() })`.

Replace `web/app/composables/useQuickReply.ts` with:

```ts
import { encodeText, FOLLOW_SIZE } from '~/utils/protocol'
import type { TerminalTransport } from '~/utils/transport/types'
import type { SessionInfo } from './useSessions'

/**
 * Sends to a session without opening its terminal: a short-lived control connection over the existing transports
 * (relay for hosted sessions, so no WebRTC negotiation). The hello follows the session's size (FOLLOW_SIZE), so the
 * round trip never resizes it. `reply` submits a line (the owner types it and presses Enter 250 ms later, which a TUI
 * takes as Enter); `send` writes keys as they are, for a quick-reply option such as a digit.
 */
export function useQuickReply() {
  const { create } = useTerminalTransport()
  const admin = useAdminToken()
  const sending = useState<Set<string>>('quickReplySending', () => new Set())

  function mark(id: string, on: boolean) {
    const next = new Set(sending.value)
    if (on) next.add(id)
    else next.delete(id)
    sending.value = next
  }

  async function over(session: SessionInfo, token: string | undefined, act: (t: TerminalTransport) => void): Promise<void> {
    if (sending.value.has(session.id)) return
    mark(session.id, true)
    const t = create({ sessionId: session.id, token: token ?? admin.token.value, kind: session.kind, forceRelay: true })
    try {
      const welcome = await t.connect(FOLLOW_SIZE)
      if (welcome.role !== 'control') throw new Error('This link is view-only')
      act(t)
      // The owner finishes a submission after the connection goes; this only lets the frame leave.
      await new Promise((r) => setTimeout(r, 150))
    } finally {
      t.close()
      mark(session.id, false)
    }
  }

  function send(session: SessionInfo, input: string, opts: { token?: string } = {}): Promise<void> {
    return over(session, opts.token, (t) => t.sendInput(encodeText(input)))
  }

  function reply(session: SessionInfo, text: string, opts: { token?: string } = {}): Promise<void> {
    return over(session, opts.token, (t) => t.submit(text))
  }

  return { send, reply, sending }
}
```

`web/app/pages/sessions/[id].vue`: the `terminal` ref's type (`:93`) gains `submit: (t: string) => boolean`, and `reply` (`:188-190`) becomes:

```ts
function reply(text: string) {
  if (!terminal.value?.submit(text)) toast.add({ title: 'Not connected', description: 'Reconnect the terminal and try again.', color: 'warning' })
}
```

`web/app/pages/join/[token].vue`: the `terminal` ref's type (`:28`) gains `submit: (t: string) => boolean`, and `reply` (`:134-136`) becomes:

```ts
function reply(text: string) {
  if (!terminal.value?.submit(text)) toast.add({ title: 'Not connected', color: 'warning' })
}
```

`web/app/pages/wall.vue` `reply` (`:127-129`) becomes:

```ts
function reply(s: SessionInfo, text: string) {
  quick.reply(s, text).catch(fail(`Reply to ${s.name} failed`))
}
```

(`option` keeps `quick.send(s, o.input)`: an option's input is keys, a digit with no Enter for Claude Code's permission dialog.)

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: PASS.

- [ ] **Step 9: `docs/protocol.md`.** In the client → owner table, after the `file_get` row (`:39`):

```
| `submit` | `text` | Controllers only; `text` at most 4096 bytes (`bad_frame` beyond, `read_only` from a view link, also on the relay). The owner submits it as a line: line breaks and tabs become spaces and other control characters are dropped, the text is written as a bracketed paste while the program has that mode on (`ESC[?2004h`), then a carriage return is written on its own 250 ms later, so that a TUI takes it as Enter rather than as part of a paste. The Enter answers the prompt that was showing when the text was typed; a prompt raised during the pause is not answered and the Enter is left out. The submission goes on if the client disconnects meanwhile. The reply boxes use it; keys typed into the terminal stay INPUT |
```

and in "Hosted sessions: signaling and relay" (`:88`), "View-role INPUT and `resize` are dropped by the server before they reach the host." becomes "View-role INPUT, `resize` and `submit` are dropped by the server before they reach the host (`read_only`)."

- [ ] **Step 10: Check and commit**

Run: `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test`
Expected: clean, PASS.

```bash
git add internal/session/submit.go internal/session/submit_test.go internal/session/local.go internal/session/attention.go internal/session/pattern.go internal/session/report_test.go internal/proto/control.go internal/api/ws_viewer.go internal/api/ws_e2e_test.go internal/hostagent/peer.go internal/signal/hosted.go internal/signal/signal_test.go web/app/utils/protocol.ts web/app/utils/transport/types.ts web/app/utils/transport/base.ts web/app/components/TerminalView.vue web/app/composables/useQuickReply.ts web/app/pages/sessions/[id].vue web/app/pages/join/[token].vue web/app/pages/wall.vue docs/protocol.md
git commit -m "session: a submission is a paste, then Enter 250 ms later; the reply boxes submit; the trust question is watched"
```

**Done when:**

- `internal/session/submit.go`: `Local.Submit` types the text as a bracketed paste when the program has that mode on, then a carriage return of its own `SubmitPause` (250 ms) later; one submission at a time per session, no session lock across the pause, cancellable; when a question came up in the pause the Enter is left out and the text is never sent again; `BeforeEnter` runs just before the Enter; an agent that confirms a prompt gets one more Enter when no `working` report follows within `ConfirmWait` (3 s). `submit_test.go` covers each.
- The session follows the program's bracketed-paste mode (`BracketedPaste`), title-only output does not move `LastOutputAt`, and the trust watcher (`ScreenTail`, `NewScreenWatcher`, `SourceTrust`) raises needs input with the question's words, answered only by a write with Enter in it.
- `Type` and `TypeUnlessWaiting` still exist (Task 4 removes them); a person's keystrokes (`Input`) stay raw.
- The `submit` control message (`proto.CtlSubmit`, `Submit`, `MaxSubmit` = 4096) is handled in `ws_viewer.go` and `hostagent/peer.go`, refused from a view link (on the relay too), and tested.
- The reply boxes (the quick reply, the session page's and the join page's reply bar, the wall's) submit through `TerminalView`'s `submit`; an option's input still goes through `quick.send` as keys.
- `docs/protocol.md` has the `submit` row and the relay sentence.
- `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test` pass.
- Committed: "session: a submission is a paste, then Enter 250 ms later; the reply boxes submit; the trust question is watched".

---

### Task 3: Yolo and the trust prompt in the catalog and the launch

**Order:** after Task 2, or in parallel with Tasks 1 and 2 in a separate worktree, merged after them (it wires Task 2's `TrustPattern` and `ConfirmSubmit` into the session options). Its care points are the launch path's environment rules and Claude Code's settings files.

`yolo: true` in the config, `CONDUCTOR_YOLO=1` and `conductor serve --yolo` make every launched agent skip its permission prompts through its recipe; a launch (`POST /api/sessions` `yolo`) and a crew (`yolo`) override the server's default, and a crew's effective choice is fixed on its run so members started or added later follow it. The recipe is catalog data — `yolo: {args, env}` on an agent, bounded like the command — applied in the one launch path, `createLocalSession`: its arguments after the command and the launch's own, its environment over the agent's own through `pty.BuildEnv`'s filtered `set` (never the inject map; a recipe cannot name `CONDUCTOR_*`). Two agents need more than data, and get it from their adapters: Claude Code honours only the last `--settings`, so the key that skips its "Bypass Permissions mode" warning (`skipDangerousModePermissionPrompt`) rides in the hooks settings file (`claude-yolo.json`, `claude-tools-yolo.json`, or `claude-yolo-only.json` when the hooks are not wired); Codex takes a per-launch trust override, `-c projects={"<repo>"={trust_level="trusted"}}`, which is added only with the recipe, for the launch directory's repository top and, for a worktree, the main repository's (Codex resolves a worktree's trust to it) — argv, never a shell string, every path a TOML string. The session says whether a recipe was applied (`Info.yolo`); an agent with no recipe is launched as usual, with a notice in its activity (and the run log), and no badge. The same task adds the catalog's `trustPrompt` (the words of an agent's workspace-trust question, which Task 2's watcher matches) and wires it, with the adapter's `ConfirmsSubmit` (Claude Code), into the session's options.

Spec/code note: the yolo research proposed masking the recipe's environment in `Redacted`; the spec's recipes carry flags, not secrets (`COPILOT_ALLOW_ALL=true`, `GOOSE_MODE=auto`), so `Redacted` is left as it is and the README says not to put a secret in a recipe. The research's `Member.Yolo` is not built: the spec gives the switch to launches and crews.

**Files:**
- Modify: `internal/catalog/catalog.go` — the `Agent` struct (`:21-42`), the bounds (`:106-115`), `validate` (before the envPassthrough checks `:249`), after `validate` (before `// cut keeps` `:263`), `inherit` (`:388-399`), `Agent.clone` (`:435-444`); `internal/catalog/defaults.go` (the claude … dsh entries); `internal/catalog/catalog_test.go` (append); `internal/agents/adapter.go` (`:21-23`); `internal/agents/registry.go` (after `All` `:65`, `InjectFor` `:74-98`); `internal/agents/claude.go` (imports, `:20-30`, the adapter literal `:41-47`); `internal/agents/codex.go` (imports, the adapter literal `:73-75`); `internal/agents/adapter_test.go`, `assets_test.go` (every `InjectFor` call; append); `internal/hostagent/hooks.go` (`:54`); `internal/config/config.go` (`:110-111`, `:215-217`), `config_test.go` (append); `internal/cli/serve.go` (`:34`, `:67-69`), `internal/cli/completion.go` (`:98`), `internal/cli/up.go` (`:110-112`, `:133`), `internal/cli/up_test.go` (append); `internal/session/info.go` (`:60-63`); `internal/crew/crew.go` (`Crew` `:99-100`); `internal/crew/worktree.go` (append), `worktree_test.go` (imports, append); `internal/crew/run.go` (`Launcher` `:33-35`, `Run` `:113-128`, `run` `:172-176`, `add` `:355-356`, `launch` `:466-467`, `snapshot` `:1015`); `internal/crew/run_test.go` (`launchCall`, `fakeLauncher.Launch`, append); `internal/api/sessions.go` (imports, `createSessionRequest` `:26-37`, `createLocalSession` `:116-131`, `:158-185`, `:193-195`, after it); `internal/api/runs.go` (`Launch` `:43-54`, `handleLaunchCrew` `:137`); `internal/api/crews.go` (`crewInput` `:30-39`, `:194-197`); `internal/api/catalog.go` (`handleCatalog` `:86`); `internal/api/api_test.go` (`:828`)
- Create: `internal/api/yolo_test.go`

**Interfaces:**
- Consumes: Task 2's `session.Options.TrustPattern`, `ConfirmSubmit`; `catalog.CompilePattern`, `envNamePattern`, `maxCommandArg`, `maxEnvKey`, `cut`; `agents.tomlString`, `jsonAsset`, `hookLists`; `crew.git`; `api.gitCheckTimeout` (`paths.go`), test helpers `agentBody`, `e.save`, `e.launch`, `e.local`, `dialViewer`, `realRoot`, `e.sendCrew`, `e.waitRunning`, `errorCode`.
- Produces:
```go
// internal/catalog
type Yolo struct {
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}
func (y *Yolo) Empty() bool
// Agent gains: Yolo *Yolo `json:"yolo,omitempty"`; TrustPrompt string `json:"trustPrompt,omitempty"`
// internal/agents
// Adapter gains: YoloInject func(hooksDir string, sig catalog.Signal) ([]string, map[string]string); TrustArgs func(dirs []string) []string; ConfirmsSubmit bool
func InjectFor(agentID string, hooksDir string, sig catalog.Signal, yolo bool) ([]string, map[string]string)
func TrustArgsFor(agentID string, dirs func() []string) []string
func ConfirmsSubmit(agentID string, sig catalog.Signal) bool
// internal/config: Config.Yolo bool `json:"yolo"` (CONDUCTOR_YOLO 1/true, 0/false)
// internal/session: Info.Yolo bool `json:"yolo,omitempty"`
// internal/crew
type LaunchSpec struct {
	AgentID, Name, Cwd string
	Args               []string
	Env                map[string]string
	Ref                session.CrewRef
	Yolo               bool
}
type Launcher interface{ Launch(ctx context.Context, spec LaunchSpec) (*session.Local, error) }
// Crew gains Yolo *bool `json:"yolo,omitempty"`; Run gains Yolo bool `json:"yolo"`
func RepoRoots(ctx context.Context, dir string) []string
// internal/api: createSessionRequest gains Yolo *bool `json:"yolo,omitempty"`; GET /api/catalog adds "yoloDefault"
func noYoloRecipe(agent catalog.Agent) string
```

- [ ] **Step 1: Write the failing catalog tests.** Append to `internal/catalog/catalog_test.go` (it imports `maps`, `slices`, `strings` already):

```go
// A yolo recipe is bounded like the command and the environment it adds to,
// and never names Conductor's own variables, which BuildEnv would drop.
func TestYoloValidation(t *testing.T) {
	ok := Agent{ID: "x", Name: "X", Command: []string{"x"}, Yolo: &Yolo{Args: []string{"--yes"}, Env: map[string]string{"X_MODE": "auto"}}}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, y := range []Yolo{
		{Args: []string{""}},
		{Args: []string{"a\x00b"}},
		{Args: []string{strings.Repeat("a", 4097)}},
		{Args: slices.Repeat([]string{"-y"}, 17)},
		{Env: map[string]string{"CONDUCTOR_NOTIFY_TOKEN": "x"}},
		{Env: map[string]string{"BAD NAME": "x"}},
		{Env: map[string]string{"X": strings.Repeat("v", 4097)}},
		{Env: map[string]string{"X": "a\x00"}},
	} {
		a := ok
		a.Yolo = &y
		if err := validate(a); err == nil || !strings.Contains(err.Error(), "yolo") {
			t.Errorf("%+v: %v", y, err)
		}
	}
	empty := ok
	empty.Yolo = &Yolo{}
	if err := validate(empty); err != nil || !empty.Yolo.Empty() || !(*Yolo)(nil).Empty() {
		t.Fatalf("an empty recipe: %v", err)
	}
	bad := ok
	bad.TrustPrompt = ".*"
	if err := validate(bad); err == nil || !strings.Contains(err.Error(), "trustPrompt") {
		t.Fatalf("a trust prompt matching nothing: %v", err)
	}
}

// A saved override that leaves the recipe out takes the replaced agent's, an
// empty one has none, and one of its own replaces it whole; the trust prompt
// is inherited too. Redacted leaves the recipe as it is: its values are
// flags, not secrets.
func TestOverlayInheritsTheYoloRecipe(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{Agents: []Agent{
		{ID: "claude", Name: "Claude, mine", Command: []string{"claude"}},
		{ID: "codex", Name: "Codex", Command: []string{"codex"}, Yolo: &Yolo{}},
		{ID: "copilot", Name: "Copilot", Command: []string{"copilot"}, Yolo: &Yolo{Args: []string{"--allow-all-tools"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	claude, _ := c.Get("claude")
	if claude.Yolo == nil || !slices.Equal(claude.Yolo.Args, []string{"--dangerously-skip-permissions"}) || claude.TrustPrompt == "" {
		t.Fatalf("claude: %+v %q", claude.Yolo, claude.TrustPrompt)
	}
	if codex, _ := c.Get("codex"); !codex.Yolo.Empty() {
		t.Fatalf("codex: %+v", codex.Yolo)
	}
	copilot, _ := c.Get("copilot")
	if !slices.Equal(copilot.Yolo.Args, []string{"--allow-all-tools"}) || copilot.Yolo.Env != nil {
		t.Fatalf("copilot: %+v", copilot.Yolo)
	}
	if r := copilot.Redacted(); r.Yolo == nil || r.Yolo.Env != nil || !slices.Equal(r.Yolo.Args, copilot.Yolo.Args) {
		t.Fatalf("redacted: %+v", r.Yolo)
	}
	base, _ := Default().Get("copilot")
	if r := base.Redacted(); r.Yolo.Env["COPILOT_ALLOW_ALL"] != "true" {
		t.Fatalf("a recipe's env is not a secret: %+v", r.Yolo)
	}
	cp := c.Clone()
	got, _ := cp.Get("claude")
	got.Yolo.Args[0] = "changed"
	if again, _ := c.Get("claude"); again.Yolo.Args[0] != "--dangerously-skip-permissions" {
		t.Fatal("a clone shares the recipe")
	}
}

// The built-ins' recipes, as the yolo research found them (docs/features.md,
// the adapter matrix): pi and the shell have none.
func TestDefaultYoloRecipes(t *testing.T) {
	want := map[string]Yolo{
		"claude":   {Args: []string{"--dangerously-skip-permissions"}},
		"codex":    {Args: []string{"--dangerously-bypass-approvals-and-sandbox"}},
		"agy":      {Args: []string{"--dangerously-skip-permissions"}},
		"copilot":  {Args: []string{"--yolo"}, Env: map[string]string{"COPILOT_ALLOW_ALL": "true"}},
		"cursor":   {Args: []string{"--yolo", "--trust"}},
		"opencode": {Args: []string{"--auto"}},
		"omp":      {Args: []string{"--yolo"}},
		"aider":    {Args: []string{"--yes-always"}},
		"goose":    {Env: map[string]string{"GOOSE_MODE": "auto"}},
		"amp":      {Args: []string{"--dangerously-allow-all"}},
		"dsh":      {Env: map[string]string{"DSH_PERMISSION_MODE": "danger-full-access"}},
	}
	for _, a := range defaults() {
		w, ok := want[a.ID]
		if !ok {
			if !a.Yolo.Empty() {
				t.Errorf("%s has a recipe: %+v", a.ID, a.Yolo)
			}
			continue
		}
		if a.Yolo == nil || !slices.Equal(a.Yolo.Args, w.Args) || !maps.Equal(a.Yolo.Env, w.Env) {
			t.Errorf("%s: %+v, want %+v", a.ID, a.Yolo, w)
		}
	}
	// The words the CLIs drew in the prompt investigation's captures (Claude
	// Code 2.1.287, Codex 0.159.0), as the trust watcher reads a screen: each
	// escape sequence a space.
	for id, tc := range map[string]struct{ screen, words string }{
		"claude": {"root/c1 Quick safety check: Is this a project you created or one you trust? (Like your own code", "Is this a project you created or one you trust?"},
		"codex":  {"root/x1 Trust this folder? Codex can read, edit, and run files here", "Trust this folder?"},
	} {
		a, _ := Default().Get(id)
		re, err := CompilePattern(a.TrustPrompt)
		if words := tc.words; err != nil || re.FindString(tc.screen) != words {
			t.Errorf("%s's trust prompt %q does not find %q: %v", id, a.TrustPrompt, tc.words, err)
		}
	}
}
```

Run: `go test ./internal/catalog -run 'Yolo'`
Expected: FAIL to compile, `undefined: Yolo`.

- [ ] **Step 2: The catalog.** In `internal/catalog/catalog.go`, the `Agent` struct ends (after `Signal *Signal`, `:41`) with:

```go
	// Yolo is the agent's yolo recipe: what a launch with yolo on adds to
	// skip the agent's permission prompts. nil in a saved override takes the
	// replaced agent's; an empty recipe ({}) has none, so yolo does nothing
	// for the agent.
	Yolo *Yolo `json:"yolo,omitempty"`
	// TrustPrompt matches the agent's workspace-trust question in the text of
	// its screen (escape sequences read as spaces): while it shows, a crew
	// run types no prompt and the session needs a person's answer. RE2, at
	// most 200 bytes, never matching an empty text; empty in a saved
	// override takes the replaced agent's.
	TrustPrompt string `json:"trustPrompt,omitempty"`
}

// Yolo is an agent's yolo recipe: Args go after the agent's command and the
// launch's own arguments, Env over the agent's own environment, through the
// same filtered environment (never as Conductor's own variables).
type Yolo struct {
	Args []string          `json:"args,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// Empty reports whether y adds nothing to a launch: nil, or no arguments and
// no environment.
func (y *Yolo) Empty() bool { return y == nil || (len(y.Args) == 0 && len(y.Env) == 0) }

// clone returns a copy of y that shares nothing with it.
func (y *Yolo) clone() *Yolo {
	if y == nil {
		return nil
	}
	return &Yolo{Args: slices.Clone(y.Args), Env: maps.Clone(y.Env)}
}
```

(the struct's closing brace is the one shown first.) In the size limits (`:113-114`), the signal pattern's comment becomes `// bytes in a signal pattern or a trust prompt` and after `maxSite` add:

```go
	maxYoloArgs       = 16   // elements in yolo.args
	maxYoloEnv        = 16   // entries in yolo.env
	maxYoloValue      = 4096 // bytes in a yolo.env value
```

In `validate`, before `if len(a.EnvPassthrough) > maxEnvPassthrough {` (`:249`):

```go
	if a.Yolo != nil {
		if err := validateYolo(*a.Yolo); err != nil {
			return fmt.Errorf("agent %s: yolo: %w", a.ID, err)
		}
	}
	if a.TrustPrompt != "" {
		if _, err := CompilePattern(a.TrustPrompt); err != nil {
			return fmt.Errorf("agent %s: trustPrompt: %w", a.ID, err)
		}
	}
```

Before `// cut keeps the first 40 bytes` (`:263`):

```go
// validateYolo bounds a yolo recipe like the command and the environment it
// adds to: at most 16 arguments of at most 4096 bytes without NUL, at most 16
// variables, each named like an envPassthrough entry, never CONDUCTOR_*,
// whose values have at most 4096 bytes without NUL. Its errors carry no agent
// id; validate adds it.
func validateYolo(y Yolo) error {
	if len(y.Args) > maxYoloArgs {
		return fmt.Errorf("too many args (at most %d)", maxYoloArgs)
	}
	for i, arg := range y.Args {
		if arg == "" || strings.ContainsRune(arg, 0) || len(arg) > maxCommandArg {
			return fmt.Errorf("args[%d] must be 1 to %d bytes without NUL", i, maxCommandArg)
		}
	}
	if len(y.Env) > maxYoloEnv {
		return fmt.Errorf("too many env entries (at most %d)", maxYoloEnv)
	}
	for k, v := range y.Env {
		if len(k) > maxEnvKey || !envNamePattern.MatchString(k) || strings.HasPrefix(k, "CONDUCTOR_") {
			return fmt.Errorf("invalid env name %q", cut(k))
		}
		if strings.ContainsRune(v, 0) || len(v) > maxYoloValue {
			return fmt.Errorf("env %s: the value must be at most %d bytes without NUL", k, maxYoloValue)
		}
	}
	return nil
}

```

In `inherit`, after `if a.Site == "" { a.Site = prev.Site }` (`:396-398`):

```go
		if a.Yolo == nil {
			a.Yolo = prev.Yolo.clone()
		}
		if a.TrustPrompt == "" {
			a.TrustPrompt = prev.TrustPrompt
		}
```

and add "a yolo recipe, a trust prompt" to its comment's list ("an adapter, a signal or a site it omits" → "an adapter, a signal, a site, a yolo recipe or a trust prompt it omits"). In `Agent.clone`, before `return a`: `a.Yolo = a.Yolo.clone()`.

In `internal/catalog/defaults.go`, add to the entries (after each one's `Signal:` line; leave `Site` alone, Task 10 fixes three of them):

```go
			// claude
			Yolo:        &Yolo{Args: []string{"--dangerously-skip-permissions"}},
			TrustPrompt: `Is\s*this\s*a\s*project\s*you\s*created\s*or\s*one\s*you\s*trust\?`,
			// codex
			Yolo:        &Yolo{Args: []string{"--dangerously-bypass-approvals-and-sandbox"}},
			TrustPrompt: `Trust\s*this\s*folder\?`,
			// agy
			Yolo: &Yolo{Args: []string{"--dangerously-skip-permissions"}},
			// copilot
			Yolo: &Yolo{Args: []string{"--yolo"}, Env: map[string]string{"COPILOT_ALLOW_ALL": "true"}},
			// cursor
			Yolo: &Yolo{Args: []string{"--yolo", "--trust"}},
			// opencode
			Yolo: &Yolo{Args: []string{"--auto"}},
			// omp
			Yolo: &Yolo{Args: []string{"--yolo"}},
			// aider
			Yolo: &Yolo{Args: []string{"--yes-always"}},
			// goose
			Yolo: &Yolo{Env: map[string]string{"GOOSE_MODE": "auto"}},
			// amp
			Yolo: &Yolo{Args: []string{"--dangerously-allow-all"}},
			// dsh
			Yolo: &Yolo{Env: map[string]string{"DSH_PERMISSION_MODE": "danger-full-access"}},
```

(the `// <id>` lines say which entry each goes in; they are not part of the code). pi has none: it has no permission system. The shell has none. The defaults comment gains: "Each built-in carries the yolo recipe the research found (docs/features.md, the adapter matrix: what each disables, verified live or from docs) and, for Claude Code and Codex, the words of its trust question as drawn by Claude Code 2.1.287 and Codex 0.159.0."

Run: `go test -race -count=1 ./internal/catalog`
Expected: PASS (the existing `TestDefaultsHaveSites` validates every built-in, recipes included).

- [ ] **Step 3: The adapters.** In `internal/agents/adapter.go`, after `Inject` (`:21-23`):

```go
	// YoloInject, when set, takes Inject's place for a launch with the
	// agent's yolo recipe applied, whatever the signal: Claude Code honours
	// only the last --settings, so the key that skips its bypass-permissions
	// warning rides in the hooks settings file, or in a file of its own when
	// the hooks are not wired.
	YoloInject func(hooksDir string, sig catalog.Signal) (argv []string, env map[string]string)
	// TrustArgs, when set, returns the arguments that trust dirs for one
	// launch without writing the agent's configuration (Codex's -c projects
	// override). A launch adds them only with the yolo recipe applied.
	TrustArgs func(dirs []string) []string
	// ConfirmsSubmit says the agent's hooks report working as it takes a
	// prompt (Claude Code's UserPromptSubmit): with the hook signal, a role
	// prompt not taken within session.ConfirmWait gets one more Enter.
	ConfirmsSubmit bool
```

In `internal/agents/registry.go`, after `All` (`:58-65`):

```go
// TrustArgsFor returns the arguments that trust the directories dirs gives
// for one launch of the adapter agentID (Adapter.TrustArgs), nil when it has
// none or there are no directories. dirs is called only for an adapter that
// trusts per launch: it asks git.
func TrustArgsFor(agentID string, dirs func() []string) []string {
	a, ok := Get(agentID)
	if !ok || a.TrustArgs == nil {
		return nil
	}
	if d := dirs(); len(d) > 0 {
		return a.TrustArgs(d)
	}
	return nil
}

// ConfirmsSubmit reports whether the adapter agentID reports taking a prompt
// with the agent's signal (Adapter.ConfirmsSubmit, through its hooks).
func ConfirmsSubmit(agentID string, sig catalog.Signal) bool {
	a, ok := Get(agentID)
	return ok && a.ConfirmsSubmit && sig.Kind == catalog.SignalHook
}
```

and replace `InjectFor` (`:74-98`, from its comment to its closing brace) with:

```go
// InjectFor returns the flags to append to an agent's command and the
// environment to add to its session, for the adapter agentID and the agent's
// signal. Only the "hook" signal is wired at launch, except with yolo (the
// agent's yolo recipe applied) for an adapter with YoloInject, which takes
// Inject's place whatever the signal. Any other signal, an unknown or empty
// adapter, an adapter with no launch route and a hooks dir that is not
// absolute (the flags name files in it, and a relative path would be read
// from the session's working directory) all give nil, nil.
func InjectFor(agentID string, hooksDir string, sig catalog.Signal, yolo bool) ([]string, map[string]string) {
	if !filepath.IsAbs(hooksDir) {
		return nil, nil
	}
	for _, a := range registry {
		if a.ID != agentID {
			continue
		}
		inject := a.Inject
		switch {
		case yolo && a.YoloInject != nil:
			inject = a.YoloInject
		case sig.Kind != catalog.SignalHook:
			return nil, nil
		}
		if inject == nil {
			return nil, nil
		}
		argv, env := inject(hooksDir, sig)
		if len(argv) == 0 {
			argv = nil
		}
		if len(env) == 0 {
			env = nil
		}
		return argv, env
	}
	return nil, nil
}
```

In `internal/agents/claude.go`, add `"slices"` after `"path/filepath"`, and replace `claudeHooks` and `claudeAssets` (`:20-30`) with:

```go
func claudeHooks(events ...string) string {
	return jsonAsset(`{` + claudeHookList(events...) + `}`)
}

// claudeHookList is the "hooks" member of a settings file for events.
func claudeHookList(events ...string) string {
	entry := `{"hooks":[{"type":"command","command":"{{BIN}} notify --claude-hook"}]}`
	return `"hooks":{` + hookLists(entry, events...) + `}`
}

// claudeYoloKey skips the warning Claude Code shows before its first launch
// with --dangerously-skip-permissions (the yolo recipe). Claude Code honours
// only the last --settings, so it goes in the file that carries the hooks.
const claudeYoloKey = `"skipDangerousModePermissionPrompt":true`

var (
	claudeEvents      = []string{"Notification", "Stop", "UserPromptSubmit", "PermissionRequest", "PermissionDenied"}
	claudeToolsEvents = append(slices.Clone(claudeEvents), "PostToolUse", "PostToolUseFailure", "SubagentStop")
)

var claudeAssets = map[string]string{
	"claude.json": claudeHooks(claudeEvents...),
	// Tool events are chatty: a signal asks for them with toolEvents.
	"claude-tools.json": claudeHooks(claudeToolsEvents...),
	// The same with the yolo key, for a launch with the yolo recipe, and the
	// key alone for one whose hooks are not wired.
	"claude-yolo.json":       jsonAsset(`{` + claudeYoloKey + `,` + claudeHookList(claudeEvents...) + `}`),
	"claude-tools-yolo.json": jsonAsset(`{` + claudeYoloKey + `,` + claudeHookList(claudeToolsEvents...) + `}`),
	"claude-yolo-only.json":  jsonAsset(`{` + claudeYoloKey + `}`),
}
```

and in `claudeAdapter`, after the `Inject` function:

```go
		YoloInject: func(hooksDir string, sig catalog.Signal) ([]string, map[string]string) {
			settings := "claude-yolo-only.json"
			switch {
			case sig.Kind == catalog.SignalHook && sig.ToolEvents:
				settings = "claude-tools-yolo.json"
			case sig.Kind == catalog.SignalHook:
				settings = "claude-yolo.json"
			}
			return []string{"--settings", filepath.Join(hooksDir, settings)}, nil
		},
		ConfirmsSubmit: true,
```

In `internal/agents/codex.go`, the imports become `import (\n\t"strings"\n\n\t"github.com/phenixrizen/conductor/internal/catalog"\n)`, and in `codexAdapter` before `Inject:` (`:73`):

```go
		// The per-launch trust override Codex takes in place of the trust
		// saved in config.toml: -c projects={"<dir>"={trust_level="trusted"}}
		// (the dotted -c form does not skip the question). Applied only with
		// the yolo recipe.
		TrustArgs: func(dirs []string) []string {
			parts := make([]string, 0, len(dirs))
			for _, d := range dirs {
				parts = append(parts, tomlString(d)+`={trust_level="trusted"}`)
			}
			return []string{"-c", "projects={" + strings.Join(parts, ",") + "}"}
		},
```

`internal/hostagent/hooks.go:54` gets the new argument: `agents.InjectFor(opts.Adapter, dir, catalog.Signal{Kind: catalog.SignalHook}, false)` (hosted sessions are out of yolo's scope this round). Every other `InjectFor(…)` call gains `, false` as its last argument for now: `internal/api/sessions.go:121` (Step 6 replaces it), `internal/agents/adapter_test.go` (`:35`, `:39`, `:43`, `:75`, `:142`, `:158`, `:170`), `internal/agents/assets_test.go` (`:201`, `:531`, `:537`) and `internal/api/api_test.go` (`:828`). (`internal/hostagent`'s tests import `internal/api`, so its build needs the `sessions.go` call fixed too.)

The two asset tests hold every asset to running the binary; the yolo-only settings run none. In `internal/agents/assets_test.go`, before `var jsConst = regexp.MustCompile(` add

```go
// commandless lists the assets that run no command: settings without hooks
// (TestClaudeYoloSettings checks what they hold).
var commandless = map[string]bool{"claude-yolo-only.json": true}

```

and in `TestWriteAssetsWritesEveryAsset` after `b, _ := os.ReadFile(p)` and in `TestAssetsEscapeTheBinaryPath` before `switch path.Ext(rel) {` add `if commandless[rel] { continue }`. Append to `internal/agents/adapter_test.go` (imports `encoding/json`, `path/filepath`, `slices` already):

```go
func TestClaudeYoloSettings(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		sig  catalog.Signal
		want string
	}{
		{catalog.Signal{Kind: "hook"}, "claude-yolo.json"},
		{catalog.Signal{Kind: "hook", ToolEvents: true}, "claude-tools-yolo.json"},
		{catalog.Signal{Kind: "bell"}, "claude-yolo-only.json"},
	} {
		argv, env := InjectFor("claude", dir, tc.sig, true)
		if !slices.Equal(argv, []string{"--settings", filepath.Join(dir, tc.want)}) || env != nil {
			t.Errorf("%+v: %v %v", tc.sig, argv, env)
		}
	}
	for _, name := range []string{"claude-yolo.json", "claude-tools-yolo.json", "claude-yolo-only.json"} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(claudeAssets[name]), &doc); err != nil || doc["skipDangerousModePermissionPrompt"] != true {
			t.Errorf("%s: %v %v", name, doc, err)
		}
		if _, hooks := doc["hooks"]; hooks == (name == "claude-yolo-only.json") {
			t.Errorf("%s: hooks %v", name, hooks)
		}
	}
	if argv, _ := InjectFor("codex", dir, catalog.Signal{Kind: "bell"}, true); argv != nil {
		t.Errorf("codex has no yolo injection: %v", argv)
	}
}

// Codex's per-launch trust: one -c projects override naming each directory as
// a TOML string, so a path with quotes or backslashes cannot break out of it.
func TestCodexTrustArgs(t *testing.T) {
	got := TrustArgsFor("codex", func() []string { return []string{"/work/repo", `/odd "dir"\x`} })
	want := []string{"-c", `projects={"/work/repo"={trust_level="trusted"},"/odd \"dir\"\\x"={trust_level="trusted"}}`}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	asked := false
	if TrustArgsFor("codex", func() []string { return nil }) != nil || TrustArgsFor("claude", func() []string { asked = true; return []string{"/x"} }) != nil || asked {
		t.Fatal("trust args without directories, or for an agent with none")
	}
	if !ConfirmsSubmit("claude", catalog.Signal{Kind: "hook"}) || ConfirmsSubmit("claude", catalog.Signal{Kind: "bell"}) || ConfirmsSubmit("codex", catalog.Signal{Kind: "hook"}) {
		t.Fatal("ConfirmsSubmit")
	}
}
```

Run: `go test -race -count=1 ./internal/agents ./internal/hostagent`
Expected: PASS.

- [ ] **Step 4: The switch.** `internal/config/config.go`: after `Dev bool `json:"dev"`` (`:110-111`):

```go
	// Yolo launches every agent with its yolo recipe (catalog.Agent.Yolo),
	// unless a launch or a crew says otherwise: CONDUCTOR_YOLO, conductor
	// serve --yolo.
	Yolo bool `json:"yolo"`
```

and after the `CONDUCTOR_EXAMPLES` block (`:215-217`):

```go
	switch getenv("CONDUCTOR_YOLO") {
	case "1", "true":
		cfg.Yolo = true
	case "0", "false":
		cfg.Yolo = false
	}
```

Append to `internal/config/config_test.go`:

```go
// yolo comes from the config file, and CONDUCTOR_YOLO overrides it either way.
func TestYoloFromTheFileAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"yolo": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file, env string
		want      bool
	}{
		{"", "", false},
		{path, "", true},
		{"", "1", true},
		{"", "true", true},
		{path, "0", false},
		{path, "false", false},
		{"", "yes", false},
	} {
		t.Setenv("CONDUCTOR_YOLO", tc.env)
		if cfg, err := Load(tc.file); err != nil || cfg.Yolo != tc.want {
			t.Errorf("file %q, CONDUCTOR_YOLO=%q: %v %v", tc.file, tc.env, cfg.Yolo, err)
		}
	}
}
```

`internal/cli/serve.go`: after the `examples` flag (`:34`):

```go
	yolo := fs.Bool("yolo", false, "launch every agent with its yolo recipe, skipping its permission prompts, unless a launch or a crew says otherwise (env CONDUCTOR_YOLO=1)")
```

and after `if *examples { cfg.Examples = true }` (`:67-69`):

```go
	if *yolo {
		cfg.Yolo = true
	}
	if cfg.Yolo {
		log.Warn("yolo is on: agents launch with their yolo recipes and skip their permission prompts (Codex's also drops its sandbox)")
	}
```

`internal/cli/completion.go:98`: the `serve` row's flags end `{name: "--examples"}, {name: "--yolo"}}},` (`TestCompletionSpecMatchesEveryFlag` holds the table to the flag set).

`conductor up` says when the run it launched is yolo. In `internal/cli/up.go` `runUp`, the comment above the reply (`:106`) reads "The reply is read for the run's ID and yolo alone: …", and the reply's `Run` struct (`:110-112`) gains

```go
			// Yolo: the run's members launch with their agents' yolo recipes.
			Yolo bool `json:"yolo"`
```

and before `if *open {` (`:133`):

```go
	if reply.Run.Yolo {
		fmt.Fprintln(stdout, "yolo: its members skip their permission prompts")
	}
```

Append to `internal/cli/up_test.go`:

```go
// A run launched with yolo says so on a line of its own.
func TestUpSaysWhenTheRunIsYolo(t *testing.T) {
	clearConductorEnv(t)
	srv, _ := stubServer(t, http.StatusCreated, `{"run":{"id":"r-1","yolo":true}}`)
	code, stdout, stderr, err := up(t, "crew-1", "--server", srv.URL, "--token", secretToken)
	want := "run r-1\n" + srv.URL + "/runs/r-1\nyolo: its members skip their permission prompts\n"
	if code != 0 || err != nil || stdout != want {
		t.Fatalf("exit %d %v\nstdout:\n%q\nwant:\n%q\nstderr:\n%s", code, err, stdout, want, stderr)
	}
}
```

Run: `go test -race -count=1 ./internal/config ./internal/cli`
Expected: PASS.

- [ ] **Step 5: The run carries the choice.** `internal/session/info.go`, after the `Crew *CrewRef` field (`:62`):

```go
	// Yolo says the session was launched with its agent's yolo recipe
	// applied (the agent has one, and yolo was on for the launch).
	Yolo bool `json:"yolo,omitempty"`
```

`internal/crew/crew.go`, in `Crew` before `Members` (`:100`):

```go
	// Yolo overrides the server's yolo default for the crew's runs: nil
	// follows it, false launches every member without its agent's yolo
	// recipe, true with it. A launch fixes the choice on the run.
	Yolo    *bool    `json:"yolo,omitempty"`
```

`internal/crew/run.go`: replace the `Launcher` interface (`:33-35`) with

```go
type Launcher interface {
	Launch(ctx context.Context, spec LaunchSpec) (*session.Local, error)
}

// LaunchSpec is a member's session as the engine asks a Launcher for it.
type LaunchSpec struct {
	AgentID, Name, Cwd string
	Args               []string
	// Env holds variables to set in the process: GOAL.
	Env map[string]string
	// Ref tags the session with its run.
	Ref session.CrewRef
	// Yolo is the run's yolo choice, fixed when the run was made: the agent's
	// yolo recipe is applied when it has one.
	Yolo bool
}
```

In `Run`, after `Members []MemberState` (`:123`):

```go
	// Yolo is the run's yolo choice, fixed at launch: every member, one added
	// or started later included, is launched with it.
	Yolo bool `json:"yolo"`
```

in `run`, after `id, crewID, name, goal, cwd, isolation string` (`:173`):

```go
	// yolo is the run's yolo choice, fixed when it is made.
	yolo bool
```

in `add` (`:355-356`) the literal gains `yolo: c.Yolo != nil && *c.Yolo,` (the API fixes `c.Yolo` before the launch, Step 6); in `launch` (`:466-467`) the two lines `ref := …` and `local, err := e.launcher.Launch(…)` become

```go
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: name, Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}, Yolo: r.yolo})
```

and `snapshot` (`:1015`) copies `Yolo: r.yolo`. Append to `internal/crew/worktree.go`:

```go
// RepoRoots returns the directories a per-launch trust names for dir: the top
// of its git working tree and, for a linked worktree, the main repository's
// top too, where an agent that trusts by repository looks (Codex resolves a
// worktree to its main repository). Not in a repository, or git missing or
// failing, it is dir alone. Each path is absolute; none is repeated.
func RepoRoots(ctx context.Context, dir string) []string {
	// --git-common-dir may answer relative to dir (git before 2.31 has no
	// --path-format=absolute).
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return []string{dir}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !filepath.IsAbs(lines[0]) {
		return []string{dir}
	}
	roots := []string{lines[0]}
	common := lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if filepath.Base(common) == ".git" {
		if main := filepath.Dir(common); main != lines[0] {
			roots = append(roots, main)
		}
	}
	return roots
}
```

In `internal/crew/worktree_test.go`, add `"slices"` to the imports (after `"regexp"`) and append:

```go
// RepoRoots names a repository's top and, for a linked worktree, the main
// repository's top too; outside a repository, the directory itself.
func TestRepoRoots(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoots(t.Context(), sub); !slices.Equal(got, []string{repo}) {
		t.Fatalf("below the top: %q", got)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "-b", "side", wt)
	wt, _ = filepath.EvalSymlinks(wt)
	if got := RepoRoots(t.Context(), wt); !slices.Equal(got, []string{wt, repo}) {
		t.Fatalf("a worktree: %q", got)
	}
	plain := t.TempDir()
	if got := RepoRoots(t.Context(), plain); !slices.Equal(got, []string{plain}) {
		t.Fatalf("no repository: %q", got)
	}
}
```

In `internal/crew/run_test.go`: `launchCall` gains a last field `yolo bool`; `fakeLauncher.Launch` becomes

```go
func (f *fakeLauncher) Launch(ctx context.Context, spec LaunchSpec) (*session.Local, error) {
	name, ref := spec.Name, spec.Ref
	f.mu.Lock()
	f.calls = append(f.calls, launchCall{spec.AgentID, name, spec.Cwd, slices.Clone(spec.Args), maps.Clone(spec.Env), ref, spec.Yolo})
```

with the rest of its body as it is, but `info := session.Info{ID: session.NewID(), Name: name, AgentID: spec.AgentID, Cwd: spec.Cwd, Crew: &ref, Cols: 80, Rows: 24}`; and append:

```go
// A run's yolo choice is fixed when it is made: every member, one added
// later included, is launched with it.
func TestAYoloRunLaunchesEveryMemberWithIt(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	on := true
	c := testCrew(immediate("lead", "Plan it."))
	c.Yolo = &on
	run, err := e.Launch(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Yolo {
		t.Fatalf("run %+v", run)
	}
	if err := e.AddMember(t.Context(), run.ID, immediate("qa", "Check it.")); err != nil {
		t.Fatal(err)
	}
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if len(fl.calls) != 2 || !fl.calls[0].yolo || !fl.calls[1].yolo {
		t.Fatalf("calls %+v", fl.calls)
	}
}
```

Run: `go test -race -count=1 ./internal/crew`
Expected: PASS. (`internal/api` builds again after Step 6: until then it implements the old `Launcher`.)

- [ ] **Step 6: The launch path.** `internal/api/sessions.go`: add `".../internal/crew"` to the imports (after `.../internal/catalog`). In `createSessionRequest`, after `Rows`:

```go
	// Yolo overrides the server's yolo default for this launch: nil follows
	// it, false launches without the agent's yolo recipe, true with it.
	Yolo *bool `json:"yolo,omitempty"`
```

In `createLocalSession`, replace

```go
	extra, adapterEnv := agents.InjectFor(agent.Adapter, agents.HooksDir(s.cfg.DataDir), sig)
	argv := append(append(append([]string{}, agent.Command...), req.Args...), extra...)
	env := agent.Env
	if len(adapterEnv) > 0 || len(req.Env) > 0 {
		env = maps.Clone(adapterEnv)
		if env == nil {
			env = map[string]string{}
		}
		maps.Copy(env, agent.Env)
		maps.Copy(env, req.Env)
	}
```

with

```go
	// Yolo: the launch's choice, else the server's; applied when the agent
	// has a recipe, whose arguments follow the user's and whose environment
	// goes over the agent's own, through the same filtered environment, never
	// with Conductor's own variables. An agent with a trust override (Codex)
	// trusts the launch's repository for this launch alone.
	yolo := s.cfg.Yolo
	if req.Yolo != nil {
		yolo = *req.Yolo
	}
	applied := yolo && !agent.Yolo.Empty()
	extra, adapterEnv := agents.InjectFor(agent.Adapter, agents.HooksDir(s.cfg.DataDir), sig, applied)
	argv := append(append([]string{}, agent.Command...), req.Args...)
	var yoloEnv map[string]string
	if applied {
		argv = append(argv, agent.Yolo.Args...)
		yoloEnv = agent.Yolo.Env
		argv = append(argv, agents.TrustArgsFor(agent.Adapter, func() []string {
			ctx, cancel := context.WithTimeout(context.Background(), gitCheckTimeout)
			defer cancel()
			return crew.RepoRoots(ctx, cwd)
		})...)
	}
	argv = append(argv, extra...)
	env := agent.Env
	if len(adapterEnv) > 0 || len(yoloEnv) > 0 || len(req.Env) > 0 {
		env = maps.Clone(adapterEnv)
		if env == nil {
			env = map[string]string{}
		}
		maps.Copy(env, agent.Env)
		maps.Copy(env, yoloEnv)
		maps.Copy(env, req.Env)
	}
	var trust *regexp.Regexp
	if agent.TrustPrompt != "" {
		if trust, err = catalog.CompilePattern(agent.TrustPrompt); err != nil {
			return nil, newAPIError(http.StatusInternalServerError, "invalid_agent", "the agent's trust prompt is invalid")
		}
	}
```

The `info` literal gains `Yolo: applied,` after `Branch:`, the `session.Options` literal gains (after `Pattern:`)

```go
		TrustPattern:    trust,
		ConfirmSubmit:   agents.ConfirmsSubmit(agent.Adapter, sig),
```

and the end of the function becomes

```go
	s.log.Info("session started", "session", id, "agent", agent.ID, "pid", proc.PID(), "yolo", applied)
	if yolo && !applied {
		local.Record(session.ActivityEntry{Type: session.ActivityStatus, Message: noYoloRecipe(agent)})
	}
	s.events.publish(local.Info())
	return local, nil
}

// noYoloRecipe is the notice of a launch with yolo on whose agent has no yolo
// recipe: it is launched as it would be without.
func noYoloRecipe(agent catalog.Agent) string {
	return "yolo is on, but " + agent.Name + " has no yolo recipe: launched without one"
}
```

`internal/api/runs.go`: `Launch` (`:43-54`) becomes

```go
func (s *Server) Launch(ctx context.Context, spec crew.LaunchSpec) (*session.Local, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	yolo := spec.Yolo
	ref := spec.Ref
	local, aerr := s.createLocalSession(createSessionRequest{AgentID: spec.AgentID, Name: spec.Name, Cwd: spec.Cwd, Args: spec.Args, Env: spec.Env, Yolo: &yolo}, &ref)
	if aerr != nil {
		return nil, aerr
	}
	if yolo && !local.Info().Yolo {
		if a, ok := s.Catalog().Get(spec.AgentID); ok {
			s.runs.Note(ref.RunID, session.ActivityStatus, spec.Name+": "+noYoloRecipe(a))
		}
	}
	return local, nil
}
```

and in `handleLaunchCrew`, after `c.Cwd = cwd` (`:137`):

```go
	// The run's yolo choice: the crew's, else the server's, fixed now.
	yolo := s.cfg.Yolo
	if c.Yolo != nil {
		yolo = *c.Yolo
	}
	c.Yolo = &yolo
```

`internal/api/crews.go`: `crewInput` gains `Yolo *bool `json:"yolo"`` before `Members`, and the crew it builds (`:194-197`) passes `Yolo: in.Yolo`. `internal/api/catalog.go` `handleCatalog` (`:86`):

```go
	// yoloDefault is the server's yolo choice, which a launch or a crew that
	// says nothing follows.
	writeJSON(w, http.StatusOK, map[string]any{"agents": out, "hidden": hidden, "yoloDefault": s.cfg.Yolo})
```

Create `internal/api/yolo_test.go`:

```go
package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/config"
)

// yoloScript prints the arguments it was given and two variables, then waits.
var yoloScript = []string{"/bin/sh", "-c", `printf 'ARGS[%s] MODE[%s] AGENT[%s]\n' "$*" "$YOLO_MODE" "$AGENT_VAR"; exec /bin/cat`, "sh"}

// saveYoloAgent saves an agent running yoloScript with the given yolo recipe
// (none when nil) and adapter.
func (e *testEnv) saveYoloAgent(id, adapter string, yolo map[string]any) {
	e.t.Helper()
	body := agentBody(id)
	body["command"] = yoloScript
	body["allowArgs"] = true
	body["env"] = map[string]string{"AGENT_VAR": "mine", "YOLO_MODE": "careful"}
	if adapter != "" {
		body["adapter"] = adapter
		body["signal"] = map[string]any{"kind": "bell"}
	}
	if yolo != nil {
		body["yolo"] = yolo
	}
	e.save(body)
}

func commandOf(info map[string]any) []string {
	var out []string
	for _, a := range info["command"].([]any) {
		out = append(out, a.(string))
	}
	return out
}

// A launch with yolo on applies the agent's recipe: its arguments after the
// command and the user's, its environment over the agent's own, and the
// session says so; yolo false, or the server's default when the launch says
// nothing, leaves it out.
func TestCreateSessionAppliesTheYoloRecipe(t *testing.T) {
	for _, def := range []bool{false, true} {
		e := newTestEnv(t, func(c *config.Config) { c.Yolo = def })
		e.saveYoloAgent("bold", "", map[string]any{"args": []string{"--yes", "--all"}, "env": map[string]string{"YOLO_MODE": "auto"}})
		for _, tc := range []struct {
			yolo   any
			on     bool
			output string
		}{
			{nil, def, ""},
			{true, true, ""},
			{false, false, ""},
		} {
			req := map[string]any{"agentId": "bold", "args": []string{"--resume"}}
			if tc.yolo != nil {
				req["yolo"] = tc.yolo
			}
			resp, info := e.do("POST", "/api/sessions", adminToken, req)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("launch: %d %v", resp.StatusCode, info)
			}
			id := info["id"].(string)
			t.Cleanup(func() { _ = e.local(id).Stop(t.Context()) })
			want := append(slices.Clone(yoloScript), "--resume")
			out := "ARGS[--resume] MODE[careful] AGENT[mine]"
			if tc.on {
				want = append(want, "--yes", "--all")
				out = "ARGS[--resume --yes --all] MODE[auto] AGENT[mine]"
			}
			if got := commandOf(info); !slices.Equal(got, want) || (info["yolo"] == true) != tc.on {
				t.Fatalf("default %v, yolo %v: command %q, yolo %v", def, tc.yolo, got, info["yolo"])
			}
			c := dialViewer(t, e, id, adminToken)
			c.hello(80, 24)
			c.expectOutput(out)
		}
	}
}

// Yolo on for an agent with no recipe, or with an empty one, launches it as
// it would be without, with no badge and a notice in its activity.
func TestYoloWithoutARecipeLaunchesPlainlyWithANotice(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.Yolo = true })
	e.saveYoloAgent("plain", "", nil)
	e.saveYoloAgent("emptied", "", map[string]any{})
	for _, id := range []string{"plain", "emptied"} {
		info := e.launch(id, nil)
		if info["yolo"] == true || len(commandOf(info)) != len(yoloScript) {
			t.Fatalf("%s: %v", id, info)
		}
		found := false
		for _, entry := range e.local(info["id"].(string)).Activity() {
			found = found || strings.Contains(entry.Message, "has no yolo recipe")
		}
		if !found {
			t.Fatalf("%s: no notice", id)
		}
	}
}

// A recipe cannot set Conductor's own variables, and is bounded.
func TestYoloRecipeIsValidated(t *testing.T) {
	e := newTestEnv(t, nil)
	for _, yolo := range []map[string]any{
		{"env": map[string]string{"CONDUCTOR_NOTIFY_TOKEN": "x"}},
		{"env": map[string]string{"BAD NAME": "x"}},
		{"args": []string{""}},
		{"args": slices.Repeat([]string{"-y"}, 17)},
	} {
		body := agentBody("bad")
		body["yolo"] = yolo
		resp, out := e.do("POST", "/api/catalog", adminToken, body)
		if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_agent" {
			t.Fatalf("%v: %d %v", yolo, resp.StatusCode, out)
		}
	}
}

// Codex's trust override goes with its yolo recipe, naming the launch's
// directory, and never without it.
func TestCodexTrustGoesWithYolo(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveYoloAgent("cx", "codex", map[string]any{"args": []string{"--bypass"}})
	on := e.do2(map[string]any{"agentId": "cx", "yolo": true})
	want := append(slices.Clone(yoloScript), "--bypass", "-c", `projects={"`+realRoot(t, e.root)+`"={trust_level="trusted"}}`)
	if got := commandOf(on); !slices.Equal(got, want) {
		t.Fatalf("command %q, want %q", got, want)
	}
	off := e.do2(map[string]any{"agentId": "cx"})
	if got := commandOf(off); !slices.Equal(got, yoloScript) {
		t.Fatalf("without yolo: %q", got)
	}
}

func (e *testEnv) do2(req map[string]any) map[string]any {
	e.t.Helper()
	resp, info := e.do("POST", "/api/sessions", adminToken, req)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("launch: %d %v", resp.StatusCode, info)
	}
	id := info["id"].(string)
	e.t.Cleanup(func() { _ = e.local(id).Stop(e.t.Context()) })
	return info
}

// A crew's yolo choice is the run's, and every member follows it.
func TestACrewsYoloIsTheRuns(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveYoloAgent("bold", "", map[string]any{"args": []string{"--yes"}})
	id := e.sendCrew("POST", "/api/crews", map[string]any{
		"name": "Bold", "goal": "ship", "cwd": e.root, "where": "server", "isolation": "none", "yolo": true,
		"members": []any{map[string]any{"name": "a", "agentId": "bold", "prompt": "", "start": map[string]any{"when": "immediately"}}},
	}, http.StatusCreated)["id"].(string)
	resp, out := e.do("POST", "/api/crews/"+id+"/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	run := out["run"].(map[string]any)
	sid := e.waitRunning(t, run["id"].(string), "a")
	if run["yolo"] != true || !e.local(sid).Info().Yolo {
		t.Fatalf("run %v, member yolo %v", run["yolo"], e.local(sid).Info().Yolo)
	}
	t.Cleanup(func() { _ = e.local(sid).Stop(t.Context()) })
}
```

- [ ] **Step 7: Run everything**

The fields added to aligned struct literals and structs (`defaults.go`, `info.go`, `crew.go`, the `info` literal in `sessions.go`) need realigning: run `gofmt -w internal` first.

Run: `gofmt -w internal && make lint && go test -race -count=1 ./...`
Expected: clean, PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/catalog.go internal/catalog/defaults.go internal/catalog/catalog_test.go internal/agents/adapter.go internal/agents/registry.go internal/agents/claude.go internal/agents/codex.go internal/agents/adapter_test.go internal/agents/assets_test.go internal/hostagent/hooks.go internal/config/config.go internal/config/config_test.go internal/cli/serve.go internal/cli/completion.go internal/cli/up.go internal/cli/up_test.go internal/session/info.go internal/crew/crew.go internal/crew/worktree.go internal/crew/worktree_test.go internal/crew/run.go internal/crew/run_test.go internal/api/sessions.go internal/api/runs.go internal/api/crews.go internal/api/catalog.go internal/api/api_test.go internal/api/yolo_test.go
git commit -m "launch: a global yolo switch with each agent's recipe, per launch and per crew; Codex trusted per launch with it"
```

**Done when:**

- `catalog.Agent` has `yolo {args, env}` and `trustPrompt`, validated to the bounds in "Rules and fixed values" (a recipe that names `CONDUCTOR_*` is refused), inherited and cloned; the built-ins carry their recipes and Claude Code's and Codex's trust prompts; `go test -race -count=1 ./internal/catalog` passes.
- The adapters: `YoloInject` (Claude Code's `claude-yolo.json`, `claude-tools-yolo.json` and `claude-yolo-only.json`, with `skipDangerousModePermissionPrompt`), `TrustArgs` (Codex's per-launch `-c projects=…` for the launch directory's repository top and a worktree's main repository, paths as TOML strings), `ConfirmsSubmit`; every `InjectFor` call takes the yolo argument.
- The switch: `yolo` in the config, `CONDUCTOR_YOLO`, `conductor serve --yolo` (and the completion table); `conductor up` says when its run is yolo.
- The run carries the choice (`Crew.Yolo`, `LaunchSpec`, the run's `yolo`, `Info.Yolo`), and `crew.RepoRoots` works on git before 2.31.
- `createLocalSession` applies the recipe (its arguments after the launch's own, its environment through `pty.BuildEnv`'s filtered set, never the inject map); an agent without a recipe launches as usual with a notice and no badge; `GET /api/catalog` gives `yoloDefault`; `internal/api/yolo_test.go` passes.
- `gofmt -w internal && make lint && go test -race -count=1 ./...` is clean and passes.
- Committed: "launch: a global yolo switch with each agent's recipe, per launch and per crew; Codex trusted per launch with it".

---

### Task 4: The engine types through `Submit`; readiness, the trust hold, run state and run events; the live check

**Order:** needs Tasks 2 and 3. Its care point is the engine's concurrency; its last step, the live check, needs the real `claude` and `codex`, logged in, on this machine.

The engine's three typing paths move onto `Local.Submit`: the role prompt (with `Confirm`, and the member stamped prompted in `BeforeEnter`, just before the Enter — today `markPrompted` runs before the one write, which with two writes would let a `done` from between the text and the Enter start the members after it), handoffs (`UnlessWaiting`), and broadcasts (`UnlessWaiting`, every member at once since each submission pauses; a member whose Enter was left out is reported `no_enter`). A submission whose Enter was left out because a question came up is noted in the run log and never typed again. Readiness gains the investigation's two rules: a program that turned bracketed paste on and then off is between screens (Claude Code while it starts) and is not typed into; and a trust question (Task 2's `source: trust`) holds the prompt past the cap, noted once, the wait starting over once a person answers it with Enter. `Type`, `TypeUnlessWaiting` and `write` go: `Input` is the only raw path left.

Run state: `refresh` (every `Get`, `List` and `GetWithDiffs`) marks the starting and running members whose session waits on a prompt and derives the run's `state` (running, needs input with a count, stopped, finished — a member's `done` keeps it running) — the rule `web/app/utils/runs.ts` mirrors in Task 7. Run events: the engine reports, under its lock and without waiting, each change a read of a run shows that no session change carries (`OnRunChange`: a member reserved, an entry in the log — which covers started, prompted, failed, ended early, handoffs, a stop — and a member running without a prompt); the server publishes `event: run` `{id}` on `/api/events`, and `{id, removed: true}` when the engine forgets a run, both at most 128 bytes, dropped like a session change for a client that cannot keep up.

Two mapper changes ride here because the spec's live check needs them (Spec/code note): a crew types handoffs and broadcasts only into a member that is not waiting on a prompt, and today an agent at rest reports needs input — Codex after every turn (`agent-turn-complete` → `needs_input`), Claude Code a minute after its turn (`Notification` `idle_prompt` → `needs_input`) — so a handoff to an idle Codex member would wait for a person forever and a broadcast would skip it. Both now report `done`, as Claude Code's `Stop` does; a real question (a permission request, Codex's bell, Claude Code's `permission_prompt`) still reports needs input. With Codex's turn end now `done`, the turn of the hidden thread that titles a conversation (live, Codex 0.159: its own thread id, input "Generate a concise, single-line task title…") would start a member's dependants early, so that payload maps to nothing.

The task ends with the live check against `claude` 2.1.287 and `codex` 0.159.0 on this machine: the initial prompt, a handoff and a broadcast, short and long, in fresh worktrees, with each agent's own answer as the proof.

**Files:**
- Create: `internal/crew/prompt_test.go`, `internal/api/runevents_test.go`
- Modify: `internal/crew/run.go` — `MemberState` (`:107`), after it the run states, `Run` (`:123`), `Engine.await` (`:135-136`), `Engine` after `OnForget` (`:155`), `run` (`:173`), `add` (`:355-356`), `reserve` (`:406-409`), `note` (`:418`), `prompt` (`:488-532`), `refresh` (`:1030-1049`), `awaitReady`/`readyAt` (`:1051-1093`); `internal/crew/handoff.go` (`deliver` `:170-191`); `internal/crew/crew.go` (imports, `checkTypedPrompt` `:345-353`); `internal/crew/run_test.go` (imports, `fakeLauncher`, `typed`, `waitTyped`, every `e.await =`, `TestAfterConditionIgnoresADoneThatPredatesThePrompt`, `TestReadiness`, `TestADoneAsThePromptIsTypedCounts`); `internal/crew/handoff_test.go` (`:279-281`); `internal/session/local.go` (`Input`, `Type`, `TypeUnlessWaiting`, `write` as Task 2 left them); `internal/session/local_test.go` (three `Type` tests); `internal/session/sessiontest/fakeproc_test.go` (`:26-31`); `internal/api/runs.go` (imports, the skip reasons `:36-41`, `handleBroadcast`/`broadcastTo` `:288-364`); `internal/api/events.go` (`removed` `:137-150`); `internal/api/server.go` (`OnForget` `:168-172`); `internal/api/events_test.go` (append); `internal/notify/mappers.go` (`MapClaudeHook`'s notification cases, `CodexPayload`, `MapCodex`); `internal/notify/notify_test.go` (`:24`, `TestMapCodex`, `TestMapCodexIsPromptKind`); `docs/protocol.md` (the `GET /api/events` row `:440`)

**Interfaces:**
- Consumes: Task 2's `Local.Submit`, `Submission`, `SubmitResult`, `BracketedPaste`, `SourceTrust`, `ConfirmWait`, `MaxSubmitText`; Task 3's `LaunchSpec`, `fakeLauncher.Launch(spec)`.
- Produces:
```go
// internal/crew
const RunRunning, RunNeedsInput, RunStopped, RunFinished = "running", "needs_input", "stopped", "finished"
// Run gains State string `json:"state"`, NeedsInput int `json:"needsInput"`; MemberState gains NeedsInput bool `json:"needsInput,omitempty"`
// Engine gains OnRunChange func(runID string) (called under e.mu; must not call the engine or wait)
func runState(r Run) (string, int)
type readiness struct{ state session.AttentionState; source string; lastOutput time.Time; pasteSeen, pasteOn bool }
func readyAt(rd readiness, start, now time.Time) (ready, capped, hold bool)
func awaitReady(ctx context.Context, l *session.Local, held func(question string)) error
// internal/api
const skipNoEnter = "no_enter"; const broadcastTimeout = 15 * time.Second
type runEvent struct{ ID string `json:"id"`; Removed bool `json:"removed,omitempty"` }
const maxRunEvent = 128
func (h *eventHub) run(id string, removed bool)
func (h *eventHub) broadcast(msg []byte)
// internal/session: Type, TypeUnlessWaiting and write removed
```

- [ ] **Step 1: Write the failing engine tests.** Create `internal/crew/prompt_test.go`:

```go
package crew

import (
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// nopSink is a session.Sink that drops what it is sent: a person's
// connection, for a test that types as one.
type nopSink struct{}

func (nopSink) WriteFrame([]byte) error { return nil }
func (nopSink) Close(error)             {}

// A done the agent reports after the text of its prompt but before the Enter
// is the idle state the prompt has not yet reached: it starts nothing. One
// after the Enter does.
func TestADoneBeforeTheEnterDoesNotCount(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = func(member string, p *sessiontest.FakeProc, l *session.Local) {
		if member != "core" {
			return
		}
		l.SetAttention(session.AttentionNeedsInput, "what next?", session.SourceAPI)
		<-p.Input // the text: the agent has not had the Enter yet
		l.SetAttention(session.AttentionDone, "idle", session.SourceAPI)
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("core", "Build it."), after("tests", "", "core")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	if n := logCount(t, e, run.ID, "core is done"); n != 0 {
		t.Fatalf("a done before the Enter started the members after core (%d)", n)
	}
	core, _ := fl.member("core")
	core.SetAttention(session.AttentionWorking, "", session.SourceAPI)
	core.SetAttention(session.AttentionDone, "built", session.SourceAPI)
	waitStatus(t, e, run.ID, "tests", MemberRunning, 5*time.Second)
}

// A trust question on a member's screen holds its prompt past the readiness
// cap: the member needs input with the question's words, the run says so
// once, and nothing is typed until a person answers it with Enter. Then the
// prompt is typed.
func TestATrustQuestionHoldsThePrompt(t *testing.T) {
	e, fl := newEngine(t)
	fl.trust = regexp.MustCompile(`Trust\s*this\s*folder\?`)
	fl.onLaunch = func(_ string, p *sessiontest.FakeProc, _ *session.Local) {
		_ = p.Print("\x1b[2;3HTrust\x1b[1Cthis\x1b[1Cfolder?\r\n› 1. Trust and continue")
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for logCount(t, e, run.ID, "asks") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := e.Get(run.ID)
	if !logged(got, `lead asks "Trust this folder?"`) || got.State != RunNeedsInput || got.NeedsInput != 1 || !got.Members[0].NeedsInput || got.Members[0].Status != MemberStarting {
		t.Fatalf("run %+v", got)
	}
	l, p := fl.member("lead")
	time.Sleep(3 * time.Second) // past readyMin and readyQuiet: still held
	if w := typed(p); len(w) != 0 {
		t.Fatalf("typed into the trust question: %q", w)
	}
	sub, err := l.Attach("", session.RoleControl, "", 0, 0, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Input(sub, []byte("\x1b[B")); err != nil { // the selection moves: still asking
		t.Fatal(err)
	}
	if err := l.Input(sub, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	_ = p.Print("\x1b[2J> ")
	if got := waitTyped(t, p, 5*time.Second); got != "\x1b[B\r" {
		t.Fatalf("the person typed %q", got)
	}
	if got := waitTyped(t, p, 10*time.Second); got != "Plan it.\r" {
		t.Fatalf("typed %q", got)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	if n := logCount(t, e, run.ID, "asks"); n != 1 {
		t.Fatalf("the hold was noted %d times", n)
	}
}

// A run's state: running while a member is pending, starting or running and
// none waits; needs input with how many wait; finished once every member has
// ended; stopped after a stop. A member's done keeps a run running.
func TestRunState(t *testing.T) {
	now := time.Now()
	m := func(status string, needs bool) MemberState { return MemberState{Status: status, NeedsInput: needs} }
	for _, tc := range []struct {
		name    string
		run     Run
		state   string
		waiting int
	}{
		{"all pending", Run{Members: []MemberState{m(MemberPending, false)}}, RunRunning, 0},
		{"one running", Run{Members: []MemberState{m(MemberRunning, false), m(MemberPending, false)}}, RunRunning, 0},
		{"two waiting", Run{Members: []MemberState{m(MemberRunning, true), m(MemberStarting, true), m(MemberEnded, false)}}, RunNeedsInput, 2},
		{"ended and pending", Run{Members: []MemberState{m(MemberEnded, false), m(MemberPending, false)}}, RunRunning, 0},
		{"all ended", Run{Members: []MemberState{m(MemberEnded, false), m(MemberEnded, false)}}, RunFinished, 0},
		{"stopped", Run{StoppedAt: &now, Members: []MemberState{m(MemberEnded, false)}}, RunStopped, 0},
	} {
		if state, n := runState(tc.run); state != tc.state || n != tc.waiting {
			t.Errorf("%s: %s %d, want %s %d", tc.name, state, n, tc.state, tc.waiting)
		}
	}
}

// Get derives the state from the members' sessions: a running member whose
// session waits on a prompt makes the run need input, and a done does not
// end it.
func TestGetDerivesTheRunState(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	lead, p := fl.member("lead")
	lead.SetAttention(session.AttentionNeedsInput, "Allow?", session.SourceAPI)
	if got, _ := e.Get(run.ID); got.State != RunNeedsInput || got.NeedsInput != 1 {
		t.Fatalf("waiting: %s %d", got.State, got.NeedsInput)
	}
	lead.SetAttention(session.AttentionDone, "finished", session.SourceAPI)
	if got, _ := e.Get(run.ID); got.State != RunRunning || got.Members[0].NeedsInput {
		t.Fatalf("done: %+v", got)
	}
	p.End(0)
	<-lead.Ended()
	if got, _ := e.Get(run.ID); got.State != RunFinished {
		t.Fatalf("ended: %s", got.State)
	}
}

// OnRunChange hears of every change a read of the run shows that no session
// change carries: the launch, each start, the prompt, a stop.
func TestRunChangesAreReported(t *testing.T) {
	e, fl := newEngine(t)
	var mu sync.Mutex
	var heard []string
	e.OnRunChange = func(id string) {
		mu.Lock()
		heard = append(heard, id)
		mu.Unlock()
	}
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), manual("qa", "Check it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, id := range heard {
			if id != run.ID {
				t.Fatalf("heard of %q", id)
			}
			n++
		}
		return n
	}
	afterPrompt := count()
	if afterPrompt < 3 { // launched, lead reserved and started, lead's prompt
		t.Fatalf("heard %d changes by lead's prompt", afterPrompt)
	}
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if count() == afterPrompt {
		t.Fatal("the stop was not reported")
	}
}

// A handoff whose Enter a question raised in the pause held back is noted
// and never typed again.
func TestAHandoffTypedWithoutItsEnterIsNotTypedAgain(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	fl.pause = 300 * time.Millisecond
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("core", "Build it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "core", MemberRunning, 5*time.Second)
	lead, _ := fl.member("lead")
	core, p := fl.member("core")
	typed(p)
	core.SetAttention(session.AttentionWorking, "", session.SourceAPI)
	var once sync.Once
	go func() {
		// The text arrives: a question comes up before the Enter.
		<-p.Input
		once.Do(func() { core.SetAttention(session.AttentionNeedsInput, "Allow?", session.SourceAPI) })
	}()
	lead.Record(session.ActivityEntry{Type: session.ActivityHandoff, To: "core", Message: "the handlers are in"})
	deadline := time.Now().Add(5 * time.Second)
	for logCount(t, e, run.ID, "without its Enter") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if logCount(t, e, run.ID, "without its Enter") != 1 {
		got, _ := e.Get(run.ID)
		t.Fatalf("log %+v", got.Log)
	}
	core.SetAttention(session.AttentionWorking, "", session.SourceAPI) // the question goes
	time.Sleep(200 * time.Millisecond)
	if w := typed(p); slices.ContainsFunc(w, func(s string) bool { return strings.Contains(s, "handlers") }) {
		t.Fatalf("typed again: %q", w)
	}
}
```

In `internal/crew/run_test.go`:

1. Imports: add `"cmp"` before `"context"` and `"regexp"` after `"path/filepath"`.
2. In `fakeLauncher`, after `beforeActivity`:

```go
	// trust, when set, is every session's trust prompt (Options.TrustPattern).
	trust *regexp.Regexp
	// pause is every session's pause before an Enter; 5 ms when zero.
	pause time.Duration
```

and in `Launch` the session's options become `session.Options{Log: quietLog, OnActivity: f.activity, OnChange: f.change,
		SubmitPause: cmp.Or(f.pause, 5*time.Millisecond), TrustPattern: f.trust}`.

3. Replace `typed` and `waitTyped` with the versions that read a submission whole (the 14 tests that expected one write of `"text\r"` read it again through them, unchanged):

```go
// typed returns what a process has been submitted so far, without waiting:
// the writes joined, cut after each carriage return, so that a text and the
// Enter written after it read as one "text\r"; a text still waiting for its
// Enter comes last, without one.
func typed(p *sessiontest.FakeProc) []string {
	var all string
	for {
		select {
		case b := <-p.Input:
			all += string(b)
			continue
		default:
		}
		break
	}
	var out []string
	for all != "" {
		i := strings.IndexByte(all, '\r')
		if i < 0 {
			return append(out, all)
		}
		out, all = append(out, all[:i+1]), all[i+1:]
	}
	return out
}

// waitTyped waits up to d for the next submission to a process: the writes up
// to and including the next carriage return, joined.
func waitTyped(t *testing.T, p *sessiontest.FakeProc, d time.Duration) string {
	t.Helper()
	got, ok := readSubmission(p, d)
	if !ok {
		t.Fatalf("no whole submission within %v (got %q)", d, got)
	}
	return got
}

// readSubmission reads writes to p until one ends with a carriage return,
// for up to d, and returns them joined; ok is false when d ran out.
func readSubmission(p *sessiontest.FakeProc, d time.Duration) (string, bool) {
	deadline := time.After(d)
	var got string
	for {
		select {
		case b := <-p.Input:
			got += string(b)
			if strings.HasSuffix(got, "\r") {
				return got, true
			}
		case <-deadline:
			return got, false
		}
	}
}
```

4. Every `e.await = func(...)` gets the hold callback: in `TestAfterConditionStartsOnFirstDone` (`:353`) and `TestAPromptFailureEndsOnlyThatMember` (`:1235`) the signature becomes `func(ctx context.Context, l *session.Local, held func(string)) error` and their `return awaitReady(ctx, l)` becomes `return awaitReady(ctx, l, held)`; in `internal/crew/handoff_test.go` `TestHandoffWaitsForThePrompt` (`:279`) the signature becomes `func(ctx context.Context, l *session.Local, _ func(string)) error` (it returns `nil`); in `TestNotReadyAfterTheCapTypesAnyway` (`:928`) and `TestAMemberWithoutAPromptRunsAtOnce` (`:946`) `func(context.Context, *session.Local) error` becomes `func(context.Context, *session.Local, func(string)) error`; in `TestAnExitAsThePromptIsTypedIsAnEarlyExit` (`:1268`) `func(_ context.Context, l *session.Local) error` becomes `func(_ context.Context, l *session.Local, _ func(string)) error`.

5. `TestAfterConditionIgnoresADoneThatPredatesThePrompt` (`:443-449`): the held-back entry waits for the whole submission:

```go
		_, p := fl.member("core")
		got, _ := readSubmission(p, 10*time.Second)
		prompt <- got
```

6. `TestADoneAsThePromptIsTypedCounts` (`:1291`): `<-p.Input // the prompt: done at once` becomes `readSubmission(p, 10*time.Second) // the prompt and its Enter: done at once` — a done after the Enter counts; `TestADoneBeforeTheEnterDoesNotCount` (above) pins the other side.

7. Replace `TestReadiness` (`:963-991`) with:

```go
func TestReadiness(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return start.Add(d) }
	s := time.Second
	needs, done, working, none := session.AttentionNeedsInput, session.AttentionDone, session.AttentionWorking, session.AttentionNone
	for _, tc := range []struct {
		name                string
		rd                  readiness
		now                 time.Time
		ready, capped, hold bool
	}{
		{"waiting for input at once", readiness{state: needs, source: session.SourceAPI}, at(0), true, false, false},
		{"done at once", readiness{state: done}, at(s / 2), true, false, false},
		{"working, output still coming", readiness{state: working, lastOutput: at(4500 * time.Millisecond)}, at(5 * s), false, false, false},
		{"working, but quiet", readiness{state: working, lastOutput: at(0)}, at(5 * s), true, false, false},
		{"no output yet", readiness{state: none}, at(5 * s), false, false, false},
		{"output still coming", readiness{state: none, lastOutput: at(4500 * time.Millisecond)}, at(5 * s), false, false, false},
		{"quiet, but too early", readiness{state: none, lastOutput: at(0)}, at(1500 * time.Millisecond), false, false, false},
		{"quiet for a second at two seconds", readiness{state: none, lastOutput: at(s)}, at(2 * s), true, false, false},
		{"never quiet", readiness{state: none, lastOutput: at(60 * s)}, at(60 * s), false, true, false},
		{"no output ever", readiness{state: none}, at(61 * s), false, true, false},
		{"bracketed paste on: a prompt", readiness{state: none, lastOutput: at(s), pasteSeen: true, pasteOn: true}, at(2 * s), true, false, false},
		{"bracketed paste on, then off: between screens", readiness{state: none, lastOutput: at(s), pasteSeen: true}, at(5 * s), false, false, false},
		{"between screens past the cap", readiness{state: none, lastOutput: at(s), pasteSeen: true}, at(61 * s), false, true, false},
		{"between screens, but asking", readiness{state: needs, source: session.SourceAPI, pasteSeen: true}, at(s), true, false, false},
		{"a trust question", readiness{state: needs, source: session.SourceTrust, lastOutput: at(0)}, at(5 * s), false, false, true},
		{"a trust question past the cap", readiness{state: needs, source: session.SourceTrust}, at(90 * s), false, false, true},
	} {
		ready, capped, hold := readyAt(tc.rd, start, tc.now)
		if ready != tc.ready || capped != tc.capped || hold != tc.hold {
			t.Errorf("%s: ready %v capped %v hold %v, want %v %v %v", tc.name, ready, capped, hold, tc.ready, tc.capped, tc.hold)
		}
	}
}
```

Run: `go test ./internal/crew`
Expected: FAIL to compile (`undefined: readiness`, `RunNeedsInput`, `OnRunChange`; `too many arguments` for `e.await`).

- [ ] **Step 2: Run state and run changes.** In `internal/crew/run.go`:

1. In `MemberState`, after `Err` (`:107`):

```go
	// NeedsInput is set when the member is starting or running and its
	// session waits on a prompt: a trust question before its prompt, or a
	// question of its agent's.
	NeedsInput bool `json:"needsInput,omitempty"`
```

2. Before `// Run is a launch of a crew as the engine reports it.` (`:113`):

```go
// Run states (Run.State), derived each time a run is read (refresh).
const (
	RunRunning    = "running"     // a member is pending, starting or running, and none waits on a prompt
	RunNeedsInput = "needs_input" // the session of a starting or running member waits on a prompt
	RunStopped    = "stopped"     // stopped (Stop): its sessions were stopped
	RunFinished   = "finished"    // every member ended, and none is pending
)

```

3. In `Run`, after `Members` (and Task 3's `Yolo`):

```go
	// State is the run's state (RunRunning…), NeedsInput how many members'
	// sessions wait on a prompt. A member's done keeps a run running: a done
	// agent is idle, not gone.
	State      string `json:"state"`
	NeedsInput int    `json:"needsInput"`
```

4. `Engine.await` (`:135-136`):

```go
	// await waits for a member's session to be ready, calling held with the
	// question once each time a trust question holds the prompt: awaitReady,
	// but for tests.
	await func(ctx context.Context, l *session.Local, held func(question string)) error
```

and after `OnForget` (`:155`):

```go
	// OnRunChange is called with the ID of a run each time something a read
	// of it shows changes that no session change carries: a member reserved,
	// started, running or ended, an entry in its log, a stop. It runs under
	// mu, so it must not call the engine or wait. Set it before the first
	// launch.
	OnRunChange func(runID string)
```

5. In `run`, after Task 3's `yolo bool`:

```go
	// changed is the engine's OnRunChange, or nil.
	changed func(runID string)
```

`add`'s literal gains `changed: e.OnRunChange,` (after Task 3's `yolo:`); `reserve` calls `r.touch()` after `r.starts.Add(1)`; `note` calls `r.touch()` after appending; and after `reserve`:

```go
// touch tells OnRunChange that r changed. The caller holds e.mu.
func (r *run) touch() {
	if r.changed != nil {
		r.changed(r.id)
	}
}
```

6. Replace `refresh` (`:1030-1049`, from its comment) with:

```go
// refresh shows as ended the running members of out whose session has ended
// or is gone from the server, marks the starting and running members whose
// session waits on a prompt, and derives the run's state (runState). A member
// still starting is left to its start, which ends it with the reason. It
// looks the sessions up: the caller does not hold e.mu.
func (e *Engine) refresh(out *Run) {
	for i := range out.Members {
		m := &out.Members[i]
		if m.SessionID == "" || (m.Status != MemberRunning && m.Status != MemberStarting) {
			continue
		}
		l, ok := e.lookup(m.SessionID)
		if !ok {
			if m.Status == MemberRunning {
				m.Status = MemberEnded
			}
			continue
		}
		info := l.Info()
		if info.Status.Ended() {
			if m.Status == MemberRunning {
				m.Status, m.Ended = MemberEnded, info.EndedAt
			}
			continue
		}
		m.NeedsInput = info.Attention.State == session.AttentionNeedsInput
	}
	out.State, out.NeedsInput = runState(*out)
}

// runState is a run's state and how many of its members wait on a prompt:
// stopped once stopped; finished when every member has ended and none is
// pending; needs_input while the session of a starting or running member
// waits on a prompt; running otherwise. web/app/utils/runs.ts derives the
// same from the run and the live sessions.
func runState(r Run) (string, int) {
	needs, ended := 0, 0
	for _, m := range r.Members {
		switch {
		case m.Status == MemberEnded:
			ended++
		case m.NeedsInput:
			needs++
		}
	}
	switch {
	case r.StoppedAt != nil:
		return RunStopped, needs
	case ended == len(r.Members):
		return RunFinished, 0
	case needs > 0:
		return RunNeedsInput, needs
	}
	return RunRunning, 0
}
```

- [ ] **Step 3: Readiness and the hold.** Replace `awaitReady` and `readyAt` (`:1051-1093`) with:

```go
// awaitReady waits until l is ready for its prompt (readyAt), looking every
// readyPoll. While a trust question shows it calls held with the question,
// once until the question goes, and the wait starts over when it goes: the
// cap does not run meanwhile, so a prompt is never typed into the question.
// After readyCap it returns errNotReady, and the prompt is typed anyway; once
// the session has ended it returns session.ErrSessionEnded.
func awaitReady(ctx context.Context, l *session.Local, held func(question string)) error {
	start := time.Now()
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()
	holding := false
	for {
		select {
		case <-l.Ended():
			return session.ErrSessionEnded
		default:
		}
		att := l.Info().Attention
		seen, on := l.BracketedPaste()
		now := time.Now()
		ready, capped, hold := readyAt(readiness{state: att.State, source: att.Source, lastOutput: l.LastOutputAt(), pasteSeen: seen, pasteOn: on}, start, now)
		switch {
		case hold:
			if !holding {
				held(att.Message)
			}
			holding = true
			start = now // once it is answered, the wait starts over
		case ready:
			return nil
		case capped:
			return errNotReady
		default:
			holding = false
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.Ended():
			return session.ErrSessionEnded
		case <-tick.C:
		}
	}
}

// readiness is what readyAt looks at of a session.
type readiness struct {
	state  session.AttentionState
	source string
	// lastOutput is when the output last moved (title updates aside), zero
	// before any.
	lastOutput time.Time
	// pasteSeen and pasteOn are the program's bracketed-paste mode
	// (session.Local.BracketedPaste).
	pasteSeen, pasteOn bool
}

// readyAt says whether a session is ready for its prompt at now, in a wait
// that began at start. A trust question on its screen holds the prompt (hold).
// It is ready when its agent reports that it waits for input or is done, or
// when its output has been quiet for readyQuiet and readyMin has passed, but
// never while the program has turned bracketed paste on and then off again:
// it is between screens (Claude Code while it starts), and what is typed then
// loses its Enter. capped is the wait reaching readyCap.
func readyAt(rd readiness, start, now time.Time) (ready, capped, hold bool) {
	if rd.state == session.AttentionNeedsInput && rd.source == session.SourceTrust {
		return false, false, true
	}
	if rd.state == session.AttentionNeedsInput || rd.state == session.AttentionDone {
		return true, false, false
	}
	capped = now.Sub(start) >= readyCap
	if rd.pasteSeen && !rd.pasteOn {
		return false, capped, false
	}
	if !rd.lastOutput.IsZero() && now.Sub(rd.lastOutput) >= readyQuiet && now.Sub(start) >= readyMin {
		return true, false, false
	}
	return false, capped, false
}
```

- [ ] **Step 4: The prompt through `Submit`.** Replace `prompt` (`:488-532`, from its comment to its closing brace) with:

```go
// prompt waits for m's session to be ready and submits m's prompt, the goal
// in it, as one line (typedPrompt), and m is running; a member without a
// prompt runs at once. A trust question on the member's screen holds the
// prompt until a person answers it (awaitReady), noted once in the run log
// each time. m is prompted as the Enter is written, so a done the agent
// reported before it never starts the members after m. A session that ends
// first gets no prompt: m ends, with how its process ended. An error means
// the start failed: ctx ended or the prompt could not be written.
func (e *Engine) prompt(ctx context.Context, r *run, m *member, local *session.Local) error {
	name := m.def.Name
	text := typedPrompt(m.def.Prompt, r.goal)
	hasPrompt := strings.TrimSpace(text) != ""
	if hasPrompt {
		err := e.await(ctx, local, func(question string) {
			e.mu.Lock()
			r.note(session.ActivityStatus, "%s asks %s: answer it in %s's terminal; its prompt waits", name, quote(question), name)
			e.mu.Unlock()
		})
		switch {
		case errors.Is(err, errNotReady):
			e.mu.Lock()
			r.note(session.ActivityStatus, "%s was not ready after %v: typing its prompt anyway", name, readyCap)
			e.mu.Unlock()
		case errors.Is(err, session.ErrSessionEnded):
			e.endedEarly(r, m, local)
			return nil
		case err != nil:
			return err
		}
		res, err := local.Submit(ctx, session.Submission{Text: text, ByName: typedBy, Confirm: true, BeforeEnter: func() {
			e.mu.Lock()
			m.markPrompted()
			e.mu.Unlock()
		}})
		if err != nil {
			if errors.Is(err, session.ErrSessionEnded) || endsSoon(local) {
				e.endedEarly(r, m, local)
				return nil
			}
			return fmt.Errorf("typing its prompt: %w", err)
		}
		e.mu.Lock()
		switch {
		case !res.Entered:
			// A question came up during the pause: the prompt waits in the
			// agent's input, and is never typed again.
			m.markPrompted()
			r.note(session.ActivityError, "typed %s's prompt without its Enter: %s waits on a question; answer it, then press Enter in its terminal", name, name)
		case res.Reentered:
			r.note(session.ActivityStatus, "pressed Enter again for %s: it did not report taking its prompt within %v", name, session.ConfirmWait)
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	m.markPrompted()
	m.state.Status = MemberRunning
	if hasPrompt {
		r.note(session.ActivityStatus, "typed %s's prompt", name)
	} else {
		r.touch()
	}
	e.mu.Unlock()
	m.poke() // the handoffs waiting for its prompt may go
	return nil
}
```

In `internal/crew/crew.go`, the import of `internal/proto` becomes `internal/session`, and `checkTypedPrompt` (`:345-353`) becomes:

```go
// checkTypedPrompt checks that m's prompt, with goal in place of $GOAL, made
// one line (typedPrompt), fits what a session submits at once
// (session.MaxSubmitText, the paste markers around it in one INPUT frame): a
// member never fails to start for the size of its prompt. The error matches
// ErrInvalid.
func (m Member) checkTypedPrompt(goal string) error {
	if n := typedPromptLen(m.Prompt, goal) - 1; n > session.MaxSubmitText {
		return invalidf("member %s: the prompt with the goal in place of $GOAL is %d bytes, more than %d", quote(m.Name), n, session.MaxSubmitText)
	}
	return nil
}
```

(`typedPromptLen` keeps counting the carriage return; its comment's "the bytes typing the prompt writes with its carriage return" stays true of the two writes together.)

- [ ] **Step 5: Handoffs through `Submit`.** In `internal/crew/handoff.go` `deliver`, replace from `typed, err := l.TypeUnlessWaiting(h.text+"\r", typedBy)` to the end of the loop body (`:170-191`) with:

```go
		res, err := l.Submit(r.ctx, session.Submission{Text: h.text, ByName: typedBy, UnlessWaiting: true})
		if e.tried != nil {
			e.tried(m.def.Name, res.Typed)
		}
		e.mu.Lock()
		switch {
		case err != nil && !res.Typed:
			r.note(session.ActivityError, "handoff dropped from %s to %s: %v", h.from, m.def.Name, err)
		case err != nil:
			r.note(session.ActivityError, "handoff from %s to %s typed without its Enter: %v", h.from, m.def.Name, err)
		case res.Entered:
			r.note(session.ActivityStatus, "handoff delivered from %s to %s", h.from, m.def.Name)
		case res.Typed:
			// A question came up during the pause: the text waits in the
			// agent's input, and is never typed again.
			r.note(session.ActivityError, "handoff from %s to %s typed without its Enter: %s waits on a question; answer it, then press Enter in its terminal", h.from, m.def.Name, m.def.Name)
		default:
			// Waiting for input: back to the head, the oldest again.
			m.handoffs = slices.Insert(m.handoffs, 0, h)
			if len(m.handoffs) > maxHandoffs {
				r.dropOldest(m)
			}
			r.noteQueued(m, "is waiting for input")
		}
		e.mu.Unlock()
		if err == nil && !res.Typed {
			waitWake(r, m, l)
		}
	}
}
```

and in `deliver`'s comment (`:141-143`) the lines

```go
// deliver types the handoffs waiting for m into l, its session, in order and
// each with a carriage return, once m runs, one at a time with
// session.Local.TypeUnlessWaiting: a handoff l takes while it waits for input
```

become

```go
// deliver submits the handoffs waiting for m into l, its session, in order,
// once m runs, one at a time with session.Local.Submit (UnlessWaiting: a
// paste, then Enter): a handoff l takes while it waits for input
```

Run: `go test -race -count=1 ./internal/crew`
Expected: PASS (`internal/api` builds once Step 7 is done).

- [ ] **Step 6: The raw path is the only one left.** In `internal/session/local.go`, delete `Type`, `TypeUnlessWaiting` and `write` with their comments, and make `Input` (its comment and body):

```go
// Input forwards keystrokes from a controller, raw. They answer the
// needs_input prompt that was showing as they were written, unless they are
// only a terminal's own report (isTerminalReport: xterm answering a cursor
// position query), which is written and answers nothing.
func (s *Local) Input(sub *Subscription, data []byte) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	s.mu.Lock()
	ended := s.info.Status.Ended()
	// The prompt this input answers is the one on the screen as it is typed.
	// A process that reacts before Write returns (an echo that rings the bell)
	// may raise the next one meanwhile, and typing must not clear that one.
	// Each attention change gets a new Since, which tells the prompts apart.
	promptSince := s.info.Attention.Since
	s.mu.Unlock()
	if ended {
		return ErrSessionEnded
	}
	if _, err := s.proc.Write(data); err != nil {
		return err
	}
	if isTerminalReport(data) {
		return nil
	}
	s.stampTyping(sub)
	s.answer(promptSince, sub.ID, sub.Name, true, bytes.IndexByte(data, '\r') >= 0)
	return nil
}
```

`ErrTextTooLong`'s comment becomes "refuses a submission whose text is longer than MaxSubmitText bytes once cleaned: nothing was written" and its message `fmt.Errorf("session: text to submit is longer than %d bytes", MaxSubmitText)`; drop `"strings"` from the imports. In `internal/session/local_test.go` delete `TestTypeWritesAndRecordsInput`, `TestTypeRefusesTextLongerThanAnInputFrame` and `TestTypeUnlessWaitingLeavesAPromptAlone` (with the comment above each; `submit_test.go` covers what they did) and the import of `"sync/atomic"` they used. In `internal/session/sessiontest/fakeproc_test.go` (`:26-31`):

```go
	if _, err := s.Submit(t.Context(), session.Submission{Text: "go", ByName: "crew"}); err != nil {
		t.Fatal(err)
	}
	if got := string(<-p.Input) + string(<-p.Input); got != "go\r" {
		t.Fatalf("typed %q", got)
	}
```

Run: `go test -race -count=1 ./internal/session/...`
Expected: PASS.

- [ ] **Step 7: Broadcasts through `Submit`, and run events.** `internal/api/runs.go`: drop the `internal/proto` import, add `"sync"`; the skip reasons (`:36-41`) gain

```go
	skipNoEnter    = "no_enter"    // typed, but a prompt came up before its Enter: the line waits in its input
)

// broadcastTimeout bounds a broadcast's submissions, which go on when the
// client goes away: one cut in its pause would leave a line without its Enter.
const broadcastTimeout = 15 * time.Second
```

(the closing parenthesis shown is the block's own). In `handleBroadcast` the length case becomes `case len(line) > maxBroadcast:` (4096 is well under `session.MaxSubmitText`), and from `byName := session.CleanName(req.ByName)` to the end of `broadcastTo` becomes:

```go
	byName := session.CleanName(req.ByName)
	var unique []string
	seen := map[string]bool{}
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			unique = append(unique, name)
		}
	}
	// Each member's submission pauses before its Enter: they go together,
	// one per member, and the reply keeps the order asked.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), broadcastTimeout)
	defer cancel()
	reasons := make([]string, len(unique))
	var wg sync.WaitGroup
	for i, name := range unique {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reasons[i] = s.broadcastTo(ctx, run, name, line, byName)
		}()
	}
	wg.Wait()
	sent, skipped := []string{}, []broadcastSkip{}
	for i, name := range unique {
		if reasons[i] == "" {
			sent = append(sent, name)
		} else {
			skipped = append(skipped, broadcastSkip{Member: name, Reason: reasons[i]})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "skipped": skipped})
}

// broadcastTo submits line into the member of run with the given name, unless
// its session waits on a prompt, and returns why it did not, or "".
func (s *Server) broadcastTo(ctx context.Context, run crew.Run, name, line, byName string) string {
	i := slices.IndexFunc(run.Members, func(m crew.MemberState) bool { return m.Name == name })
	if i < 0 {
		return skipUnknown
	}
	m := run.Members[i]
	if m.Status != crew.MemberRunning {
		return skipNotRunning
	}
	local, ok := s.lookupLocal(m.SessionID)
	if !ok {
		return skipNotRunning
	}
	res, err := local.Submit(ctx, session.Submission{Text: line, ByName: byName, UnlessWaiting: true})
	switch {
	case err != nil && !res.Typed:
		// It ended, or the write failed as its process went.
		if !errors.Is(err, session.ErrSessionEnded) {
			s.log.Warn("broadcast: could not type into a member", "run", run.ID, "member", name, "err", err)
		}
		return skipNotRunning
	case err != nil:
		s.log.Warn("broadcast: typed into a member without its Enter", "run", run.ID, "member", name, "err", err)
		return skipNoEnter
	case !res.Typed:
		return skipNeedsInput
	case !res.Entered:
		return skipNoEnter
	}
	return ""
}
```

and the comment of `handleBroadcast` (`:288-293`):

```go
// handleBroadcast submits a line into the members of a run a request names,
// or into every member: 200 {sent, skipped}, both in the order asked, a name
// given twice typed once. Each member's line goes in as Local.Submit types
// (a paste, then Enter 250 ms later), all at once, recorded as input by the
// admin's display name. A member whose session waits on a prompt is skipped
// (needs_input), as is one not running (not_running), a name no member has
// (unknown), and one whose Enter was left out because a prompt came up during
// the pause (no_enter): its line waits in its input, never typed again.
```

`internal/api/events.go`: replace `removed` (`:137-150`) with:

```go
// removed announces that a session left the registry.
func (h *eventHub) removed(id string) {
	h.broadcast([]byte("event: removed\ndata: " + fmt.Sprintf("{\"id\":%q}", id) + "\n\n"))
}

// runEvent is the data of a `run` event: the run to read again, or, with
// Removed, the run the server forgot. It carries no more than the ID, so it
// is at most maxRunEvent bytes whatever happened to the run.
type runEvent struct {
	ID      string `json:"id"`
	Removed bool   `json:"removed,omitempty"`
}

// maxRunEvent bounds a run event's data: a run ID is a crew ID (at most 40
// characters, crew.ValidID) and 9 more.
const maxRunEvent = 128

// run announces that a run changed in a way no session change carries
// (crew.Engine.OnRunChange), or, removed, that the engine forgot it
// (OnForget). Like a session change it is state a client cannot do without: a
// client that cannot keep up is dropped and starts over from a snapshot. It
// runs under the engine's lock and never waits.
func (h *eventHub) run(id string, removed bool) {
	b, err := json.Marshal(runEvent{ID: id, Removed: removed})
	if err != nil || len(b) > maxRunEvent {
		return
	}
	h.broadcast([]byte("event: run\ndata: " + string(b) + "\n\n"))
}

// broadcast queues msg for every client; a client that cannot keep up is
// dropped.
func (h *eventHub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			delete(h.clients, ch)
			close(ch)
		}
	}
}
```

`internal/api/server.go`, the `OnForget` hook (`:168-172`) ends with `s.events.run(runID, true)`, and after it:

```go
	// What a session change does not carry of a run reaches the browsers as
	// a run event, which they read the run again for.
	s.runs.OnRunChange = func(runID string) { s.events.run(runID, false) }
```

(the engine calls both under its lock; the hub takes only its own and never waits). Append to `internal/api/events_test.go`:

```go
// A run event names the run and nothing more, and is dropped, never cut,
// past its bound.
func TestEventHubRunEvent(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	h.run("api-sweep-0123abcd", false)
	h.run("api-sweep-0123abcd", true)
	h.run(strings.Repeat("x", maxRunEvent), false)
	for _, want := range []string{
		"event: run\ndata: {\"id\":\"api-sweep-0123abcd\"}\n\n",
		"event: run\ndata: {\"id\":\"api-sweep-0123abcd\",\"removed\":true}\n\n",
	} {
		if got := string(<-ch); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if len(ch) != 0 {
		t.Fatalf("an event over %d bytes was sent", maxRunEvent)
	}
}
```

Create `internal/api/runevents_test.go`:

```go
package api

import (
	"testing"
	"time"
)

// Run changes reach the event stream as run events: the launch, and the run
// forgotten as removed.
func TestRunEventsReachTheStream(t *testing.T) {
	e := newTestEnv(t, nil)
	ch := e.srv.events.subscribe()
	defer e.srv.events.unsubscribe(ch)
	runID := e.launchCrew(t, "Evented", catMember("a", "immediately"))
	want := `event: run` + "\ndata: " + `{"id":"` + runID + `"}` + "\n\n"
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			if string(msg) == want {
				e.srv.events.run(runID, true)
				for got := range ch {
					if string(got) == `event: run`+"\ndata: "+`{"id":"`+runID+`","removed":true}`+"\n\n" {
						return
					}
				}
				t.Fatal("no removed event")
			}
		case <-deadline:
			t.Fatal("no run event")
		}
	}
}
```

- [ ] **Step 8: An agent at rest is done.** In `internal/notify/mappers.go`: in `MapClaudeHook`'s notification cases, after `case "permission_prompt":`'s return:

```go
		case "idle_prompt":
			// Claude Code at rest after its turn ("Claude is waiting for your
			// input", sent after a minute idle) asks nothing: it is done, as
			// its Stop said, and a crew's handoffs and broadcasts reach it.
			return Request{State: "done", Message: truncate(msg, 200), Kind: "done"}, true
```

`CodexPayload` and `MapCodex` become:

```go
// CodexPayload is the subset of the Codex notify payload we use.
type CodexPayload struct {
	Type                 string   `json:"type"`
	LastAssistantMessage string   `json:"last-assistant-message"`
	InputMessages        []string `json:"input-messages"`
}

// codexTitlePrompt begins what Codex asks the hidden thread that names a
// conversation (live, codex 0.159): its turn reports through notify like a
// turn of the user's, with a thread id of its own, and is not one.
const codexTitlePrompt = "Generate a concise, single-line task title"

// MapCodex turns a Codex notify payload into an attention update: the end of
// a turn is done, as Claude Code's Stop is (a finished agent is idle, waiting
// for its next line, and a crew types handoffs and broadcasts into it); what
// Codex asks a person comes through its bell and its hooks. The turn of the
// hidden thread that titles a conversation is not the user's, and maps to
// nothing.
func MapCodex(raw []byte) (req Request, ok bool) {
	var p CodexPayload
	if json.Unmarshal(raw, &p) != nil {
		return Request{}, false
	}
	switch p.Type {
	case "agent-turn-complete":
		if len(p.InputMessages) > 0 && strings.HasPrefix(p.InputMessages[0], codexTitlePrompt) {
			return Request{}, false
		}
		msg := truncate(strings.TrimSpace(p.LastAssistantMessage), 200)
		if msg == "" {
			msg = "Codex finished its turn"
		}
		return Request{State: "done", Message: msg, Kind: "done"}, true
	}
	return Request{}, false
}
```

In `internal/notify/notify_test.go`: the `idle_prompt` case of `TestMapClaudeHook` (`:24`) expects `"done"`; `TestMapCodex` expects `req.State != "done"` to fail it; and `TestMapCodexIsPromptKind` becomes:

```go
// The end of a Codex turn is done, as Claude Code's Stop is: the agent is
// idle, and a crew's handoffs and broadcasts are typed into it.
func TestMapCodexIsDoneKind(t *testing.T) {
	req, _ := MapCodex([]byte(`{"type":"agent-turn-complete","last-assistant-message":"Need a decision"}`))
	if req.State != "done" || req.Kind != "done" {
		t.Fatalf("%+v", req)
	}
	// The hidden thread that titles the conversation (live, Codex 0.159) is not a turn of the user's.
	if _, ok := MapCodex([]byte(`{"type":"agent-turn-complete","input-messages":["Generate a concise, single-line task title for this conversation"],"last-assistant-message":"Reply READY"}`)); ok {
		t.Fatal("the title thread's turn mapped")
	}
}
```

- [ ] **Step 9: `docs/protocol.md`.** The `GET /api/events` row (`:440`) becomes:

```
| `GET /api/events` | admin | Server-Sent Events of session changes (`snapshot`, `session`, `removed`), of activity entries (`activity`, with `state`, the state an attention entry records, absent for other entries and for an attention entry from a host that does not send one), and of run changes no session event carries (`run`: `{id}`, read the run again with `GET /api/runs/{run}`, or `{id, removed: true}` when the server forgot it; at most 128 bytes; sent for a member reserved, started, prompted, failed or ended early, every entry of the run's log, and a stop; a client that cannot keep up is dropped as for a session change, and reads every run again on its next `snapshot`), see Attention and Events |
```

and in Attention (`:232-236`), the sentence "Changes are pushed to attached clients … and `activity` for every activity entry, see Events)." becomes:

```
Changes are pushed to attached clients as the `attention` control message and
to admins as `session` events on `GET /api/events` (Server-Sent Events over a
header-authenticated `fetch`: `snapshot` with the full list first, then
`session` per change, `removed{id}` when a session leaves the registry,
`activity` for every activity entry, see Events, and `run{id}` when a crew run
changes in a way no session change carries, or `run{id, removed: true}` when
the server forgets it, see Crew runs).
```

- [ ] **Step 10: Run everything, then commit**

Run: `make lint && go test -race -count=1 ./...`
Expected: clean, PASS.

```bash
git add internal/crew/run.go internal/crew/handoff.go internal/crew/crew.go internal/crew/run_test.go internal/crew/handoff_test.go internal/crew/prompt_test.go internal/session/local.go internal/session/local_test.go internal/session/sessiontest/fakeproc_test.go internal/api/runs.go internal/api/events.go internal/api/server.go internal/api/events_test.go internal/api/runevents_test.go internal/notify/mappers.go internal/notify/notify_test.go docs/protocol.md
git commit -m "crew: prompts, handoffs and broadcasts are submitted; trust questions hold the prompt; runs have a state and send run events"
```

- [ ] **Step 11: The live check against the real CLIs.** On this machine (Claude Code 2.1.287 at `~/.local/bin/claude`, Codex 0.159.0 on the nvm PATH; check `claude --version && codex --version` first and note any other version). The server runs with the user's real `HOME` (the CLIs' logins), its own data directory and public URL; the repository is a git repository with one commit that Claude Code already trusts, `$REPO` below (the prompt investigation used its scratch repository `root/c2`, not kept). Make it once: `mkdir -p /tmp/conductor-live/c2 && git -C /tmp/conductor-live/c2 init -q && git -C /tmp/conductor-live/c2 commit -q --allow-empty -m init`, then run `claude` in it, accept its trust question and quit (`~/.claude.json` then has `projects["/tmp/conductor-live/c2"].hasTrustDialogAccepted = true`). Codex is trusted per launch by the crew's yolo (Task 3's override), so neither file gains a trust entry. The test touches neither file; the CLIs keep their own bookkeeping in them (Claude Code its counters in `~/.claude.json`, Codex the `[tui.model_availability_nux]` counter in `~/.codex/config.toml`), so a copy of `config.toml` is taken and that counter put back. Quota: about ten one-word turns per agent.

```bash
ROOT=/tmp/conductor-live; REPO=$ROOT/c2   # the repository Claude Code trusts (above); ROOT is the server's allowed root
L=$(mktemp -d /tmp/conductor-live4.XXXXXX)   # this check's files: config, logs, script, output
git -C $REPO log --oneline -1   # a commit to branch the worktrees from
cp ~/.codex/config.toml $L/codex-config.before
make build-go
cat > $L/conductor.json <<EOF
{"listen": "127.0.0.1:8098", "adminToken": "live4-admin-token", "allowedRoots": ["$ROOT"], "defaultCwd": "$REPO"}
EOF
CONDUCTOR_DATA_DIR=$(mktemp -d $L/data.XXXXXX) CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8098 \
  bin/conductor serve --config $L/conductor.json > $L/server.log 2>&1 &
echo $! > $L/server.pid; sleep 2
cat > $L/live4.py <<'PY'
#!/usr/bin/env python3
"""Round 4 live check: prompts, handoffs and broadcasts, short and long, typed
into Claude Code and Codex in fresh worktrees run without a person pressing
Enter. The proof is each agent's own answer (the message of its done report),
never the echo of what was typed.

Environment: BASE (the test server, http://127.0.0.1:8098), TOK (its admin
token), REPO (a git repository with a commit that Claude Code already trusts;
Codex is trusted per launch by the crew's yolo). Exit 0 and "PASS live4", or 1
at the first failure with what was seen.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE, TOK, REPO = os.environ["BASE"], os.environ["TOK"], os.environ["REPO"]
FILLER = "This sentence is filler to make the line long; ignore it. " * 24  # about 1400 bytes
STEP_TIMEOUT = 240


def api(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + TOK, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def fail(msg, **seen):
    print("FAIL live4:", msg)
    for k, v in seen.items():
        print(f"  {k}: {json.dumps(v)[:2000]}")
    sys.exit(1)


def member(run_id, name):
    _, out = api("GET", f"/api/runs/{run_id}")
    for m in out.get("run", {}).get("members", []):
        if m["name"] == name:
            return m, out["run"]
    fail(f"no member {name}", run=out)


def wait_session(run_id, name):
    deadline = time.time() + STEP_TIMEOUT
    while time.time() < deadline:
        m, _ = member(run_id, name)
        if m.get("sessionId") and m["status"] in ("starting", "running"):
            return m["sessionId"]
        if m["status"] == "ended":
            fail(f"{name} ended", member=m)
        time.sleep(1)
    fail(f"{name} has no session")


def wait_word(run_id, name, sid, word):
    """Waits for the member's agent to report a finished turn that says word."""
    deadline = time.time() + STEP_TIMEOUT
    att = {}
    while time.time() < deadline:
        _, out = api("GET", f"/api/sessions/{sid}")
        att = out.get("session", {}).get("attention", {})
        if word in (att.get("message") or "").upper() and att.get("state") in ("done", "needs_input"):
            print(f"  {name}: {att['state']} {att.get('message')!r}")
            return
        if out.get("session", {}).get("status") in ("exited", "stopped"):
            fail(f"{name}'s session ended waiting for {word}", session=out.get("session"))
        time.sleep(1)
    _, run = api("GET", f"/api/runs/{run_id}")
    fail(f"{name} did not answer {word} within {STEP_TIMEOUT}s", attention=att, log=run.get("run", {}).get("log"))


def main():
    code, out = api("POST", "/api/crews", {
        "name": "Live round 4", "goal": "answer", "cwd": REPO, "where": "server", "isolation": "worktree", "yolo": True,
        "openAfterLaunch": False,
        "members": [
            {"name": "cc", "agentId": "claude", "prompt": "Reply with the single word READY and nothing else.", "start": {"when": "immediately"}},
            {"name": "cx", "agentId": "codex", "prompt": "Reply with the single word READY and nothing else.", "start": {"when": "immediately"}},
        ]})
    if code != 201:
        fail("create crew", reply=out)
    crew_id = out["crew"]["id"]
    code, out = api("POST", f"/api/crews/{crew_id}/launch")
    if code != 201:
        fail("launch", reply=out)
    run_id = out["run"]["id"]
    print("run", run_id)
    sid = {n: wait_session(run_id, n) for n in ("cc", "cx")}

    print("initial prompt, short")
    for n in ("cc", "cx"):
        wait_word(run_id, n, sid[n], "READY")

    print("handoffs")
    for frm, to in (("cc", "cx"), ("cx", "cc")):
        code, out = api("POST", f"/api/sessions/{sid[frm]}/events",
                        {"type": "handoff", "to": to, "message": "Reply with the single word HANDOFF and nothing else."})
        if code != 202:
            fail("handoff", reply=out)
        wait_word(run_id, to, sid[to], "HANDOFF")

    print("broadcast, short and long")
    for text, word in (("Reply with the single word BROADCAST and nothing else.", "BROADCAST"),
                       (FILLER + "Reply with the single word LONGCAST and nothing else.", "LONGCAST")):
        code, out = api("POST", f"/api/runs/{run_id}/broadcast", {"text": text, "byName": "live4"})
        if code != 200 or sorted(out.get("sent", [])) != ["cc", "cx"]:
            fail("broadcast", reply=out)
        for n in ("cc", "cx"):
            wait_word(run_id, n, sid[n], word)

    print("initial prompt, long (members added to the run)")
    for name, agent in (("cc2", "claude"), ("cx2", "codex")):
        code, out = api("POST", f"/api/runs/{run_id}/members", {
            "name": name, "agentId": agent, "prompt": FILLER + "Reply with the single word LONGSTART and nothing else.",
            "start": {"when": "immediately"}})
        if code != 201:
            fail("add member", reply=out)
        sid[name] = wait_session(run_id, name)
    for n in ("cc2", "cx2"):
        wait_word(run_id, n, sid[n], "LONGSTART")

    _, out = api("GET", f"/api/runs/{run_id}")
    log = [e.get("message", "") for e in out["run"]["log"]]
    for line in log:
        print("  log:", line)
    reentered = [line for line in log if line.startswith("pressed Enter again")]
    held = [line for line in log if " asks " in line or "without its Enter" in line]
    if held:
        fail("a prompt was held or typed without its Enter", log=held)
    api("POST", f"/api/runs/{run_id}/stop")
    api("DELETE", f"/api/crews/{crew_id}")
    print(f"PASS live4 (re-Enters: {len(reentered)})")


if __name__ == "__main__":
    main()
PY
BASE=http://127.0.0.1:8098 TOK=live4-admin-token REPO=$REPO python3 $L/live4.py | tee $L/live4.out
kill $(cat $L/server.pid)
diff $L/codex-config.before ~/.codex/config.toml   # only Codex's own nux counter may differ: put it back with
cp $L/codex-config.before ~/.codex/config.toml
grep -c "$REPO" ~/.codex/config.toml          # 0: Codex saved no trust
```

Expected: `PASS live4`, every step answered by the agent with its word (READY, HANDOFF, BROADCAST, LONGCAST, LONGSTART), no run log line with "asks" or "without its Enter", and no trust entry for the repository in `~/.codex/config.toml`. A "pressed Enter again" line for `cc`/`cc2` is allowed (the 3 s re-Enter doing its job); the script counts them. If a step fails, `live4.out` and the run log say which; fix, rebuild and run again; do not loosen the script. Keep `live4.out`: Task 10's "Open verification (round 4)" lists this run as passed against Claude Code 2.1.287 and Codex 0.159.0, so when it passes against other versions, write those (and the date) there instead, and when it cannot be run, say so there instead of listing it as passed. Remove `$L/data.*` afterwards; the worktrees stay under `$REPO/.conductor/worktrees/` (git ignores them through `info/exclude`) and may be removed with `git -C $REPO worktree prune` after deleting them.

**Done when:**

- The role prompt (with `Confirm`, the member stamped prompted in `BeforeEnter`), handoffs (`UnlessWaiting`) and broadcasts (`UnlessWaiting`, every member at once) go through `Local.Submit`; a submission whose Enter was left out is noted in the run log and never typed again, and a broadcast member left without its Enter is reported `no_enter`.
- Readiness: a program that turned bracketed paste on and then off is not typed into; a trust question holds the prompt past the cap, noted once, and the wait starts over once a person answers it with Enter.
- `Type`, `TypeUnlessWaiting` and `write` are gone: `Input` is the only raw path.
- Every read of a run derives its `state` (running, needs input with a count, stopped, finished); `OnRunChange` reports what no session change carries, and the server publishes `event: run` `{id}`, or `{id, removed: true}`, at most 128 bytes, on `/api/events`.
- An agent at rest reports `done` (Claude Code's `idle_prompt`, Codex's turn end), and Codex's title-thread payload maps to nothing.
- `docs/protocol.md`'s `GET /api/events` row and the Attention sentence say so.
- `make lint && go test -race -count=1 ./...` is clean and passes.
- Committed: "crew: prompts, handoffs and broadcasts are submitted; trust questions hold the prompt; runs have a state and send run events".
- The live check (Step 11) prints `PASS live4` against the real CLIs, every word answered, no "asks" or "without its Enter" line, no trust entry for the repository in `~/.codex/config.toml`, and `config.toml` put back; or, where it could not run, Task 10's "Open verification (round 4)" says so instead of listing it as passed.

---

### Task 5: Agent sessions are named and resumable

**Order:** needs Tasks 3 and 4. Its care points are an id that reaches argv and the engine's member lifecycle.

Each built-in agent with a known way to name or learn its own session carries a session recipe beside its yolo recipe (`session` on the catalog agent): `startArgs` that choose the id at launch where the CLI allows it (`{id}` filled with a fresh UUID, or a `cdr-<uuid>` name for Goose), `idFrom: hook` where the id is captured from the hook payloads `conductor notify` already maps (Claude Code's `session_id`, Codex's `thread-id`, Copilot's `sessionId`, Cursor's `conversation_id`, Antigravity's `conversationId`), an `idPolicy` (Codex: the lowest id, its main thread, not its title thread), `resumeArgs` placed right after the command (Codex resumes with a subcommand), an anchored `idPattern`, and `resumeNeedsCwd`. The id lives on the Conductor session (`agentSession {id, resumable, source}` in `Info` and on the stream); it becomes resumable once the agent reports a turn (Claude Code writes no transcript before its first prompt, and `--resume` of such an id fails, live). An ended session can be resumed — `POST /api/sessions/{id}/resume`: a new session with the same agent, name, working directory, arguments, yolo choice and crew membership, launched with `command ++ resumeArgs(id)`; without a recipe, or before a turn, it is relaunched plainly and the reply's `notice` says so. A crew member is resumed as the member (`Engine.ResumeMember`) in its worktree and branch; a resumed member gets no prompt (its conversation has it), a member started anew gets its role prompt again. Ids are checked twice before they reach argv: the shape every id has (a letter or digit first, so never a flag; `[A-Za-z0-9._:-]`; at most 128 bytes) and the agent's pattern. One conversation is resumed by one session at a time.

Scope (Spec/code note): the recipes are the resume report's (`docs/round4/session-resume.md`); agents whose ids reach Conductor only through a plugin (opencode, omp, amp, pi's capture, dsh) and aider (whose handle is a chat-history file) get no recipe this round and are relaunched plainly — Task 10 lists them as deferred. Resume works while the ended session is listed (`exitedRetention`, 10 min by default) and, for a crew member, while the run is kept (the member keeps its last agent session); a store of resume records beyond that is deferred. A member of a stopped run is refused (`409 run_stopped`): reopening a stopped run would swap the run's context under goroutines that read it. Hosted sessions answer `400 hosted_session`. A positional prompt in an ad-hoc session's arguments is passed again on resume (the README says so).

**Files:**
- Create: `internal/session/agentsession.go`, `internal/session/agentsession_test.go`, `internal/api/resume.go`, `internal/api/resume_test.go`
- Modify: `internal/catalog/catalog.go` (imports, the `Agent` struct after Task 3's `TrustPrompt`, `validate`, before `// cut keeps`, `inherit`, `Agent.clone`); `internal/catalog/defaults.go` (before `defaults()`, the claude, codex, agy, copilot, cursor, pi and goose entries); `internal/catalog/catalog_test.go` (append); `internal/session/info.go` (after Task 3's `Yolo`); `internal/session/local.go` (`Options`, `Info()`); `internal/notify/notify.go` (`Request`, `Send`); `internal/notify/mappers.go` (`ClaudeHook`, `MapClaudeHook`, `CodexPayload`, `MapCodex`, `MapCodexHook`, `MapCopilotHook`, `MapCursorHook`, `MapAgyHook`); `internal/notify/mappers_test.go`, `notify_test.go` (append); `internal/cli/notify_test.go` (`:194`, `:196`); `internal/api/attention.go` (`attentionRequest` `:11-16`, `handleAttention` `:180-183`); `internal/api/sessions.go` (`createSessionRequest`, `createLocalSession`); `internal/api/runs.go` (`Launch`, `runError`); `internal/api/server.go` (routes); `internal/crew/run.go` (`LaunchSpec`, the errors, `MemberState`, append); `internal/crew/handoff.go` (`OnChange` `:47-54`); `internal/crew/run_test.go` (`launchCall`, `fakeLauncher.Launch`); `internal/crew/prompt_test.go` (imports, append)

**Interfaces:**
- Consumes: Task 3's `createLocalSession` path, `LaunchSpec`, `Launched` options seam; Task 4's `MapCodex`, `prompt`/`finish`, `runError`.
- Produces:
```go
// internal/catalog
type SessionRecipe struct {
	StartArgs      []string `json:"startArgs,omitempty"`
	NewID          string   `json:"newId,omitempty"`    // "uuid" (default) | "name"
	IDFrom         string   `json:"idFrom,omitempty"`   // "hook" | ""
	IDPolicy       string   `json:"idPolicy,omitempty"` // "latest" (default) | "lowest"
	ResumeArgs     []string `json:"resumeArgs,omitempty"`
	IDPattern      string   `json:"idPattern,omitempty"`
	ResumeNeedsCwd bool     `json:"resumeNeedsCwd,omitempty"`
}
func (r *SessionRecipe) Empty() bool
const IDArg = "{id}"
func Expand(args []string, id string) []string
// Agent gains Session *SessionRecipe `json:"session,omitempty"`
// internal/session
type AgentSession struct{ ID string `json:"id"`; Resumable bool `json:"resumable"`; Source string `json:"source"` }
const AgentSessionSet, AgentSessionHook, AgentSessionResumed = "set", "hook", "resumed"
const MaxAgentSessionID = 128
const PolicyLatest, PolicyLowest = "latest", "lowest"
func ValidAgentSessionID(id string, pattern *regexp.Regexp) bool
func (s *Local) SetAgentSession(as AgentSession)
func (s *Local) ReportAgentSession(id, policy string, turn bool)
type Launched struct{ AgentID, Name, Cwd string; Args []string; Env map[string]string; Yolo bool }
func (s *Local) Launched() Launched
// Info gains AgentSession *AgentSession `json:"agentSession,omitempty"`, ResumedFrom string `json:"resumedFrom,omitempty"`; Options gains Launched Launched
// internal/notify: Request gains AgentSession string `json:"agentSession,omitempty"`, Turn bool `json:"turn,omitempty"`
// internal/crew
// LaunchSpec gains Resume, ResumedFrom string; MemberState gains AgentSession *session.AgentSession `json:"agentSession,omitempty"`
var ErrMemberRunning = errors.New("the member is still running")
func (e *Engine) ResumeMember(ctx context.Context, runID, name, resume string) (string, error)
// internal/api
// POST /api/sessions/{id}/resume, POST /api/runs/{run}/members/{name}/resume → 201 {session, resumed, notice?}
// attentionRequest gains AgentSession string `json:"agentSession"`, Turn bool `json:"turn"`
```

- [ ] **Step 1: The catalog's recipe, test first.** Append to `internal/catalog/catalog_test.go`:

```go
// A session recipe is bounded like a command, places {id} as a whole argument
// once, says how the id is known, and anchors an id pattern that cannot match
// an empty id or one that reads as a flag. The empty recipe disables an
// inherited one.
func TestSessionRecipeValidation(t *testing.T) {
	good := SessionRecipe{StartArgs: []string{"--session-id", IDArg}, ResumeArgs: []string{"--resume", IDArg}, IDPattern: `^[0-9a-f-]{36}$`}
	a := Agent{ID: "x", Name: "X", Command: []string{"x"}, Session: &good}
	if err := validate(a); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]SessionRecipe{
		"no resume args":       {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume"}},
		"{id} twice":           {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{IDArg, IDArg}},
		"{id} inside an arg":   {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume=" + IDArg}},
		"no way to the id":     {IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"unanchored pattern":   {IDFrom: "hook", IDPattern: `[0-9a-f]+`, ResumeArgs: good.ResumeArgs},
		"pattern takes a flag": {IDFrom: "hook", IDPattern: `^.+$`, ResumeArgs: good.ResumeArgs},
		"pattern takes empty":  {IDFrom: "hook", IDPattern: `^[a-z]*$`, ResumeArgs: good.ResumeArgs},
		"bad policy":           {IDFrom: "hook", IDPolicy: "newest", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"bad idFrom":           {IDFrom: "screen", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"bad newId":            {StartArgs: good.StartArgs, NewID: "ulid", IDPattern: good.IDPattern, ResumeArgs: good.ResumeArgs},
		"NUL":                  {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: []string{"--resume\x00", IDArg}},
		"too many args":        {StartArgs: good.StartArgs, IDPattern: good.IDPattern, ResumeArgs: append(slices.Repeat([]string{"-x"}, 16), IDArg)},
	} {
		b := a
		b.Session = &r
		if err := validate(b); err == nil || !strings.Contains(err.Error(), "session") {
			t.Errorf("%s: %v", name, err)
		}
	}
	b := a
	b.Session = &SessionRecipe{}
	if err := validate(b); err != nil || !b.Session.Empty() || !(*SessionRecipe)(nil).Empty() {
		t.Fatalf("the empty recipe: %v", err)
	}
	if got := Expand([]string{"resume", IDArg, "-c", "x={id}y"}, "abc"); !slices.Equal(got, []string{"resume", "abc", "-c", "x={id}y"}) {
		t.Fatalf("Expand: %q", got)
	}
}

// The built-ins' session recipes, as the resume research found them (the
// adapter matrix says which are verified live): the agents whose ids reach
// Conductor only through a plugin, and the shell, have none yet.
func TestDefaultSessionRecipes(t *testing.T) {
	with := map[string][]string{
		"claude":  {"--resume", IDArg},
		"codex":   {"resume", IDArg, "-c", `tui.resume_cwd="session"`},
		"agy":     {"--conversation", IDArg},
		"copilot": {"--session-id", IDArg},
		"cursor":  {"--resume", IDArg},
		"pi":      {"--session-id", IDArg},
		"goose":   {"session", "--resume", "--name", IDArg},
	}
	for _, a := range defaults() {
		want, ok := with[a.ID]
		if !ok {
			if !a.Session.Empty() {
				t.Errorf("%s has a recipe: %+v", a.ID, a.Session)
			}
			continue
		}
		if a.Session.Empty() || !slices.Equal(a.Session.ResumeArgs, want) {
			t.Errorf("%s: %+v", a.ID, a.Session)
		}
	}
	claude, _ := Default().Get("claude")
	re := regexp.MustCompile(claude.Session.IDPattern)
	if !re.MatchString("3f80c8bd-0000-4000-8000-000000000001") || re.MatchString("--dangerously-skip-permissions") {
		t.Fatal("claude's id pattern")
	}
	codex, _ := Default().Get("codex")
	if codex.Session.IDPolicy != "lowest" || codex.Session.IDFrom != "hook" || len(codex.Session.StartArgs) != 0 {
		t.Fatalf("codex: %+v", codex.Session)
	}
	if c := Default(); c.ApplyOverlay(Overlay{Agents: []Agent{{ID: "claude", Name: "C", Command: []string{"claude"}}}}) != nil {
		t.Fatal("overlay")
	} else if a, _ := c.Get("claude"); a.Session.Empty() {
		t.Fatal("a saved override that leaves the recipe out lost the built-in's")
	}
}
```

Run: `go test ./internal/catalog -run 'Session'` — Expected: FAIL to compile (`undefined: SessionRecipe`).

In `internal/catalog/catalog.go`, add `"reflect"` to the imports (after `"os"`), end the `Agent` struct (after Task 3's `TrustPrompt`) with

```go
	// Session is the agent's session recipe: how Conductor names the agent's
	// own session and resumes it. nil in a saved override takes the replaced
	// agent's; an empty recipe ({}) has none, so Resume relaunches plainly.
	Session *SessionRecipe `json:"session,omitempty"`
```

and after `Yolo.clone`:

```go
// SessionRecipe is how Conductor names an agent's own session (its
// conversation) and resumes it. "{id}" is a whole argument, never part of
// one: it stands for a fresh id in StartArgs and for the stored id in
// ResumeArgs, which go right after the agent's command (Codex resumes with a
// subcommand).
type SessionRecipe struct {
	// StartArgs choose the id at launch: "{id}" becomes a fresh one (NewID).
	// Empty: the agent chooses, and reports it (IDFrom).
	StartArgs []string `json:"startArgs,omitempty"`
	// NewID is how a fresh id is made: "uuid" (the default) or "name"
	// ("cdr-" and a uuid).
	NewID string `json:"newId,omitempty"`
	// IDFrom is "hook" when the agent's hook reports carry its id
	// (internal/notify reads it), "" when only StartArgs set it.
	IDFrom string `json:"idFrom,omitempty"`
	// IDPolicy says which of the ids the agent reports is its session's:
	// "latest" (the default), or "lowest" (Codex, whose title thread
	// reports too).
	IDPolicy string `json:"idPolicy,omitempty"`
	// ResumeArgs resume the stored id: "{id}" is it.
	ResumeArgs []string `json:"resumeArgs,omitempty"`
	// IDPattern is the shape of the agent's ids: anchored RE2 (^…$), at most
	// 200 bytes. An id that does not match, or does not begin with a letter
	// or a digit, is never stored nor passed.
	IDPattern string `json:"idPattern,omitempty"`
	// ResumeNeedsCwd says the agent resumes only in the directory the session
	// ran in: when that is gone, Resume is refused rather than run elsewhere.
	ResumeNeedsCwd bool `json:"resumeNeedsCwd,omitempty"`
}

// Empty reports whether r is no recipe: nil, or nothing to resume with.
func (r *SessionRecipe) Empty() bool { return r == nil || len(r.ResumeArgs) == 0 }

// IDArg is the placeholder of a recipe's id.
const IDArg = "{id}"

// Expand returns args with each IDArg element replaced by id.
func Expand(args []string, id string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == IDArg {
			a = id
		}
		out[i] = a
	}
	return out
}

func (r *SessionRecipe) clone() *SessionRecipe {
	if r == nil {
		return nil
	}
	c := *r
	c.StartArgs, c.ResumeArgs = slices.Clone(r.StartArgs), slices.Clone(r.ResumeArgs)
	return &c
}
```

In `validate`, after Task 3's trust prompt check:

```go
	if a.Session != nil {
		if err := validateSession(*a.Session); err != nil {
			return fmt.Errorf("agent %s: session: %w", a.ID, err)
		}
	}
```

before `// cut keeps the first 40 bytes`:

```go
// validateSession holds a session recipe to the bounds of a command: at most
// 16 arguments of at most 4096 bytes without NUL each way, "{id}" a whole
// argument, once in ResumeArgs and once in StartArgs when it has any; a known
// NewID, IDFrom and IDPolicy; a way to know the id (StartArgs or IDFrom); and
// an anchored IDPattern that matches neither an empty id nor one that begins
// with a dash. The empty recipe, which disables an inherited one, passes.
func validateSession(r SessionRecipe) error {
	if reflect.ValueOf(r).IsZero() {
		return nil
	}
	count := func(name string, args []string, need bool) error {
		if len(args) > maxYoloArgs {
			return fmt.Errorf("too many %s (at most %d)", name, maxYoloArgs)
		}
		n := 0
		for i, a := range args {
			if a == "" || strings.ContainsRune(a, 0) || len(a) > maxCommandArg {
				return fmt.Errorf("%s[%d] must be 1 to %d bytes without NUL", name, i, maxCommandArg)
			}
			if a == IDArg {
				n++
			} else if strings.Contains(a, IDArg) {
				return fmt.Errorf("%s[%d]: %s must be a whole argument", name, i, IDArg)
			}
		}
		if (need || len(args) > 0) && n != 1 {
			return fmt.Errorf("%s must hold %s once", name, IDArg)
		}
		return nil
	}
	if err := count("resumeArgs", r.ResumeArgs, true); err != nil {
		return err
	}
	if err := count("startArgs", r.StartArgs, false); err != nil {
		return err
	}
	switch {
	case r.NewID != "" && r.NewID != "uuid" && r.NewID != "name":
		return fmt.Errorf("newId must be uuid or name")
	case r.IDFrom != "" && r.IDFrom != "hook":
		return fmt.Errorf("idFrom must be hook or empty")
	case r.IDPolicy != "" && r.IDPolicy != "latest" && r.IDPolicy != "lowest":
		return fmt.Errorf("idPolicy must be latest or lowest")
	case len(r.StartArgs) == 0 && r.IDFrom == "":
		return fmt.Errorf("startArgs or idFrom must say how the id is known")
	}
	if len(r.IDPattern) > maxSignalPattern || !strings.HasPrefix(r.IDPattern, "^") || !strings.HasSuffix(r.IDPattern, "$") {
		return fmt.Errorf("idPattern must be anchored (^…$), at most %d bytes", maxSignalPattern)
	}
	re, err := regexp.Compile(r.IDPattern)
	if err != nil {
		return fmt.Errorf("idPattern: %w", err)
	}
	if re.MatchString("") || re.MatchString("-x") || re.MatchString("--x") {
		return fmt.Errorf("idPattern must not match an empty id or one that begins with a dash")
	}
	return nil
}

```

in `inherit`, after Task 3's trust prompt: `if a.Session == nil { a.Session = prev.Session.clone() }`, and in `Agent.clone`, after `a.Yolo = a.Yolo.clone()`: `a.Session = a.Session.clone()`.

`ResumeNeedsCwd` is used by the resume route (Step 5): the stored directory goes through `resolveCwd` again; when it is gone, a resume whose recipe needs it is refused (`400 invalid_cwd`), anything else starts in the agent's or the server's default directory.

In `internal/catalog/defaults.go`, after `package catalog` and before `// defaults lists the built-in agents` (the comment of `defaults()`, which stays on it):

```go
// uuidPattern is the shape of a UUID as the agents print it.
const uuidPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

```

and in the entries, after each one's `Yolo` (Task 3; after `Signal:` for pi, which has none) — each recipe from the resume report, verified as its adapter-matrix row says (Task 10):

```go
			// claude: verified live (2.1.287)
			Session: &SessionRecipe{StartArgs: []string{"--session-id", IDArg}, IDFrom: "hook", ResumeArgs: []string{"--resume", IDArg}, IDPattern: uuidPattern},
			// codex: verified live (0.159.0); the id is captured, never chosen
			Session: &SessionRecipe{IDFrom: "hook", IDPolicy: "lowest", ResumeArgs: []string{"resume", IDArg, "-c", `tui.resume_cwd="session"`}, IDPattern: uuidPattern, ResumeNeedsCwd: true},
			// agy: verified live in print mode (1.2.14)
			Session: &SessionRecipe{IDFrom: "hook", ResumeArgs: []string{"--conversation", IDArg}, IDPattern: uuidPattern, ResumeNeedsCwd: true},
			// copilot: verified live (1.0.59)
			Session: &SessionRecipe{StartArgs: []string{"--session-id", IDArg}, IDFrom: "hook", ResumeArgs: []string{"--session-id", IDArg}, IDPattern: uuidPattern},
			// cursor: from docs
			Session: &SessionRecipe{IDFrom: "hook", ResumeArgs: []string{"--resume", IDArg}, IDPattern: uuidPattern, ResumeNeedsCwd: true},
			// pi: from source and docs
			Session: &SessionRecipe{StartArgs: []string{"--session-id", IDArg}, ResumeArgs: []string{"--session-id", IDArg}, IDPattern: `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`, ResumeNeedsCwd: true},
			// goose: from source and docs
			Session: &SessionRecipe{StartArgs: []string{"session", "--name", IDArg}, NewID: "name", ResumeArgs: []string{"session", "--resume", "--name", IDArg}, IDPattern: `^cdr-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, ResumeNeedsCwd: true},
```

(the `//` lines say which entry each goes in.) Run: `go test -race -count=1 ./internal/catalog` — Expected: PASS.

- [ ] **Step 2: The session's agent session.** Create `internal/session/agentsession.go`:

```go
package session

import (
	"regexp"
	"strings"
)

// An agent's own session (its conversation) as Conductor knows it, for
// Resume: an id Conductor chose at launch, one the agent reported through its
// hooks, or the one a resumed session took over.

// Where an agent session's id came from (AgentSession.Source).
const (
	AgentSessionSet     = "set"     // Conductor chose it at launch
	AgentSessionHook    = "hook"    // the agent reported it
	AgentSessionResumed = "resumed" // this session resumed it
)

// AgentSession is the agent's own session in a Conductor session.
type AgentSession struct {
	ID string `json:"id"`
	// Resumable is set once the agent has reported a turn (or the session
	// resumed one): an agent never prompted keeps nothing to resume (Claude
	// Code writes no transcript until its first request).
	Resumable bool   `json:"resumable"`
	Source    string `json:"source"`
}

// MaxAgentSessionID bounds an agent session id, in bytes.
const MaxAgentSessionID = 128

// agentSessionShape is what every agent session id looks like, whatever its
// agent's own pattern says: a letter or digit first, so that an id can never
// read as a flag, then letters, digits and . _ : -.
var agentSessionShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidAgentSessionID reports whether id has the shape of an agent session
// id and matches the agent's pattern (catalog.SessionRecipe.IDPattern).
func ValidAgentSessionID(id string, pattern *regexp.Regexp) bool {
	return len(id) <= MaxAgentSessionID && agentSessionShape.MatchString(id) && pattern != nil && pattern.MatchString(id)
}

// Agent session id policies (catalog.SessionRecipe.IDPolicy).
const (
	PolicyLatest = "latest" // the newest id reported is the session's
	PolicyLowest = "lowest" // the smallest: Codex's main thread, not its title thread
)

// SetAgentSession records the agent session a launch chose or resumed.
func (s *Local) SetAgentSession(as AgentSession) {
	s.mu.Lock()
	s.info.AgentSession = &as
	s.mu.Unlock()
	s.notifyChange()
}

// ReportAgentSession takes the id an agent's report carries, already checked
// (ValidAgentSessionID): the session's when it has none, or by policy (the
// latest one; the lowest one); turn marks the session resumable. A change
// notifies OnChange.
func (s *Local) ReportAgentSession(id, policy string, turn bool) {
	s.mu.Lock()
	cur := s.info.AgentSession
	next := AgentSession{ID: id, Source: AgentSessionHook}
	switch {
	case cur == nil:
	case cur.ID == id:
		next = *cur
	case policy == PolicyLowest && strings.Compare(id, cur.ID) > 0:
		next = *cur
	}
	next.Resumable = next.Resumable || (turn && next.ID == id)
	changed := cur == nil || *cur != next
	if changed {
		s.info.AgentSession = &next
	}
	s.mu.Unlock()
	if changed {
		s.notifyChange()
	}
}

// Launched is what a server session was launched with, kept for Resume: the
// agent, the name, the working directory, the arguments and variables of the
// launch, and its yolo choice.
type Launched struct {
	AgentID, Name, Cwd string
	Args               []string
	Env                map[string]string
	Yolo               bool
}

// Launched returns what the session was launched with (Options.Launched).
func (s *Local) Launched() Launched {
	l := s.opts.Launched
	l.Args = append([]string(nil), l.Args...)
	env := make(map[string]string, len(l.Env))
	for k, v := range l.Env {
		env[k] = v
	}
	l.Env = env
	return l
}
```

In `internal/session/info.go`, after Task 3's `Yolo`:

```go
	// AgentSession is the agent's own session, when known (Resume).
	AgentSession *AgentSession `json:"agentSession,omitempty"`
	// ResumedFrom is the session this one resumed or relaunched.
	ResumedFrom string `json:"resumedFrom,omitempty"`
```

In `internal/session/local.go`, `Options` ends with

```go
	// Launched is what the session was launched with, for Resume.
	Launched Launched
```

and `Info()` copies the agent session, so that no caller shares it:

```go
	info := s.info
	info.Viewers = s.hub.Count()
	if info.AgentSession != nil {
		as := *info.AgentSession
		info.AgentSession = &as
	}
	return info
```

Create `internal/session/agentsession_test.go`:

```go
package session

import (
	"regexp"
	"strings"
	"testing"
)

// The agent's reported id becomes the session's, by the agent's policy: the
// latest one, or the lowest (Codex's main thread, not its title thread); a
// turn makes it resumable.
func TestReportAgentSession(t *testing.T) {
	s, _ := newLocalWith(t, quiet(Options{}))
	s.ReportAgentSession("01a0fc76-4817", PolicyLowest, false)
	s.ReportAgentSession("01a0fc76-3ab9", PolicyLowest, true)
	s.ReportAgentSession("01a0fc76-9999", PolicyLowest, true)
	if as := s.Info().AgentSession; as == nil || as.ID != "01a0fc76-3ab9" || !as.Resumable || as.Source != AgentSessionHook {
		t.Fatalf("lowest: %+v", as)
	}
	s, _ = newLocalWith(t, quiet(Options{}))
	s.SetAgentSession(AgentSession{ID: "a", Source: AgentSessionSet})
	s.ReportAgentSession("a", PolicyLatest, false)
	if as := s.Info().AgentSession; as.ID != "a" || as.Resumable || as.Source != AgentSessionSet {
		t.Fatalf("set: %+v", as)
	}
	s.ReportAgentSession("b", PolicyLatest, true)
	if as := s.Info().AgentSession; as.ID != "b" || !as.Resumable {
		t.Fatalf("latest: %+v", as)
	}
	re := regexp.MustCompile(`^[a-z0-9-]+$`)
	for id, want := range map[string]bool{"abc-1": true, "-abc": false, "": false, strings.Repeat("a", 129): false, "a b": false} {
		if got := ValidAgentSessionID(id, re); got != want {
			t.Errorf("ValidAgentSessionID(%q) = %v", id, got)
		}
	}
}
```

Run: `go test -race -count=1 ./internal/session` — Expected: PASS.

- [ ] **Step 3: Capture from the hooks.** `internal/notify/notify.go`, `Request` ends with

```go
	// AgentSession is the agent's own session id, as its hook payload names
	// it (Claude Code's session_id, Codex's thread-id…), and Turn says the
	// payload reports a turn: a prompt taken or finished. They go with an
	// attention state only; the server keeps the id for Resume.
	AgentSession string `json:"agentSession,omitempty"`
	Turn         bool   `json:"turn,omitempty"`
```

and in `Send`'s loop, before `failed := fmt.Errorf(…)`:

```go
		if status == http.StatusBadRequest && strings.Contains(msg, "unknown field") && (req.AgentSession != "" || req.Turn) && req.Event == "" {
			// A server older than the agent-session fields: the state alone.
			req.AgentSession, req.Turn = "", false
			if body, err = json.Marshal(req); err != nil {
				return err
			}
			continue
		}
```

`internal/notify/mappers.go`:

1. `ClaudeHook` gains `SessionID string `json:"session_id"``; `MapClaudeHook`'s body after the unmarshal becomes a wrapper around the old switch, renamed `mapClaudeHook(h ClaudeHook) (req Request, ok bool)`:

```go
	req, ok = mapClaudeHook(h)
	if ok && req.Event == "" {
		req.AgentSession = truncate(h.SessionID, 128)
		req.Turn = h.HookEventName == "Stop" || h.HookEventName == "UserPromptSubmit"
	}
	return req, ok
}

func mapClaudeHook(h ClaudeHook) (req Request, ok bool) {
	switch h.HookEventName {
```

2. `CodexPayload` gains `ThreadID string `json:"thread-id"`` (after `LastAssistantMessage`), and `MapCodex`'s done (Task 4) carries it: `return Request{State: "done", Message: msg, Kind: "done", AgentSession: truncate(p.ThreadID, 128), Turn: true}, true`.
3. `MapCodexHook`'s struct gains `SessionID string `json:"session_id"``; its `Stop` returns `…, Kind: "done", AgentSession: truncate(h.SessionID, 128), Turn: true}`.
4. `MapCopilotHook`'s struct gains `SessionID string `json:"sessionId"``; `agentstop` returns `Request{State: "done", Kind: "done", AgentSession: truncate(h.SessionID, 128), Turn: true}` and `userpromptsubmitted` `Request{State: "working", AgentSession: truncate(h.SessionID, 128), Turn: true}`.
5. `MapCursorHook`'s struct gains `ConversationID string `json:"conversation_id"``; `stop` returns `Request{State: "done", Kind: "done", AgentSession: truncate(h.ConversationID, 128), Turn: true}`.
6. `MapAgyHook`'s struct gains `ConversationID string `json:"conversationId"``; the termination returns `…, Kind: "done", AgentSession: truncate(h.ConversationID, 128), Turn: true}`.

Append to `internal/notify/mappers_test.go`:

```go
// The mappers carry the agent's own session id on an attention state, and
// say when the payload is of a turn: Claude Code's session_id, Codex's
// thread-id. Codex's title thread reports nothing.
func TestMappersCarryTheAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  func() (Request, bool)
		id   string
		turn bool
	}{
		{"claude stop", func() (Request, bool) {
			return MapClaudeHook([]byte(`{"hook_event_name":"Stop","session_id":"3f80c8bd-0000-4000-8000-000000000001"}`))
		}, "3f80c8bd-0000-4000-8000-000000000001", true},
		{"claude notification", func() (Request, bool) {
			return MapClaudeHook([]byte(`{"hook_event_name":"Notification","message":"hi","session_id":"s1"}`))
		}, "s1", false},
		{"codex turn", func() (Request, bool) {
			return MapCodex([]byte(`{"type":"agent-turn-complete","thread-id":"01a0fc72-f99b-7331-aec3-818001309536","input-messages":["reply READY"],"last-assistant-message":"READY"}`))
		}, "01a0fc72-f99b-7331-aec3-818001309536", true},
	} {
		req, ok := tc.got()
		if !ok || req.AgentSession != tc.id || req.Turn != tc.turn {
			t.Errorf("%s: %+v %v", tc.name, req, ok)
		}
	}
	for name, got := range map[string]func() (Request, bool){
		"codex hook": func() (Request, bool) { return MapCodexHook([]byte(`{"hook_event_name":"Stop","session_id":"t1"}`)) },
		"copilot": func() (Request, bool) {
			return MapCopilotHook([]byte(`{"hook_event_name":"agentStop","sessionId":"t1"}`))
		},
		"cursor": func() (Request, bool) {
			return MapCursorHook([]byte(`{"hook_event_name":"stop","conversation_id":"t1"}`))
		},
		"agy": func() (Request, bool) {
			return MapAgyHook([]byte(`{"terminationReason":"done","conversationId":"t1"}`))
		},
	} {
		if req, ok := got(); !ok || req.AgentSession != "t1" || !req.Turn {
			t.Errorf("%s: %+v %v", name, req, ok)
		}
	}
	if req, _ := MapClaudeHook([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","session_id":"s1"}`)); req.AgentSession != "" {
		t.Errorf("an event carried the id: %+v", req)
	}
	if _, ok := MapCodex([]byte(`{"type":"agent-turn-complete","thread-id":"01a0fc76-4817","input-messages":["Generate a concise, single-line task title for this conversation"]}`)); ok {
		t.Error("the title thread's turn was mapped")
	}
}
```

and to `internal/notify/notify_test.go`:

```go
// A server that predates the agent-session fields refuses them as unknown:
// the state goes again without them, once.
func TestSendDropsTheAgentSessionForAnOlderServer(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if _, ok := body["agentSession"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"json: unknown field \"agentSession\""}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/api/sessions/s1/attention", "tok", Request{State: "done", AgentSession: "s1", Turn: true}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[1]["state"] != "done" || bodies[1]["agentSession"] != nil || bodies[1]["turn"] != nil {
		t.Fatalf("bodies %v", bodies)
	}
}
```

In `internal/cli/notify_test.go` `TestNotifyHookFlagsMapTheirPayloads`, the Copilot case (`:194`) now expects `map[string]any{"state": "done", "kind": "done", "agentSession": "s", "turn": true}` and the Antigravity case (`:196`) `map[string]any{"state": "done", "message": "completed", "kind": "done", "turn": true}`.

Run: `go test -race -count=1 ./internal/notify ./internal/cli` — Expected: PASS.

- [ ] **Step 4: The engine resumes a member.** `internal/crew/run.go`: `LaunchSpec` ends with

```go
	// Resume, when set, is the agent session the launch resumes with its
	// agent's recipe (ResumeMember); ResumedFrom the session it follows.
	Resume, ResumedFrom string
```

the errors gain `ErrMemberRunning  = errors.New("the member is still running")` (after `ErrRunStopped`), `MemberState` gains, after Task 4's `NeedsInput`,

```go
	// AgentSession is the agent's own session in the member's latest session,
	// kept when that session has left the server: what ResumeMember resumes.
	AgentSession *session.AgentSession `json:"agentSession,omitempty"`
```

and at the end of the file:

```go
// ResumeMember starts an ended member of a run again, in its working
// directory (its worktree and branch, which stay), as a session that resumes
// the agent session resume names with the agent's recipe, or, with resume
// "", as a fresh one: then its role prompt is typed again once it is ready,
// as at its first start; a resumed agent has its conversation, and gets no
// prompt. It returns the new session's ID once the session exists. A member
// still starting or running is refused (ErrMemberRunning), and so is one of a
// stopped run (ErrRunStopped).
func (e *Engine) ResumeMember(ctx context.Context, runID, name, resume string) (string, error) {
	e.mu.Lock()
	r, ok := e.runs[runID]
	if !ok {
		e.mu.Unlock()
		return "", ErrRunNotFound
	}
	m := r.member(name)
	var err error
	switch {
	case m == nil:
		err = ErrMemberNotFound
	case r.stopping:
		err = ErrRunStopped
	case m.state.Status == MemberPending:
		err = ErrMemberRunning
	case m.state.Status != MemberEnded && !e.ended(m):
		err = ErrMemberRunning
	}
	if err != nil {
		e.mu.Unlock()
		return "", err
	}
	old := m.state.SessionID
	cwd := r.cwd
	if r.isolation == IsolationWorktree && m.state.Worktree != "" {
		cwd = filepath.Join(m.state.Worktree, r.prefix)
	}
	m.state.Status = MemberStarting
	r.starts.Add(1)
	r.touch()
	e.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()
	local, err := e.launcher.Launch(ctx, LaunchSpec{AgentID: m.def.AgentID, Name: name, Cwd: cwd, Args: slices.Clone(m.def.Args),
		Env: map[string]string{"GOAL": r.goal}, Ref: session.CrewRef{RunID: r.id, CrewID: r.crewID, Member: name}, Yolo: r.yolo,
		Resume: resume, ResumedFrom: old})
	if err != nil {
		r.starts.Done()
		e.fail(r, m, err)
		return "", fmt.Errorf("member %s: %w", quote(name), err)
	}
	id := local.Info().ID
	started := time.Now().UTC()
	e.mu.Lock()
	if old != "" {
		e.bySession.Delete(old)
	}
	m.state.SessionID, m.state.Started, m.state.Ended, m.state.Err = id, &started, nil, ""
	e.bySession.Store(id, sessionMember{r, m})
	if resume != "" {
		m.state.Status = MemberRunning
		r.note(session.ActivityStatus, "%s resumed its conversation", name)
	} else {
		// A new conversation: its prompt is typed again, and only a done
		// after that prompt counts.
		m.prompted, m.promptedAt = false, time.Time{}
		r.note(session.ActivityStatus, "%s started anew", name)
	}
	e.mu.Unlock()
	if resume != "" {
		r.starts.Done()
		m.poke()
		return id, nil
	}
	go e.finish(r, m, local) // types its prompt again, and ends the start
	return id, nil
}

// ended reports whether m's session has ended or is gone, as refresh shows
// it. The caller holds e.mu; it reads the session, which never calls into the
// engine while it holds its lock.
func (e *Engine) ended(m *member) bool {
	if m.state.SessionID == "" {
		return false
	}
	l, ok := e.lookup(m.state.SessionID)
	return !ok || l.Info().Status.Ended()
}
```

`internal/crew/handoff.go`, `OnChange` keeps the member's agent session (the engine never calls into a session while it holds `e.mu`, and OnChange runs outside the session's lock, so taking `e.mu` here keeps the lock order):

```go
func (e *Engine) OnChange(info session.Info) {
	if info.Crew == nil {
		return
	}
	v, ok := e.bySession.Load(info.ID)
	if !ok {
		return
	}
	sm := v.(sessionMember)
	if info.AgentSession != nil {
		// Kept on the member, for a resume once the session has left the
		// server.
		e.mu.Lock()
		if sm.m.state.SessionID == info.ID {
			as := *info.AgentSession
			sm.m.state.AgentSession = &as
		}
		e.mu.Unlock()
	}
	sm.m.poke()
}
```

and its comment gains: "It also keeps the agent session the change carries on the member." In `internal/crew/run_test.go`, `launchCall` gains `resume string` and `fakeLauncher.Launch` records `spec.Resume`. In `internal/crew/prompt_test.go`, add `"errors"` to the imports and append:

```go
// ResumeMember starts an ended member again, as the member, in its directory:
// with an agent session id it resumes it and types no prompt; without one it
// starts anew and types the prompt again. A member that runs, or one of a
// stopped run, is refused.
func TestResumeMember(t *testing.T) {
	e, fl := newEngine(t)
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), manual("qa", "Check it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	if _, err := e.ResumeMember(t.Context(), run.ID, "lead", "x"); !errors.Is(err, ErrMemberRunning) {
		t.Fatalf("running: %v", err)
	}
	if _, err := e.ResumeMember(t.Context(), run.ID, "qa", ""); !errors.Is(err, ErrMemberRunning) {
		t.Fatalf("pending: %v", err)
	}
	lead, p := fl.member("lead")
	p.End(0)
	<-lead.Ended()
	id, err := e.ResumeMember(t.Context(), run.ID, "lead", "3f80c8bd-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	m := memberState(t, e, run.ID, "lead")
	fl.mu.Lock()
	call := fl.calls[len(fl.calls)-1]
	fl.mu.Unlock()
	if m.SessionID != id || m.Status != MemberRunning || call.resume != "3f80c8bd-0000-4000-8000-000000000001" || call.cwd != "/work" {
		t.Fatalf("resumed: %+v %+v", m, call)
	}
	_, p2 := fl.member("lead")
	time.Sleep(100 * time.Millisecond)
	if w := typed(p2); len(w) != 0 {
		t.Fatalf("a resumed member was typed %q", w)
	}
	got, _ := e.Get(run.ID)
	if !logged(got, "lead resumed its conversation") {
		t.Fatalf("log %+v", got.Log)
	}
	// Anew: the prompt again.
	l2, _ := fl.member("lead")
	_, p2 = fl.member("lead")
	p2.End(0)
	<-l2.Ended()
	if _, err := e.ResumeMember(t.Context(), run.ID, "lead", ""); err != nil {
		t.Fatal(err)
	}
	_, p3 := fl.member("lead")
	if got := waitTyped(t, p3, 5*time.Second); got != "Plan it.\r" {
		t.Fatalf("anew: typed %q", got)
	}
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResumeMember(t.Context(), run.ID, "lead", ""); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("stopped: %v", err)
	}
}
```

Run: `go test -race -count=1 ./internal/crew` — Expected: PASS.

- [ ] **Step 5: The routes.** `internal/api/attention.go`: `attentionRequest` ends with

```go
	// AgentSession and Turn: the agent's own session id its report names,
	// and whether the report is of a turn (Resume).
	AgentSession string `json:"agentSession"`
	Turn         bool   `json:"turn"`
```

and `handleAttention` (`:180-183`) becomes

```go
	if len(req.AgentSession) > session.MaxAgentSessionID {
		writeError(w, http.StatusBadRequest, "invalid_request", "agentSession is longer than 128 bytes")
		return
	}
	if !reportAttention(w, d, source, state, req.Message, req.Kind, req.Options) {
		return
	}
	if local, ok := d.(*session.Local); ok && req.AgentSession != "" {
		s.captureAgentSession(local, req.AgentSession, req.Turn)
	}
	writeJSON(w, http.StatusOK, map[string]any{"attention": d.Info().Attention})
```

(a hosted session's id is dropped: resume is server-only this round). Create `internal/api/resume.go`:

```go
package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"regexp"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/crew"
	"github.com/phenixrizen/conductor/internal/session"
)

// Resume. A server session whose agent has a session recipe
// (catalog.SessionRecipe) knows its agent's own session: the id it chose at
// launch, or the one the agent's hooks report. An ended session can be
// resumed: a new session with the same agent, name, directory, arguments,
// yolo choice and crew membership, launched with the recipe's resume
// arguments; without a recipe, or before the agent has had a turn, it is
// relaunched plainly and the reply says so.

// sessionPattern returns agent's id pattern and its recipe, or nil when it
// has no recipe.
func sessionPattern(agent catalog.Agent) (*regexp.Regexp, *catalog.SessionRecipe) {
	r := agent.Session
	if r.Empty() {
		return nil, nil
	}
	re, err := regexp.Compile(r.IDPattern)
	if err != nil {
		return nil, nil
	}
	return re, r
}

// captureAgentSession keeps the agent session id a report of local's agent
// names, when the agent's recipe takes ids from its hooks and the id has the
// recipe's shape; anything else is dropped.
func (s *Server) captureAgentSession(local *session.Local, id string, turn bool) {
	agent, ok := s.Catalog().Get(local.Info().AgentID)
	if !ok {
		return
	}
	re, r := sessionPattern(agent)
	if r == nil || r.IDFrom != "hook" || !session.ValidAgentSessionID(id, re) {
		s.log.Debug("agent session id dropped", "session", local.Info().ID)
		return
	}
	policy := r.IDPolicy
	if policy == "" {
		policy = session.PolicyLatest
	}
	local.ReportAgentSession(id, policy, turn)
}

// newAgentSessionID makes a fresh id for a recipe's StartArgs: a UUID, or
// "cdr-" and one for NewID "name".
func newAgentSessionID(kind string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	u := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	if kind == "name" {
		return "cdr-" + u
	}
	return u
}

// resumeID is the agent session id a resume of info would take up: its
// agent's, when the agent has a recipe and has had a turn, and the id has
// the recipe's shape; "" for a plain relaunch.
func resumeID(agent catalog.Agent, as *session.AgentSession) string {
	re, r := sessionPattern(agent)
	if r == nil || as == nil || !as.Resumable || !session.ValidAgentSessionID(as.ID, re) {
		return ""
	}
	return as.ID
}

// liveAgentSession reports whether a session that runs holds the agent
// session id: one conversation is resumed by one session at a time.
func (s *Server) liveAgentSession(id string) bool {
	for _, info := range s.registry.List() {
		if !info.Status.Ended() && info.AgentSession != nil && info.AgentSession.ID == id {
			return true
		}
	}
	return false
}

// resumeReply is the reply of the resume routes.
func resumeReply(info session.Info, resumed bool, agent catalog.Agent) map[string]any {
	out := map[string]any{"session": info, "resumed": resumed}
	if !resumed {
		if agent.Session.Empty() {
			out["notice"] = agent.Name + " has no session recipe: started anew"
		} else {
			out["notice"] = "nothing to resume yet (the agent had no turn): started anew"
		}
	}
	return out
}

// handleResumeSession resumes an ended server session: 201 {session,
// resumed, notice?}. A crew member's session is resumed as the member
// (Engine.ResumeMember).
func (s *Server) handleResumeSession(w http.ResponseWriter, r *http.Request) {
	d, ok := s.registry.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	local, ok := d.(*session.Local)
	if !ok {
		writeError(w, http.StatusBadRequest, "hosted_session", "a hosted session is resumed from its machine")
		return
	}
	info := local.Info()
	if !info.Status.Ended() {
		writeError(w, http.StatusConflict, "still_running", "the session is still running")
		return
	}
	if info.Crew != nil {
		s.resumeMember(w, r, info.Crew.RunID, info.Crew.Member)
		return
	}
	launched := local.Launched()
	agent, ok := s.Catalog().Get(launched.AgentID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_agent", "the session's agent is no longer in the catalog")
		return
	}
	id := resumeID(agent, info.AgentSession)
	if id != "" && s.liveAgentSession(id) {
		writeError(w, http.StatusConflict, "already_resumed", "a running session holds that conversation")
		return
	}
	// The session's directory, unless it is gone: then an agent that resumes
	// only where it ran is refused, and anything else starts in the agent's
	// or the server's default directory.
	cwd := launched.Cwd
	if _, err := s.resolveCwd(cwd); err != nil {
		if id != "" && agent.Session.ResumeNeedsCwd {
			writeError(w, http.StatusBadRequest, "invalid_cwd", "the session's working directory is gone: "+err.Error())
			return
		}
		cwd = ""
	}
	yolo := launched.Yolo
	next, aerr := s.createLocalSession(createSessionRequest{AgentID: launched.AgentID, Name: launched.Name, Cwd: cwd,
		Args: launched.Args, Env: launched.Env, Yolo: &yolo, resume: id, resumedFrom: info.ID}, nil)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	writeJSON(w, http.StatusCreated, resumeReply(next.Info(), id != "", agent))
}

// handleResumeRunMember resumes an ended member of a run, in its worktree:
// 201 {session, resumed, notice?}.
func (s *Server) handleResumeRunMember(w http.ResponseWriter, r *http.Request) {
	s.resumeMember(w, r, r.PathValue("run"), r.PathValue("name"))
}

func (s *Server) resumeMember(w http.ResponseWriter, r *http.Request, runID, name string) {
	run, ok := s.runs.Get(runID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	var m *crew.MemberState
	for i := range run.Members {
		if run.Members[i].Name == name {
			m = &run.Members[i]
		}
	}
	if m == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such member in the run")
		return
	}
	agent, ok := s.Catalog().Get(m.AgentID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_agent", "the member's agent is no longer in the catalog")
		return
	}
	id := resumeID(agent, m.AgentSession)
	if id != "" && s.liveAgentSession(id) {
		writeError(w, http.StatusConflict, "already_resumed", "a running session holds that conversation")
		return
	}
	ctx, cancel := runContext(r)
	defer cancel()
	sessionID, err := s.runs.ResumeMember(ctx, runID, name, id)
	if err != nil {
		s.runError(w, "resume member", runID, err)
		return
	}
	d, ok := s.registry.Get(sessionID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "launch_failed", "the resumed session is gone")
		return
	}
	writeJSON(w, http.StatusCreated, resumeReply(d.Info(), id != "", agent))
}
```

`internal/api/sessions.go`: `createSessionRequest` gains, after Task 3's `Yolo`,

```go
	// resume, set by Resume, is the agent session id the launch resumes with
	// the agent's recipe; resumedFrom the session it resumes or relaunches.
	resume, resumedFrom string
```

in `createLocalSession` Task 3's `argv := append(append([]string{}, agent.Command...), req.Args...)` becomes

```go
	// The agent's own session: resumed by its recipe, or named at launch when
	// the recipe lets Conductor choose; its arguments go right after the
	// command, as Codex resumes with a subcommand.
	argv := append([]string{}, agent.Command...)
	var agentSession *session.AgentSession
	switch r := agent.Session; {
	case req.resume != "":
		argv = append(argv, catalog.Expand(r.ResumeArgs, req.resume)...)
		agentSession = &session.AgentSession{ID: req.resume, Resumable: true, Source: session.AgentSessionResumed}
	case !r.Empty() && len(r.StartArgs) > 0:
		id := newAgentSessionID(r.NewID)
		argv = append(argv, catalog.Expand(r.StartArgs, id)...)
		agentSession = &session.AgentSession{ID: id, Source: session.AgentSessionSet}
	}
	argv = append(argv, req.Args...)
```

the `info` literal gains `AgentSession: agentSession,` and `ResumedFrom: req.resumedFrom,` after Task 3's `Yolo: applied,`, and the options gain (after Task 3's `ConfirmSubmit:`) `Launched: session.Launched{AgentID: agent.ID, Name: name, Cwd: cwd, Args: slices.Clone(req.Args), Env: maps.Clone(req.Env), Yolo: yolo},`. (A resume whose stored directory no longer resolves is handled in `handleResumeSession` above: refused for a recipe with `resumeNeedsCwd`, else started in the default directory.)

`internal/api/runs.go`: `Launch` passes the resume through, `createSessionRequest{…, Yolo: &yolo, resume: spec.Resume, resumedFrom: spec.ResumedFrom}`; `runError` gains, after `ErrRunStopped`'s case:

```go
	case errors.Is(err, crew.ErrMemberRunning):
		writeError(w, http.StatusConflict, "still_running", err.Error())
```

`internal/api/server.go`, the routes: after `POST /api/runs/{run}/stop`, `mux.HandleFunc("POST /api/runs/{run}/members/{name}/resume", s.requireAdmin(s.handleResumeRunMember))`; after `DELETE /api/sessions/{id}`, `mux.HandleFunc("POST /api/sessions/{id}/resume", s.requireAdmin(s.handleResumeSession))`.

Create `internal/api/resume_test.go`:

```go
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// resumable saves an agent that prints its arguments and waits, with a
// session recipe that names the session at launch and resumes it.
func (e *testEnv) saveResumable(id string) {
	e.t.Helper()
	body := agentBody(id)
	body["command"] = []string{"/bin/sh", "-c", `printf 'ARGS[%s]\n' "$*"; exec /bin/cat`, "sh"}
	body["allowArgs"] = true
	body["session"] = map[string]any{
		"startArgs": []string{"--session-id", "{id}"}, "idFrom": "hook",
		"resumeArgs": []string{"--resume", "{id}"},
		"idPattern":  `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
	}
	e.save(body)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// stopAndWait stops a session and waits until it shows ended.
func (e *testEnv) stopAndWait(id string) {
	e.t.Helper()
	l := e.local(id)
	_ = l.Stop(e.t.Context())
	<-l.Ended()
}

// A session named at launch is resumed with the recipe's arguments once the
// agent has had a turn; before one, it is relaunched plainly and the reply
// says so; one conversation is resumed by one session at a time.
func TestResumeASession(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	info := e.launch("talker", []string{"--verbose"})
	as, _ := info["agentSession"].(map[string]any)
	cmd := commandOf(info)
	if as == nil || !uuidRe.MatchString(as["id"].(string)) || as["resumable"] != false || as["source"] != "set" ||
		!slices.Equal(cmd[4:], []string{"--session-id", as["id"].(string), "--verbose"}) {
		t.Fatalf("launch: %v %q", as, cmd)
	}
	id := info["id"].(string)

	// No turn yet: a plain relaunch.
	e.stopAndWait(id)
	resp, out := e.do("POST", "/api/sessions/"+id+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != false || !strings.Contains(out["notice"].(string), "started anew") {
		t.Fatalf("plain: %d %v", resp.StatusCode, out)
	}
	plain := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(plain["id"].(string)) })
	if plain["resumedFrom"] != id || slices.Contains(commandOf(plain), "--resume") {
		t.Fatalf("plain: %v", plain)
	}

	// A turn, reported by the agent's hook with its id: resumable.
	tok := e.agentToken(plain["id"].(string))
	pid := plain["agentSession"].(map[string]any)["id"].(string)
	resp, out = e.do("POST", "/api/sessions/"+plain["id"].(string)+"/attention", tok, map[string]any{"state": "done", "agentSession": pid, "turn": true})
	if resp.StatusCode != http.StatusOK || !e.local(plain["id"].(string)).Info().AgentSession.Resumable {
		t.Fatalf("report: %d %v", resp.StatusCode, out)
	}
	e.stopAndWait(plain["id"].(string))
	resp, out = e.do("POST", "/api/sessions/"+plain["id"].(string)+"/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
	if got := commandOf(next); !slices.Equal(got[4:], []string{"--resume", pid, "--verbose"}) || next["name"] != info["name"] {
		t.Fatalf("resumed: %q %v", got, next["name"])
	}
	// The same conversation again, while it runs: refused.
	if resp, out := e.do("POST", "/api/sessions/"+plain["id"].(string)+"/resume", adminToken, nil); resp.StatusCode != http.StatusConflict || errorCode(out) != "already_resumed" {
		t.Fatalf("twice: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", "/api/sessions/"+next["id"].(string)+"/resume", adminToken, nil); resp.StatusCode != http.StatusConflict || errorCode(out) != "still_running" {
		t.Fatalf("running: %d %v", resp.StatusCode, out)
	}
}

// An id that does not have the recipe's shape, or that would read as a flag,
// is never kept.
func TestAReportedIDOfTheWrongShapeIsDropped(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	info := e.launch("talker", nil)
	id := info["id"].(string)
	set := info["agentSession"].(map[string]any)["id"].(string)
	tok := e.agentToken(id)
	for _, bad := range []string{"--dangerously-skip-permissions", "not-a-uuid", strings.Repeat("a", 129)} {
		resp, _ := e.do("POST", "/api/sessions/"+id+"/attention", tok, map[string]any{"state": "working", "agentSession": bad, "turn": true})
		if bad == strings.Repeat("a", 129) {
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("long: %d", resp.StatusCode)
			}
			continue
		}
		if as := e.local(id).Info().AgentSession; as.ID != set || as.Resumable {
			t.Fatalf("%q was kept: %+v", bad, as)
		}
	}
}

// A crew member resumes in its worktree as the member, with no prompt typed.
func TestResumeACrewMember(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	runID := e.launchCrew(t, "Talk", map[string]any{"name": "a", "agentId": "talker", "prompt": "", "start": map[string]any{"when": "immediately"}})
	sid := e.waitRunning(t, runID, "a")
	as := e.local(sid).Info().AgentSession
	tok := e.agentToken(sid)
	e.do("POST", "/api/sessions/"+sid+"/attention", tok, map[string]any{"state": "done", "agentSession": as.ID, "turn": true})
	e.stopAndWait(sid)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out := e.do("GET", "/api/runs/"+runID, adminToken, nil)
		if runMember(t, out["run"].(map[string]any), "a")["status"] == "ended" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, out := e.do("POST", "/api/runs/"+runID+"/members/a/resume", adminToken, nil)
	if resp.StatusCode != http.StatusCreated || out["resumed"] != true {
		t.Fatalf("resume: %d %v", resp.StatusCode, out)
	}
	next := out["session"].(map[string]any)
	t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
	crew := next["crew"].(map[string]any)
	if crew["runId"] != runID || crew["member"] != "a" || !slices.Contains(commandOf(next), "--resume") {
		t.Fatalf("resumed member: %v", next)
	}
	if got := e.waitRunning(t, runID, "a"); got != next["id"] {
		t.Fatalf("member's session %s, want %s", got, next["id"])
	}
}

// A session whose directory is gone resumes in the default directory, unless
// its agent resumes only where it ran: then the resume is refused.
func TestResumeWhenTheDirectoryIsGone(t *testing.T) {
	e := newTestEnv(t, nil)
	e.saveResumable("talker")
	needs := agentBody("strict")
	needs["command"] = []string{"/bin/sh", "-c", "exec /bin/cat"}
	needs["session"] = map[string]any{"startArgs": []string{"--id", "{id}"}, "idFrom": "hook", "resumeArgs": []string{"--resume", "{id}"},
		"idPattern": `^[0-9a-f-]{36}$`, "resumeNeedsCwd": true}
	e.save(needs)
	for _, tc := range []struct {
		agent string
		code  int
	}{{"talker", http.StatusCreated}, {"strict", http.StatusBadRequest}} {
		dir := filepath.Join(e.root, "gone-"+tc.agent)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		resp, info := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": tc.agent, "cwd": dir})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("launch: %d %v", resp.StatusCode, info)
		}
		id := info["id"].(string)
		as := info["agentSession"].(map[string]any)["id"].(string)
		e.do("POST", "/api/sessions/"+id+"/attention", e.agentToken(id), map[string]any{"state": "done", "agentSession": as, "turn": true})
		e.stopAndWait(id)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		resp, out := e.do("POST", "/api/sessions/"+id+"/resume", adminToken, nil)
		if resp.StatusCode != tc.code {
			t.Fatalf("%s: %d %v", tc.agent, resp.StatusCode, out)
		}
		if tc.code == http.StatusCreated {
			next := out["session"].(map[string]any)
			t.Cleanup(func() { e.stopAndWait(next["id"].(string)) })
			if next["cwd"] != realRoot(t, e.root) {
				t.Fatalf("resumed in %v", next["cwd"])
			}
		} else if errorCode(out) != "invalid_cwd" {
			t.Fatalf("%s: %v", tc.agent, out)
		}
	}
}
```

- [ ] **Step 6: Run everything, then commit**

The new fields realign their structs and literals (`defaults.go`, `info.go`, the `info` and options literals in `sessions.go`, the mappers' payload structs): run `gofmt -w internal` first.

Run: `gofmt -w internal && make lint && go test -race -count=1 ./...`
Expected: clean, PASS.

```bash
git add internal/catalog/catalog.go internal/catalog/defaults.go internal/catalog/catalog_test.go internal/session/agentsession.go internal/session/agentsession_test.go internal/session/info.go internal/session/local.go internal/notify/notify.go internal/notify/mappers.go internal/notify/mappers_test.go internal/notify/notify_test.go internal/cli/notify_test.go internal/api/attention.go internal/api/resume.go internal/api/resume_test.go internal/api/sessions.go internal/api/runs.go internal/api/server.go internal/crew/run.go internal/crew/handoff.go internal/crew/run_test.go internal/crew/prompt_test.go
git commit -m "sessions: each agent's session is named or captured, and an ended session or crew member can be resumed"
```

**Done when:**

- `catalog.SessionRecipe` (`startArgs`, `newId`, `idFrom`, `idPolicy`, `resumeArgs`, `idPattern`, `resumeNeedsCwd`) is validated to its bounds (`{id}` a whole argument once; an anchored pattern that never matches `""` or a dash-led id), inherited and cloned; claude, codex, agy, copilot, cursor, pi and goose carry their recipes.
- `internal/session/agentsession.go`: an id is kept only when it matches the shape (`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`) and the agent's pattern; it becomes resumable once the agent reports a turn; `Info` carries `agentSession` and `resumedFrom`.
- `conductor notify` sends `agentSession` and `turn` from the five mapped hooks and retries once without them against an older server; the attention route stores them (a hosted session's id is dropped).
- `Engine.ResumeMember` resumes a member in its worktree and branch without a prompt; a member of a stopped run is refused `409 run_stopped`.
- `POST /api/sessions/{id}/resume` and `POST /api/runs/{run}/members/{name}/resume` (admin) resume with the recipe's arguments, or relaunch plainly with a `notice`; a resume whose stored directory is gone is refused for a recipe that needs it (`400 invalid_cwd`); hosted sessions answer `400 hosted_session`; `resume_test.go` passes.
- `gofmt -w internal && make lint && go test -race -count=1 ./...` is clean and passes.
- Committed: "sessions: each agent's session is named or captured, and an ended session or crew member can be resumed".

---

### Task 6: Interactive tiles that fill their width

**Order:** needs the wire shapes of Tasks 1, 2, 4 and 5, and their server for its headless check. Tasks 6, 7 and 8 run in that order: each edits files the one before left. This task changes how xterm fits and sizes a shared session; its proof is a headless check against a built server, and a measurement a pixel off needs a look at the cause, not a looser assertion.

The wall's and the crew view's tiles become live terminals a person types into, sized to their own pane: a tile fits its pane at an 11 px
font with no scrollback, sends that size in its hello and on its own pane changes, and so sizes its session (latest controller wins,
unchanged). A view-only tile (the join page with a view link) keeps the scaled rendering and asks for no size (`0 × 0`, Task 1). `readOnly`
in `TerminalView` now means only "no input"; how a terminal meets its pane is the `fit` prop's alone (`fill`, `scale`, `tile`).

Measured on a build of this plan's code (Chromium 1117, DOM renderer): the unused width of every wall and crew-view tile at 1440×900 and 1920×1080 is
2–6 px against a 6.6–7 px cell (was 16–37 %), the three seeded 148×57 sessions went 148×57 → 84×25 (tiles) → 108×30 (1920 tiles) → 189×57
(full view) → 108×30 (back on the grid) and were never 80×24, and typing `echo $((6*7))xyz` into a tile printed `42xyz`. One thing the
check found and the code handles: xterm measures its cells again as it renders the first new size after the bundled font settles, so a fit
can come out a few columns too wide (114 columns of 7 px drawn in a 759 px pane); `fitAndResize` checks the screen a frame later and fits
again, at most twice.

**Files:**
- Create: `web/app/utils/tile.ts`, `web/app/utils/tile.test.ts`
- Modify: `web/app/components/TerminalView.vue` — imports `:6-9`; the `readOnly` and `fit` props' docs `:15`, `:22-27`; state after `const notice = ref('')` `:50`; `measure` and `scheduleResize` `:106-118` (replaced); `handleControl`'s `welcome` `:175-182` and `resize` `:186-188`; `connect` `:234-238`; `onMounted`'s first fit `:315`, its observer registration `:325-326`; `onBeforeUnmount` `:330-333`; the host's class `:344`
- Modify: `web/app/components/SessionTile.vue` — the whole file (a tile is a live terminal, not a button)
- Modify: `web/app/components/JoinCrewGrid.vue` — imports and doc `:5-11`, after `open()` `:30-32`, the live tile `:38-70`
- Modify: `web/app/assets/css/main.css` — a `.terminal-tile .xterm` rule before `/* Scrollbars are hidden in every pane` `:134`
- Not committed: `$PW/tiles-check.js` (the headless-check directory, see Headless checks)

The wall (`pages/wall.vue`) and the crew view (`pages/runs/[run].vue`) need no change of their own: both mount `SessionTile` and open the
full view on its `select`, which now comes from the open button, a double-click on the tile's header, or Enter or Space on the tile's frame.
Task 2 adds `submit` to `TerminalView`'s `defineExpose`; this task does not touch that line. Task 8 adds a badge and a Resume button to
`SessionTile`'s header, after this task.

**Interfaces:**
- Consumes: `FOLLOW_SIZE` from `web/app/utils/protocol.ts` (Task 1: `{ cols: 0, rows: 0 } as const`); the server's hello rule (Task 1,
  `proto.HelloSize`: a controller's two dimensions in 1–500 set the PTY's, `0 × 0` and anything else follow it, a view-role hello never
  resizes); `Welcome.role`; `Role` from `protocol.ts`.
- Produces:
```ts
// web/app/utils/tile.ts
export const TILE_FONT_SIZE = 11
export const FIT_DEBOUNCE_MS = 100
export const MAX_DIMENSION = 500
export function helloSize(sizes: boolean, cols: number, rows: number): { cols: number; rows: number }
export interface Box { width: number; height: number }
export function tileScale(pane: Box, screen: Box): number
// web/app/components/TerminalView.vue
fit?: 'fill' | 'scale' | 'tile'   // prop; readOnly?: boolean now means "no input" only
```

- [ ] **Step 1: Write the failing test** `web/app/utils/tile.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { MAX_DIMENSION, helloSize, tileScale } from './tile'

describe('helloSize', () => {
  it('carries the fitted size of a view that sizes the session', () => {
    expect(helloSize(true, 111, 31)).toEqual({ cols: 111, rows: 31 })
  })

  it('is 0×0, follow the session, for a view that does not size it', () => {
    expect(helloSize(false, 111, 31)).toEqual({ cols: 0, rows: 0 })
  })

  it('is 0×0 for a pane not laid out yet, never a default 80×24', () => {
    expect(helloSize(true, 0, 24)).toEqual({ cols: 0, rows: 0 })
    expect(helloSize(true, 80, 0)).toEqual({ cols: 0, rows: 0 })
    expect(helloSize(true, Number.NaN, 24)).toEqual({ cols: 0, rows: 0 })
  })

  it('never asks for more than the protocol takes', () => {
    expect(helloSize(true, 1536, 700)).toEqual({ cols: MAX_DIMENSION, rows: MAX_DIMENSION })
  })
})

describe('tileScale', () => {
  it('is 1 while the screen fits the pane', () => {
    expect(tileScale({ width: 570, height: 346 }, { width: 568, height: 344 })).toBe(1)
  })

  it('shrinks a screen another viewer made larger, by the tighter side', () => {
    expect(tileScale({ width: 570, height: 346 }, { width: 1140, height: 346 })).toBe(0.5)
    expect(tileScale({ width: 570, height: 346 }, { width: 570, height: 692 })).toBe(0.5)
  })

  it('is 1 for a box not laid out', () => {
    expect(tileScale({ width: 0, height: 346 }, { width: 1140, height: 346 })).toBe(1)
    expect(tileScale({ width: 570, height: 346 }, { width: 0, height: 0 })).toBe(1)
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- app/utils/tile.test.ts`
Expected: FAIL, `Failed to resolve import "./tile"`.

- [ ] **Step 3: Write `web/app/utils/tile.ts`**:

```ts
import { FOLLOW_SIZE } from './protocol'

/** A tile's terminal font size, in pixels: fixed, so a tile fits more columns than a scaled view of the PTY's grid. */
export const TILE_FONT_SIZE = 11

/** How long a terminal waits after its own pane last changed size before it fits and resizes the session (the full view uses the same). */
export const FIT_DEBOUNCE_MS = 100

/** The largest terminal dimension the protocol takes (proto.MaxTerminalDimension). */
export const MAX_DIMENSION = 500

/**
 * The size a terminal's hello carries: the size it fitted to its pane when it sizes the session (`sizes`: a full view or a tile with
 * control), else `{0, 0}`, which the server reads as "take the session's size": a scaled view, a view-only link, or a pane not laid out yet
 * (no column or no row fits), so that no viewer resets a session by accident. Never more than 500 a side.
 */
export function helloSize(sizes: boolean, cols: number, rows: number): { cols: number; rows: number } {
  if (!sizes || !(cols >= 1) || !(rows >= 1)) return { ...FOLLOW_SIZE }
  return { cols: Math.min(Math.floor(cols), MAX_DIMENSION), rows: Math.min(Math.floor(rows), MAX_DIMENSION) }
}

export interface Box {
  width: number
  height: number
}

/**
 * The factor a tile shrinks its terminal by to show the whole screen when another viewer has sized the session larger than the tile (latest
 * controller wins): 1 when the screen, with its padding, fits the pane; never more than 1, and 1 when either box is not laid out.
 */
export function tileScale(pane: Box, screen: Box): number {
  if (!(pane.width > 0) || !(pane.height > 0) || !(screen.width > 0) || !(screen.height > 0)) return 1
  return Math.min(1, pane.width / screen.width, pane.height / screen.height)
}
```

- [ ] **Step 4: Run the test**

Run: `npm --prefix web test -- app/utils/tile.test.ts`
Expected: PASS, 7 tests.

- [ ] **Step 5: `TerminalView.vue`: separate input from sizing, add the tile mode.** `sizes()` decides whether this view sizes the
session (a `fill` or `tile` view that takes input, unless the welcome said `view`); `measure()` fits only then and returns `0 × 0`
otherwise or before the pane is laid out (never xterm's default 80×24); `scheduleResize()` runs only for this view's own pane changes, the
font, and the tab becoming visible again, never for another viewer's `resize` broadcast, which the terminal follows and, in tile mode,
scales down to show whole (`applyTileScale`). Apply these edits in order:

Edit 1 (line 6 before this step). Replace:

```vue
import { findFileLocations } from '~/utils/links'
import { ALT_PASSTHROUGH_CODES } from '~/composables/useShortcuts'
import { closeReason, encodeText, type ActivityEntry, type ControlMessage, type FileResponse, type TransportKind, type ViewerInfo, type Welcome } from '~/utils/protocol'
import type { CloseInfo, TerminalTransport, TransportState } from '~/utils/transport/types'

const props = withDefaults(
```

with:

```vue
import { findFileLocations } from '~/utils/links'
import { ALT_PASSTHROUGH_CODES } from '~/composables/useShortcuts'
import { closeReason, encodeText, FOLLOW_SIZE, type ActivityEntry, type ControlMessage, type FileResponse, type Role, type TransportKind, type ViewerInfo, type Welcome } from '~/utils/protocol'
import type { CloseInfo, TerminalTransport, TransportState } from '~/utils/transport/types'
import { FIT_DEBOUNCE_MS, helloSize, tileScale } from '~/utils/tile'

const props = withDefaults(
```

Edit 2 (line 13 before this step). Replace:

```vue
    /** Creates a fresh transport for each (re)connection. */
    createTransport: () => TerminalTransport
    readOnly?: boolean
    /** Connect on mount. Vue casts absent booleans to false, hence the explicit default. */
```

with:

```vue
    /** Creates a fresh transport for each (re)connection. */
    createTransport: () => TerminalTransport
    /** No input: keys and pastes reach nothing. How the terminal is sized is the `fit` prop's alone. */
    readOnly?: boolean
    /** Connect on mount. Vue casts absent booleans to false, hence the explicit default. */
```

Edit 3 (line 21 before this step). Replace:

```vue
    compact?: boolean
    /**
     * `fill` (default) fits the terminal to the pane and, for controllers,
     * resizes the session. `scale` keeps the owner's cols × rows and scales
     * the whole terminal down to fit: previews, thumbnails, wall tiles.
     */
    fit?: 'fill' | 'scale'
    /** Focus the terminal once connected (controllers only). */
    autoFocus?: boolean
```

with:

```vue
    compact?: boolean
    /**
     * How the terminal meets its pane. `fill` (default, the full view) fits
     * the pane and, with control, sizes the session to it. `tile` does the
     * same for a wall or crew tile, at the font size given and with no
     * scrollback (so no scrollbar gutter is reserved), and scales the screen
     * down to show it whole while another viewer has made the session larger
     * than the tile. `scale` keeps the session's cols × rows and scales the
     * whole terminal into the pane: a view-only tile; its hello asks for no
     * size (0 × 0, follow the session).
     */
    fit?: 'fill' | 'scale' | 'tile'
    /** Focus the terminal once connected (controllers only). */
    autoFocus?: boolean
```

Edit 4 (line 49 before this step). Replace:

```vue
const fileView = ref(false)
const notice = ref('')
/** True once the process is gone: the cursor is hidden and stops blinking. */
const ended = ref(false)
```

with:

```vue
const fileView = ref(false)
const notice = ref('')
/** The role the server gave this connection: a view-only one never sizes the session, whatever the page asked for. */
const role = ref<Role | ''>('')
/** The size the last hello carried: 0 × 0 when this view did not size the session as it connected. */
let helloSent: { cols: number; rows: number } = { ...FOLLOW_SIZE }
/** True once the process is gone: the cursor is hidden and stops blinking. */
const ended = ref(false)
```

Edit 5 (line 104 before this step). Replace:

```vue
}

function measure(): { cols: number; rows: number } {
  if (!props.readOnly) fit?.fit()
  return { cols: term?.cols ?? 80, rows: term?.rows ?? 24 }
}

function scheduleResize() {
  if (props.readOnly) return
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => {
    const { cols, rows } = measure()
    transport?.resize(cols, rows)
  }, 100)
}
```

with:

```vue
}

/**
 * Whether this view sizes the session: a full view or a tile that takes input, unless the server said this connection may only watch. A
 * scaled or read-only view follows the session's size instead.
 */
function sizes(): boolean {
  return props.fit !== 'scale' && !props.readOnly && role.value !== 'view'
}

/** Fits the terminal to its pane when this view sizes the session, and returns the size to ask for: 0 × 0 (follow) otherwise, or before the pane is laid out. */
function measure(): { cols: number; rows: number } {
  const h = host.value
  if (!sizes() || !term || !fit || !h?.clientWidth || !h.clientHeight) return helloSize(false, 0, 0)
  fit.fit()
  return helloSize(true, term.cols, term.rows)
}

/**
 * Fits and resizes the session once this view's own pane has stopped changing size (FIT_DEBOUNCE_MS), as it becomes visible again, or once
 * the font has loaded. Never in answer to another viewer's resize: two views of one session take turns only when one of them attaches or
 * its pane changes (latest controller wins), never back and forth.
 */
function scheduleResize() {
  if (!sizes()) return
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => fitAndResize(2), FIT_DEBOUNCE_MS)
}

/**
 * Fits, resizes the session, and checks a frame later that the screen fits its pane: xterm may measure its cells again as it renders the
 * new size (the bundled font settling in), and then the fit is done again, `retries` times at most.
 */
function fitAndResize(retries: number) {
  const { cols, rows } = measure()
  if (cols) transport?.resize(cols, rows)
  window.requestAnimationFrame(() => {
    if (retries > 0 && cols && overflows()) fitAndResize(retries - 1)
    else scheduleTileScale()
  })
}

/** Whether the terminal's screen, with its padding, is wider or taller than its pane. */
function overflows(): boolean {
  const el = term?.element
  const h = host.value
  const screen = el?.querySelector<HTMLElement>('.xterm-screen')
  if (!el || !h || !screen) return false
  const style = getComputedStyle(el)
  const padX = Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight)
  const padY = Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom)
  return screen.offsetWidth + padX > h.clientWidth || screen.offsetHeight + padY > h.clientHeight
}

function onVisibility() {
  if (document.visibilityState === 'visible') scheduleResize()
}

// Tile mode's safety net: while another viewer has made the session larger
// than the tile, the screen is scaled down to show it whole; at the tile's own
// size it is not scaled at all.
let tileFrame: number | undefined
function applyTileScale() {
  const el = term?.element
  const h = host.value
  if (props.fit !== 'tile' || !el || !h) return
  const screen = el.querySelector<HTMLElement>('.xterm-screen')
  if (!screen) return
  const style = getComputedStyle(el)
  const padX = Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight)
  const padY = Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom)
  const s = tileScale({ width: h.clientWidth, height: h.clientHeight }, { width: screen.offsetWidth + padX, height: screen.offsetHeight + padY })
  el.style.transform = s < 1 ? `scale(${s})` : ''
}

function scheduleTileScale() {
  if (props.fit !== 'tile' || tileFrame !== undefined) return
  tileFrame = window.requestAnimationFrame(() => {
    tileFrame = undefined
    applyTileScale()
  })
}
```

Edit 6 (line 174 before this step). Replace:

```vue
  switch (msg.t) {
    case 'welcome':
      fileView.value = msg.fileView
      existsCache.clear()
      // Sessions that already ended are marked once the scrollback has replayed.
      endedAtWelcome = isEnded(msg.status)
      if (props.readOnly && msg.cols && msg.rows) term?.resize(msg.cols, msg.rows)
      emit('welcome', msg)
      break
```

with:

```vue
  switch (msg.t) {
    case 'welcome':
      role.value = msg.role
      fileView.value = msg.fileView
      existsCache.clear()
      // Sessions that already ended are marked once the scrollback has replayed.
      endedAtWelcome = isEnded(msg.status)
      // A view that does not size the session shows it at its size, and so
      // does one whose pane was not laid out as it connected, until it fits.
      if (msg.cols && msg.rows && (!sizes() || !helloSent.cols)) term?.resize(msg.cols, msg.rows)
      scheduleTileScale()
      emit('welcome', msg)
      break
```

Edit 7 (line 185 before this step). Replace:

```vue
      break
    case 'resize':
      if (msg.cols && msg.rows && (term?.cols !== msg.cols || term?.rows !== msg.rows)) term?.resize(msg.cols, msg.rows)
      break
    case 'status':
```

with:

```vue
      break
    case 'resize':
      // Another viewer's resize is followed, never answered with one of this view's own.
      if (msg.cols && msg.rows && (term?.cols !== msg.cols || term?.rows !== msg.rows)) term?.resize(msg.cols, msg.rows)
      scheduleTileScale()
      break
    case 'status':
```

Edit 8 (line 233 before this step). Replace:

```vue
  term.reset()
  try {
    await t.connect(measure())
    t.ping()
    if (!props.readOnly) t.resize(term.cols, term.rows)
    if (props.autoFocus && !props.readOnly) term.focus()
  } catch (e) {
```

with:

```vue
  term.reset()
  try {
    role.value = ''
    helloSent = measure()
    await t.connect(helloSent)
    t.ping()
    if (sizes()) {
      const { cols, rows } = measure()
      if (cols) t.resize(cols, rows)
    }
    if (props.autoFocus && !props.readOnly) term.focus()
  } catch (e) {
```

Edit 9 (line 313 before this step). Replace:

```vue
    transport?.sendInput(bytes)
  })
  if (!props.readOnly) fit.fit()
  observer = new ResizeObserver((entries) => {
    if (props.fit === 'scale') {
```

with:

```vue
    transport?.sendInput(bytes)
  })
  if (sizes() && host.value?.clientWidth && host.value.clientHeight) fit.fit()
  observer = new ResizeObserver((entries) => {
    if (props.fit === 'scale') {
```

Edit 10 (line 324 before this step). Replace:

```vue
  observer.observe(host.value!)
  if (props.fit === 'scale' && term.element) observer.observe(term.element)
  pingTimer = window.setInterval(() => transport?.ping(), 10000)
  if (props.autoConnect) connect()
```

with:

```vue
  observer.observe(host.value!)
  if (props.fit === 'scale' && term.element) observer.observe(term.element)
  // A tab brought back sizes its session again: the view the person looks at wins.
  document.addEventListener('visibilitychange', onVisibility)
  pingTimer = window.setInterval(() => transport?.ping(), 10000)
  if (props.autoConnect) connect()
```

Edit 11 (line 330 before this step). Replace:

```vue
onBeforeUnmount(() => {
  observer?.disconnect()
  stopTheme?.()
  if (scaleFrame !== undefined) window.cancelAnimationFrame(scaleFrame)
  window.clearTimeout(resizeTimer)
  window.clearInterval(pingTimer)
```

with:

```vue
onBeforeUnmount(() => {
  observer?.disconnect()
  document.removeEventListener('visibilitychange', onVisibility)
  stopTheme?.()
  if (scaleFrame !== undefined) window.cancelAnimationFrame(scaleFrame)
  if (tileFrame !== undefined) window.cancelAnimationFrame(tileFrame)
  window.clearTimeout(resizeTimer)
  window.clearInterval(pingTimer)
```

Edit 12 (line 342 before this step). Replace:

```vue
<template>
  <div class="relative h-full w-full overflow-hidden" :class="compact ? '' : 'rounded-lg border border-default'">
    <div ref="host" class="terminal-host" :class="{ 'terminal-compact': compact, 'terminal-scale': props.fit === 'scale' }" :data-ended="ended ? 'true' : undefined" :aria-label="readOnly ? 'terminal (read-only)' : 'terminal'" role="region" />

    <div v-if="notice && !compact" class="absolute top-2 right-2 z-10">
```

with:

```vue
<template>
  <div class="relative h-full w-full overflow-hidden" :class="compact ? '' : 'rounded-lg border border-default'">
    <div ref="host" class="terminal-host" :class="{ 'terminal-compact': compact, 'terminal-scale': props.fit === 'scale', 'terminal-tile': props.fit === 'tile' }" :data-ended="ended ? 'true' : undefined" :aria-label="readOnly ? 'terminal (read-only)' : 'terminal'" role="region" />

    <div v-if="notice && !compact" class="absolute top-2 right-2 z-10">
```


- [ ] **Step 6: `SessionTile.vue`: a live tile.** Replace the whole file with:

```vue
<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import type { TerminalTransport } from '~/utils/transport/types'
import { isEnded } from '~/utils/attention'
import { agentInitials } from '~/utils/sessions'
import { TILE_FONT_SIZE } from '~/utils/tile'

/**
 * A session as a live tile: its terminal fits the tile and takes keys in place (a click on it focuses it); the full view is one action
 * away: the open button, a double-click on the header, or Enter or Space on the tile's frame when the frame itself has the focus.
 */
const props = defineProps<{
  session: SessionInfo
  createTransport: () => TerminalTransport
}>()

const emit = defineEmits<{ select: [] }>()

const events = useEvents()
// An ended session may still carry the prompt it was waiting on: it waits no more (as in the sidebar).
const needsInput = computed(() => props.session.attention?.state === 'needs_input' && !isEnded(props.session.status))
const status = computed(() => {
  // The amber dot follows the Events page's Badge route for needs_input; the label stays.
  if (needsInput.value) return { label: 'Needs input', cls: 'text-warning', dot: events.routes.value.needs_input.badge ? 'bg-warning' : '' }
  if (props.session.status === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: props.session.status.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
})
const host = computed(() => (props.session.kind === 'hosted' ? `hosted · ${props.session.hostName || 'dev machine'}` : 'server'))
</script>

<template>
  <!-- The page's own control (the crew view's selection box) sits beside the tile, not in it: one control per element. -->
  <div class="relative h-full min-h-0 min-w-0">
    <div v-if="$slots.leading" class="absolute left-[11px] top-2 z-10 flex">
      <slot name="leading" />
    </div>
    <div
      class="group flex h-full min-h-0 flex-col overflow-hidden rounded-lg border bg-elevated/40 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-primary"
      :class="needsInput ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented focus-within:border-accented'"
      role="group"
      tabindex="0"
      data-session-tile
      :data-session-id="props.session.id"
      :aria-label="`${props.session.name}: a live terminal. Enter opens it in full.`"
      @keydown.enter.self.prevent="emit('select')"
      @keydown.space.self.prevent="emit('select')"
    >
      <div class="flex cursor-pointer items-center gap-2 border-b border-default px-2.5 py-1 text-xs shrink-0 select-none" :class="$slots.leading && 'pl-8'" data-tile-header @dblclick="emit('select')">
        <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(props.session.agentId) }}</span>
        <span class="font-semibold truncate flex-1 text-[13px]">{{ props.session.name }}</span>
        <EventMarkBadge :session-id="props.session.id" />
        <span class="flex items-center gap-1.5 text-[11.5px]" :class="status.cls"><span v-if="status.dot" class="size-[7px] rounded-full" :class="status.dot" aria-hidden="true" />{{ status.label }}</span>
        <UTooltip text="Open in full (or double-click here)">
          <UButton icon="i-lucide-maximize-2" size="xs" color="neutral" variant="ghost" :aria-label="`Open ${props.session.name}`" data-tile-open @click.stop="emit('select')" />
        </UTooltip>
      </div>
      <div class="flex-1 min-h-0" data-tile-terminal>
        <TerminalView :create-transport="props.createTransport" fit="tile" compact :auto-focus="false" :font-size="TILE_FONT_SIZE" :scrollback="0" />
      </div>
      <div class="flex items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted shrink-0">
        <slot name="footer">
          <span class="truncate">{{ host }}</span>
          <span class="ml-auto flex-none">{{ props.session.viewers ? `${props.session.viewers} here` : '—' }}</span>
        </slot>
      </div>
    </div>
  </div>
</template>
```

The frame is a focusable group, not a button: a click on the terminal focuses xterm (its keys go to the session, and the page's plain-key
shortcuts pause because xterm's textarea has the focus, as in the full view; the Alt chords still pass through `ALT_PASSTHROUGH_CODES`);
Enter and Space open the full view only when the frame itself has the focus (`.self`), never while the terminal has it.

- [ ] **Step 7: `JoinCrewGrid.vue`: a control link's tiles type, a view link's stay scaled.**

Edit 1 (line 5 before this step). Replace:

```vue
import { joinTileStatus } from '~/utils/crews'
import { agentInitials } from '~/utils/sessions'

/**
 * The member tiles of a run link: a live, read-only terminal for each member that runs, a placeholder for the rest. Opening one is the
 * page's to do. What the tiles heard is the page's too (v-model:heard), so it outlasts the grid while a member is open in full.
 */
const props = defineProps<{
```

with:

```vue
import { joinTileStatus } from '~/utils/crews'
import { agentInitials } from '~/utils/sessions'
import { TILE_FONT_SIZE } from '~/utils/tile'

/**
 * The member tiles of a run link: a live terminal for each member that runs, a placeholder for the rest. With a control link a tile
 * fits its pane and takes keys in place, as on the wall; with a view link it shows the member's screen scaled, at the member's size, and
 * asks for none. Opening one in full is the page's to do. What the tiles heard is the page's too (v-model:heard), so it outlasts the grid
 * while a member is open in full.
 */
const props = defineProps<{
```

Edit 2 (line 31 before this step). Replace:

```vue
  if (m.sessionId) emit('open', m, latest[m.name])
}
</script>
```

with:

```vue
  if (m.sessionId) emit('open', m, latest[m.name])
}
/** A control link types into the tiles; a view link only watches them. */
const interactive = computed(() => props.role === 'control')
</script>
```

Edit 3 (line 38 before this step). Replace:

```vue
      <div
        v-if="m.sessionId"
        class="flex min-h-0 cursor-pointer flex-col overflow-hidden rounded-lg border bg-elevated/40 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary"
        :class="heard[m.name]?.attention === 'needs_input' ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'"
        role="button"
        tabindex="0"
        :aria-label="`Open ${m.name}`"
        :data-member="m.name"
        @click="open(m)"
        @keydown.enter.prevent="open(m)"
        @keydown.space.prevent="open(m)"
      >
        <div class="flex flex-none items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
          <span class="flex items-center gap-1.5 text-[11.5px]" :class="joinTileStatus(m, heard[m.name]).cls"><span class="size-[7px] rounded-full" :class="joinTileStatus(m, heard[m.name]).dot" aria-hidden="true" />{{ joinTileStatus(m, heard[m.name]).label }}</span>
        </div>
        <div class="pointer-events-none min-h-0 flex-1">
          <TerminalView
            :create-transport="props.transportFor(m)"
            read-only
            fit="scale"
            compact
            :auto-focus="false"
            @status="(s) => hear(m.name, { status: s })"
            @attention="(a) => hear(m.name, { attention: a.state })"
```

with:

```vue
      <div
        v-if="m.sessionId"
        class="flex min-h-0 flex-col overflow-hidden rounded-lg border bg-elevated/40 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary"
        :class="[heard[m.name]?.attention === 'needs_input' ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented', !interactive && 'cursor-pointer']"
        :role="interactive ? 'group' : 'button'"
        tabindex="0"
        :aria-label="interactive ? `${m.name}: a live terminal. Enter opens it in full.` : `Open ${m.name}`"
        :data-member="m.name"
        @click="!interactive && open(m)"
        @keydown.enter.self.prevent="open(m)"
        @keydown.space.self.prevent="open(m)"
      >
        <div class="flex flex-none cursor-pointer select-none items-center gap-2 border-b border-default px-2.5 py-1 text-xs" @dblclick="open(m)">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
          <span class="flex items-center gap-1.5 text-[11.5px]" :class="joinTileStatus(m, heard[m.name]).cls"><span class="size-[7px] rounded-full" :class="joinTileStatus(m, heard[m.name]).dot" aria-hidden="true" />{{ joinTileStatus(m, heard[m.name]).label }}</span>
          <UButton icon="i-lucide-maximize-2" size="xs" color="neutral" variant="ghost" :aria-label="`Open ${m.name}`" data-tile-open @click.stop="open(m)" />
        </div>
        <div class="min-h-0 flex-1" :class="!interactive && 'pointer-events-none'">
          <TerminalView
            :create-transport="props.transportFor(m)"
            :read-only="!interactive"
            :fit="interactive ? 'tile' : 'scale'"
            compact
            :auto-focus="false"
            :font-size="interactive ? TILE_FONT_SIZE : 13"
            :scrollback="interactive ? 0 : 5000"
            @status="(s) => hear(m.name, { status: s })"
            @attention="(a) => hear(m.name, { attention: a.state })"
```

Edit 4 (line 66 before this step). Replace:

```vue
        <div class="flex flex-none items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted">
          <span class="truncate">{{ props.agentName(m.agentId) }}</span>
          <span class="ml-auto flex-none">{{ role === 'control' ? 'open to type' : 'open' }}</span>
        </div>
      </div>
```

with:

```vue
        <div class="flex flex-none items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted">
          <span class="truncate">{{ props.agentName(m.agentId) }}</span>
          <span class="ml-auto flex-none">{{ interactive ? 'type here · double-click to open' : 'open' }}</span>
        </div>
      </div>
```


- [ ] **Step 8: `main.css`: the tile's scale origin.**

Edit 1 (line 132 before this step). Replace:

```css
}

/* Scrollbars are hidden in every pane; the mouse wheel still scrolls. */
.terminal-host .xterm-viewport {
```

with:
```css
}

/* Tile mode: the terminal fits the tile; while another viewer has made the
   session larger, TerminalView scales it down from the top left corner. */
.terminal-tile .xterm {
  transform-origin: 0 0;
}

/* Scrollbars are hidden in every pane; the mouse wheel still scrolls. */
.terminal-host .xterm-viewport {
```


- [ ] **Step 9: Type-check and test**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: no type error; every vitest passes (the 7 of `tile.test.ts` among them).

- [ ] **Step 10: The headless check.** Build the binary with this UI and start the test server as in Headless checks (its own home, data
directory and public URL; Task 1's server already applies the hello rule). Write `$PW/tiles-check.js`:
```js
// Round 4 Task 6: tiles fill their width, size their sessions (never 80x24), re-assert after the full view, take keys; view links stay scaled. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const token = 'dev-admin-token-change-me'
async function api(method, path, body) {
  const r = await fetch(base + path, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body && JSON.stringify(body) })
  const text = await r.text()
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`)
  return text ? JSON.parse(text) : {}
}
const sizes = async () => Object.fromEntries((await api('GET', '/api/sessions')).sessions.map((s) => [s.id, `${s.cols}x${s.rows}`]))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
/** Waits until the sizes of `ids` have not changed for 600 ms and none is `not`, and returns them. */
async function settled(ids, not) {
  let last = ''
  for (let i = 0, still = 0; i < 100; i++) {
    const s = await sizes()
    const now = ids.map((id) => s[id]).join(',')
    still = now === last && !ids.some((id) => s[id] === not) ? still + 1 : 0
    if (still >= 3) return s
    last = now
    await sleep(200)
  }
  throw new Error(`sizes never settled: ${last}`)
}
/** Measures the tiles of `ids` under `scope`: the pane, the drawn screen and its padding, by session. */
const measure = (page, scope, ids) =>
  page.$$eval(`${scope} [data-session-tile]`, (tiles, ids) =>
    tiles.filter((t) => ids.includes(t.dataset.sessionId)).map((t) => {
      const host = t.querySelector('.terminal-host')
      const xterm = t.querySelector('.xterm')
      const screen = t.querySelector('.xterm-screen')
      const cs = getComputedStyle(xterm)
      return {
        id: t.dataset.sessionId,
        hostW: host.clientWidth,
        hostH: host.clientHeight,
        screenW: screen.offsetWidth,
        screenH: screen.offsetHeight,
        padX: parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight),
        padY: parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom),
        scaled: !!xterm.style.transform,
      }
    }), ids)
async function assertFills(page, scope, ids, label) {
  const s = await settled(ids, '148x57')
  const tiles = await measure(page, scope, ids)
  assert.equal(tiles.length, ids.length, `${label}: ${ids.length} tiles`)
  for (const t of tiles) {
    const [cols, rows] = s[t.id].split('x').map(Number)
    const cellW = t.screenW / cols
    const rowH = t.screenH / rows
    const unusedW = t.hostW - t.padX - t.screenW
    const unusedH = t.hostH - t.padY - t.screenH
    console.log(label, t.id.slice(0, 6), `${cols}x${rows}`, 'unused', unusedW.toFixed(1), 'px of a', cellW.toFixed(1), 'px cell;', unusedH.toFixed(1), 'px of a', rowH.toFixed(1), 'px row')
    assert.ok(!(cols === 80 && rows === 24), `${label}: ${t.id} is not reset to 80x24`)
    assert.ok(!t.scaled, `${label}: ${t.id} is drawn at its own size, not scaled`)
    assert.ok(unusedW >= 0 && unusedW < cellW, `${label}: ${t.id} leaves under one cell of width (${unusedW})`)
    assert.ok(unusedH >= 0 && unusedH < rowH, `${label}: ${t.id} leaves under one row of height (${unusedH})`)
  }
  return s
}
;(async () => {
  // Three sessions seeded at 148x57; a sampler watches that none is ever reset to 80x24.
  const seeded = []
  for (const name of ['tile-a', 'tile-b', 'tile-c']) seeded.push((await api('POST', '/api/sessions', { agentId: 'shell', name, cols: 148, rows: 57 })).id)
  const seen = new Set()
  let sampling = true
  const sampler = (async () => {
    while (sampling) {
      const s = await sizes()
      for (const id of seeded) seen.add(s[id])
      await sleep(100)
    }
  })()
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript((t) => localStorage.setItem('conductor.adminToken', t), token)
    const page = await ctx.newPage()
    await page.goto(base + '/wall', { waitUntil: 'commit' })
    // Other sessions may be on the wall too: these three are measured.
    const shown = (ids) => page.waitForFunction((ids) => ids.every((id) => document.querySelector(`[data-session-id="${id}"] .xterm-screen`)), ids)
    await shown(seeded)
    const wall1440 = await assertFills(page, '', seeded, 'wall 1440x900')

    // Typing into a tile reaches its session: the shell answers.
    await page.locator(`[data-session-id="${seeded[0]}"] [data-tile-terminal]`).click()
    await page.keyboard.type('echo $((6*7))xyz')
    await page.keyboard.press('Enter')
    await page.waitForFunction((id) => document.querySelector(`[data-session-id="${id}"] .xterm-rows`)?.textContent?.includes('42xyz'), seeded[0])
    console.log('typed into a tile: the shell answered')

    await page.setViewportSize({ width: 1920, height: 1080 })
    const wall1920 = await assertFills(page, '', seeded, 'wall 1920x1080')
    assert.notEqual(wall1920[seeded[0]], wall1440[seeded[0]], 'a wider window gives the tile more columns')

    // The full view sizes the session; back on the grid the tile sizes it again.
    const first = seeded[0]
    await page.locator(`[data-session-id="${first}"] [data-tile-open]`).click()
    await page.waitForURL(/focus=/)
    const full = (await settled([first], wall1920[first]))[first]
    console.log('full view', full, 'tile', wall1920[first])
    await page.getByRole('button', { name: 'Back to the grid' }).click()
    await shown(seeded)
    const back = (await settled([first], full))[first]
    assert.equal(back, wall1920[first], 'the tile sized its session again on return')

    // The crew view's tiles do the same.
    const crew = await api('POST', '/api/crews', {
      name: 'Tiles', goal: 'fill', cwd: '', where: 'server', isolation: 'none', openAfterLaunch: true,
      members: ['a', 'b', 'c'].map((n) => ({ name: n, agentId: 'shell', prompt: '', start: { when: 'immediately' } })),
    })
    const { run } = await api('POST', `/api/crews/${crew.crew.id}/launch`)
    const members = run.members.map((m) => m.sessionId)
    await page.goto(`${base}/runs/${run.id}`, { waitUntil: 'commit' })
    await page.waitForFunction(() => document.querySelectorAll('[data-run-grid] [data-session-tile] .xterm-screen').length === 3)
    await assertFills(page, '[data-run-grid]', members, 'crew 1920x1080')
    await page.setViewportSize({ width: 1440, height: 900 })
    await assertFills(page, '[data-run-grid]', members, 'crew 1440x900')

    // A view link's tiles stay scaled and size nothing.
    const before = await sizes()
    const link = await api('POST', `/api/runs/${run.id}/links`, { role: 'view' })
    const guest = await (await browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage()
    await guest.goto(link.url.replace(/^https?:\/\/[^/]+/, base), { waitUntil: 'commit' })
    await guest.getByPlaceholder('Priya Shah').fill('Guest')
    await guest.getByRole('button', { name: 'Join crew' }).click()
    await guest.waitForFunction(() => document.querySelectorAll('[data-join-tiles] .terminal-scale .xterm-screen').length === 3)
    await sleep(1500)
    const after = await sizes()
    for (const id of members) assert.equal(after[id], before[id], `the guest's tile did not resize ${id}`)
    console.log('view link: scaled, sizes unchanged')
  } finally {
    sampling = false
    await sampler
    await browser.close()
  }
  console.log('seeded sizes seen', [...seen].join(' '))
  assert.ok(!seen.has('80x24'), 'no seeded session was ever reset to 80x24')
  console.log('PASS tiles-check')
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
```

Run:
```bash
make web-build && make build-go
PW=/tmp/conductor-pw   # as in Headless checks: playwright-core and the check scripts
T=$(mktemp -d $PW/server.XXXXXX)
HOME=$T CONDUCTOR_DATA_DIR=$T/.conductor CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8099 \
  bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 > $T/server.log 2>&1 &
echo $! > $T/server.pid
sleep 2
(cd $PW && node tiles-check.js)
kill $(cat $T/server.pid); rm -rf "$T"
```

Expected: `PASS tiles-check`, after lines like `wall 1920x1080 … 108x30 unused 3.0 px of a 7.0 px cell` and
`seeded sizes seen 148x57 … 189x57` with no `80x24`. Kill the server by its pid, never `pkill -f`.

- [ ] **Step 11: Commit**
```bash
git add web/app/utils/tile.ts web/app/utils/tile.test.ts web/app/components/TerminalView.vue web/app/components/SessionTile.vue \
  web/app/components/JoinCrewGrid.vue web/app/assets/css/main.css
git commit -m "web: tiles are live terminals that fit their pane and size their session; view-only tiles stay scaled and ask for no size"
```

**Done when:**

- `web/app/utils/tile.ts` and its 7 tests in `tile.test.ts` pass.
- `TerminalView` keeps input (`readOnly`) apart from sizing (`fit`: `fill`, `scale`, `tile`): a tile fits its pane at an 11 px font with no scrollback, sends that size in its hello and on its own pane changes (never xterm's default 80×24), and follows another viewer's `resize` without sending one, scaled down to show the whole screen.
- `SessionTile` is a live terminal in a focusable frame: a click focuses the terminal; the open button, a double-click on the header, or Enter or Space on the frame itself open the full view.
- `JoinCrewGrid`: a control link's tiles type; a view link's stay scaled and ask for no size (`0 × 0`).
- `main.css` has the tile's `.terminal-tile .xterm` rule.
- `npm --prefix web run typecheck && npm --prefix web test` pass, and `tiles-check.js` prints `PASS tiles-check` with no `80x24` among the seeded sizes seen; the test server was stopped by its pid and its directory removed.
- Committed: "web: tiles are live terminals that fit their pane and size their session; view-only tiles stay scaled and ask for no size".

---

### Task 7: Runs in the live store, the Crews page's runs, the sidebar grouping, broadcast to everyone

**Order:** after Task 6; its headless check needs Task 4's run events on the server. It moves every page's run reads into the one live store, with ordering rules (tickets, a coalescing window) that must hold against the server's run events, and ends with a headless check.

The runs join the sessions in the one live store (`useAttention`): read on every snapshot of the event stream, when a `run` event names one
(Task 4: `{id}` to read again, `{id, removed: true}` once forgotten), and when a member session of one changes status; never on a timer. Run
events are gathered for 250 ms (`RUN_EVENT_WINDOW_MS`) and each run named is read once, or all of them with one `GET /api/runs` when more
than 8 are named; every read takes a ticket when it is asked, and a reply never replaces what a later read or a removal already applied.
The crew view's 10 s poll and its own session watcher go; so does the layout's run-name cache (`RunNameAsks`): run names come from the
store. The Crews page lists each crew's runs under it (`CrewRuns`); the full sidebar groups sessions by run in each section, from one helper
(`sidebarGroups`) that the rail's `railGroups` is now built on; the crew view's broadcast selection defaults to every member with a live
session, and only the person's own ticks are kept, so no read of the run and no run event clears one.

Where the spec and the code at HEAD disagree, the spec wins: `memberStatus` called a starting member never `needs_input` ("its prompt is
still to be typed"); a starting member whose session waits now counts as needing input, because the session waits on its trust question
(Task 4 holds the prompt and marks the member `needsInput`). Go's `runState` (Task 4) and `runState` here share their rule and their table
(`TestRunState`'s six rows).

**Files:**
- Create: `web/app/utils/runs.ts`, `web/app/utils/runs.test.ts`, `web/app/components/CrewRuns.vue`, `web/app/components/SidebarRunHeader.vue`
- Modify: `web/app/composables/useSessions.ts` — `BroadcastSkipReason` `:176-177`; `RunMember`, after `error?: string` `:255-256`; `RunInfo`, its end `:276-278`. (Task 8 touches other hunks of this file afterwards.)
- Modify: `web/app/utils/crews.ts` — `memberStatus` `:154-166`; `runActive` `:180-183` (removed); `SKIP_REASON` `:233-237`; `RUN_NAME_RETRY_MS` and `RunNameAsks` `:310-336` (removed); `broadcastSelection` appended
- Modify: `web/app/utils/crews.test.ts` — imports `:5-28`; the `run()` fixture `:99-109`; `memberStatus` `:128-131`; `runCounts` `:166`; `runActive` `:176-182` and `RunNameAsks` `:435-463` (removed); `broadcastSelection` appended
- Modify: `web/app/utils/sidebar.ts` — `RailDot` to the end `:102-139`: `sidebarGroups`, and `railGroups` built on it
- Modify: `web/app/utils/sidebar.test.ts` — imports `:13-15`; `sidebarGroups` appended (the `railGroups` tests stay and pass unchanged)
- Modify: `web/app/composables/useAttention.ts` — imports `:1-4`; module state `:9-10`; after `sessions` `:88-91`; `upsert` `:113-115`; `remove` `:121-122`; `handleBlock` `:228-234`; `poll` `:240-244`; the token watcher `:261-266`; the return `:280`
- Modify: `web/app/layouts/default.vue` — imports `:2-5`; the run-name block `:93-133`
- Modify: `web/app/components/SidebarRail.vue` — `:19-22`
- Modify: `web/app/components/SessionSidebar.vue` — the whole file
- Modify: `web/app/pages/runs/[run].vue` — imports and header comment `:2-11`; state `:22-31`; `load`, `changed` and the session watcher `:35-68`; the selection `:91-105`; the timers `:177-191`; the checkbox `:233`; the broadcast bar `:264`
- Modify: `web/app/components/BroadcastBar.vue` — doc and props `:4-11`; `canSend` `:22`; the scope text `:45`; the button `:56`
- Modify: `web/app/pages/crews/[[id]].vue` — imports `:2-6`; `runs` `:42`; `refresh` `:67`, `:77`; `refreshRuns` `:112-120`; `runsOf` and `status` `:148-157`; `launch` `:348-349`; the session watcher `:380-392`; `onBeforeUnmount` `:401-405`; the list entry `:438-464`
- Not committed: `$PW/runs-check.js` (the headless-check directory, see Headless checks)

**Interfaces:**
- Consumes: Task 4's run JSON (`state`, `needsInput`, `yolo` on a run; `needsInput` on a member) and its `run` event on `GET /api/events`;
  Task 4's broadcast skip reason `no_enter`; `api.getRun`, `api.listRuns`, `api.stopRun` (`useSessions`).
- Produces:
```ts
// web/app/utils/runs.ts
export const RUN_EVENT_WINDOW_MS = 250
export const RUN_EVENT_LIST_AT = 8
export type RunState = RunInfo['state']
export function runState(run: RunInfo, sessions: readonly SessionInfo[]): { state: RunState; needs: number }
export function runLive(state: RunState): boolean
export function runBadge(state: RunState, needs: number): { label: string; color: 'success' | 'warning' | 'neutral' }
export function memberDot(status: MemberStatus): { label: string; dot: string }
export interface RunReader { get(id: string): Promise<RunInfo | null>; list(): Promise<RunInfo[]> }
export class RunStore {
  readonly runs: Map<string, RunInfo>
  constructor(reader: RunReader, changed: () => void, windowMs?: number)
  list(): RunInfo[]
  apply(run: RunInfo, ticket?: number): boolean
  remove(id: string, ticket?: number): void
  applyList(list: readonly RunInfo[], ticket: number): void
  read(id: string): Promise<RunInfo | null>
  readAll(): Promise<void>
  schedule(id: string): void
  flush(): Promise<void>
  clear(): void
}
// web/app/utils/crews.ts
export interface BroadcastMember { name: string; live: boolean; waiting: boolean }
export function broadcastSelection(members: readonly BroadcastMember[], choices: Readonly<Record<string, boolean>>): { selected: string[]; sending: string[]; waiting: string[] }
// removed: runActive, RUN_NAME_RETRY_MS, RunNameAsks
// web/app/utils/sidebar.ts
export const SIDEBAR_SECTIONS: readonly ['needs', 'running', 'exited']
export interface SidebarGroup { key: string; runId?: string; label?: string; sessions: SessionInfo[] }
export function sidebarGroups(sessions: readonly SessionInfo[], runNames?: Readonly<Record<string, string>>): Record<SessionGroupKey, SidebarGroup[]>
// web/app/composables/useAttention.ts: useAttention() also returns
runs: ComputedRef<RunInfo[]>                 // newest first
runNames: ComputedRef<Record<string, string>>
runOf(id: string): RunInfo | undefined
refreshRun(id: string): Promise<RunInfo | null>
applyRun(run: RunInfo): void
// web/app/components/BroadcastBar.vue props
{ runId: string; members: string[]; willType: number; waiting: number; disabled?: boolean }
```

The page hooks the e2e suite (Task 9) selects are rendered exactly so: on the Crews page each run row is
`[data-crew-runs] [data-run="<run id>"]` with `data-state` (`running`, `needs_input`, `stopped`, `finished`), its members
`[data-run-member="<name>"]` with `data-status` (`pending`, `starting`, `running`, `needs_input`, `ended`), and a link named "Open" to
`/runs/<id>`; in the full sidebar a run's group is `[data-sidebar-group="<run id>"]` whose header is a link to `/runs/<id>` named after the
run (`[data-sidebar-run-group]`), and the run-only variant keeps `[data-sidebar-run]`; on the crew view the broadcast checkbox is
`[data-member="<name>"] [data-broadcast-pick]`, the button `[data-broadcast-send]` ("Send to N") and its scope `[data-broadcast-scope]`.

- [ ] **Step 1: The run types.** In `web/app/composables/useSessions.ts`:

Edit 1 (line 174 before this step). Replace:

```ts
}

/** Why a broadcast skipped a member: waiting on a prompt, not running (no session yet, its prompt not typed yet, or ended), or not a member. */
export type BroadcastSkipReason = 'needs_input' | 'not_running' | 'unknown'

/** Reply of POST /api/runs/{run}/broadcast, both lists in the order asked. */
```

with:

```ts
}

/**
 * Why a broadcast skipped a member: waiting on a prompt, not running (no session yet, its prompt not typed yet, or ended), not a member, or
 * typed without its Enter (a prompt came up during the pause before it; the line waits in the member's input).
 */
export type BroadcastSkipReason = 'needs_input' | 'not_running' | 'unknown' | 'no_enter'

/** Reply of POST /api/runs/{run}/broadcast, both lists in the order asked. */
```

Edit 2 (line 254 before this step). Replace:

```ts
  /** Why it ended before it ran: it could not start, or its process ended before its prompt was typed. */
  error?: string
  /**
   * GET /api/runs/{run} only: lines of tracked files its worktree adds and removes against the commit it began from,
```

with:

```ts
  /** Why it ended before it ran: it could not start, or its process ended before its prompt was typed. */
  error?: string
  /** Starting or running, and its session waits on a prompt (a trust question before its prompt, or its agent's question), as last read. */
  needsInput?: boolean
  /**
   * GET /api/runs/{run} only: lines of tracked files its worktree adds and removes against the commit it began from,
```

Edit 3 (line 276 before this step). Replace:

```ts
  /** The run's own log, oldest first, at most 200 entries: launched, member started, prompt typed, run links created and revoked, stopped. */
  log: ActivityEntry[]
}
```

with:

```ts
  /** The run's own log, oldest first, at most 200 entries: launched, member started, prompt typed, run links created and revoked, stopped. */
  log: ActivityEntry[]
  /**
   * As last read: running while a member is pending, starting or running and none waits; needs_input while a starting or running member's
   * session waits on a prompt (`needsInput` of them); stopped after a stop; finished once every member has ended. The live view is
   * `runState` (utils/runs.ts), which brings it up to date with the sessions.
   */
  state: 'running' | 'needs_input' | 'stopped' | 'finished'
  needsInput: number
  /** The run's yolo choice, fixed at launch: every member, one added later included, follows it. */
  yolo: boolean
}
```


- [ ] **Step 2: Write the failing test** `web/app/utils/runs.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import { RUN_EVENT_LIST_AT, RUN_EVENT_WINDOW_MS, RunStore, memberDot, runBadge, runLive, runState, type RunReader } from './runs'

const member = (p: Partial<RunMember>): RunMember => ({ name: 'lead', agentId: 'claude', start: { when: 'immediately' }, status: 'running', ...p })

const run = (p: Partial<RunInfo> = {}): RunInfo => ({
  id: 'api-sweep-1a2b3c4d',
  crewId: 'api-sweep',
  name: 'API sweep',
  goal: 'ship',
  cwd: '/srv/api',
  isolation: 'worktree',
  startedAt: '2026-10-01T10:00:00Z',
  members: [],
  log: [],
  state: 'running',
  needsInput: 0,
  yolo: false,
  ...p,
})

const session = (p: Partial<SessionInfo>): SessionInfo =>
  ({ id: 's1', name: 'lead', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: '2026-10-01T10:00:00Z', ...p }) as SessionInfo

describe('runState', () => {
  // The rows of TestRunState in internal/crew/prompt_test.go, from the run as read (no live session).
  const m = (status: RunMember['status'], needsInput = false) => member({ name: `${status}${Math.random()}`, status, needsInput })
  it.each([
    ['all pending', run({ members: [m('pending')] }), 'running', 0],
    ['one running', run({ members: [m('running'), m('pending')] }), 'running', 0],
    ['two waiting', run({ members: [m('running', true), m('starting', true), m('ended')] }), 'needs_input', 2],
    ['ended and pending', run({ members: [m('ended'), m('pending')] }), 'running', 0],
    ['all ended', run({ members: [m('ended'), m('ended')] }), 'finished', 0],
    ['stopped', run({ stoppedAt: '2026-10-01T11:00:00Z', members: [m('ended')] }), 'stopped', 0],
  ] as const)('%s', (_, r, state, needs) => {
    expect(runState(r, [])).toEqual({ state, needs })
  })

  it('follows the live sessions: a prompt raised, a session ended', () => {
    const r = run({ members: [member({ sessionId: 's1' })] })
    const crew = { runId: r.id, crewId: r.crewId, member: 'lead' }
    expect(runState(r, [session({ crew, attention: { state: 'needs_input' } })])).toEqual({ state: 'needs_input', needs: 1 })
    expect(runState(r, [session({ crew, attention: { state: 'done' } })])).toEqual({ state: 'running', needs: 0 })
    expect(runState(r, [session({ crew, status: 'exited' })])).toEqual({ state: 'finished', needs: 0 })
  })
})

describe('runLive and runBadge', () => {
  it('says which runs still go, and how', () => {
    expect([runLive('running'), runLive('needs_input'), runLive('stopped'), runLive('finished')]).toEqual([true, true, false, false])
    expect(runBadge('needs_input', 2)).toEqual({ label: 'Needs input 2', color: 'warning' })
    expect(runBadge('running', 0).label).toBe('Running')
    expect(runBadge('finished', 0).label).toBe('Finished')
    expect(memberDot('needs_input').label).toBe('needs input')
  })
})

function reader(over: Partial<RunReader> = {}): RunReader & { gets: string[]; lists: number } {
  const r = {
    gets: [] as string[],
    lists: 0,
    get: async (id: string) => {
      r.gets.push(id)
      return run({ id })
    },
    list: async () => {
      r.lists++
      return [] as RunInfo[]
    },
    ...over,
  }
  return r
}

describe('RunStore', () => {
  afterEach(() => vi.useRealTimers())

  it('never lets an older reply replace a newer one', async () => {
    let release!: (r: RunInfo) => void
    const slow = new Promise<RunInfo>((res) => (release = res))
    let calls = 0
    const store = new RunStore(reader({ get: async () => (++calls === 1 ? slow : run({ id: 'r1', state: 'stopped' })) }), () => {})
    const first = store.read('r1')
    await store.read('r1') // asked later, answered first
    release(run({ id: 'r1', state: 'running' }))
    await first
    expect(store.runs.get('r1')?.state).toBe('stopped')
  })

  it('drops a run the server forgot, and an older read does not bring it back', async () => {
    let release!: (r: RunInfo) => void
    const store = new RunStore(reader({ get: () => new Promise<RunInfo>((res) => (release = res)) }), () => {})
    store.apply(run({ id: 'r1' }))
    const late = store.read('r1')
    store.remove('r1')
    release(run({ id: 'r1' }))
    await late
    expect(store.runs.has('r1')).toBe(false)
  })

  it('gathers events for a window and reads each run named once', async () => {
    vi.useFakeTimers()
    const r = reader()
    const store = new RunStore(r, () => {})
    store.schedule('a')
    store.schedule('a')
    store.schedule('b')
    expect(r.gets).toEqual([])
    await vi.advanceTimersByTimeAsync(RUN_EVENT_WINDOW_MS)
    expect(r.gets.sort()).toEqual(['a', 'b'])
    expect(store.runs.size).toBe(2)
  })

  it('reads every run with one list when more than RUN_EVENT_LIST_AT are named in a window', async () => {
    vi.useFakeTimers()
    const r = reader({ list: async () => [run({ id: 'x' })] })
    const store = new RunStore(r, () => {})
    for (let i = 0; i <= RUN_EVENT_LIST_AT; i++) store.schedule(`r${i}`)
    await vi.advanceTimersByTimeAsync(RUN_EVENT_WINDOW_MS)
    expect(r.gets).toEqual([])
    expect([...store.runs.keys()]).toEqual(['x'])
  })

  it('keeps a run a list does not have when a later read of it was applied, and forgets one it does not have otherwise', () => {
    const store = new RunStore(reader(), () => {})
    store.apply(run({ id: 'old' }), 1)
    store.apply(run({ id: 'new' }), 5)
    store.applyList([], 3)
    expect([...store.runs.keys()]).toEqual(['new'])
  })

  it('lists the newest first, and forgets all on clear, replies in flight included', async () => {
    let release!: (r: RunInfo) => void
    const store = new RunStore(reader({ get: () => new Promise<RunInfo>((res) => (release = res)) }), () => {})
    store.apply(run({ id: 'a', startedAt: '2026-10-01T09:00:00Z' }))
    store.apply(run({ id: 'b', startedAt: '2026-10-01T10:00:00Z' }))
    expect(store.list().map((r) => r.id)).toEqual(['b', 'a'])
    const late = store.read('c')
    store.clear()
    release(run({ id: 'c' }))
    await late
    expect(store.runs.size).toBe(0)
  })
})
```

Run: `npm --prefix web test -- app/utils/runs.test.ts`
Expected: FAIL, `Failed to resolve import "./runs"`.

- [ ] **Step 3: `crews.ts`: the member rule, the broadcast selection, and what goes.** In `web/app/utils/crews.ts`:

Edit 1 (line 153 before this step). Replace:

```ts

/**
 * A member's state from the run as last read, brought up to date by its live session: a running member whose session waits on a prompt
 * `needs_input`, a member whose session ended `ended`, and a pending member whose session exists `starting`. A starting member is never
 * `needs_input`: its prompt is still to be typed.
 */
export function memberStatus(run: RunInfo, member: RunMember, sessions: readonly SessionInfo[]): MemberStatus {
```

with:

```ts

/**
 * A member's state from the run as last read, brought up to date by its live session: a member whose session ended `ended`, a starting or
 * running member whose session waits on a prompt `needs_input` (a starting one waits on its trust question: its prompt is held until a
 * person answers), and a pending member whose session exists `starting`. Without a live session the run's own `needsInput` decides.
 */
export function memberStatus(run: RunInfo, member: RunMember, sessions: readonly SessionInfo[]): MemberStatus {
```

Edit 2 (line 161 before this step). Replace:

```ts
  const s = memberSession(run, member, sessions)
  if (s && isEnded(s.status)) return 'ended'
  if (member.status === 'running') return s?.attention?.state === 'needs_input' ? 'needs_input' : 'running'
  if (member.status === 'pending' && s) return 'starting'
  return member.status
}
```

with:

```ts
  const s = memberSession(run, member, sessions)
  if (s && isEnded(s.status)) return 'ended'
  const waiting = s ? s.attention?.state === 'needs_input' : !!member.needsInput
  if (member.status === 'pending') return s ? (waiting ? 'needs_input' : 'starting') : 'pending'
  return waiting ? 'needs_input' : member.status
}
```

Edit 3 (line 176 before this step). Replace:

```ts
  }
  return { needs, running }
}

/** Whether a run still goes: not stopped, and a member has not ended. */
export function runActive(run: RunInfo): boolean {
  return !run.stoppedAt && run.members.some((m) => m.status !== 'ended')
}
```

with:

```ts
  }
  return { needs, running }
}
```

Edit 4 (line 235 before this step). Replace:

```ts
  not_running: 'not running',
  unknown: 'not a member',
}
```

with:

```ts
  not_running: 'not running',
  unknown: 'not a member',
  no_enter: 'typed, no Enter: a question came up',
}
```

Edit 5 (line 308 before this step). Replace:

```ts
}

/** How long a run name that could not be read waits before it is asked for again. */
export const RUN_NAME_RETRY_MS = 5_000

/**
 * Which run names the layout reads: each once, and again after a failure once RUN_NAME_RETRY_MS has passed; never again once the server
 * says it does not have the run (gone), as it does for a run forgotten past the kept-runs limit whose member sessions are still listed.
 */
export class RunNameAsks {
  /** By run: when it may be asked again; Infinity while asked or read. */
  private next = new Map<string, number>()
  /** The runs the server does not have: never asked again. */
  private never = new Set<string>()
  shouldAsk(id: string, now = Date.now()): boolean {
    if (this.never.has(id)) return false
    const at = this.next.get(id)
    if (at !== undefined && now < at) return false
    this.next.set(id, Infinity)
    return true
  }
  failed(id: string, now = Date.now()): void {
    if (!this.never.has(id)) this.next.set(id, now + RUN_NAME_RETRY_MS)
  }
  gone(id: string): void {
    this.never.add(id)
    this.next.delete(id)
  }
}

/** A run link's member tile: its label and classes, from what its terminal last reported (`heard`), else the run's own status. */
export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string } {
```

with:

```ts
}

/** A run link's member tile: its label and classes, from what its terminal last reported (`heard`), else the run's own status. */
export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string } {
```

Edit 6 (line 343 before this step). Replace:

```ts
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}
```

with:

```ts
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}

/** A member as the broadcast bar weighs it: whether its session runs, and whether that session waits on a prompt. */
export interface BroadcastMember {
  name: string
  live: boolean
  waiting: boolean
}

/**
 * The crew view's broadcast selection. Every member whose session runs is selected unless the person unticked it (`choices`, by name: the
 * person's own ticks and unticks, which outlast every read of the run and every run event); a member that starts later is selected as it
 * appears. `selected` is what the bar sends, in the run's order; `sending` the selected members the server will type into; `waiting` the
 * selected members it will skip because their sessions wait on a prompt.
 */
export function broadcastSelection(members: readonly BroadcastMember[], choices: Readonly<Record<string, boolean>>): { selected: string[]; sending: string[]; waiting: string[] } {
  const selected = members.filter((m) => choices[m.name] ?? m.live)
  return {
    selected: selected.map((m) => m.name),
    sending: selected.filter((m) => m.live && !m.waiting).map((m) => m.name),
    waiting: selected.filter((m) => m.live && m.waiting).map((m) => m.name),
  }
}
```


- [ ] **Step 4: `crews.test.ts` follows.** The run fixture gains the new fields, the starting-member rule changes as above (the runCounts row
`logs`, starting and waiting, now counts as waiting), `runActive` and `RunNameAsks` go with their tests, and `broadcastSelection` gets its
own:

Edit 1 (line 5 before this step). Replace:

```ts
  argsFrom,
  broadcastByName,
  broadcastSummary,
  crewFeed,
```

with:

```ts
  argsFrom,
  broadcastByName,
  broadcastSelection,
  broadcastSummary,
  crewFeed,
```

Edit 2 (line 15 before this step). Replace:

```ts
  memberStatus,
  pageAfterDelete,
  RUN_NAME_RETRY_MS,
  RunNameAsks,
  runActive,
  runCounts,
  sidebarRunFor,
```

with:

```ts
  memberStatus,
  pageAfterDelete,
  runCounts,
  sidebarRunFor,
```

Edit 3 (line 108 before this step). Replace:

```ts
  members,
  log: [],
})
```

with:

```ts
  members,
  log: [],
  state: 'running',
  needsInput: 0,
  yolo: false,
})
```

Edit 4 (line 126 before this step). Replace:

```ts
  })

  it('does not call a starting member needs_input: its prompt is still to be typed', () => {
    const waiting = session({ crew, attention: { state: 'needs_input' } })
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [waiting])).toBe('starting')
  })
```

with:

```ts
  })

  it('calls a starting member needs_input while its session waits: its trust question holds its prompt', () => {
    const waiting = session({ crew, attention: { state: 'needs_input', source: 'trust' } })
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [waiting])).toBe('needs_input')
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [session({ crew })])).toBe('starting')
  })

  it("takes the run's own needsInput when no live session says otherwise", () => {
    expect(memberStatus(r, member({ status: 'starting', needsInput: true }), [])).toBe('needs_input')
    expect(memberStatus(r, member({ status: 'running', sessionId: 's1', needsInput: true }), [session({ crew })])).toBe('running')
  })
```

Edit 5 (line 166 before this step). Replace:

```ts
      session({ id: 'e', crew: tag('logs'), attention: { state: 'needs_input' } }),
    ]
    expect(runCounts(r, live)).toEqual({ needs: 1, running: 2 })
  })

  it('is zero for a run with nothing running', () => {
    expect(runCounts(run([member({ status: 'ended' })]), [])).toEqual({ needs: 0, running: 0 })
  })
})

describe('runActive', () => {
  it('is true while the run is not stopped and a member has not ended', () => {
    expect(runActive(run([member({ status: 'ended' }), member({ name: 'b', status: 'pending' })]))).toBe(true)
    expect(runActive(run([member({ status: 'ended' })]))).toBe(false)
    expect(runActive({ ...run([member({ status: 'pending' })]), stoppedAt: '2026-09-29T11:00:00Z' })).toBe(false)
  })
})
```

with:

```ts
      session({ id: 'e', crew: tag('logs'), attention: { state: 'needs_input' } }),
    ]
    expect(runCounts(r, live)).toEqual({ needs: 2, running: 2 })
  })

  it('is zero for a run with nothing running', () => {
    expect(runCounts(run([member({ status: 'ended' })]), [])).toEqual({ needs: 0, running: 0 })
  })
})
```

Edit 6 (line 433 before this step). Replace:

```ts
})

describe('RunNameAsks', () => {
  it('asks once, and again after a failure once the retry time has passed', () => {
    const asks = new RunNameAsks()
    expect(asks.shouldAsk('r1', 0)).toBe(true)
    expect(asks.shouldAsk('r1', 1)).toBe(false)
    asks.failed('r1', 10)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS - 1)).toBe(false)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS)).toBe(true)
    expect(asks.shouldAsk('r2', 0)).toBe(true)
  })

  it('never asks again for a run the server does not have, whatever fails later', () => {
    vi.useFakeTimers()
    try {
      const asks = new RunNameAsks()
      expect(asks.shouldAsk('r1')).toBe(true)
      asks.failed('r1')
      vi.advanceTimersByTime(RUN_NAME_RETRY_MS)
      expect(asks.shouldAsk('r1')).toBe(true)
      asks.gone('r1')
      asks.failed('r1')
      vi.advanceTimersByTime(RUN_NAME_RETRY_MS * 100)
      expect(asks.shouldAsk('r1')).toBe(false)
      expect(asks.shouldAsk('r2')).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('joinTileStatus', () => {
  it("prefers what the tile's terminal reported, and flags a prompt", () => {
```

with:

```ts
})

describe('joinTileStatus', () => {
  it("prefers what the tile's terminal reported, and flags a prompt", () => {
```

Edit 7 (line 470 before this step). Replace:

```ts
    expect(joinTileStatus({ status: 'pending' })).toMatchObject({ label: 'pending', cls: 'text-muted' })
  })
})
```

with:

```ts
    expect(joinTileStatus({ status: 'pending' })).toMatchObject({ label: 'pending', cls: 'text-muted' })
  })
})

describe('broadcastSelection', () => {
  const m = (name: string, live = true, waiting = false) => ({ name, live, waiting })

  it('selects every member whose session runs, and none that has none', () => {
    expect(broadcastSelection([m('lead'), m('core'), m('tests', false)], {})).toEqual({ selected: ['lead', 'core'], sending: ['lead', 'core'], waiting: [] })
  })

  it('keeps an untick through every read, and selects a member that starts later', () => {
    const choices = { core: false }
    expect(broadcastSelection([m('lead'), m('core'), m('tests', false)], choices).selected).toEqual(['lead'])
    expect(broadcastSelection([m('lead'), m('core'), m('tests')], choices).selected).toEqual(['lead', 'tests'])
  })

  it('keeps a tick on a member with no session yet, and sends only to those the server will type into', () => {
    const r = broadcastSelection([m('lead', true, true), m('core'), m('tests', false)], { tests: true })
    expect(r).toEqual({ selected: ['lead', 'core', 'tests'], sending: ['core'], waiting: ['lead'] })
  })
})
```


- [ ] **Step 5: Write `web/app/utils/runs.ts`**:

```ts
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { memberStatus, type MemberStatus } from './crews'

/** How long the store gathers the runs that run events and member sessions name before it reads them. */
export const RUN_EVENT_WINDOW_MS = 250
/** More runs than this named in one window are read with one GET /api/runs rather than one read each. */
export const RUN_EVENT_LIST_AT = 8

export type RunState = RunInfo['state']

/**
 * A run's state and how many of its members wait on a prompt, from the run as last read brought up to date by the live sessions
 * (memberStatus): stopped once stopped; finished once every member has ended; needs_input while a starting or running member's session
 * waits on a prompt; running otherwise, pending members included (a member's done keeps it running: a done agent is idle, not gone). The
 * same rule as runState in internal/crew/run.go.
 */
export function runState(run: RunInfo, sessions: readonly SessionInfo[]): { state: RunState; needs: number } {
  let needs = 0
  let ended = 0
  for (const m of run.members) {
    const st = memberStatus(run, m, sessions)
    if (st === 'ended') ended++
    else if (st === 'needs_input') needs++
  }
  if (run.stoppedAt) return { state: 'stopped', needs }
  if (ended === run.members.length) return { state: 'finished', needs: 0 }
  if (needs > 0) return { state: 'needs_input', needs }
  return { state: 'running', needs: 0 }
}

/** Whether a run still goes: running or waiting on a person. */
export function runLive(state: RunState): boolean {
  return state === 'running' || state === 'needs_input'
}

/** A run's state as a badge: its words and its colour. */
export function runBadge(state: RunState, needs: number): { label: string; color: 'success' | 'warning' | 'neutral' } {
  switch (state) {
    case 'needs_input':
      return { label: `Needs input ${needs}`, color: 'warning' }
    case 'stopped':
      return { label: 'Stopped', color: 'neutral' }
    case 'finished':
      return { label: 'Finished', color: 'neutral' }
  }
  return { label: 'Running', color: 'success' }
}

/** A member's status as a dot beside its avatar, and in words. */
export function memberDot(status: MemberStatus): { label: string; dot: string } {
  switch (status) {
    case 'needs_input':
      return { label: 'needs input', dot: 'bg-warning' }
    case 'running':
      return { label: 'running', dot: 'bg-success' }
    case 'starting':
      return { label: 'starting', dot: 'bg-info' }
    case 'ended':
      return { label: 'ended', dot: 'bg-neutral-400' }
  }
  return { label: 'pending', dot: 'bg-transparent ring-1 ring-neutral-400' }
}

/** How the store reads runs: one (null when the server does not have it), or every run. */
export interface RunReader {
  get(id: string): Promise<RunInfo | null>
  list(): Promise<RunInfo[]>
}

/**
 * The runs the browser knows, by id, and the reads that keep them current. Every read takes a ticket as it is asked; its answer replaces a
 * run only when no later read or removal of that run has been applied, so a slow, older reply never replaces a newer one. The runs named by
 * run events, and the runs of member sessions that changed, are gathered for RUN_EVENT_WINDOW_MS and read then: one read each, or a single
 * list when more than RUN_EVENT_LIST_AT are named. `changed` is called after every change, for the page state to follow.
 */
export class RunStore {
  readonly runs = new Map<string, RunInfo>()
  private ticket = 0
  /** By run: the ticket of the read or removal last applied. */
  private applied = new Map<string, number>()
  /** Replies to reads asked before this ticket are dropped: they were asked before a clear. */
  private floor = 0
  private pending = new Set<string>()
  private timer: ReturnType<typeof setTimeout> | undefined

  constructor(
    private readonly reader: RunReader,
    private readonly changed: () => void,
    private readonly windowMs = RUN_EVENT_WINDOW_MS,
  ) {}

  /** The runs, newest first. */
  list(): RunInfo[] {
    return [...this.runs.values()].sort((a, b) => b.startedAt.localeCompare(a.startedAt))
  }

  /** Takes a run as a read with `ticket` answered it (by default the newest: the reply of an action such as a stop). False when a later read or removal of it was applied first. */
  apply(run: RunInfo, ticket = ++this.ticket): boolean {
    if (!this.take(run.id, ticket)) return false
    this.runs.set(run.id, run)
    this.changed()
    return true
  }

  /** Drops a run the server forgot, unless a later read of it was applied first. */
  remove(id: string, ticket = ++this.ticket): void {
    if (!this.take(id, ticket)) return
    if (this.runs.delete(id)) this.changed()
  }

  /** Takes every run as a list read with `ticket` gave them: a run missing from it was forgotten, unless a later read of it was applied first. */
  applyList(list: readonly RunInfo[], ticket: number): void {
    const seen = new Set<string>()
    for (const r of list) {
      seen.add(r.id)
      if (this.take(r.id, ticket)) this.runs.set(r.id, r)
    }
    for (const id of [...this.runs.keys()]) if (!seen.has(id) && this.take(id, ticket)) this.runs.delete(id)
    this.changed()
  }

  /** Reads one run now; resolves to it, or null when the server does not have it. A failure is thrown and changes nothing. */
  async read(id: string): Promise<RunInfo | null> {
    const ticket = ++this.ticket
    const run = await this.reader.get(id)
    if (run) this.apply(run, ticket)
    else this.remove(id, ticket)
    return run && (this.runs.get(id) ?? run)
  }

  /** Reads every run now. A failure is thrown and changes nothing. */
  async readAll(): Promise<void> {
    const ticket = ++this.ticket
    this.applyList(await this.reader.list(), ticket)
  }

  /** Gathers a run to read at the end of the window. */
  schedule(id: string): void {
    this.pending.add(id)
    if (this.timer === undefined) this.timer = setTimeout(() => void this.flush(), this.windowMs)
  }

  /** Reads what the window gathered; a read that fails waits for the next event or snapshot. */
  async flush(): Promise<void> {
    this.timer = undefined
    const ids = [...this.pending]
    this.pending.clear()
    if (!ids.length) return
    if (ids.length > RUN_EVENT_LIST_AT) {
      await this.readAll().catch(() => {})
      return
    }
    await Promise.all(ids.map((id) => this.read(id).catch(() => null)))
  }

  /** Forgets everything: another token may be another server. Replies still on their way are dropped by their tickets. */
  clear(): void {
    clearTimeout(this.timer)
    this.timer = undefined
    this.pending.clear()
    this.floor = ++this.ticket
    this.applied.clear()
    this.runs.clear()
    this.changed()
  }

  /** Marks `id` as answered by `ticket`, unless a later ticket was applied to it. */
  private take(id: string, ticket: number): boolean {
    if (ticket < this.floor || (this.applied.get(id) ?? 0) > ticket) return false
    this.applied.set(id, ticket)
    return true
  }
}
```

- [ ] **Step 6: Run the tests**

Run: `npm --prefix web test -- app/utils/runs.test.ts app/utils/crews.test.ts`
Expected: PASS (14 and 41 tests).

- [ ] **Step 7: `sidebar.ts`: one grouping for the full sidebar and the rail.** In `web/app/utils/sidebar.ts` replace everything from
`export type RailDot` to the end of the file (`railGroups`) as follows; the `railGroups` tests pass unchanged:

Edit 1 (line 100 before this step). Replace:

```ts
}

export type RailDot = 'needs' | 'running' | 'idle' | 'exited'
export interface RailItem {
```

with:

```ts
}

/** The sidebar's three sections, in its order. */
export const SIDEBAR_SECTIONS = ['needs', 'running', 'exited'] as const satisfies readonly SessionGroupKey[]

/** One group of a sidebar section: the sessions of no run (no `runId`), or the members of one run under its name. */
export interface SidebarGroup {
  /** Unique in the sidebar: a run's members can sit in several sections (one needs you, one runs), each its own group. */
  key: string
  runId?: string
  /** The run's name from `runNames`, else its crew's id. */
  label?: string
  sessions: SessionInfo[]
}

/**
 * The sidebar's sessions by section (needs you, running, exited, in groupSessions' order), and in each section the sessions of no run first,
 * then one group per run, in the order its first member comes, named from `runNames` (the live store's runs) or its crew's id. The full
 * sidebar and the rail both draw from it.
 */
export function sidebarGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): Record<SessionGroupKey, SidebarGroup[]> {
  const g = groupSessions([...sessions])
  const out = { needs: [], running: [], exited: [] } as Record<SessionGroupKey, SidebarGroup[]>
  for (const key of SIDEBAR_SECTIONS) {
    const list = g[key]
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out[key].push({ key: `${key}:`, sessions: loose })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out[key].push({ key: `${key}:${runId}`, runId, label: runNames[runId] || members[0]?.crew?.crewId, sessions: members })
  }
  return out
}

export type RailDot = 'needs' | 'running' | 'idle' | 'exited'
export interface RailItem {
```

Edit 2 (line 116 before this step). Replace:

```ts
}

/**
 * The rail's avatars in the full sidebar's order (needs you, running,
 * exited), the members of one run kept together under a thin label, the
 * run's name from `runNames` or its crew's id. Within a section the
 * sessions of no run come first.
 */
export function railGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): RailGroup[] {
  const g = groupSessions([...sessions])
  const out: RailGroup[] = []
  const item = (s: SessionInfo, dot: RailDot): RailItem => ({ id: s.id, name: s.name, agentId: s.agentId, dot, message: s.attention?.message || undefined })
  const section = (key: SessionGroupKey, list: SessionInfo[], dot: (s: SessionInfo) => RailDot) => {
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out.push({ key: `${key}:`, items: loose.map((s) => item(s, dot(s))) })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out.push({ key: `${key}:${runId}`, runId, label: runNames[runId] || members[0]?.crew?.crewId, items: members.map((s) => item(s, dot(s))) })
  }
  section('needs', g.needs, () => 'needs')
  section('running', g.running, (s) => (s.status === 'running' ? 'running' : 'idle'))
  section('exited', g.exited, () => 'exited')
  return out
}
```

with:

```ts
}

/** The rail's avatars: sidebarGroups' groups in order, each session as an avatar with its dot. */
export function railGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): RailGroup[] {
  const sections = sidebarGroups(sessions, runNames)
  const dot = (key: SessionGroupKey, s: SessionInfo): RailDot => (key === 'needs' ? 'needs' : key === 'exited' ? 'exited' : s.status === 'running' ? 'running' : 'idle')
  return SIDEBAR_SECTIONS.flatMap((key) =>
    sections[key].map((g) => ({
      key: g.key,
      runId: g.runId,
      label: g.label,
      items: g.sessions.map((s) => ({ id: s.id, name: s.name, agentId: s.agentId, dot: dot(key, s), message: s.attention?.message || undefined })),
    })),
  )
}
```


and in `web/app/utils/sidebar.test.ts`:

Edit 1 (line 13 before this step). Replace:

```ts
  runOpen,
  sessionOpen,
  sidebarSessions,
  writeSidebarMode,
```

with:

```ts
  runOpen,
  sessionOpen,
  sidebarGroups,
  sidebarSessions,
  writeSidebarMode,
```

Edit 2 (line 175 before this step). Replace:

```ts
    expect(railGroups([session({ status: 'starting' })])[0]!.items[0]!.dot).toBe('idle')
  })
})
```

with:

```ts
    expect(railGroups([session({ status: 'starting' })])[0]!.items[0]!.dot).toBe('idle')
  })
})

describe('sidebarGroups', () => {
  it('puts the sessions of no run first in each section, then one group per run under its name', () => {
    const list = [
      session({ id: 'a', name: 'alone', createdAt: '2026-10-01T09:05:00Z' }),
      session({ id: 'b', crew: { runId: 'r1', crewId: 'api-sweep', member: 'core' }, createdAt: '2026-10-01T09:04:00Z' }),
      session({ id: 'c', crew: { runId: 'r1', crewId: 'api-sweep', member: 'lead' }, attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z' } }),
      session({ id: 'd', crew: { runId: 'r2', crewId: 'docs', member: 'w' }, createdAt: '2026-10-01T09:03:00Z' }),
      session({ id: 'e', crew: { runId: 'r1', crewId: 'api-sweep', member: 'tests' }, status: 'exited', endedAt: '2026-10-01T09:07:00Z' }),
    ]
    const g = sidebarGroups(list, { r1: 'API sweep' })
    const shape = (k: 'needs' | 'running' | 'exited') => g[k].map((x) => [x.key, x.label, x.sessions.map((s) => s.id)])
    expect(shape('needs')).toEqual([['needs:r1', 'API sweep', ['c']]])
    expect(shape('running')).toEqual([
      ['running:', undefined, ['a']],
      ['running:r1', 'API sweep', ['b']],
      ['running:r2', 'docs', ['d']],
    ])
    expect(shape('exited')).toEqual([['exited:r1', 'API sweep', ['e']]])
  })

  it('has empty sections for no sessions', () => {
    expect(sidebarGroups([])).toEqual({ needs: [], running: [], exited: [] })
  })
})
```


Run: `npm --prefix web test -- app/utils/sidebar.test.ts`
Expected: PASS (15 tests).

- [ ] **Step 8: The live store keeps the runs.** In `web/app/composables/useAttention.ts`:

Edit 1 (line 1 before this step). Replace:

```ts
import type { SessionInfo } from './useSessions'
import { attentionFavicon, needingInput, newlyNeedingInput, playChime } from '~/utils/attention'
import { eventAlert, type RoutedEvent } from '~/utils/events'
import type { SessionActivity } from '~/utils/protocol'

const SETTINGS_KEY = 'conductor.attention.settings'
```

with:

```ts
import type { RunInfo, SessionInfo } from './useSessions'
import { ApiError } from './useApi'
import { attentionFavicon, needingInput, newlyNeedingInput, playChime } from '~/utils/attention'
import { eventAlert, type RoutedEvent } from '~/utils/events'
import type { SessionActivity } from '~/utils/protocol'
import { RunStore } from '~/utils/runs'

const SETTINGS_KEY = 'conductor.attention.settings'
```

Edit 2 (line 9 before this step). Replace:

```ts
let lastChime = 0
let stopEvents: (() => void) | undefined

export interface AttentionSettings {
```

with:

```ts
let lastChime = 0
let stopEvents: (() => void) | undefined
/** The runs of the live store: one per app, made by the first useAttention. */
let runStore: RunStore | undefined

export interface AttentionSettings {
```

Edit 3 (line 90 before this step). Replace:

```ts
    return Array.from(store.value.sessions.values()).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  })
  const needsInput = computed(() => needingInput(sessions.value))
  const count = computed(() => needsInput.value.length)
```

with:

```ts
    return Array.from(store.value.sessions.values()).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  })

  // The runs, beside the sessions: read on every snapshot (a stream that
  // starts, or starts again), when a run event names one, and when a member
  // session of one changes status; never on a timer. A reply older than one
  // already applied is dropped (RunStore's tickets).
  const runsVersion = useState<number>('attentionRunsVersion', () => 0)
  const runs: RunStore = (runStore ??= new RunStore(
    {
      get: (id) =>
        api.getRun(id).catch((e) => {
          if (e instanceof ApiError && e.status === 404) return null
          throw e
        }),
      list: () => api.listRuns(),
    },
    () => runsVersion.value++,
  ))
  /** Every run the server keeps, newest first. */
  const runList = computed<RunInfo[]>(() => {
    void runsVersion.value
    return runs.list()
  })
  /** The runs' names by id, for the sidebar's run headers. */
  const runNames = computed<Record<string, string>>(() => Object.fromEntries(runList.value.map((r) => [r.id, r.name])))
  /** The run with this id as last read, if the store has it. */
  function runOf(id: string): RunInfo | undefined {
    void runsVersion.value
    return runs.runs.get(id)
  }
  const needsInput = computed(() => needingInput(sessions.value))
  const count = computed(() => needsInput.value.length)
```

Edit 4 (line 113 before this step). Replace:

```ts
  function upsert(s: SessionInfo) {
    const prev = new Map(store.value.sessions)
    store.value.sessions.set(s.id, s)
    react(prev, store.value.sessions)
```

with:

```ts
  function upsert(s: SessionInfo) {
    const prev = new Map(store.value.sessions)
    // A member that starts, ends or goes changes its run as the run reports it.
    if (s.crew && prev.get(s.id)?.status !== s.status) runs.schedule(s.crew.runId)
    store.value.sessions.set(s.id, s)
    react(prev, store.value.sessions)
```

Edit 5 (line 120 before this step). Replace:

```ts

  function remove(id: string) {
    store.value.sessions.delete(id)
    bump()
```

with:

```ts

  function remove(id: string) {
    const gone = store.value.sessions.get(id)
    if (gone?.crew) runs.schedule(gone.crew.runId)
    store.value.sessions.delete(id)
    bump()
```

Edit 6 (line 226 before this step). Replace:

```ts
    try {
      const payload = JSON.parse(data.join('\n'))
      if (event === 'snapshot') replaceAll(payload as SessionInfo[])
      else if (event === 'session') upsert(payload as SessionInfo)
      else if (event === 'removed') remove((payload as { id: string }).id)
      else if (event === 'activity') {
        const { sessionId, ...entry } = payload as SessionActivity
        events.push(sessionId, entry)
```

with:

```ts
    try {
      const payload = JSON.parse(data.join('\n'))
      if (event === 'snapshot') {
        replaceAll(payload as SessionInfo[])
        // What changed while the stream was away: every run, once.
        runs.readAll().catch(() => {})
      } else if (event === 'session') upsert(payload as SessionInfo)
      else if (event === 'removed') remove((payload as { id: string }).id)
      else if (event === 'run') {
        const r = payload as { id: string; removed?: boolean }
        if (r.removed) runs.remove(r.id)
        else runs.schedule(r.id)
      } else if (event === 'activity') {
        const { sessionId, ...entry } = payload as SessionActivity
        events.push(sessionId, entry)
```

Edit 7 (line 242 before this step). Replace:

```ts
    try {
      replaceAll(await api.list())
      store.value.error = ''
    } catch (e) {
```

with:

```ts
    try {
      replaceAll(await api.list())
      // The fallback while the stream is down reads the runs with the sessions.
      await runs.readAll()
      store.value.error = ''
    } catch (e) {
```

Edit 8 (line 263 before this step). Replace:

```ts
        store.value.sessions = new Map()
        bump()
        events.reset()
        stream()
```

with:

```ts
        store.value.sessions = new Map()
        bump()
        runs.clear()
        events.reset()
        stream()
```

Edit 9 (line 278 before this step). Replace:

```ts
  }

  return { sessions, needsInput, count, connected, error, start, stop, refresh: poll }
}
```

with:

```ts
  }

  /** Reads one run now (a page that shows it, as it opens); resolves to it, or null when the server does not have it. */
  function refreshRun(id: string): Promise<RunInfo | null> {
    return runs.read(id)
  }

  /** Takes a run an action answered with (a start, a stop, a member added): it is the newest there is. */
  function applyRun(run: RunInfo) {
    runs.apply(run)
  }

  return { sessions, needsInput, count, connected, error, start, stop, refresh: poll, runs: runList, runNames, runOf, refreshRun, applyRun }
}
```


- [ ] **Step 9: Run names from the store: the layout and the rail.** In `web/app/layouts/default.vue`:

Edit 1 (line 1 before this step). Replace:

```vue
<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'
import { ApiError } from '~/composables/useApi'
import { RUN_NAME_RETRY_MS, RunNameAsks, sidebarRunFor } from '~/utils/crews'
import { SIDEBAR_SIZE } from '~/utils/sidebar'
```

with:

```vue
<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'
import { sidebarRunFor } from '~/utils/crews'
import { SIDEBAR_SIZE } from '~/utils/sidebar'
```

Edit 2 (line 93 before this step). Replace:

```vue
// The sidebar's group variant: on a crew view (/runs/<id>), and on the page
// of any member session of a run however it was reached, the sidebar lists
// only that run's members. The run's name comes from a cache the crew view
// fills; for a member session opened elsewhere it is read once per run, and
// again RUN_NAME_RETRY_MS after a failure, the crew's id standing in until then;
// a run the server does not have (404) is never asked for again.
const api = useSessions()
const sidebarRun = computed(() => sidebarRunFor(route.path, attention.sessions.value))
const runRoute = computed(() => route.path.startsWith('/runs/'))
const runNames = useState<Record<string, string>>('crewRunNames', () => ({}))
const asks = new RunNameAsks()
let retry: ReturnType<typeof setTimeout> | undefined
async function readRunName(id: string) {
  // The crew view reads its run itself.
  if (runRoute.value || runNames.value[id] || !asks.shouldAsk(id)) return
  try {
    const r = await api.getRun(id)
    runNames.value = { ...runNames.value, [r.id]: r.name }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      // Forgotten past the kept-runs limit: the crew id stands in for good.
      asks.gone(id)
      return
    }
    // The crew id stands in until a later try reads it.
    asks.failed(id)
    clearTimeout(retry)
    retry = setTimeout(() => {
      if (sidebarRun.value === id) readRunName(id)
    }, RUN_NAME_RETRY_MS)
  }
}
watch(sidebarRun, (id) => {
  if (id) readRunName(id)
}, { immediate: true })
onBeforeUnmount(() => clearTimeout(retry))
const sidebarRunName = computed(() => {
  const id = sidebarRun.value
  if (!id) return undefined
  return runNames.value[id] || attention.sessions.value.find((s) => s.crew?.runId === id)?.crew?.crewId
})
```

with:

```vue
// The sidebar's group variant: on a crew view (/runs/<id>), and on the page
// of any member session of a run however it was reached, the sidebar lists
// only that run's members. The run's name comes from the live store's runs;
// the crew's id stands in for a run the server no longer keeps.
const sidebarRun = computed(() => sidebarRunFor(route.path, attention.sessions.value))
const runRoute = computed(() => route.path.startsWith('/runs/'))
const sidebarRunName = computed(() => {
  const id = sidebarRun.value
  if (!id) return undefined
  return attention.runNames.value[id] || attention.sessions.value.find((s) => s.crew?.runId === id)?.crew?.crewId
})
```


In `web/app/components/SidebarRail.vue`:

Edit 1 (line 17 before this step). Replace:

```vue
const launch = useLaunchModal()
const route = useRoute()
const runNames = useState<Record<string, string>>('crewRunNames', () => ({}))

const shown = computed(() => sidebarSessions(attention.sessions.value, props.runId))
const names = computed(() => (props.runId && props.runName ? { ...runNames.value, [props.runId]: props.runName } : runNames.value))
const groups = computed(() => railGroups(shown.value, names.value))
const needsDot = computed(() => needsDotShown(events.routes.value))
```

with:

```vue
const launch = useLaunchModal()
const route = useRoute()

const shown = computed(() => sidebarSessions(attention.sessions.value, props.runId))
const names = computed(() => (props.runId && props.runName ? { ...attention.runNames.value, [props.runId]: props.runName } : attention.runNames.value))
const groups = computed(() => railGroups(shown.value, names.value))
const needsDot = computed(() => needsDotShown(events.routes.value))
```


- [ ] **Step 10: The full sidebar groups by run.** Create `web/app/components/SidebarRunHeader.vue`:

```vue
<script setup lang="ts">
import { runOpen } from '~/utils/sidebar'

/** The header of a crew run's members in a sidebar section: the run's name, linking to its crew view. */
const props = defineProps<{ runId: string; label?: string }>()
const route = useRoute()
</script>

<template>
  <NuxtLink
    :to="`/runs/${encodeURIComponent(props.runId)}`"
    class="mt-1 flex items-center gap-1.5 rounded-sm px-2 py-0.5 text-[11.5px] font-semibold"
    :class="runOpen(route.path, props.runId) ? 'text-highlighted' : 'text-muted hover:text-highlighted'"
    :aria-current="runOpen(route.path, props.runId) ? 'page' : undefined"
    data-sidebar-run-group
  >
    <UIcon name="i-lucide-layout-grid" class="size-3.5 flex-none" />
    <span class="truncate">{{ props.label ?? props.runId }}</span>
  </NuxtLink>
</template>
```

and replace the whole of `web/app/components/SessionSidebar.vue` with:

```vue
<script setup lang="ts">
import type { SessionInfo } from '~/composables/useSessions'
import { filterSessions, relativeTime, sessionMeta } from '~/utils/sessions'
import { needsDotShown, runOpen, sessionOpen, sidebarGroups, sidebarSessions } from '~/utils/sidebar'

/**
 * The full sidebar. In each section (needs you, running, exited) the sessions of no run come first, then the members of each crew run under
 * a header naming the run, which links to its crew view (sidebarGroups, as the rail groups them). With `runId`, only the sessions of that
 * run, under a header naming it (`runName`) with a link back to its crew view.
 */
const props = defineProps<{ runId?: string; runName?: string }>()

const attention = useAttention()
const events = useEvents()
const route = useRoute()
const launch = useLaunchModal()

const query = ref('')
const filterInput = useTemplateRef<{ inputRef?: HTMLInputElement }>('filterInput')
const now = ref(Date.now())
let tick: number | undefined

const shown = computed(() => sidebarSessions(attention.sessions.value, props.runId))
const groups = computed(() => sidebarGroups(filterSessions(shown.value, query.value), attention.runNames.value))
const count = (key: 'needs' | 'running' | 'exited') => groups.value[key].reduce((n, g) => n + g.sessions.length, 0)
const needsDot = computed(() => needsDotShown(events.routes.value))
const empty = computed(() => shown.value.length === 0)
/** The run-only variant names its run once, at the top: its groups need no header of their own. */
const runHeaders = computed(() => !props.runId)

function active(id: string) {
  return sessionOpen(route.path, id)
}

function exitLabel(s: SessionInfo) {
  const code = s.status === 'exited' && s.exitCode !== undefined ? `exit ${s.exitCode}` : s.status
  return `${code} · ${relativeTime(s.endedAt ?? s.createdAt, now.value, { suffix: true })}`
}

function focusFilter() {
  filterInput.value?.inputRef?.focus()
}

defineExpose({ focusFilter })

onMounted(() => {
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => window.clearInterval(tick))
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex flex-col gap-2.5 px-1 pb-2">
      <UButton label="Launch agent" icon="i-lucide-plus" block @click="launch.show()">
        <template #trailing><UKbd value="N" size="sm" class="ml-auto opacity-70" /></template>
      </UButton>
      <UInput ref="filterInput" v-model="query" placeholder="Filter sessions, paths, people" size="sm" icon="i-lucide-slash" :ui="{ base: 'font-normal' }" />
    </div>

    <div class="flex-1 min-h-0 overflow-y-auto px-1 flex flex-col gap-3.5" data-session-list>
      <div v-if="runId" class="flex flex-col gap-0.5 rounded-md bg-elevated/60 px-2.5 py-2" data-sidebar-run>
        <span class="text-[11px] font-semibold uppercase tracking-wider text-muted">Crew</span>
        <NuxtLink :to="`/runs/${encodeURIComponent(runId)}`" class="flex items-center gap-1.5 text-sm font-semibold text-highlighted hover:underline" :aria-current="runOpen(route.path, runId) ? 'page' : undefined">
          <UIcon name="i-lucide-layout-grid" class="size-4 flex-none text-muted" />
          <span class="truncate">{{ runName || runId }}</span>
        </NuxtLink>
        <span v-if="!runOpen(route.path, runId)" class="text-xs text-muted">Only this crew's members are listed.</span>
      </div>
      <p v-if="empty && runId" class="px-2 py-4 text-xs text-muted leading-relaxed">No member of this crew has a session yet.</p>
      <p v-else-if="empty" class="px-2 py-4 text-xs text-muted leading-relaxed">No sessions yet. Launch an agent here or run <code>conductor host</code> from your machine.</p>

      <section v-if="count('needs')" class="flex flex-col gap-0.5">
        <h3 class="flex items-center gap-2 px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-warning">
          Needs you <span class="rounded-full bg-warning/20 px-1.5 text-warning tracking-normal">{{ count('needs') }}</span>
        </h3>
        <div v-for="g in groups.needs" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex gap-2.5 rounded-md px-2.5 py-2 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" solid />
            <div class="min-w-0 flex-1 flex flex-col gap-0.5">
              <div class="flex items-center gap-1.5">
                <span class="truncate text-sm font-semibold">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <span v-if="needsDot" class="ml-auto size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
              </div>
              <span class="truncate text-xs">{{ s.attention?.message || 'Waiting for input' }}</span>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
            </div>
          </NuxtLink>
        </div>
      </section>

      <section v-if="count('running')" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Running · {{ count('running') }}</h3>
        <div v-for="g in groups.running" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" />
            <div class="min-w-0 flex-1 flex flex-col">
              <div class="flex items-center gap-1.5 min-w-0">
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
            </div>
            <span class="size-2 rounded-full flex-none" :class="s.status === 'running' ? 'bg-success' : 'bg-neutral-400'" aria-hidden="true" />
          </NuxtLink>
        </div>
      </section>

      <section v-if="count('exited')" class="flex flex-col gap-0.5">
        <h3 class="px-2 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted">Exited · {{ count('exited') }}</h3>
        <div v-for="g in groups.exited" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 opacity-75 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs opacity-100' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" dashed />
            <div class="min-w-0 flex-1 flex flex-col">
              <div class="flex items-center gap-1.5 min-w-0">
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ exitLabel(s) }}</span>
            </div>
          </NuxtLink>
        </div>
      </section>
    </div>
  </div>
</template>
```

- [ ] **Step 11: The crew view reads the store, and broadcasts to everyone.** In `web/app/pages/runs/[run].vue`:

Edit 1 (line 1 before this step). Replace:

```vue
<script setup lang="ts">
import type { RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import { ApiError } from '~/composables/useApi'
import { crewFeed, memberStatus, runCounts, takeViewLink } from '~/utils/crews'
import { bestGrid, lastItemSpan } from '~/utils/wall'

// The crew view: a tile for every member of one run, its activity and a
// broadcast bar. Sessions come from the live store (useAttention); the run
// itself, for member states, branches and diff stats, is read on arrival,
// when a member's session starts or ends, and every 10 s while this page is
// open: the diff stats are nowhere else.

const route = useRoute()
```

with:

```vue
<script setup lang="ts">
import type { RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import { broadcastSelection, crewFeed, memberStatus, runCounts, takeViewLink } from '~/utils/crews'
import { bestGrid, lastItemSpan } from '~/utils/wall'

// The crew view: a tile for every member of one run, its activity and a
// broadcast bar. Sessions and the run come from the live store (useAttention):
// the run, with its member states, branches and diff stats, is read as the page
// opens and again whenever a run event or a member session's change says it
// changed. Nothing polls.

const route = useRoute()
```

Edit 2 (line 21 before this step). Replace:

```vue

const runId = computed(() => String(route.params.run))
const run = ref<RunInfo | null>(null)
const error = ref('')
const gone = ref(false)
const now = ref(Date.now())
/** Run names by id, for the sidebar's header (layouts/default.vue). */
const runNames = useState<Record<string, string>>('crewRunNames', () => ({}))
/** The view link of the launch that opened this page, shown once: it lives in this page alone and goes with it, or when dismissed. */
const launchLink = ref(takeViewLink(String(route.params.run)))
```

with:

```vue

const runId = computed(() => String(route.params.run))
/** The run as the live store last read it. */
const run = computed<RunInfo | null>(() => live.runOf(runId.value) ?? null)
const error = ref('')
const gone = ref(false)
const now = ref(Date.now())
/** The view link of the launch that opened this page, shown once: it lives in this page alone and goes with it, or when dismissed. */
const launchLink = ref(takeViewLink(String(route.params.run)))
```

Edit 3 (line 33 before this step). Replace:

```vue
useHead({ title: computed(() => run.value?.name || 'Crew') })

async function load() {
  if (!admin.hasToken.value) {
```

with:

```vue
useHead({ title: computed(() => run.value?.name || 'Crew') })

/** Reads the run as the page opens: its diff stats are read with it. Later reads are the live store's. */
async function load() {
  if (!admin.hasToken.value) {
```

Edit 4 (line 40 before this step). Replace:

```vue
  const id = runId.value
  try {
    const r = await api.getRun(id)
    if (id !== runId.value) return
    run.value = r
    error.value = ''
    gone.value = false
    if (runNames.value[r.id] !== r.name) runNames.value = { ...runNames.value, [r.id]: r.name }
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) gone.value = true
    else error.value = (e as Error).message
  }
}

function changed(r: RunInfo) {
  run.value = r
  load()
}

/** The run's sessions in the live store. */
const sessions = computed(() => live.sessions.value.filter((s) => s.crew?.runId === runId.value))

// Read the run again when one of its sessions appears, changes status or goes.
const sessionsKey = computed(() => sessions.value.map((s) => `${s.id}:${s.status}`).join(','))
let reloadTimer: number | undefined
watch(sessionsKey, () => {
  window.clearTimeout(reloadTimer)
  reloadTimer = window.setTimeout(load, 300)
})

interface Tile {
```

with:

```vue
  const id = runId.value
  try {
    const r = await live.refreshRun(id)
    if (id !== runId.value) return
    error.value = ''
    gone.value = !r
  } catch (e) {
    if (id === runId.value) error.value = (e as Error).message
  }
}

/** An action (add, start, stop) answered with the run as it is now. */
function changed(r: RunInfo) {
  live.applyRun(r)
}

/** The run's sessions in the live store. */
const sessions = computed(() => live.sessions.value.filter((s) => s.crew?.runId === runId.value))

interface Tile {
```

Edit 5 (line 89 before this step). Replace:

```vue
const counts = computed(() => (run.value ? runCounts(run.value, live.sessions.value) : { needs: 0, running: 0 }))

// Broadcast selection, by member name.
const selected = ref<Set<string>>(new Set())
function toggle(name: string, on: boolean | 'indeterminate') {
  const next = new Set(selected.value)
  if (on === true) next.add(name)
  else next.delete(name)
  selected.value = next
}
const selectedNames = computed(() => tiles.value.map((t) => t.name).filter((n) => selected.value.has(n)))

watch(runId, () => {
  run.value = null
  selected.value = new Set()
  load()
})
```

with:

```vue
const counts = computed(() => (run.value ? runCounts(run.value, live.sessions.value) : { needs: 0, running: 0 }))

// Broadcast selection: every member whose session runs, unless the person
// unticked it; a member that starts later is selected as it appears. Only the
// person's own ticks are kept (choices), so no read of the run and no run
// event clears one; another run starts afresh.
const choices = ref<Record<string, boolean>>({})
const selection = computed(() =>
  broadcastSelection(
    tiles.value.map((t) => ({
      name: t.name,
      live: !!t.session && (t.session.status === 'running' || t.session.status === 'starting'),
      waiting: t.session?.attention?.state === 'needs_input',
    })),
    choices.value,
  ),
)
function isSelected(name: string) {
  return selection.value.selected.includes(name)
}
function toggle(name: string, on: boolean | 'indeterminate') {
  choices.value = { ...choices.value, [name]: on === true }
}

watch(runId, () => {
  choices.value = {}
  gone.value = false
  load()
})
```

Edit 6 (line 175 before this step). Replace:

```vue
)

let poll: number | undefined
let tick: number | undefined
onMounted(() => {
  live.start()
  load()
  poll = window.setInterval(() => {
    if (document.visibilityState === 'visible') load()
  }, 10000)
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => {
  window.clearInterval(poll)
  window.clearInterval(tick)
  window.clearTimeout(reloadTimer)
})
watch(() => admin.token.value, load)
```

with:

```vue
)

let tick: number | undefined
onMounted(() => {
  live.start()
  load()
  // The clock of the header's "up 5 min": no read of the server.
  tick = window.setInterval(() => (now.value = Date.now()), 30000)
})
onBeforeUnmount(() => {
  window.clearInterval(tick)
})
watch(() => admin.token.value, load)
```

Edit 7 (line 231 before this step). Replace:

```vue
              <SessionTile v-if="t.session" :session="t.session" :create-transport="transportFor(t.session)" :data-member="t.name" @select="router.push(`/sessions/${t.session.id}`)">
                <template #leading>
                  <UCheckbox :model-value="selected.has(t.name)" :aria-label="`Select ${t.name} for the broadcast`" @update:model-value="toggle(t.name, $event)" />
                </template>
                <template #footer>
```

with:

```vue
              <SessionTile v-if="t.session" :session="t.session" :create-transport="transportFor(t.session)" :data-member="t.name" @select="router.push(`/sessions/${t.session.id}`)">
                <template #leading>
                  <UCheckbox :model-value="isSelected(t.name)" :aria-label="`Select ${t.name} for the broadcast`" data-broadcast-pick @update:model-value="toggle(t.name, $event)" />
                </template>
                <template #footer>
```

Edit 8 (line 262 before this step). Replace:

```vue
        </div>
        <div class="flex-none px-3 pb-3">
          <BroadcastBar :run-id="runId" :members="selectedNames" :disabled="!run || !!run.stoppedAt" />
        </div>
      </template>
```

with:

```vue
        </div>
        <div class="flex-none px-3 pb-3">
          <BroadcastBar :run-id="runId" :members="selection.selected" :will-type="selection.sending.length" :waiting="selection.waiting.length" :disabled="!run || !!run.stoppedAt" />
        </div>
      </template>
```


In `web/app/components/BroadcastBar.vue`:

Edit 1 (line 6 before this step). Replace:

```vue
 * /api/runs/{run}/broadcast), recorded as input by the display name, or by
 * the server's OS user when none is set (broadcastByName). The server skips a
 * member waiting on a prompt; the toast says who got the line and who was
 * skipped, and why.
 */
const props = defineProps<{ runId: string; members: string[]; disabled?: boolean }>()

const api = useSessions()
```

with:

```vue
 * /api/runs/{run}/broadcast), recorded as input by the display name, or by
 * the server's OS user when none is set (broadcastByName). The server skips a
 * member waiting on a prompt; the button counts the members it will type
 * into (`willType`) and says how many selected ones it will skip (`waiting`);
 * the toast says who got the line and who was skipped, and why.
 */
const props = defineProps<{ runId: string; members: string[]; willType: number; waiting: number; disabled?: boolean }>()

const api = useSessions()
```

Edit 2 (line 20 before this step). Replace:

```vue
const MAX = 4096
const tooLong = computed(() => new TextEncoder().encode(text.value).length > MAX)
const canSend = computed(() => !props.disabled && !sending.value && props.members.length > 0 && !!text.value.trim() && !tooLong.value)

async function send() {
```

with:

```vue
const MAX = 4096
const tooLong = computed(() => new TextEncoder().encode(text.value).length > MAX)
const canSend = computed(() => !props.disabled && !sending.value && props.willType > 0 && !!text.value.trim() && !tooLong.value)

async function send() {
```

Edit 3 (line 43 before this step). Replace:

```vue
    <UIcon name="i-lucide-megaphone" class="size-[18px] flex-none text-muted" />
    <span class="flex-none text-sm font-semibold text-highlighted">Broadcast</span>
    <span class="flex-none text-xs text-muted">to {{ members.length }} selected · skips agents waiting on a prompt</span>
    <UInput
      v-model="text"
```

with:

```vue
    <UIcon name="i-lucide-megaphone" class="size-[18px] flex-none text-muted" />
    <span class="flex-none text-sm font-semibold text-highlighted">Broadcast</span>
    <span class="flex-none text-xs text-muted" data-broadcast-scope>to {{ members.length }} selected<template v-if="waiting"> · <span class="text-warning">{{ waiting }} waiting skipped</span></template></span>
    <UInput
      v-model="text"
```

Edit 4 (line 54 before this step). Replace:

```vue
      :disabled="disabled"
    />
    <UButton type="submit" size="sm" :label="`Send to ${members.length}`" trailing-icon="i-lucide-corner-down-left" :loading="sending" :disabled="!canSend" />
  </form>
</template>
```

with:

```vue
      :disabled="disabled"
    />
    <UButton type="submit" size="sm" :label="`Send to ${props.willType}`" trailing-icon="i-lucide-corner-down-left" :loading="sending" :disabled="!canSend" data-broadcast-send />
  </form>
</template>
```


- [ ] **Step 12: The Crews page lists each crew's runs.** Create `web/app/components/CrewRuns.vue`:

```vue
<script setup lang="ts">
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { memberStatus } from '~/utils/crews'
import { memberDot, runBadge, runLive, runState } from '~/utils/runs'
import { relativeTime } from '~/utils/sessions'

/**
 * A crew's runs on the Crews page, newest first: each with its age, its state (with how many members wait), its members as avatars with
 * their status, a member's error, a link to its crew view and, while it goes, a Stop button. The first SHOWN show; the rest one click away.
 */
const props = defineProps<{ runs: RunInfo[]; sessions: SessionInfo[]; now: number }>()

const api = useSessions()
const live = useAttention()
const toast = useToast()

/** How many runs show before "Show N more". */
const SHOWN = 5
const all = ref(false)
const shown = computed(() => (all.value ? props.runs : props.runs.slice(0, SHOWN)))

const rows = computed(() =>
  shown.value.map((r) => {
    const st = runState(r, props.sessions)
    return {
      run: r,
      state: st,
      badge: runBadge(st.state, st.needs),
      live: runLive(st.state),
      members: r.members.map((m) => {
        const status = memberStatus(r, m, props.sessions)
        return { m, status, dot: memberDot(status) }
      }),
      errors: r.members.filter((m) => m.error),
    }
  }),
)

const stopping = ref('')
async function stop(r: RunInfo) {
  stopping.value = r.id
  try {
    live.applyRun(await api.stopRun(r.id))
    toast.add({ title: 'Run stopped', description: 'Every member was stopped; the worktrees and branches stay.', icon: 'i-lucide-square', color: 'neutral' })
  } catch (e) {
    toast.add({ title: 'Stop failed', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    stopping.value = ''
  }
}
</script>

<template>
  <ul v-if="runs.length" class="flex flex-col gap-1.5" data-crew-runs>
    <li v-for="row in rows" :key="row.run.id" class="flex flex-col gap-1 rounded-md bg-elevated/40 px-2 py-1.5" :data-run="row.run.id" :data-state="row.state.state">
      <div class="flex min-w-0 items-center gap-1.5 text-xs">
        <NuxtLink :to="`/runs/${encodeURIComponent(row.run.id)}`" class="min-w-0 truncate font-medium text-default hover:underline" :title="row.run.id">
          {{ relativeTime(row.run.startedAt, now, { suffix: true }) }}
        </NuxtLink>
        <UBadge :label="row.badge.label" :color="row.badge.color" variant="subtle" size="sm" class="flex-none" data-run-state />
        <UBadge v-if="row.run.yolo" label="yolo" icon="i-lucide-shield-off" color="warning" variant="subtle" size="sm" class="flex-none" />
        <div class="flex-1" />
        <UButton :to="`/runs/${encodeURIComponent(row.run.id)}`" label="Open" size="xs" color="neutral" variant="ghost" :title="`Open the crew view of ${row.run.id}`" data-run-open />
        <UButton v-if="row.live" icon="i-lucide-square" size="xs" color="error" variant="ghost" :aria-label="`Stop the run ${row.run.id}`" :loading="stopping === row.run.id" data-run-stop @click="stop(row.run)" />
      </div>
      <div class="flex flex-wrap gap-1" data-run-members>
        <span v-for="x in row.members" :key="x.m.name" class="relative" :title="`${x.m.name} · ${x.dot.label}`" :data-run-member="x.m.name" :data-status="x.status">
          <SessionAvatar :agent-id="x.m.agentId" :solid="x.status === 'needs_input'" :dashed="x.status === 'ended' || x.status === 'pending'" />
          <span class="absolute -right-0.5 -top-0.5 size-2 rounded-full ring-2 ring-default" :class="x.dot.dot" aria-hidden="true" />
        </span>
      </div>
      <p v-for="m in row.errors" :key="m.name" class="truncate text-[11px] text-error" :title="m.error">{{ m.name }}: {{ m.error }}</p>
    </li>
    <li v-if="runs.length > SHOWN">
      <UButton :label="all ? 'Show fewer' : `Show ${runs.length - SHOWN} more`" size="xs" color="neutral" variant="link" @click="all = !all" />
    </li>
  </ul>
</template>
```

In `web/app/pages/crews/[[id]].vue` (the list entry becomes a `<div>` holding the crew's link and, after it, its runs: a link holds no
other link):

Edit 1 (line 3 before this step). Replace:

```vue
import { ApiError } from '~/composables/useApi'
import { agentIcon } from '~/utils/agentIcons'
import { crewKey, defaultCrew, holdViewLink, pageAfterDelete, runActive, summaryOf, toCrewInput, toDraft, type DraftCrew } from '~/utils/crews'
import { relativeTime, shortCwd } from '~/utils/sessions'
```

with:

```vue
import { ApiError } from '~/composables/useApi'
import { agentIcon } from '~/utils/agentIcons'
import { crewKey, defaultCrew, holdViewLink, pageAfterDelete, summaryOf, toCrewInput, toDraft, type DraftCrew } from '~/utils/crews'
import { runLive, runState } from '~/utils/runs'
import { relativeTime, shortCwd } from '~/utils/sessions'
```

Edit 2 (line 40 before this step). Replace:

```vue
const agents = useState<AgentInfo[]>('crewAgents', () => [])
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
const runs = ref<RunInfo[]>([])
const loading = ref(false)
const loaded = ref(false)
```

with:

```vue
const agents = useState<AgentInfo[]>('crewAgents', () => [])
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
const loading = ref(false)
const loaded = ref(false)
```

Edit 3 (line 65 before this step). Replace:

```vue
  loading.value = true
  try {
    const [list, a, r] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalog(), api.listRuns()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
```

with:

```vue
  loading.value = true
  try {
    const [list, a] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalog()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
```

Edit 4 (line 75 before this step). Replace:

```vue
    total.value = list.total
    agents.value = a
    runs.value = r
    error.value = ''
    loaded.value = true
```

with:

```vue
    total.value = list.total
    agents.value = a
    error.value = ''
    loaded.value = true
```

Edit 5 (line 112 before this step). Replace:

```vue
watch(page, refresh)

async function refreshRuns() {
  try {
    runs.value = await api.listRuns()
  } catch {
    /* the badges keep what they showed */
  }
}

// A draft for the crew on screen, seeded from the crew read in full once it is read; /crews moves to the first crew of the page.
watch(
```

with:

```vue
watch(page, refresh)

// A draft for the crew on screen, seeded from the crew read in full once it is read; /crews moves to the first crew of the page.
watch(
```

Edit 6 (line 146 before this step). Replace:

```vue
}

/** Runs of the crew in the server's memory, newest first. */
function runsOf(id: string): RunInfo[] {
  return runs.value.filter((r) => r.crewId === id)
}

function status(key: string): { label: string; color: 'warning' | 'success' | 'neutral' } {
  if (isDirty(key)) return { label: 'Draft changes', color: 'warning' }
  if (runsOf(key).some(runActive)) return { label: 'Running', color: 'success' }
  return { label: 'Ready', color: 'neutral' }
}
```

with:

```vue
}

/** Runs of the crew in the server's memory, newest first, from the live store: run events keep them current. */
function runsOf(id: string): RunInfo[] {
  return id ? live.runs.value.filter((r) => r.crewId === id) : []
}

function status(key: string): { label: string; color: 'warning' | 'success' | 'neutral' } {
  if (isDirty(key)) return { label: 'Draft changes', color: 'warning' }
  if (runsOf(key).some((r) => runLive(runState(r, live.sessions.value).state))) return { label: 'Running', color: 'success' }
  return { label: 'Ready', color: 'neutral' }
}
```

Edit 7 (line 347 before this step). Replace:

```vue
    }
    const { run, viewLink } = launched
    runs.value = [run, ...runs.value.filter((r) => r.id !== run.id)]
    const ttl = c.viewLinkTtlSeconds ?? 0
    if (c.openAfterLaunch) {
```

with:

```vue
    }
    const { run, viewLink } = launched
    live.applyRun(run)
    const ttl = c.viewLinkTtlSeconds ?? 0
    if (c.openAfterLaunch) {
```

Edit 8 (line 378 before this step). Replace:

```vue
}

// "Running" follows the live store: when a crew member's session starts or
// ends, the runs are read again. Nothing polls.
const crewSessions = computed(() =>
  live.sessions.value
    .filter((s) => s.crew)
    .map((s) => `${s.id}:${s.status}`)
    .join(','),
)
let runsTimer: number | undefined
watch(crewSessions, () => {
  window.clearTimeout(runsTimer)
  runsTimer = window.setTimeout(refreshRuns, 300)
})

let tick: number | undefined
onMounted(() => {
```

with:

```vue
}

let tick: number | undefined
onMounted(() => {
```

Edit 9 (line 402 before this step). Replace:

```vue
  mounted = false
  window.clearInterval(tick)
  window.clearTimeout(runsTimer)
})
watch(
```

with:

```vue
  mounted = false
  window.clearInterval(tick)
})
watch(
```

Edit 10 (line 436 before this step). Replace:

```vue
        <nav class="flex flex-none flex-col gap-2 border-b border-default p-4 md:w-60 md:border-b-0 md:border-r" aria-label="Crews" data-crew-list>
          <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
          <NuxtLink
            v-for="item in list"
            :key="item.key || 'new'"
            :to="item.to"
            class="flex flex-col gap-1.5 rounded-md p-3 transition-colors"
            :class="item.key === selectedKey ? 'bg-default ring ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-current="item.key === selectedKey ? 'page' : undefined"
            data-crew-item
          >
            <span class="flex items-center gap-2">
              <span class="truncate text-sm font-semibold text-highlighted">{{ item.crew.name || 'Untitled crew' }}</span>
              <UBadge :label="status(item.key).label" :color="status(item.key).color" variant="subtle" size="sm" class="ml-auto flex-none" />
            </span>
            <span class="flex flex-wrap gap-1">
              <template v-for="(m, i) in item.crew.members" :key="i">
                <span v-if="agentOf(m.agentId)?.icon" class="grid size-6 place-items-center rounded-md bg-elevated text-primary" :title="`${m.name} · ${agentOf(m.agentId)?.name}`">
                  <UIcon :name="agentIcon(agentOf(m.agentId)!.icon)" class="size-3.5" />
                </span>
                <SessionAvatar v-else :agent-id="m.agentId" />
              </template>
            </span>
            <!-- The directory is cut short on its own line; what follows always shows whole. -->
            <span class="flex min-w-0 flex-col font-mono text-[11px] text-muted" data-crew-meta>
              <span class="truncate" :title="meta(item.crew, item.key || undefined).cwd">{{ meta(item.crew, item.key || undefined).cwd }}</span>
              <span>{{ meta(item.crew, item.key || undefined).rest }}</span>
            </span>
          </NuxtLink>
          <p v-if="loaded && !list.length" class="px-1 text-sm text-muted">No crews yet.</p>
          <UPagination v-if="total > PAGE_SIZE" v-model:page="page" :total="total" :items-per-page="PAGE_SIZE" size="xs" class="mt-2 self-center" />
```

with:

```vue
        <nav class="flex flex-none flex-col gap-2 border-b border-default p-4 md:w-60 md:border-b-0 md:border-r" aria-label="Crews" data-crew-list>
          <UAlert v-if="error" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="error" />
          <!-- A crew's runs follow its link, not inside it: a link holds no other link. -->
          <div v-for="item in list" :key="item.key || 'new'" class="flex flex-col gap-1" data-crew-entry>
            <NuxtLink
              :to="item.to"
              class="flex flex-col gap-1.5 rounded-md p-3 transition-colors"
              :class="item.key === selectedKey ? 'bg-default ring ring-default shadow-xs' : 'hover:bg-elevated/60'"
              :aria-current="item.key === selectedKey ? 'page' : undefined"
              data-crew-item
            >
              <span class="flex items-center gap-2">
                <span class="truncate text-sm font-semibold text-highlighted">{{ item.crew.name || 'Untitled crew' }}</span>
                <UBadge :label="status(item.key).label" :color="status(item.key).color" variant="subtle" size="sm" class="ml-auto flex-none" />
              </span>
              <span class="flex flex-wrap gap-1">
                <template v-for="(m, i) in item.crew.members" :key="i">
                  <span v-if="agentOf(m.agentId)?.icon" class="grid size-6 place-items-center rounded-md bg-elevated text-primary" :title="`${m.name} · ${agentOf(m.agentId)?.name}`">
                    <UIcon :name="agentIcon(agentOf(m.agentId)!.icon)" class="size-3.5" />
                  </span>
                  <SessionAvatar v-else :agent-id="m.agentId" />
                </template>
              </span>
              <!-- The directory is cut short on its own line; what follows always shows whole. -->
              <span class="flex min-w-0 flex-col font-mono text-[11px] text-muted" data-crew-meta>
                <span class="truncate" :title="meta(item.crew, item.key || undefined).cwd">{{ meta(item.crew, item.key || undefined).cwd }}</span>
                <span>{{ meta(item.crew, item.key || undefined).rest }}</span>
              </span>
            </NuxtLink>
            <CrewRuns :runs="runsOf(item.key)" :sessions="live.sessions.value" :now="now" class="px-1" />
          </div>
          <p v-if="loaded && !list.length" class="px-1 text-sm text-muted">No crews yet.</p>
          <UPagination v-if="total > PAGE_SIZE" v-model:page="page" :total="total" :items-per-page="PAGE_SIZE" size="xs" class="mt-2 self-center" />
```


- [ ] **Step 13: Type-check and test**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: no type error (no page uses `runActive`, `RunNameAsks` or the `crewRunNames` state any more: `grep -rn "crewRunNames\|RunNameAsks\|runActive" web/app` prints nothing); every vitest passes.

- [ ] **Step 14: The headless check.** With Task 4's run events on the server, build and start the test server as in Task 6, Step 10,
and write `$PW/runs-check.js`:

```js
// Round 4 Task 7: the Crews page lists a run under its crew with its members' statuses, the sidebar groups by run, the crew view reads
// nothing on a timer, and its broadcast goes to everyone by default with an untick that outlasts run events. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const token = 'dev-admin-token-change-me'
async function api(method, path, body) {
  const r = await fetch(base + path, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body && JSON.stringify(body) })
  const text = await r.text()
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`)
  return text ? JSON.parse(text) : {}
}
const shell = (name, when = 'immediately') => ({ name, agentId: 'shell', prompt: '', start: { when } })
;(async () => {
  const { crew } = await api('POST', '/api/crews', { name: 'Runs check', goal: 'see the runs', cwd: '', where: 'server', isolation: 'none', openAfterLaunch: true, members: ['a', 'b', 'c'].map((n) => shell(n)) })
  const { run } = await api('POST', `/api/crews/${crew.id}/launch`)
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript((t) => localStorage.setItem('conductor.adminToken', t), token)
    const page = await ctx.newPage()

    // The Crews page: the run under its crew, its members running.
    await page.goto(`${base}/crews/${crew.id}`, { waitUntil: 'commit' })
    const row = page.locator(`[data-crew-runs] [data-run="${run.id}"]`)
    await row.waitFor()
    await page.waitForFunction((id) => document.querySelector(`[data-run="${id}"]`)?.getAttribute('data-state') === 'running', run.id)
    await page.waitForFunction((id) => [...document.querySelectorAll(`[data-run="${id}"] [data-run-member]`)].map((m) => m.getAttribute('data-status')).join() === 'running,running,running', run.id)
    assert.equal(await row.getByRole('link', { name: 'Open' }).getAttribute('href'), `/runs/${run.id}`)
    console.log('crews page: the run, running, three members running')

    // The sidebar: the run's name heads its members.
    const header = page.locator(`[data-session-list] a[href="/runs/${run.id}"]`)
    await header.waitFor()
    assert.equal((await header.textContent()).trim(), 'Runs check')
    assert.equal(await page.locator(`[data-session-list] [data-sidebar-group="${run.id}"] a[href^="/sessions/"]`).count(), 3)
    console.log('sidebar: grouped under the run')

    // The crew view: nothing read on a timer.
    await page.goto(`${base}/runs/${run.id}`, { waitUntil: 'commit' })
    const send = page.locator('[data-broadcast-send]')
    await page.waitForFunction(() => document.querySelector('[data-broadcast-send]')?.textContent?.includes('Send to 3'))
    let reads = 0
    page.on('request', (r) => {
      if (r.url().includes('/api/runs')) reads++
    })
    await page.waitForTimeout(11000)
    assert.equal(reads, 0, 'the crew view reads no run on a timer')
    console.log('crew view: no read in 11 s, broadcast to all 3')

    // An untick outlasts a run event: a member added makes one.
    await page.locator('[data-member="b"] [data-broadcast-pick]').click()
    await page.waitForFunction(() => document.querySelector('[data-broadcast-send]')?.textContent?.includes('Send to 2'))
    await api('POST', `/api/runs/${run.id}/members`, shell('d', 'manual'))
    await page.locator('[data-member="d"]').waitFor()
    assert.ok(reads > 0, 'the run event made the store read the run')
    assert.match(await send.textContent(), /Send to 2/, 'the untick survived the run event')
    // A member that starts later is selected as it appears.
    await api('POST', `/api/runs/${run.id}/members/d/start`)
    await page.waitForFunction(() => document.querySelector('[data-broadcast-send]')?.textContent?.includes('Send to 3'))
    // One waiting is counted apart: the server skips it.
    const a = (await api('GET', `/api/runs/${run.id}`)).run.members.find((m) => m.name === 'a').sessionId
    await api('POST', `/api/sessions/${a}/attention`, { state: 'needs_input', message: 'Allow?' })
    await page.waitForFunction(() => document.querySelector('[data-broadcast-scope]')?.textContent?.includes('1 waiting skipped'))
    assert.match(await send.textContent(), /Send to 2/)
    console.log('broadcast: untick kept through a run event, a late member selected, one waiting skipped')

    // Stopping from the Crews page: the row says so without a reload.
    await page.goto(`${base}/crews/${crew.id}`, { waitUntil: 'commit' })
    await page.locator(`[data-run="${run.id}"] [data-run-stop]`).click()
    await page.waitForFunction((id) => document.querySelector(`[data-run="${id}"]`)?.getAttribute('data-state') === 'stopped', run.id)
    console.log('crews page: stopped')
  } finally {
    await browser.close()
  }
  console.log('PASS runs-check')
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
```

Run: `make web-build && make build-go`, start the server as in Task 6, then `(cd $PW && node runs-check.js)`; stop the server by its
pid and remove its data directory.
Expected: `PASS runs-check`, after `crew view: no read in 11 s, broadcast to all 3` and `broadcast: untick kept through a run event, a late
member selected, one waiting skipped`.

- [ ] **Step 15: Commit**

```bash
git add web/app/utils/runs.ts web/app/utils/runs.test.ts web/app/components/CrewRuns.vue web/app/components/SidebarRunHeader.vue \
  web/app/composables/useSessions.ts web/app/utils/crews.ts web/app/utils/crews.test.ts web/app/utils/sidebar.ts web/app/utils/sidebar.test.ts \
  web/app/composables/useAttention.ts web/app/layouts/default.vue web/app/components/SidebarRail.vue web/app/components/SessionSidebar.vue \
  'web/app/pages/runs/[run].vue' web/app/components/BroadcastBar.vue 'web/app/pages/crews/[[id]].vue'
git commit -m "web: runs live in the one store and follow run events; the Crews page lists them, the sidebar groups by run, a broadcast goes to everyone"
```

**Done when:**

- `web/app/utils/runs.ts` (the run state, mirroring Go's `runState` and its six-row table; the 250 ms coalescing; the tickets) and `runs.test.ts`, `crews.test.ts` and `sidebar.test.ts` pass (14, 41 and 15 tests).
- The live store (`useAttention`) keeps the runs: read on every snapshot of the event stream, on a `run` event (more than 8 pending → one `GET /api/runs`), and when a member session changes status; never on a timer; a reply never replaces what a later read or a removal applied.
- Run names come from the store (`RunNameAsks`, `runActive` and `crewRunNames` are gone); the full sidebar groups sessions by run in each section (`sidebarGroups`, `SidebarRunHeader`) and the rail's `railGroups` is built on it.
- The crew view reads the store (no 10 s poll); its broadcast selection defaults to every member with a live session and keeps a person's own ticks through re-reads and run events; the bar reads "Send to N" and counts the waiting members skipped.
- The Crews page lists each crew's runs (`CrewRuns`) with the data attributes Task 9 reads (`data-crew-runs`, `data-run`, `data-state`, `data-run-member`, `data-status`, the "Open" link).
- `npm --prefix web run typecheck && npm --prefix web test` pass, the `grep` of Step 13 prints nothing, and `runs-check.js` prints `PASS runs-check`.
- Committed: "web: runs live in the one store and follow run events; the Crews page lists them, the sidebar groups by run, a broadcast goes to everyone".

---

### Task 8: Yolo and Resume in the workbench

**Order:** after Task 7; its headless check needs Tasks 3 and 5 on the server. It spreads one choice (yolo) and one action (Resume) over six screens, whose wording and placement need checking by eye as well as by the check.

The workbench shows and sets what Tasks 3 and 5 built. The Launch dialog's server tab gets a yolo switch that starts at the server's
default (`yoloDefault` of `GET /api/catalog`), says when the launch overrides it, previews what the agent's recipe adds, and warns when the
agent has none (it launches as usual, with no badge). The crew editor gets a select: the server's default, on, or off (`yolo` left out,
`true`, `false`). The agent editor gets a "Yolo recipe" section: its arguments (an `ArgvInput`), its variables (shown, never masked: they
are mode switches, not secrets), and "No yolo recipe", saved as `{}`, which also drops a built-in's; empty fields save nothing, so an agent
that replaces a built-in keeps the built-in's recipe; the trust prompt and the session recipe, which the form has no control for, are
carried over from the agent edited, as its adapter is. A `YoloBadge` marks a session launched with a recipe applied, in the session
header, the tile's header and the sidebar's rows (warning colour and `i-lucide-shield-off`; never the junction mark). A `ResumeButton`
starts an ended session again: Resume when its agent has a conversation to resume (`agentSession.resumable`), else Relaunch ("a new
conversation"); the server decides in the end and the toast gives its `notice`. It sits in an ended session's header, on an ended tile
(the crew view; the wall's grid lists running sessions only, so the wall shows it in its focus header), beside the sidebar's exited rows
(icon only, outside the row's link) and on an ended member's placeholder on the crew view (the run route, for a member whose session has
left the server). The session header shows the agent's own session id when it has one (mono, cut short, copied on click).

**Files:**
- Create: `web/app/utils/yolo.ts`, `web/app/utils/yolo.test.ts`, `web/app/components/YoloBadge.vue`, `web/app/components/ResumeButton.vue`
- Modify: `web/app/composables/useSessions.ts` (as Task 7 left it) — `SessionInfo` `:29-31` and the new `AgentSession`, `YoloRecipe`, `SessionRecipe`, `ResumeResult` after it; `AgentInfo` `:63-65`; `AgentInput` `:85-87`; `CrewInfo` `:221-224`; `RunMember` `:259-262`; `create` and the resume calls `:333-340`; `catalogInfo` after `catalog`
- Modify: `web/app/utils/crews.ts` (as Task 7 left it) — `toCrewInput` `:18-21`; `web/app/utils/crews.test.ts` — `toCrewInput` `:62-64`
- Modify: `web/app/utils/agentForm.ts` — imports `:1`; before `AgentForm` `:18-21`; `AgentForm`'s end and `Field` `:31-37`; `formFromAgent`'s end `:53-56`; `formErrors`' end `:95-98`; `agentPayload`'s end `:138-141`; `web/app/utils/agentForm.test.ts` — imports `:3`, a `describe` appended
- Modify: `web/app/components/AddAgentSlideover.vue` — doc `:9-13`; after `removeEnv` `:108-110`; before the `allowArgs` switch `:252`
- Modify: `web/app/components/LaunchSessionModal.vue` — imports `:5-6`; state `:19-22`; the open watcher `:35-39`; before `server` `:46`; `submit`'s body `:88-90`; the server tab's arguments field `:147-150`
- Modify: `web/app/components/CrewEditor.vue` — imports `:6`; props `:14`; before `viewLink` `:71`; after the view-link switch `:202`
- Modify: `web/app/pages/crews/[[id]].vue` (as Task 7 left it) — `agents` `:40-43`; `refresh` `:65-79`; the editor's props `:451-454`
- Modify: `web/app/pages/sessions/[id].vue` — after `stored` `:60`; the header's title and right slot `:278-286` (Task 2 changes `reply`, another hunk)
- Modify: `web/app/components/SessionTile.vue` (as Task 6 left it) — the header `:50-54`
- Modify: `web/app/components/SessionSidebar.vue` (as Task 7 left it) — the needs and running rows `:89-92`, `:114-117`; the exited rows `:126-145`
- Modify: `web/app/pages/runs/[run].vue` (as Task 7 left it) — the placeholder tile's buttons `:250-253`
- Modify: `web/app/pages/wall.vue` — the focus header's buttons `:206-207` (Task 2 changes `reply`, another hunk)
- Not committed: `$PW/yolo-resume-check.js` (the headless-check directory, see Headless checks)

**Interfaces:**
- Consumes: Task 3's `yolo` on `POST /api/sessions` and on a crew, `yolo` on a session and a run, an agent's `yolo` and `trustPrompt`,
  `yoloDefault` on `GET /api/catalog`; Task 5's `agentSession` and `resumedFrom` on a session and `agentSession` on a run member, an agent's
  `session` recipe, `POST /api/sessions/{id}/resume` and `POST /api/runs/{run}/members/{name}/resume` (`201 {session, resumed, notice?}`).
- Produces:
```ts
// web/app/composables/useSessions.ts
export interface AgentSession { id: string; resumable: boolean; source: 'set' | 'hook' | 'resumed' }
export interface YoloRecipe { args?: string[]; env?: Record<string, string> }
export interface SessionRecipe { startArgs?: string[]; newId?: 'uuid' | 'name'; idFrom?: 'hook'; idPolicy?: 'latest' | 'lowest'; resumeArgs?: string[]; idPattern?: string; resumeNeedsCwd?: boolean }
export interface ResumeResult { session: SessionInfo; resumed: boolean; notice?: string }
// SessionInfo: yolo?, agentSession?, resumedFrom?; AgentInfo/AgentInput: yolo?, trustPrompt?, session?; CrewInfo: yolo?; RunMember: agentSession?
create(body: { agentId: string; name?: string; cwd?: string; args?: string[]; cols?: number; rows?: number; yolo?: boolean }): Promise<SessionInfo>
catalogInfo(): Promise<{ agents: AgentInfo[]; yoloDefault: boolean }>
resumeSession(id: string): Promise<ResumeResult>
resumeRunMember(runId: string, name: string): Promise<ResumeResult>
// web/app/utils/yolo.ts
export type YoloChoice = 'default' | 'on' | 'off'
export function yoloChoice(yolo: boolean | undefined): YoloChoice
export function yoloFromChoice(choice: YoloChoice): boolean | undefined
export function effectiveYolo(choice: boolean | undefined, serverDefault: boolean): boolean
export function hasYoloRecipe(agent: Pick<AgentInfo, 'yolo'> | undefined): boolean
export function yoloSummary(agent: Pick<AgentInfo, 'name' | 'command' | 'yolo'> | undefined, on: boolean, extra?: string[]): { argv: string[]; env: string[]; applies: boolean; notice: string }
export function resumeLabel(session: Pick<SessionInfo, 'agentSession'> | undefined): { label: 'Resume' | 'Relaunch'; icon: string; tooltip: string }
// web/app/utils/agentForm.ts
export interface YoloRow { uid: number; key: string; value: string }
export const YOLO_ENV_NAME: RegExp
export const MAX_YOLO = 16
// AgentForm: yoloArgs: string[]; yoloPending: string; yoloEnv: YoloRow[]; yoloNone: boolean; Field gains 'yolo'
export function yoloArgsOf(f: Pick<AgentForm, 'yoloArgs' | 'yoloPending'>): string[]
export function yoloOut(f: Pick<AgentForm, 'yoloArgs' | 'yoloPending' | 'yoloEnv' | 'yoloNone'>): YoloRecipe | undefined
// components
<YoloBadge :icon?="boolean" />                                   // [data-yolo-badge]
<ResumeButton :session? :run-id? :member? :stay? :icon-only? :size? @resumed />   // [data-resume][data-resume-kind="label"|"icon"]
<CrewEditor ... :yolo-default="boolean" />
```

- [ ] **Step 1: The types and the calls.** In `web/app/composables/useSessions.ts`:

Edit 1 (line 29 before this step). Replace:

```ts
  createdAt: string
  endedAt?: string
}
```

with:

```ts
  createdAt: string
  endedAt?: string
  /** Launched with its agent's yolo recipe applied: the agent skips its permission prompts. Missing when no recipe was applied. */
  yolo?: boolean
  /** The agent's own session (its conversation), when its agent has a session recipe: what Resume resumes. */
  agentSession?: AgentSession
  /** The session this one resumed or relaunched. */
  resumedFrom?: string
}

/** An agent's own session as Conductor knows it: the id it chose at launch (`set`), the one the agent's hooks reported (`hook`), or the one a resume took up (`resumed`). */
export interface AgentSession {
  id: string
  /** The agent has had a turn: there is a conversation to resume. Before one, Resume relaunches plainly. */
  resumable: boolean
  source: 'set' | 'hook' | 'resumed'
}

/** What a launch with yolo on adds to the agent's: arguments after its command and the launch's own, variables over its own. Not secret: shown as they are. */
export interface YoloRecipe {
  args?: string[]
  env?: Record<string, string>
}

/** How Conductor names an agent's own session and resumes it (catalog.SessionRecipe); `{id}` stands for the id, a whole argument. */
export interface SessionRecipe {
  startArgs?: string[]
  newId?: 'uuid' | 'name'
  idFrom?: 'hook'
  idPolicy?: 'latest' | 'lowest'
  resumeArgs?: string[]
  idPattern?: string
  resumeNeedsCwd?: boolean
}

/** Reply of the resume routes: the new session, whether it resumed the agent's conversation, and why not when it did not. */
export interface ResumeResult {
  session: SessionInfo
  resumed: boolean
  notice?: string
}
```

Edit 2 (line 63 before this step). Replace:

```ts
  /** The agent's website, an https URL, when known: a built-in's, or what was saved with the agent. */
  site?: string
}
```

with:

```ts
  /** The agent's website, an https URL, when known: a built-in's, or what was saved with the agent. */
  site?: string
  /** Its yolo recipe; `{}` is none. Missing: no recipe. */
  yolo?: YoloRecipe
  /** RE2 matched against its screen's text: its workspace-trust question, while which a crew types no prompt. */
  trustPrompt?: string
  /** Its session recipe, for Resume. */
  session?: SessionRecipe
}
```

Edit 3 (line 85 before this step). Replace:

```ts
  adapter?: string
  signal?: AgentSignal
}
```

with:

```ts
  adapter?: string
  signal?: AgentSignal
  /** Left out, an agent that replaces a built-in keeps the built-in's; `{}` has none. */
  yolo?: YoloRecipe
  trustPrompt?: string
  session?: SessionRecipe
}
```

Edit 4 (line 221 before this step). Replace:

```ts
  /** Lifetime of the view link a launch creates; none when missing. */
  viewLinkTtlSeconds?: number
  /** At most 12. The whole crew is at most 1 MiB as its file. */
  members: CrewMember[]
```

with:

```ts
  /** Lifetime of the view link a launch creates; none when missing. */
  viewLinkTtlSeconds?: number
  /** Overrides the server's yolo default for the crew's runs: missing follows it. A launch fixes the choice on the run. */
  yolo?: boolean
  /** At most 12. The whole crew is at most 1 MiB as its file. */
  members: CrewMember[]
```

Edit 5 (line 259 before this step). Replace:

```ts
  /** Starting or running, and its session waits on a prompt (a trust question before its prompt, or its agent's question), as last read. */
  needsInput?: boolean
  /**
   * GET /api/runs/{run} only: lines of tracked files its worktree adds and removes against the commit it began from,
```

with:

```ts
  /** Starting or running, and its session waits on a prompt (a trust question before its prompt, or its agent's question), as last read. */
  needsInput?: boolean
  /** The agent session of its latest session, kept once that session has left the server: what its Resume resumes. */
  agentSession?: AgentSession
  /**
   * GET /api/runs/{run} only: lines of tracked files its worktree adds and removes against the commit it began from,
```

Edit 6 (line 333 before this step). Replace:

```ts
    list: () => request<{ sessions: SessionInfo[] }>('/api/sessions').then((r) => r.sessions ?? []),
    get: (id: string, token?: string) => request<{ session: SessionInfo; role: Role; links?: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}`, { token }),
    create: (body: { agentId: string; name?: string; cwd?: string; args?: string[]; cols?: number; rows?: number }) =>
      request<SessionInfo>('/api/sessions', { method: 'POST', body }),
    stop: (id: string) => request<SessionInfo | void>(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    catalog: () => request<{ agents: AgentInfo[] }>('/api/catalog').then((r) => r.agents ?? []),
    /** The launchable agents plus the ids hidden from the catalog, which `unhideAgent` brings back. */
    catalogWithHidden: () =>
```

with:

```ts
    list: () => request<{ sessions: SessionInfo[] }>('/api/sessions').then((r) => r.sessions ?? []),
    get: (id: string, token?: string) => request<{ session: SessionInfo; role: Role; links?: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}`, { token }),
    /** `yolo` overrides the server's default for this launch; left out, the default holds. */
    create: (body: { agentId: string; name?: string; cwd?: string; args?: string[]; cols?: number; rows?: number; yolo?: boolean }) =>
      request<SessionInfo>('/api/sessions', { method: 'POST', body }),
    /** Starts an ended session again: resumed with its agent's recipe when it has had a turn, else relaunched (`notice` says why). */
    resumeSession: (id: string) => request<ResumeResult>(`/api/sessions/${encodeURIComponent(id)}/resume`, { method: 'POST' }),
    /** Starts an ended member of a run again, in its worktree, as the session route does for its session. */
    resumeRunMember: (runId: string, name: string) =>
      request<ResumeResult>(`/api/runs/${encodeURIComponent(runId)}/members/${encodeURIComponent(name)}/resume`, { method: 'POST' }),
    stop: (id: string) => request<SessionInfo | void>(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    catalog: () => request<{ agents: AgentInfo[] }>('/api/catalog').then((r) => r.agents ?? []),
    /** The launchable agents and the server's yolo default (`yolo` in its config, CONDUCTOR_YOLO, serve --yolo). */
    catalogInfo: () => request<{ agents: AgentInfo[]; yoloDefault?: boolean }>('/api/catalog').then((r) => ({ agents: r.agents ?? [], yoloDefault: !!r.yoloDefault })),
    /** The launchable agents plus the ids hidden from the catalog, which `unhideAgent` brings back. */
    catalogWithHidden: () =>
```


In `web/app/utils/crews.ts`, `toCrewInput` passes the crew's choice through (left out, it follows the server's default):

Edit 1 (line 18 before this step). Replace:

```ts
    openAfterLaunch: c.openAfterLaunch,
    viewLinkTtlSeconds: c.viewLinkTtlSeconds,
    members: c.members.map(toCrewMember),
  }
```

with:

```ts
    openAfterLaunch: c.openAfterLaunch,
    viewLinkTtlSeconds: c.viewLinkTtlSeconds,
    yolo: c.yolo,
    members: c.members.map(toCrewMember),
  }
```


and its test, in `web/app/utils/crews.test.ts`:

Edit 1 (line 62 before this step). Replace:

```ts
      ],
    })
  })
```

with:

```ts
      ],
    })
  })

  it("carries the crew's yolo choice, and leaves it out to follow the server's default", () => {
    expect(wire(toCrewInput({ ...info, yolo: true })).yolo).toBe(true)
    expect(wire(toCrewInput({ ...info, yolo: false })).yolo).toBe(false)
    expect(wire(toCrewInput(info))).not.toHaveProperty('yolo')
  })
```


- [ ] **Step 2: Write the failing tests** `web/app/utils/yolo.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { effectiveYolo, hasYoloRecipe, resumeLabel, yoloChoice, yoloFromChoice, yoloSummary } from './yolo'

describe('the crew editor yolo select', () => {
  it('round-trips the default, on and off', () => {
    for (const v of [undefined, true, false]) expect(yoloFromChoice(yoloChoice(v))).toBe(v)
    expect(yoloChoice(undefined)).toBe('default')
  })
})

describe('effectiveYolo', () => {
  it('is the launch or crew choice, else the server default', () => {
    expect(effectiveYolo(undefined, true)).toBe(true)
    expect(effectiveYolo(false, true)).toBe(false)
    expect(effectiveYolo(true, false)).toBe(true)
  })
})

describe('yoloSummary', () => {
  const claude = { name: 'Claude Code', command: ['claude'], yolo: { args: ['--dangerously-skip-permissions'] } }
  const copilot = { name: 'Copilot CLI', command: ['copilot'], yolo: { args: ['--yolo'], env: { COPILOT_ALLOW_ALL: 'true' } } }
  it('adds the recipe after the command and the extra arguments when on', () => {
    expect(yoloSummary(claude, true, ['--model', 'opus'])).toEqual({ argv: ['claude', '--model', 'opus', '--dangerously-skip-permissions'], env: [], applies: true, notice: '' })
    expect(yoloSummary(copilot, true).env).toEqual(['COPILOT_ALLOW_ALL'])
  })
  it('adds nothing when off', () => {
    expect(yoloSummary(claude, false)).toMatchObject({ argv: ['claude'], applies: false, notice: '' })
  })
  it('says so for an agent without a recipe, or with an empty one', () => {
    expect(hasYoloRecipe({ yolo: {} })).toBe(false)
    expect(yoloSummary({ name: 'Shell', command: ['/bin/bash', '-l'] }, true)).toMatchObject({ applies: false, notice: expect.stringContaining('Shell has no yolo recipe') })
    expect(yoloSummary({ name: 'X', command: ['x'], yolo: {} }, true).applies).toBe(false)
  })
})

describe('resumeLabel', () => {
  it('resumes a conversation the agent has had, and relaunches otherwise', () => {
    expect(resumeLabel({ agentSession: { id: 'u', resumable: true, source: 'set' } }).label).toBe('Resume')
    expect(resumeLabel({ agentSession: { id: 'u', resumable: false, source: 'set' } }).label).toBe('Relaunch')
    expect(resumeLabel({}).label).toBe('Relaunch')
  })
})
```

and append to `web/app/utils/agentForm.test.ts` (with `yoloOut` added to its import):

Edit 1 (line 1 before this step). Replace:

```ts
import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { AGENT_ID_PATTERN, MASK, agentPayload, commandCheck, commandOf, formErrors, formFromAgent, signalOut, siteError } from './agentForm'
import { slugId } from './argv'
```

with:

```ts
import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { AGENT_ID_PATTERN, MASK, agentPayload, commandCheck, commandOf, formErrors, formFromAgent, signalOut, siteError, yoloOut } from './agentForm'
import { slugId } from './argv'
```

Edit 2 (line 149 before this step). Replace:

```ts
    expect(commandCheck({ found: false, unknown: 'timeout' }, 'aider')).toEqual({ state: 'slow' })
  })
})
```

with:

```ts
    expect(commandCheck({ found: false, unknown: 'timeout' }, 'aider')).toEqual({ state: 'slow' })
  })
})

describe('the yolo recipe', () => {
  const copilot: AgentInfo = {
    id: 'copilot',
    name: 'Copilot CLI',
    command: ['copilot'],
    allowArgs: true,
    yolo: { args: ['--yolo'], env: { COPILOT_ALLOW_ALL: 'true' } },
    trustPrompt: 'Do you trust',
    session: { startArgs: ['--session-id', '{id}'], resumeArgs: ['--session-id', '{id}'], idPattern: '^x$' },
  }

  it('round-trips a recipe, and carries the trust prompt and the session recipe the form has no control for', () => {
    const f = formFromAgent(copilot, counter())
    expect(f.yoloArgs).toEqual(['--yolo'])
    expect(f.yoloEnv.map((r) => [r.key, r.value])).toEqual([['COPILOT_ALLOW_ALL', 'true']])
    expect(agentPayload(f, copilot)).toMatchObject({ yolo: copilot.yolo, trustPrompt: 'Do you trust', session: copilot.session })
  })

  it('saves {} for no recipe, and nothing for empty fields, which keeps a built-in recipe', () => {
    const f = formFromAgent(copilot, counter())
    expect(yoloOut({ ...f, yoloNone: true })).toEqual({})
    expect(yoloOut({ ...f, yoloArgs: [], yoloEnv: [] })).toBeUndefined()
    expect(formFromAgent({ ...copilot, yolo: {} }, counter()).yoloNone).toBe(true)
  })

  it('counts what is typed and not yet an argument', () => {
    const f = { ...formFromAgent(undefined, counter()), yoloPending: '--yes "a b"' }
    expect(yoloOut(f)).toEqual({ args: ['--yes', 'a b'] })
  })

  it("refuses what the server refuses: Conductor's variables, bad names, an open quote", () => {
    const ok = () => ({ ...formFromAgent(undefined, counter()), name: 'X', id: 'x', command: ['x'] })
    expect(formErrors({ ...ok(), yoloEnv: [{ uid: 1, key: 'CONDUCTOR_TOKEN', value: 'x' }] }).yolo).toBeTruthy()
    expect(formErrors({ ...ok(), yoloEnv: [{ uid: 1, key: 'BAD NAME', value: 'x' }] }).yolo).toBeTruthy()
    expect(formErrors({ ...ok(), yoloPending: '"open' }).yolo).toBeTruthy()
    expect(formErrors({ ...ok(), yoloEnv: [{ uid: 1, key: 'GOOSE_MODE', value: 'auto' }] })).toEqual({})
  })
})
```


Run: `npm --prefix web test -- app/utils/yolo.test.ts app/utils/agentForm.test.ts`
Expected: FAIL: `./yolo` does not resolve, and `yoloOut` is not exported.

- [ ] **Step 3: Write `web/app/utils/yolo.ts`**:

```ts
import type { AgentInfo, SessionInfo } from '~/composables/useSessions'

/** A crew's yolo choice as its editor's select shows it: the server's default, or on, or off. */
export type YoloChoice = 'default' | 'on' | 'off'

/** The select's value for a crew's `yolo`. */
export function yoloChoice(yolo: boolean | undefined): YoloChoice {
  return yolo === undefined ? 'default' : yolo ? 'on' : 'off'
}

/** A crew's `yolo` for the select's value: left out for the server's default. */
export function yoloFromChoice(choice: YoloChoice): boolean | undefined {
  return choice === 'default' ? undefined : choice === 'on'
}

/** Whether a launch runs with yolo: its own choice, else the server's default. */
export function effectiveYolo(choice: boolean | undefined, serverDefault: boolean): boolean {
  return choice ?? serverDefault
}

/** Whether an agent has a yolo recipe: something to add to a launch. */
export function hasYoloRecipe(agent: Pick<AgentInfo, 'yolo'> | undefined): boolean {
  return !!(agent?.yolo?.args?.length || Object.keys(agent?.yolo?.env ?? {}).length)
}

/**
 * What a launch of `agent` with `extra` arguments runs, yolo `on` or not: the argv the server builds (its command, the arguments, the
 * recipe's arguments when it applies; the adapter's flags come after, on the server), the recipe's variables by name, and a notice when
 * yolo is on and the agent has no recipe: it is launched as it would be without, and no badge shows.
 */
export function yoloSummary(agent: Pick<AgentInfo, 'name' | 'command' | 'yolo'> | undefined, on: boolean, extra: string[] = []): { argv: string[]; env: string[]; applies: boolean; notice: string } {
  if (!agent) return { argv: [], env: [], applies: false, notice: '' }
  const applies = on && hasYoloRecipe(agent)
  return {
    argv: [...agent.command, ...extra, ...(applies ? (agent.yolo?.args ?? []) : [])],
    env: applies ? Object.keys(agent.yolo?.env ?? {}).sort() : [],
    applies,
    notice: on && !applies ? `${agent.name} has no yolo recipe: it launches as usual, with its permission prompts.` : '',
  }
}

/**
 * What an ended session's start-again button says: Resume when its agent has a conversation to resume (a session recipe, and a turn had),
 * else Relaunch, which starts a new conversation. The server decides in the end, and says why when it relaunches.
 */
export function resumeLabel(session: Pick<SessionInfo, 'agentSession'> | undefined): { label: 'Resume' | 'Relaunch'; icon: string; tooltip: string } {
  if (session?.agentSession?.resumable) return { label: 'Resume', icon: 'i-lucide-history', tooltip: 'Starts it again with its conversation' }
  return { label: 'Relaunch', icon: 'i-lucide-rotate-ccw', tooltip: 'Starts it again: a new conversation' }
}
```

- [ ] **Step 4: The agent form's yolo fields.** In `web/app/utils/agentForm.ts`:

Edit 1 (line 1 before this step). Replace:

```ts
import type { AgentInfo, AgentInput, AgentSignal, CommandCheckReply } from '~/composables/useSessions'
import { hasOpenQuote, splitArgs } from './argv'
```

with:

```ts
import type { AgentInfo, AgentInput, AgentSignal, CommandCheckReply, YoloRecipe } from '~/composables/useSessions'
import { hasOpenQuote, splitArgs } from './argv'
```

Edit 2 (line 18 before this step). Replace:

```ts
}

/** The add-agent form. `pendingCommand` is what is typed in the command field and not yet an argument. */
export interface AgentForm {
```

with:

```ts
}

/** One variable of the yolo recipe: shown as it is, never masked (a mode switch, not a secret). */
export interface YoloRow {
  uid: number
  key: string
  value: string
}

/** The server's rule for a yolo variable's name (validateYolo in internal/catalog): never one of Conductor's own. */
export const YOLO_ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

/** At most this many yolo arguments, and as many variables (internal/catalog). */
export const MAX_YOLO = 16

/** The add-agent form. `pendingCommand` is what is typed in the command field and not yet an argument. */
export interface AgentForm {
```

Edit 3 (line 31 before this step). Replace:

```ts
  signal: SignalKind
  pattern: string
}

export type Field = 'name' | 'id' | 'command' | 'pattern' | 'env' | 'site'

/** The form for `a`, or an empty one. Stored env values come in masked, by name; passthrough names follow as rows without a value. */
```

with:

```ts
  signal: SignalKind
  pattern: string
  /** The yolo recipe: its arguments, what is typed and not yet one, and its variables. */
  yoloArgs: string[]
  yoloPending: string
  yoloEnv: YoloRow[]
  /** "No yolo recipe": saved as `{}`, which also drops a built-in's. */
  yoloNone: boolean
}

export type Field = 'name' | 'id' | 'command' | 'pattern' | 'env' | 'site' | 'yolo'

/** The form for `a`, or an empty one. Stored env values come in masked, by name; passthrough names follow as rows without a value. */
```

Edit 4 (line 53 before this step). Replace:

```ts
    signal: a?.signal?.kind ?? 'bell',
    pattern: a?.signal?.pattern ?? '',
  }
}
```

with:

```ts
    signal: a?.signal?.kind ?? 'bell',
    pattern: a?.signal?.pattern ?? '',
    yoloArgs: [...(a?.yolo?.args ?? [])],
    yoloPending: '',
    yoloEnv: Object.entries(a?.yolo?.env ?? {})
      .sort(([x], [y]) => x.localeCompare(y))
      .map(([key, value]) => ({ uid: uid(), key, value })),
    yoloNone: !!a?.yolo && !a.yolo.args?.length && !Object.keys(a.yolo.env ?? {}).length,
  }
}

/** The yolo arguments the form holds: its arguments, then what is typed and not yet one. */
export function yoloArgsOf(f: Pick<AgentForm, 'yoloArgs' | 'yoloPending'>): string[] {
  return [...f.yoloArgs, ...splitArgs(f.yoloPending)]
}

/**
 * The yolo recipe to save: `{}` for "No yolo recipe"; the arguments and variables when there are any; else nothing, so that an agent that
 * replaces a built-in keeps the built-in's and a new agent has none.
 */
export function yoloOut(f: Pick<AgentForm, 'yoloArgs' | 'yoloPending' | 'yoloEnv' | 'yoloNone'>): YoloRecipe | undefined {
  if (f.yoloNone) return {}
  const args = yoloArgsOf(f)
  const env: Record<string, string> = {}
  for (const r of f.yoloEnv) if (r.key.trim()) env[r.key.trim()] = r.value
  if (!args.length && !Object.keys(env).length) return undefined
  return { args: args.length ? args : undefined, env: Object.keys(env).length ? env : undefined }
}
```

Edit 5 (line 95 before this step). Replace:

```ts
    seen.add(key)
    if (!r.masked && r.value === MASK) e.env = `${key}: ${MASK} stands for a stored value; type the real value`
  }
  return e
```

with:

```ts
    seen.add(key)
    if (!r.masked && r.value === MASK) e.env = `${key}: ${MASK} stands for a stored value; type the real value`
  }
  if (!f.yoloNone) {
    const yoloKeys = f.yoloEnv.map((r) => r.key.trim()).filter(Boolean)
    if (hasOpenQuote(f.yoloPending)) e.yolo = 'Close the quote in the yolo arguments, or remove it'
    else if (yoloArgsOf(f).length > MAX_YOLO || yoloKeys.length > MAX_YOLO) e.yolo = `At most ${MAX_YOLO} yolo arguments and ${MAX_YOLO} variables`
    else if (yoloKeys.some((k) => !YOLO_ENV_NAME.test(k) || k.startsWith('CONDUCTOR_'))) e.yolo = "A yolo variable's name is letters, digits and _, not starting with a digit nor CONDUCTOR_"
    else if (new Set(yoloKeys).size !== yoloKeys.length) e.yolo = 'A yolo variable is listed twice'
    else if (f.yoloEnv.some((r) => !r.key.trim() && r.value)) e.yolo = 'Give every yolo variable a name'
  }
  return e
```

Edit 6 (line 138 before this step). Replace:

```ts
    adapter: prev?.adapter,
    signal: signalOut(f.signal, f.pattern, prev?.signal),
  }
}
```

with:

```ts
    adapter: prev?.adapter,
    signal: signalOut(f.signal, f.pattern, prev?.signal),
    yolo: yoloOut(f),
    // What the form has no control for comes from the agent edited.
    trustPrompt: prev?.trustPrompt,
    session: prev?.session,
  }
}
```


Run: `npm --prefix web test -- app/utils/yolo.test.ts app/utils/agentForm.test.ts app/utils/crews.test.ts`
Expected: PASS.

- [ ] **Step 5: The badge and the button.** Create `web/app/components/YoloBadge.vue`:

```vue
<script setup lang="ts">
/**
 * "yolo": the session was launched with its agent's yolo recipe, so the agent skips its permission prompts. It says Conductor applied the
 * recipe, not that no dialog can appear. `icon` shows the icon alone, for a sidebar row.
 */
withDefaults(defineProps<{ icon?: boolean }>(), { icon: false })
</script>

<template>
  <UTooltip text="Launched with its agent's yolo recipe: it skips its permission prompts">
    <UIcon v-if="icon" name="i-lucide-shield-off" class="size-3.5 flex-none text-warning" aria-label="yolo" data-yolo-badge />
    <UBadge v-else label="yolo" icon="i-lucide-shield-off" color="warning" variant="subtle" size="sm" class="flex-none" data-yolo-badge />
  </UTooltip>
</template>
```

and `web/app/components/ResumeButton.vue`:

```vue
<script setup lang="ts">
import type { ResumeResult, SessionInfo } from '~/composables/useSessions'
import { resumeLabel } from '~/utils/yolo'

/**
 * Starts an ended session again (POST /api/sessions/{id}/resume), or an ended member of a run that has no session left on the server
 * (`runId` and `member`: POST /api/runs/{run}/members/{name}/resume): Resume when its agent has a conversation to resume, else Relaunch.
 * The new session opens unless `stay` (the crew view, where its tile appears); a relaunch toasts why it did not resume.
 */
const props = withDefaults(
  defineProps<{ session?: SessionInfo; runId?: string; member?: string; stay?: boolean; iconOnly?: boolean; size?: 'xs' | 'sm' | 'md' }>(),
  { stay: false, iconOnly: false, size: 'sm' },
)
const emit = defineEmits<{ resumed: [result: ResumeResult] }>()

const api = useSessions()
const live = useAttention()
const toast = useToast()
const busy = ref(false)
const look = computed(() => resumeLabel(props.session ?? live.runOf(props.runId ?? '')?.members.find((m) => m.name === props.member)))

async function go() {
  busy.value = true
  try {
    const r = props.session ? await api.resumeSession(props.session.id) : await api.resumeRunMember(props.runId!, props.member!)
    if (r.notice) toast.add({ title: `${r.session.name} started anew`, description: r.notice, icon: 'i-lucide-rotate-ccw', color: 'neutral' })
    else toast.add({ title: `${r.session.name} resumed`, icon: 'i-lucide-history', color: 'success' })
    emit('resumed', r)
    if (!props.stay) await navigateTo(`/sessions/${r.session.id}`)
  } catch (e) {
    toast.add({ title: 'It did not start', description: (e as Error).message, icon: 'i-lucide-triangle-alert', color: 'error' })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UTooltip :text="look.tooltip">
    <UButton
      :icon="look.icon"
      :label="iconOnly ? undefined : look.label"
      :aria-label="look.label"
      :size="size"
      color="neutral"
      variant="outline"
      :loading="busy"
      data-resume
      :data-resume-kind="iconOnly ? 'icon' : 'label'"
      @click.stop.prevent="go"
    />
  </UTooltip>
</template>
```

- [ ] **Step 6: The agent editor's "Yolo recipe".** In `web/app/components/AddAgentSlideover.vue`:

Edit 1 (line 9 before this step). Replace:

```vue
 * agent with the same ID on the server, so an edit sends the whole agent:
 * fields this form has no control for (working directory, icon, adapter, the
 * tool-events flag) are carried over from the agent being edited.
 */
const props = defineProps<{
```

with:

```vue
 * agent with the same ID on the server, so an edit sends the whole agent:
 * fields this form has no control for (working directory, icon, adapter, the
 * tool-events flag, the trust prompt, the session recipe) are carried over
 * from the agent being edited.
 */
const props = defineProps<{
```

Edit 2 (line 108 before this step). Replace:

```vue
function removeEnv(rowUid: number) {
  form.env = form.env.filter((r) => r.uid !== rowUid)
}
```

with:

```vue
function removeEnv(rowUid: number) {
  form.env = form.env.filter((r) => r.uid !== rowUid)
}
function addYoloEnv() {
  form.yoloEnv.push({ uid: uid(), key: '', value: '' })
}
function removeYoloEnv(rowUid: number) {
  form.yoloEnv = form.yoloEnv.filter((r) => r.uid !== rowUid)
}
```

Edit 3 (line 250 before this step). Replace:

```vue
        </div>

        <USwitch v-model="form.allowArgs" label="Accept extra arguments" description="The Launch dialog can append arguments to this command." />
```

with:

```vue
        </div>

        <div class="text-sm" data-yolo-recipe>
          <div class="font-medium text-default">Yolo recipe</div>
          <p class="mt-1 text-xs text-muted">
            What a launch with yolo on adds so the agent skips its permission prompts: arguments after the command, variables over its own.
            Shown as they are: put no secret here.
          </p>
          <USwitch v-model="form.yoloNone" label="No yolo recipe" description="Yolo leaves this agent as it is, with a notice." size="sm" class="mt-2" />
          <template v-if="!form.yoloNone">
            <div class="mt-2">
              <ArgvInput v-model="form.yoloArgs" v-model:pending="form.yoloPending" placeholder="--dangerously-skip-permissions" :invalid="!!shown.yolo" />
            </div>
            <div class="mt-2 flex flex-col gap-2">
              <div v-for="row in form.yoloEnv" :key="row.uid" class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-2">
                <UInput v-model="row.key" placeholder="NAME" size="sm" aria-label="Yolo variable name" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" />
                <UInput v-model="row.value" placeholder="value" size="sm" aria-label="Yolo variable value" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" />
                <UButton icon="i-lucide-trash-2" size="xs" color="neutral" variant="ghost" :aria-label="`Remove ${row.key || 'variable'}`" @click="removeYoloEnv(row.uid)" />
              </div>
              <UButton label="Add variable" icon="i-lucide-plus" size="xs" color="neutral" variant="ghost" class="self-start" @click="addYoloEnv" />
            </div>
          </template>
          <p v-if="shown.yolo" class="mt-1 text-sm text-error">{{ shown.yolo }}</p>
        </div>

        <USwitch v-model="form.allowArgs" label="Accept extra arguments" description="The Launch dialog can append arguments to this command." />
```


- [ ] **Step 7: The Launch dialog's switch.** In `web/app/components/LaunchSessionModal.vue`:

Edit 1 (line 5 before this step). Replace:

```vue
import { splitArgs } from '~/utils/argv'
import { hostAdapter, hostCommand } from '~/utils/hostCommand'

const open = defineModel<boolean>('open', { default: false })
```

with:

```vue
import { splitArgs } from '~/utils/argv'
import { hostAdapter, hostCommand } from '~/utils/hostCommand'
import { effectiveYolo, yoloSummary } from '~/utils/yolo'

const open = defineModel<boolean>('open', { default: false })
```

Edit 2 (line 19 before this step). Replace:

```vue
const error = ref('')
const knownHosted = ref<Set<string>>(new Set())

const state = reactive<{ agentId: string; runsOn: 'server' | 'local'; name: string; cwd: string; args: string }>({ agentId: '', runsOn: 'server', name: '', cwd: '', args: '' })

const selected = computed(() => agents.value.find((a) => a.id === state.agentId))
```

with:

```vue
const error = ref('')
const knownHosted = ref<Set<string>>(new Set())
/** The server's yolo default (GET /api/catalog). */
const yoloDefault = ref(false)

/** `yolo` is this launch's own choice; undefined follows the server's default. */
const state = reactive<{ agentId: string; runsOn: 'server' | 'local'; name: string; cwd: string; args: string; yolo?: boolean }>({ agentId: '', runsOn: 'server', name: '', cwd: '', args: '' })

const selected = computed(() => agents.value.find((a) => a.id === state.agentId))
```

Edit 3 (line 35 before this step). Replace:

```vue
  knownHosted.value = new Set(live.sessions.value.filter((s) => s.kind === 'hosted').map((s) => s.id))
  loading.value = true
  try {
    agents.value = await api.catalog()
    if (!state.agentId && offered.value[0]) state.agentId = offered.value[0].id
  } catch (e) {
```

with:

```vue
  knownHosted.value = new Set(live.sessions.value.filter((s) => s.kind === 'hosted').map((s) => s.id))
  loading.value = true
  state.yolo = undefined
  try {
    const info = await api.catalogInfo()
    agents.value = info.agents
    yoloDefault.value = info.yoloDefault
    if (!state.agentId && offered.value[0]) state.agentId = offered.value[0].id
  } catch (e) {
```

Edit 4 (line 44 before this step). Replace:

```vue
  }
})

const server = computed(() => httpBase.value || (import.meta.client ? location.origin : ''))
```

with:

```vue
  }
})

// Yolo: the launch's switch shows the server's default until it is moved;
// the preview shows what the recipe adds, and an agent without one says so.
const yolo = computed({
  get: () => effectiveYolo(state.yolo, yoloDefault.value),
  set: (on: boolean) => (state.yolo = on === yoloDefault.value ? undefined : on),
})
const extraArgs = computed(() => (selected.value?.allowArgs && state.args.trim() ? splitArgs(state.args) : []))
const yoloView = computed(() => yoloSummary(selected.value, yolo.value, extraArgs.value))

const server = computed(() => httpBase.value || (import.meta.client ? location.origin : ''))
```

Edit 5 (line 88 before this step). Replace:

```vue
      cwd: state.cwd || undefined,
      args: selected.value?.allowArgs && state.args.trim() ? splitArgs(state.args) : undefined,
    })
    toast.add({ title: 'Session started', description: session.name, color: 'success', icon: 'i-lucide-play' })
```

with:

```vue
      cwd: state.cwd || undefined,
      args: selected.value?.allowArgs && state.args.trim() ? splitArgs(state.args) : undefined,
      yolo: state.yolo,
    })
    toast.add({ title: 'Session started', description: session.name, color: 'success', icon: 'i-lucide-play' })
```

Edit 6 (line 147 before this step). Replace:

```vue
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
        </template>
```

with:

```vue
            <UInput v-model="state.args" placeholder="--model opus" class="w-full font-mono" />
          </UFormField>
          <div class="flex flex-col gap-1.5" data-launch-yolo>
            <USwitch
              v-model="yolo"
              label="Yolo"
              :description="state.yolo === undefined ? `The server's default (${yoloDefault ? 'on' : 'off'})` : 'For this launch'"
              data-yolo-switch
            />
            <p v-if="yoloView.notice" class="flex items-center gap-1.5 text-xs text-warning" data-yolo-missing><UIcon name="i-lucide-triangle-alert" class="size-3.5 flex-none" />{{ yoloView.notice }}</p>
            <p v-else-if="yoloView.applies" class="text-xs text-muted">
              Skips its permission prompts: <code class="font-mono" data-yolo-argv>{{ yoloView.argv.join(' ') }}</code><template v-if="yoloView.env.length"> with {{ yoloView.env.join(', ') }}</template>
            </p>
          </div>
        </template>
```


- [ ] **Step 8: The crew editor's select.** In `web/app/components/CrewEditor.vue`:

Edit 1 (line 5 before this step). Replace:

```vue
import { gitCheckLine, type GitCheckView } from '~/utils/dirInput'
import { shortCwd } from '~/utils/sessions'

/**
```

with:

```vue
import { gitCheckLine, type GitCheckView } from '~/utils/dirInput'
import { shortCwd } from '~/utils/sessions'
import { yoloChoice, yoloFromChoice, type YoloChoice } from '~/utils/yolo'

/**
```

Edit 2 (line 12 before this step). Replace:

```vue
 */
const crew = defineModel<DraftCrew>({ required: true })
const props = defineProps<{ agents: AgentInfo[]; dirty: boolean; saving?: boolean; launching?: boolean }>()
const emit = defineEmits<{ save: []; discard: []; duplicate: []; launch: []; delete: [] }>()
```

with:

```vue
 */
const crew = defineModel<DraftCrew>({ required: true })
const props = defineProps<{ agents: AgentInfo[]; dirty: boolean; saving?: boolean; launching?: boolean; yoloDefault?: boolean }>()
const emit = defineEmits<{ save: []; discard: []; duplicate: []; launch: []; delete: [] }>()
```

Edit 3 (line 68 before this step). Replace:

```vue
  { label: 'Shared working directory', value: 'none' },
]

const viewLink = computed({
```

with:

```vue
  { label: 'Shared working directory', value: 'none' },
]

// Yolo for the crew's runs: the server's default, or on, or off. A launch
// fixes the choice on the run, so members started later follow it.
const yoloItems = computed<Array<{ label: string; value: YoloChoice }>>(() => [
  { label: `Yolo: the server's default (${props.yoloDefault ? 'on' : 'off'})`, value: 'default' },
  { label: 'Yolo on: skip permission prompts', value: 'on' },
  { label: 'Yolo off', value: 'off' },
])
const yolo = computed({
  get: () => yoloChoice(crew.value.yolo),
  set: (c: YoloChoice) => (crew.value = { ...crew.value, yolo: yoloFromChoice(c) }),
})

const viewLink = computed({
```

Edit 4 (line 201 before this step). Replace:

```vue
        <USwitch :model-value="crew.openAfterLaunch" label="Open the crew view after launch" size="sm" @update:model-value="set('openAfterLaunch', $event)" />
        <USwitch v-model="viewLink" :label="viewLinkLabel" size="sm" />
      </div>
    </div>
```

with:

```vue
        <USwitch :model-value="crew.openAfterLaunch" label="Open the crew view after launch" size="sm" @update:model-value="set('openAfterLaunch', $event)" />
        <USwitch v-model="viewLink" :label="viewLinkLabel" size="sm" />
        <USelect v-model="yolo" :items="yoloItems" aria-label="Yolo" class="w-full" data-crew-yolo />
      </div>
    </div>
```


and in `web/app/pages/crews/[[id]].vue`, the page reads the server's default with the catalog and hands it to the editor:

Edit 1 (line 40 before this step). Replace:

```vue
const crewLoading = ref<string>()
const agents = useState<AgentInfo[]>('crewAgents', () => [])
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
const loading = ref(false)
```

with:

```vue
const crewLoading = ref<string>()
const agents = useState<AgentInfo[]>('crewAgents', () => [])
/** The server's yolo default, for the editor's select. */
const yoloDefault = useState<boolean>('crewYoloDefault', () => false)
const drafts = useState<Record<string, DraftCrew>>('crewDrafts', () => ({}))
const loading = ref(false)
```

Edit 2 (line 65 before this step). Replace:

```vue
  loading.value = true
  try {
    const [list, a] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalog()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
```

with:

```vue
  loading.value = true
  try {
    const [list, a] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalogInfo()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
```

Edit 3 (line 74 before this step). Replace:

```vue
    crews.value = list.crews
    total.value = list.total
    agents.value = a
    error.value = ''
    loaded.value = true
```

with:

```vue
    crews.value = list.crews
    total.value = list.total
    agents.value = a.agents
    yoloDefault.value = a.yoloDefault
    error.value = ''
    loaded.value = true
```

Edit 4 (line 451 before this step). Replace:

```vue
            v-model="draft"
            :agents="agents"
            :dirty="isDirty(selectedKey!)"
            :saving="saving"
```

with:

```vue
            v-model="draft"
            :agents="agents"
            :yolo-default="yoloDefault"
            :dirty="isDirty(selectedKey!)"
            :saving="saving"
```


- [ ] **Step 9: Badges and Resume where sessions show.** In `web/app/pages/sessions/[id].vue`:

Edit 1 (line 59 before this step). Replace:

```vue
// shows); the terminal's own attention message arrives a moment earlier.
const stored = computed(() => live.sessions.value.find((s) => s.id === id.value))
watch(
  () => stored.value?.attention,
```

with:

```vue
// shows); the terminal's own attention message arrives a moment earlier.
const stored = computed(() => live.sessions.value.find((s) => s.id === id.value))
/** The session as the live store has it, else as read: its yolo badge and its agent session follow the stream. */
const current = computed(() => stored.value ?? session.value)
const ended = computed(() => !!current.value && (current.value.status === 'exited' || current.value.status === 'stopped'))
const copy = useCopy()
watch(
  () => stored.value?.attention,
```

Edit 2 (line 278 before this step). Replace:

```vue
              <AttentionBadge :attention="attention" />
              <SessionStatusBadge v-if="session && session.status !== 'running'" :status="session.status" :exit-code="session.exitCode" />
            </div>
            <span class="truncate font-mono text-[11.5px] text-muted">{{ meta }}</span>
          </div>
        </template>
        <template #right>
          <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" class="hidden md:inline-flex" />
          <ViewerAvatars :viewers="viewers" class="hidden md:flex" />
```

with:

```vue
              <AttentionBadge :attention="attention" />
              <SessionStatusBadge v-if="session && session.status !== 'running'" :status="session.status" :exit-code="session.exitCode" />
              <YoloBadge v-if="current?.yolo" />
            </div>
            <span class="flex min-w-0 items-center gap-2 font-mono text-[11.5px] text-muted">
              <span class="truncate">{{ meta }}</span>
              <button
                v-if="current?.agentSession"
                type="button"
                class="hidden max-w-40 flex-none truncate hover:text-default md:inline"
                :title="`The agent's own session (${current.agentSession.source}): ${current.agentSession.id}. Click to copy.`"
                data-agent-session
                @click="copy(current.agentSession.id, 'Agent session copied')"
              >
                {{ current.agentSession.id }}
              </button>
            </span>
          </div>
        </template>
        <template #right>
          <ResumeButton v-if="ended && current" :session="current" />
          <TransportBadge :kind="transport.kind" :state="transport.state" :rtt="transport.rtt" class="hidden md:inline-flex" />
          <ViewerAvatars :viewers="viewers" class="hidden md:flex" />
```


In `web/app/components/SessionTile.vue`:

Edit 1 (line 50 before this step). Replace:

```vue
        <span class="font-semibold truncate flex-1 text-[13px]">{{ props.session.name }}</span>
        <EventMarkBadge :session-id="props.session.id" />
        <span class="flex items-center gap-1.5 text-[11.5px]" :class="status.cls"><span v-if="status.dot" class="size-[7px] rounded-full" :class="status.dot" aria-hidden="true" />{{ status.label }}</span>
        <UTooltip text="Open in full (or double-click here)">
          <UButton icon="i-lucide-maximize-2" size="xs" color="neutral" variant="ghost" :aria-label="`Open ${props.session.name}`" data-tile-open @click.stop="emit('select')" />
```

with:

```vue
        <span class="font-semibold truncate flex-1 text-[13px]">{{ props.session.name }}</span>
        <EventMarkBadge :session-id="props.session.id" />
        <YoloBadge v-if="props.session.yolo" icon />
        <span class="flex items-center gap-1.5 text-[11.5px]" :class="status.cls"><span v-if="status.dot" class="size-[7px] rounded-full" :class="status.dot" aria-hidden="true" />{{ status.label }}</span>
        <ResumeButton v-if="isEnded(props.session.status)" :session="props.session" stay icon-only size="xs" />
        <UTooltip text="Open in full (or double-click here)">
          <UButton icon="i-lucide-maximize-2" size="xs" color="neutral" variant="ghost" :aria-label="`Open ${props.session.name}`" data-tile-open @click.stop="emit('select')" />
```


In `web/app/components/SessionSidebar.vue` (an exited row's Resume sits beside its link, not in it):

Edit 1 (line 89 before this step). Replace:

```vue
                <span class="truncate text-sm font-semibold">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <span v-if="needsDot" class="ml-auto size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
              </div>
```

with:

```vue
                <span class="truncate text-sm font-semibold">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <YoloBadge v-if="s.yolo" icon />
                <span v-if="needsDot" class="ml-auto size-2 rounded-full bg-warning flex-none" aria-hidden="true" />
              </div>
```

Edit 2 (line 114 before this step). Replace:

```vue
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
```

with:

```vue
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
                <YoloBadge v-if="s.yolo" icon />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ sessionMeta(s, now) }}</span>
```

Edit 3 (line 126 before this step). Replace:

```vue
        <div v-for="g in groups.exited" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <NuxtLink
            v-for="s in g.sessions"
            :key="s.id"
            :to="`/sessions/${s.id}`"
            class="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 opacity-75 transition-colors"
            :class="[active(s.id) ? 'bg-default border border-default shadow-xs opacity-100' : 'hover:bg-elevated/60', g.runId && runHeaders && 'ml-2']"
          >
            <SessionAvatar :agent-id="s.agentId" dashed />
            <div class="min-w-0 flex-1 flex flex-col">
              <div class="flex items-center gap-1.5 min-w-0">
                <span class="truncate text-sm font-medium">{{ s.name }}</span>
                <EventMarkBadge :session-id="s.id" />
              </div>
              <span class="truncate font-mono text-[11px] text-muted">{{ exitLabel(s) }}</span>
            </div>
          </NuxtLink>
        </div>
      </section>
```

with:

```vue
        <div v-for="g in groups.exited" :key="g.key" class="flex flex-col gap-0.5" :data-sidebar-group="g.runId ?? ''">
          <SidebarRunHeader v-if="g.runId && runHeaders" :run-id="g.runId" :label="g.label" />
          <!-- The resume button sits beside the row's link, not in it: a link holds no button. -->
          <div v-for="s in g.sessions" :key="s.id" class="flex items-center gap-1" :class="g.runId && runHeaders && 'ml-2'">
            <NuxtLink
              :to="`/sessions/${s.id}`"
              class="flex min-w-0 flex-1 items-center gap-2.5 rounded-md px-2.5 py-1.5 opacity-75 transition-colors"
              :class="active(s.id) ? 'bg-default border border-default shadow-xs opacity-100' : 'hover:bg-elevated/60'"
            >
              <SessionAvatar :agent-id="s.agentId" dashed />
              <div class="min-w-0 flex-1 flex flex-col">
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="truncate text-sm font-medium">{{ s.name }}</span>
                  <EventMarkBadge :session-id="s.id" />
                  <YoloBadge v-if="s.yolo" icon />
                </div>
                <span class="truncate font-mono text-[11px] text-muted">{{ exitLabel(s) }}</span>
              </div>
            </NuxtLink>
            <ResumeButton v-if="s.kind === 'server'" :session="s" icon-only size="xs" />
          </div>
        </div>
      </section>
```


In `web/app/pages/runs/[run].vue` (an ended member whose session has left the server; a stopped run's members are not offered, as the
server refuses them with `409 run_stopped`):

Edit 1 (line 250 before this step). Replace:

```vue
                    @click="startMember(t.name)"
                  />
                </div>
              </div>
```

with:

```vue
                    @click="startMember(t.name)"
                  />
                  <ResumeButton v-else-if="t.member?.status === 'ended' && !run?.stoppedAt" :run-id="runId" :member="t.name" stay size="xs" />
                </div>
              </div>
```


In `web/app/pages/wall.vue`:

Edit 1 (line 206 before this step). Replace:

```vue
            <UButton label="Open page" icon="i-lucide-square-terminal" color="neutral" variant="soft" :to="`/sessions/${focused.id}`" />
            <UButton v-if="isActive(focused)" label="Stop" icon="i-lucide-square" color="error" variant="soft" @click="stop(focused)" />
          </template>
          <FullscreenButton />
```

with:

```vue
            <UButton label="Open page" icon="i-lucide-square-terminal" color="neutral" variant="soft" :to="`/sessions/${focused.id}`" />
            <UButton v-if="isActive(focused)" label="Stop" icon="i-lucide-square" color="error" variant="soft" @click="stop(focused)" />
            <ResumeButton v-else-if="focused.kind === 'server'" :session="focused" />
          </template>
          <FullscreenButton />
```


- [ ] **Step 10: Type-check and test**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: no type error; every vitest passes.

- [ ] **Step 11: The headless check.** With Tasks 3 and 5 on the server, build and start the test server as in Task 6, Step 10 (yolo off by
default: no `CONDUCTOR_YOLO`), and write `$PW/yolo-resume-check.js`:

```js
// Round 4 Task 8: the Launch dialog's yolo switch and the badge it leads to, the notice for an agent without a recipe, the crew
// editor's select, and Resume / Relaunch of an ended session. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const token = 'dev-admin-token-change-me'
async function api(method, path, body) {
  const r = await fetch(base + path, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body && JSON.stringify(body) })
  const text = await r.text()
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`)
  return text ? JSON.parse(text) : {}
}
const uuid = '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
;(async () => {
  await api('POST', '/api/catalog', {
    id: 'bold',
    name: 'Bold',
    command: ['/bin/sh', '-c', 'echo ARGS:"$*"; exec /bin/bash -l', 'sh'],
    allowArgs: true,
    yolo: { args: ['--yolo-flag'] },
    session: { startArgs: ['--session-id', '{id}'], idFrom: 'hook', resumeArgs: ['--resume', '{id}'], idPattern: uuid },
  })
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript((t) => localStorage.setItem('conductor.adminToken', t), token)
    const page = await ctx.newPage()
    await page.goto(base + '/agents', { waitUntil: 'commit' })
    await page.getByRole('button', { name: 'Launch agent' }).first().click()
    const toggle = page.locator('[data-launch-yolo] button[role="switch"]')

    // An agent without a recipe: the notice, and no badge to come.
    await page.getByRole('radio', { name: 'Shell' }).click()
    assert.equal(await toggle.getAttribute('aria-checked'), 'false', "the server's default is off")
    await toggle.click()
    await page.locator('[data-yolo-missing]').waitFor()
    console.log('launch dialog: an agent without a recipe says so')

    // An agent with one: the argv shows it, and the session wears the badge.
    await page.getByRole('radio', { name: 'Bold' }).click()
    assert.equal(await toggle.getAttribute('aria-checked'), 'true', 'the switch stays where it was moved')
    assert.match(await page.locator('[data-yolo-argv]').textContent(), /--yolo-flag/)
    await page.getByRole('button', { name: 'Launch', exact: true }).click()
    await page.waitForURL(/\/sessions\//)
    const first = page.url().split('/sessions/')[1]
    await page.locator('[data-yolo-badge]').first().waitFor()
    const s1 = (await api('GET', `/api/sessions/${first}`)).session
    assert.equal(s1.yolo, true)
    assert.ok(s1.command.includes('--yolo-flag'), 'the recipe is in the argv')
    assert.ok(s1.agentSession?.id && !s1.agentSession.resumable, 'named at launch, nothing to resume yet')
    await page.locator('[data-agent-session]').waitFor()
    console.log('yolo badge and the agent session id in the header')

    // A turn reported with the id makes it resumable; ended, it resumes.
    await api('POST', `/api/sessions/${first}/attention`, { state: 'done', agentSession: s1.agentSession.id, turn: true })
    await api('DELETE', `/api/sessions/${first}`)
    const resume = page.locator('[data-resume][data-resume-kind="label"]')
    await page.waitForFunction(() => document.querySelector('[data-resume][data-resume-kind="label"]')?.textContent?.includes('Resume'))
    await resume.click()
    await page.waitForURL((u) => u.pathname.startsWith('/sessions/') && !u.pathname.endsWith(first))
    const s2 = (await api('GET', `/api/sessions/${page.url().split('/sessions/')[1]}`)).session
    assert.equal(s2.resumedFrom, first)
    assert.deepEqual(s2.command.slice(4, 6), ['--resume', s1.agentSession.id], 'resumed with the recipe and the id')
    assert.equal(s2.yolo, true, 'with the same yolo choice')
    console.log('resume: the same conversation, the same yolo')

    // No turn: Relaunch.
    const s3 = await api('POST', '/api/sessions', { agentId: 'bold' })
    await api('DELETE', `/api/sessions/${s3.id}`)
    await page.goto(`${base}/sessions/${s3.id}`, { waitUntil: 'commit' })
    await page.waitForFunction(() => document.querySelector('[data-resume][data-resume-kind="label"]')?.textContent?.includes('Relaunch'))
    console.log('relaunch: offered when there is nothing to resume')

    // The crew editor's select names the server's default.
    await page.goto(base + '/crews', { waitUntil: 'commit' })
    await page.getByRole('button', { name: 'New crew' }).click()
    await page.locator('[data-crew-yolo]').waitFor()
    assert.match(await page.locator('[data-crew-yolo]').textContent(), /server's default \(off\)/)
    console.log("crew editor: the select shows the server's default")
  } finally {
    await browser.close()
  }
  console.log('PASS yolo-resume-check')
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
```

Run: `make web-build && make build-go`, start the server as in Task 6, then `(cd $PW && node yolo-resume-check.js)`; stop the
server by its pid and remove its data directory.
Expected: `PASS yolo-resume-check`, after `resume: the same conversation, the same yolo` and `relaunch: offered when there is nothing to
resume`.

- [ ] **Step 12: Commit**

```bash
git add web/app/utils/yolo.ts web/app/utils/yolo.test.ts web/app/components/YoloBadge.vue web/app/components/ResumeButton.vue \
  web/app/composables/useSessions.ts web/app/utils/crews.ts web/app/utils/crews.test.ts web/app/utils/agentForm.ts web/app/utils/agentForm.test.ts \
  web/app/components/AddAgentSlideover.vue web/app/components/LaunchSessionModal.vue web/app/components/CrewEditor.vue \
  'web/app/pages/crews/[[id]].vue' 'web/app/pages/sessions/[id].vue' web/app/components/SessionTile.vue web/app/components/SessionSidebar.vue \
  'web/app/pages/runs/[run].vue' web/app/pages/wall.vue
git commit -m "web: a yolo switch, select and recipe, a badge where it applied, and Resume for an ended session"
```

**Done when:**

- `useSessions.ts` has the yolo, agent-session and resume types and the two resume calls; `toCrewInput` passes the crew's `yolo` through.
- `web/app/utils/yolo.ts` and the agent form's yolo fields exist, with their tests in `yolo.test.ts` and `agentForm.test.ts`.
- `YoloBadge` (a warning-coloured `UBadge` with `i-lucide-shield-off`, never the junction mark) and `ResumeButton` (Resume when `agentSession.resumable`, else Relaunch; the toast gives the server's `notice`) exist.
- The agent editor has its "Yolo recipe" section ("No yolo recipe" saved as `{}`), the Launch dialog its switch (the server's default, the override said, the recipe previewed, a warning for an agent without one), the crew editor its select (the server's default, on, off).
- The badge and Resume show in the session header (with the agent's session id, cut short, copied on click), on the tiles, in the sidebar's rows (Resume outside the row's link), on the crew view's ended placeholder (not for a stopped run) and in the wall's focus header.
- `npm --prefix web run typecheck && npm --prefix web test` pass, and `yolo-resume-check.js` prints `PASS yolo-resume-check`.
- Committed: "web: a yolo switch, select and recipe, a badge where it applied, and Resume for an ended session".

---

### Task 9: The Playwright suite for the example crews

**Order:** needs every earlier task. It adds files under `web/e2e/` and edits build files; it changes no Go and no workbench code. Its UI tests read hooks that Tasks 6 and 7 add (Step 1 checks them).

**Files:**
- Create: `web/e2e/stub-agent.sh`, `web/e2e/conductor.e2e.json`, `web/e2e/conductor.e2e-live.json`, `web/e2e/state.ts`, `web/e2e/server.ts`, `web/e2e/global-setup.ts`, `web/e2e/global-teardown.ts`, `web/e2e/fixtures.ts`, `web/e2e/examples.spec.ts`, `web/e2e/live.spec.ts`, `web/e2e/tsconfig.json`, `web/playwright.config.ts`
- Modify: `web/package.json` (`:11` after `"test": "vitest run",` two scripts; `:27` devDependencies, through `npm install`), `web/package-lock.json` (regenerated by `npm install`), `Makefile` (`:10` `.PHONY`; after `:66` `test-web: web-typecheck ## frontend checks` the new target), `.gitignore` (after `:7` `web/.output/`), `.github/workflows/ci.yml` (after `:50`, the end of the `go` job, a job `e2e`), `AGENTS.md` (Pinned versions, after `:76` `| @iconify-json/lucide (icon client bundle) | 1.2.137 |`; Checks block, after `:57` `make build-go                  # embeds whatever is in internal/web/dist`). Task 10 edits other rows of `AGENTS.md` (the Map): these two hunks are this task's alone.

**Interfaces:**
- Consumes: the built server (`bin/conductor`, or `CONDUCTOR_E2E_BIN`), which embeds whatever `internal/web/dist` holds (`make test-e2e` runs `web-build` first); `conductor serve --config <file> --listen 127.0.0.1:<port>` with `CONDUCTOR_DATA_DIR`, `CONDUCTOR_PUBLIC_URL`, `CONDUCTOR_ALLOWED_ROOTS`, `CONDUCTOR_DEFAULT_CWD`, `CONDUCTOR_ADMIN_TOKEN` (`internal/config.applyEnv`); a config-file agent whose id is a built-in's replaces it whole (`catalog.Load`: no adapter, no yolo, trust or session recipe); the session environment Conductor injects (`CONDUCTOR_BIN`, `CONDUCTOR_RUN`, `CONDUCTOR_MEMBER`, `CONDUCTOR_NOTIFY_URL`, `CONDUCTOR_NOTIFY_TOKEN`); `conductor notify --state working|done [--message M]` and `--event handoff --to T --message M`; the admin API (`GET /api/health`, `GET /api/crews/{id}`, `POST /api/crews`, `DELETE /api/crews/{id}`, `POST /api/crews/{id}/launch`, `GET /api/runs/{run}`, `POST /api/runs/{run}/stop`, `GET /api/sessions/{id}`); the run log entries of Task 4 (`typed <m>'s prompt`, `<a> is done: starting <b>, <c>`, `handoff delivered from <a> to <b>`); the admin token in localStorage `conductor.adminToken` (`useAdminToken`); the test hooks listed in Step 1.
- Produces: `make test-e2e`; `npm --prefix web run test:e2e` (`playwright test`) and `npm --prefix web run typecheck:e2e` (`tsc -p e2e --noEmit`); the CI job `e2e`; the environment the suite reads: `CONDUCTOR_E2E_BIN` (the server binary), `CONDUCTOR_E2E_KEEP=1` (keep the scratch root and its `server.log`), `CONDUCTOR_E2E_LIVE=1` with `CONDUCTOR_E2E_LIVE_REPO=<an already-trusted git repository>` (the live check), `STUB_MERGE_WAIT_S` (the stub's wait for a branch, 60 s), and `CONDUCTOR_E2E_STATE` (set by the global setup: the state file).
```ts
// web/e2e/state.ts
export const STATE_ENV = 'CONDUCTOR_E2E_STATE'
export interface E2EState { baseURL: string; token: string; port: number; pid: number; root: string; home: string; data: string; repo: string; log: string }
export function readState(file?: string): E2EState
export function writeState(file: string, s: E2EState): void
// web/e2e/server.ts
export const PORT_MIN = 18400, PORT_MAX = 18499
export function serverBinary(): string
export function pickPort(): Promise<number>
export function git(cwd: string, ...args: string[]): string
export function scratchRepo(dir: string): void
export function renderConfig(template: string, out: string): void
export function startServer(o: { config: string; home: string; data: string; allowedRoot: string; defaultCwd: string; log: string; passEnv?: string[] }): Promise<{ baseURL: string; token: string; port: number; pid: number }>
export function stopServer(pid: number): Promise<void>
// web/e2e/fixtures.ts
export class Api { constructor(server: { baseURL: string; token: string }); call<T>(method, path, body?): Promise<{ status: number; body: T }>; ok<T>(method, path, body?): Promise<T>; crew(id); launchCrew(id): Promise<Run>; run(id): Promise<Run>; stopRun(id); session(id): Promise<Session> }
export function logged(run: Run, text: string): boolean
export function member(run: Run, name: string): RunMember
export function sidebarLinks(page: Page): Promise<string[]>
export const test // @playwright/test's, with `api` (test) and `state` (worker) fixtures, baseURL from the state, and the admin token in localStorage
```

The plan-fixed values this task uses (Rules and fixed values): `@playwright/test` 1.44.1, whose `playwright-core` names Chromium revision 1117 (125.0.6422.26) in its `browsers.json`, the revision cached at `~/.cache/ms-playwright/chromium-1117`; `@types/node` 22.20.5; ports 18400–18499, the first free one from 18400 + (pid mod 50); the server answers `/api/health` within 20 s; the stub waits at most 60 s for a branch, polling every 0.5 s; each test at most 120 s (the live one 180 s).

- [ ] **Step 1: Check the test hooks the specs read.** The UI tests find what a person sees through these attributes and links. The first six exist at HEAD; the rest come from Tasks 6 and 7 of this plan. Run each `grep`; for one that finds nothing, add the attribute to the element named, and nothing else.

| Hook | Element | From | Check |
|---|---|---|---|
| `[data-crews-empty]` with the button **Load the examples** | the Crews page's `UEmpty` | HEAD | `grep -n 'data-crews-empty' web/app/pages/crews/[[id]].vue` |
| `[data-crew-item]` | a crew's link in the Crews list | HEAD (Task 7 keeps it on the link) | `grep -n 'data-crew-item' web/app/pages/crews/[[id]].vue` |
| `[data-launch]` | the crew editor's Launch button | HEAD | `grep -n 'data-launch' web/app/components/CrewEditor.vue` |
| `[data-run-grid]` and `[data-member="<name>"]` | the crew view's grid, and each tile (`SessionTile`'s root through `$attrs`, or the placeholder) | HEAD | `grep -n 'data-run-grid\|data-member' web/app/pages/runs/[run].vue` |
| `[data-session-tile]` | `SessionTile`'s frame; the terminal inside has xterm's `.xterm-screen` | HEAD; Task 6 makes a click on the terminal focus it in place | `grep -n 'data-session-tile' web/app/components/SessionTile.vue` |
| `[data-crew-feed]` | the crew activity card | HEAD | `grep -n 'data-crew-feed' web/app/components/CrewFeed.vue` |
| `[data-crew-runs] [data-run="<runId>"][data-state="<state>"]` | a run's row under its crew; `data-state` is the run's derived state (`running`, `needs_input`, `stopped`, `finished`) | Task 7 | `grep -n 'data-crew-runs\|data-run=\|data-state' web/app/pages/crews/[[id]].vue web/app/components/CrewRuns.vue` |
| `[data-run-member="<name>"][data-status="<status>"]` | each member's avatar in that row; `data-status` is `memberStatus` (`pending`, `starting`, `running`, `needs_input`, `ended`) | Task 7 | `grep -n 'data-run-member' web/app/components/CrewRuns.vue` |
| a link named **Open** to `/runs/<runId>` | in that row | Task 7 | by eye in the row's template |
| `[data-session-list] a[href="/runs/<runId>"]`, then the members' `a[href="/sessions/<id>"]` | the full sidebar's run header and its member rows, in that order | Task 7 | `grep -n '/runs/' web/app/components/SessionSidebar.vue web/app/components/SidebarRunHeader.vue` |

Run: each `grep` above.
Expected: every hook found. (Task 7 renders the runs list in `CrewRuns.vue` and the sidebar's run header in `SidebarRunHeader.vue`; a hook missing there is added on the element the table names, for example `:data-run="r.id" :data-state="state(r)"` on the run's `<li>`.)

- [ ] **Step 2: The stub agent.** Create `web/e2e/stub-agent.sh` and make it executable (the config runs it as `/bin/bash <path>`, so the bit is for a person running it by hand):

```bash
#!/usr/bin/env bash
# stub-agent.sh stands in for Claude Code and Codex in the e2e suite
# (web/e2e/conductor.e2e.json gives the claude and codex ids this script).
# It never runs what it reads. Each line typed into it (a role prompt, a
# handoff, a broadcast, a person's words) is echoed; in it, only the merges
# the example crews ask for are recognised, by fixed patterns, for branches
# of this run alone (crew/<run>/<member>, both ids checked), and run as git's
# argv. Then it commits a file of its own on its branch and reports done
# through conductor notify. It turns bracketed paste on, as the agents do, and
# takes the paste markers off what it reads.
set -euo pipefail

member=${CONDUCTOR_MEMBER:-agent}
run=${CONDUCTOR_RUN:-}
conductor=${CONDUCTOR_BIN:-conductor}
wait_s=${STUB_MERGE_WAIT_S:-60}

run_re='^[a-z0-9][a-z0-9-]{0,40}-[0-9a-f]{8}$'
member_re='^[a-z0-9][a-z0-9._-]{0,39}$'
target_re='(git merge --no-edit|wait until) crew/(\$CONDUCTOR_RUN|[a-z0-9][a-z0-9-]*)/([a-z0-9][a-z0-9._-]*)'
handoff_re='--event handoff --to ([a-z0-9][a-z0-9._-]{0,39})'
paste_start=$'\e[200~'
paste_end=$'\e[201~'
git_id=(-c user.name=conductor-e2e -c user.email=e2e@conductor.invalid -c commit.gpgsign=false)

[[ $member =~ $member_re ]] || member=agent
[[ $run =~ $run_re ]] || run=
handed_off=

# report sends a report to Conductor; outside a session it does nothing.
report() { "$conductor" notify "$@" >/dev/null 2>&1 || true; }

# holds reports whether branch $1 has stub-$2.txt, the file member $2 commits.
holds() { git cat-file -e "$1:stub-$2.txt" 2>/dev/null; }

# answer answers one line.
answer() {
	local line=$1 rest=$1 kind target_run target branch deadline top to
	local -a merged=() failed=()
	printf 'got: %s\n' "$line"
	report --state working
	while [[ $rest =~ $target_re ]]; do
		kind=${BASH_REMATCH[1]}
		target_run=${BASH_REMATCH[2]}
		target=${BASH_REMATCH[3]}
		rest=${rest#*"${BASH_REMATCH[0]}"}
		while [[ $target == *. ]]; do target=${target%.}; done
		[[ $target_run == '$CONDUCTOR_RUN' ]] && target_run=$run
		if [[ -z $run || $target_run != "$run" || ! $target =~ $member_re ]]; then
			printf 'stub: crew/%s/%s is not a branch of this run: left alone\n' "$target_run" "$target"
			continue
		fi
		branch=crew/$run/$target
		if [[ $kind == 'wait until' ]]; then
			deadline=$((SECONDS + wait_s))
			until holds "$branch" "$target"; do
				if ((SECONDS >= deadline)); then
					printf 'stub: %s did not hold stub-%s.txt within %ss\n' "$branch" "$target" "$wait_s"
					break
				fi
				sleep 0.5
			done
			continue
		fi
		if git "${git_id[@]}" merge -q --no-edit -- "$branch" >/dev/null 2>&1; then
			merged+=("$target")
		else
			git merge --abort >/dev/null 2>&1 || true
			failed+=("$target")
		fi
	done
	top=$(git rev-parse --show-toplevel 2>/dev/null) || top=.
	printf '%s\n' "${line:0:200}" >>"$top/stub-$member.txt"
	git -C "$top" add -- "stub-$member.txt" >/dev/null 2>&1 || true
	git -C "$top" "${git_id[@]}" commit -q -m "stub: $member answers" >/dev/null 2>&1 || true
	if [[ -z $handed_off && $line =~ $handoff_re ]]; then
		to=${BASH_REMATCH[1]}
		if [[ $to != "$member" ]]; then
			handed_off=1
			report --event handoff --to "$to" --message "stub $member: over to $to"
		fi
	fi
	printf 'stub: merged [%s] failed [%s]\n' "${merged[*]-}" "${failed[*]-}"
	report --state done --message "got: ${line:0:100}; merged: ${merged[*]-none}; failed: ${failed[*]-none}"
}

printf 'conductor e2e stub: %s in run %s\n' "$member" "${run:-none}"
printf '\e[?2004h> '
while IFS= read -r line; do
	line=${line//"$paste_start"/}
	line=${line//"$paste_end"/}
	line=${line%$'\r'}
	if [[ -n ${line//[[:space:]]/} ]]; then
		answer "$line"
	fi
	printf '> '
done
```

Run: `chmod +x web/e2e/stub-agent.sh && bash -n web/e2e/stub-agent.sh && echo ok`
Expected: `ok`.

Then drive it through a PTY in a scratch repository, the way a member's session types into it: four worktrees on `crew/<run>/<member>` branches, the text as a bracketed paste and the carriage return 250 ms later, a fake `CONDUCTOR_BIN` that logs what the stub reports. Not committed; everything goes under one scratch directory, removed at the end.

```bash
T=$(mktemp -d) && STUB=$PWD/web/e2e/stub-agent.sh && RUN=example-todo-app-0a1b2c3d && cd "$T"
git init -q repo && git -C repo symbolic-ref HEAD refs/heads/main && git -C repo -c user.name=a -c user.email=a@b commit -q --allow-empty -m init
for m in lead core cli tester; do git -C repo worktree add -q -b "crew/$RUN/$m" "../wt-$m" main; done
printf '#!/bin/sh\nprintf "%%s|" "$@" >> "%s/notify.log"; echo >> "%s/notify.log"\n' "$T" "$T" > fakebin && chmod +x fakebin
cat > drive.py <<'EOF'
import os, pty, sys, time, select
stub, cwd, member, run, line = sys.argv[1:6]
fake = os.path.abspath('fakebin')
pid, fd = pty.fork()
if pid == 0:
    os.chdir(cwd)
    os.execve('/bin/bash', ['/bin/bash', stub], dict(os.environ, CONDUCTOR_MEMBER=member, CONDUCTOR_RUN=run, CONDUCTOR_BIN=fake, STUB_MERGE_WAIT_S='6'))
out = b''
def pump(t):
    global out
    end = time.time() + t
    while time.time() < end:
        if select.select([fd], [], [], 0.1)[0]:
            try: out += os.read(fd, 4096)
            except OSError: return
pump(0.5); os.write(fd, b'\x1b[200~' + line.encode() + b'\x1b[201~'); pump(0.25); os.write(fd, b'\r')
pump(float(os.environ.get('WAIT', '3'))); os.write(fd, b'\x04'); pump(0.5)
print(out.decode(errors='replace').replace('\x1b', '^['))
EOF
python3 drive.py "$STUB" wt-lead lead $RUN 'You lead. Commit PLAN.md.' > /dev/null
python3 drive.py "$STUB" wt-cli cli $RUN 'git merge --no-edit crew/$CONDUCTOR_RUN/lead (run). Build.' > /dev/null
(WAIT=8 python3 drive.py "$STUB" wt-tester tester $RUN 'git merge --no-edit crew/$CONDUCTOR_RUN/lead, then git merge --no-edit crew/$CONDUCTOR_RUN/cli. wait until crew/$CONDUCTOR_RUN/core holds it, then git merge --no-edit crew/$CONDUCTOR_RUN/core. Hand with: notify --event handoff --to core --message "x". git merge --no-edit crew/other-run-12345678/lead' > tester.out &)
sleep 2; python3 drive.py "$STUB" wt-core core $RUN 'git merge --no-edit crew/$CONDUCTOR_RUN/lead. Build core.' > /dev/null; sleep 7
grep -a 'stub:' tester.out; cat notify.log
for m in lead cli core; do git -C repo cat-file -e "crew/$RUN/tester:stub-$m.txt" && echo "tester holds stub-$m.txt"; done
cd - > /dev/null && rm -rf "$T"
```

Expected (checked on this machine with git 2.25.1 and bash 5): the tester waits for core's commit, then merges all three, and leaves the other run's branch alone; it hands off once; every member reports working, then done with what it merged:

```text
stub: crew/other-run-12345678/lead is not a branch of this run: left alone
stub: merged [lead cli core] failed []
notify|--state|working|
notify|--state|done|--message|got: You lead. Commit PLAN.md.; merged: none; failed: none|
…
notify|--event|handoff|--to|core|--message|stub tester: over to core|
notify|--state|done|--message|got: git merge --no-edit crew/$CONDUCTOR_RUN/lead, then git merge --no-edit crew/$CONDUCTOR_RUN/cli. wait; merged: lead cli core; failed: none|
tester holds stub-lead.txt
tester holds stub-cli.txt
tester holds stub-core.txt
```

- [ ] **Step 3: Pin Playwright.** Install the two dev dependencies at exact versions, which rewrites `web/package.json`'s `devDependencies` and `web/package-lock.json`:

Run: `npm --prefix web install --no-audit --no-fund --save-dev --save-exact @playwright/test@1.44.1 @types/node@22.20.5`
Expected: `web/package.json` devDependencies gain `"@playwright/test": "1.44.1"` and `"@types/node": "22.20.5"` (exact, no caret: the Chromium revision follows the Playwright version).

Then in `web/package.json`, after `"test": "vitest run",` (`:11`) add the two scripts:
```json
    "test": "vitest run",
    "test:e2e": "playwright test",
    "typecheck:e2e": "tsc -p e2e --noEmit",
```

Run: `cd web && npx playwright --version && node -e "console.log(require('playwright-core/browsers.json').browsers[0])"; cd ..`
Expected: `Version 1.44.1` and `{ name: 'chromium', revision: '1117', installByDefault: true, browserVersion: '125.0.6422.26' }`. Vitest is unaffected: its config includes `app/**/*.test.ts` only, and `nuxt typecheck` does not see `web/e2e` (the Nuxt tsconfigs include `app/**`, not `e2e/**`).

- [ ] **Step 4: The test configs.** `web/e2e/conductor.e2e.json` gives `claude` and `codex` the stub; `@STUB@` is replaced by the stub's absolute path when the global setup renders the file into the scratch root (a config file has no variables). Everything that places the server (listen address, public URL, allowed root, default working directory, data directory, admin token) comes from the command line and the environment the global setup gives it.
```json
{
  "iceServers": [],
  "catalog": {
    "disableDefaults": false,
    "agents": [
      {
        "id": "claude",
        "name": "Claude Code (e2e stub)",
        "description": "web/e2e/stub-agent.sh in place of Claude Code: the example crews run without the real agent.",
        "command": ["/bin/bash", "@STUB@"],
        "allowArgs": false,
        "signal": { "kind": "none" }
      },
      {
        "id": "codex",
        "name": "Codex CLI (e2e stub)",
        "description": "web/e2e/stub-agent.sh in place of Codex: the example crews run without the real agent.",
        "command": ["/bin/bash", "@STUB@"],
        "allowArgs": false,
        "signal": { "kind": "none" }
      }
    ]
  }
}
```

`web/e2e/conductor.e2e-live.json`, for the live check's own server, keeps the built-in catalog:
```json
{
  "iceServers": []
}
```

- [ ] **Step 5: The harness.** `web/e2e/state.ts`:
```ts
import { readFileSync, writeFileSync } from 'node:fs'

/** Where the global setup writes the state of the server it started, for the specs and the teardown. */
export const STATE_ENV = 'CONDUCTOR_E2E_STATE'

/** The test server the global setup started, and its scratch directories. */
export interface E2EState {
  /** `http://127.0.0.1:<port>`, also the server's public URL. */
  baseURL: string
  /** The admin token, a fresh one for each run of the suite. */
  token: string
  port: number
  /** The server's process, killed by pid at teardown. */
  pid: number
  /** The scratch root: the server's HOME, data directory, log and the repository live under it; it is the allowed root. */
  root: string
  home: string
  data: string
  /** The scratch git repository (one commit): the server's default working directory, so the example crews work in it. */
  repo: string
  /** The server's log. */
  log: string
}

export function readState(file = process.env[STATE_ENV]): E2EState {
  if (!file) throw new Error(`${STATE_ENV} is not set: run the suite with playwright test, whose global setup starts the server`)
  return JSON.parse(readFileSync(file, 'utf8')) as E2EState
}

export function writeState(file: string, s: E2EState): void {
  writeFileSync(file, JSON.stringify(s, null, 2) + '\n', { mode: 0o600 })
}
```

`web/e2e/server.ts` (the server is started with a clean environment: a `CONDUCTOR_YOLO` or a token in the developer's shell never reaches it; git before 2.28 has no `init -b`, so the branch is set with `symbolic-ref`):
```ts
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, openSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, join, resolve } from 'node:path'
import { randomBytes } from 'node:crypto'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))

/** The checkout's root, where `make build-go` leaves bin/conductor. */
export const repoRoot = resolve(here, '..', '..')
/** The stub agent the test config gives the claude and codex ids. */
export const stubPath = join(here, 'stub-agent.sh')

/** The ports the suite may listen on: the first free one from 18400 + (pid mod 50), wrapping. */
export const PORT_MIN = 18400
export const PORT_MAX = 18499
/** How long the server has to answer /api/health. */
const HEALTH_WAIT_MS = 20_000

/** The server binary: CONDUCTOR_E2E_BIN, else bin/conductor of this checkout. */
export function serverBinary(): string {
  const bin = process.env.CONDUCTOR_E2E_BIN || join(repoRoot, 'bin', 'conductor')
  if (!existsSync(bin)) throw new Error(`${bin} is missing: build it first (make build-go), or run make test-e2e`)
  return bin
}

function portFree(port: number): Promise<boolean> {
  return new Promise((done) => {
    const s = createServer()
    s.once('error', () => done(false))
    s.listen(port, '127.0.0.1', () => s.close(() => done(true)))
  })
}

export async function pickPort(): Promise<number> {
  const span = PORT_MAX - PORT_MIN + 1
  const first = process.pid % 50
  for (let i = 0; i < span; i++) {
    const port = PORT_MIN + ((first + i) % span)
    if (await portFree(port)) return port
  }
  throw new Error(`no free port from ${PORT_MIN} to ${PORT_MAX}`)
}

/** git as argv, in cwd; its output. */
export function git(cwd: string, ...args: string[]): string {
  return execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

/** Makes dir a git repository on branch main with one commit, its identity in its own config (the server's HOME has none). */
export function scratchRepo(dir: string): void {
  git(dir, 'init', '-q')
  // git before 2.28 has no init -b.
  git(dir, 'symbolic-ref', 'HEAD', 'refs/heads/main')
  git(dir, 'config', 'user.name', 'conductor-e2e')
  git(dir, 'config', 'user.email', 'e2e@conductor.invalid')
  git(dir, 'config', 'commit.gpgsign', 'false')
  writeFileSync(join(dir, 'README.md'), '# e2e scratch repository\n')
  git(dir, 'add', 'README.md')
  git(dir, 'commit', '-q', '-m', 'init')
}

/** Writes the config template with every command element "@STUB@" made the stub's absolute path. */
export function renderConfig(template: string, out: string): void {
  const cfg = JSON.parse(readFileSync(template, 'utf8')) as { catalog?: { agents?: Array<{ command: string[] }> } }
  for (const a of cfg.catalog?.agents ?? []) a.command = a.command.map((arg) => (arg === '@STUB@' ? stubPath : arg))
  writeFileSync(out, JSON.stringify(cfg, null, 2) + '\n')
}

export interface Started {
  baseURL: string
  token: string
  port: number
  pid: number
}

/**
 * Starts `conductor serve` with config and a clean environment: PATH, LANG, the HOME given, and the CONDUCTOR_* values that place it
 * (data directory, public URL, allowed root, default working directory, admin token); of the caller's own variables only the ones
 * passEnv names, never a CONDUCTOR_* one, so a CONDUCTOR_YOLO or a token set in the shell cannot reach it. It returns once /api/health answers, and kills the server when it does not.
 */
export async function startServer(o: {
  config: string
  home: string
  data: string
  allowedRoot: string
  defaultCwd: string
  log: string
  /** Names of the caller's variables to pass on as well, when set (the live check's USER, SHELL, XDG_*). */
  passEnv?: string[]
}): Promise<Started> {
  const bin = serverBinary()
  const port = await pickPort()
  const baseURL = `http://127.0.0.1:${port}`
  const token = randomBytes(24).toString('hex')
  const out = openSync(o.log, 'a')
  const child = spawn(bin, ['serve', '--config', o.config, '--listen', `127.0.0.1:${port}`], {
    env: {
      PATH: process.env.PATH ?? '/usr/local/bin:/usr/bin:/bin',
      LANG: 'C.UTF-8',
      HOME: o.home,
      CONDUCTOR_DATA_DIR: o.data,
      CONDUCTOR_PUBLIC_URL: baseURL,
      CONDUCTOR_ALLOWED_ROOTS: o.allowedRoot,
      CONDUCTOR_DEFAULT_CWD: o.defaultCwd,
      CONDUCTOR_ADMIN_TOKEN: token,
      ...Object.fromEntries((o.passEnv ?? []).flatMap((k) => (process.env[k] === undefined || k.startsWith('CONDUCTOR_') ? [] : [[k, process.env[k]!]]))),
    },
    stdio: ['ignore', out, out],
  })
  const pid = child.pid
  if (pid === undefined) throw new Error(`${bin} did not start`)
  const deadline = Date.now() + HEALTH_WAIT_MS
  for (;;) {
    if (child.exitCode !== null) throw new Error(`the server exited with ${child.exitCode}:\n${tail(o.log)}`)
    try {
      const res = await fetch(`${baseURL}/api/health`)
      if (res.ok) return { baseURL, token, port, pid }
    } catch {
      /* not listening yet */
    }
    if (Date.now() > deadline) {
      child.kill('SIGKILL')
      throw new Error(`the server did not answer /api/health within ${HEALTH_WAIT_MS / 1000} s:\n${tail(o.log)}`)
    }
    await new Promise((r) => setTimeout(r, 200))
  }
}

/** Stops the server: SIGTERM, then SIGKILL after 5 s. By pid, never by name. */
export async function stopServer(pid: number): Promise<void> {
  const alive = () => {
    try {
      process.kill(pid, 0)
      return true
    } catch {
      return false
    }
  }
  if (!alive()) return
  process.kill(pid, 'SIGTERM')
  const deadline = Date.now() + 5_000
  while (alive() && Date.now() < deadline) await new Promise((r) => setTimeout(r, 100))
  if (alive()) process.kill(pid, 'SIGKILL')
}

function tail(file: string): string {
  try {
    return readFileSync(file, 'utf8').split('\n').slice(-30).join('\n')
  } catch {
    return '(no log)'
  }
}
```

`web/e2e/global-setup.ts`:
```ts
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { renderConfig, scratchRepo, startServer, stopServer } from './server'
import { STATE_ENV, writeState, type E2EState } from './state'

const here = dirname(fileURLToPath(import.meta.url))

/**
 * Starts the server the specs drive: bin/conductor with web/e2e/conductor.e2e.json (claude and codex are the stub agent), a HOME,
 * a data directory and a scratch git repository of its own under one temporary root, which is its only allowed root. The state goes
 * to a file the specs and the teardown read (CONDUCTOR_E2E_STATE). On any failure what was started is stopped and the root removed.
 */
export default async function globalSetup(): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), 'conductor-e2e-'))
  let pid: number | undefined
  try {
    const home = join(root, 'home')
    const data = join(root, 'data')
    const repo = join(root, 'repo')
    mkdirSync(home)
    mkdirSync(repo)
    scratchRepo(repo)
    const config = join(root, 'conductor.json')
    renderConfig(join(here, 'conductor.e2e.json'), config)
    const log = join(root, 'server.log')
    const started = await startServer({ config, home, data, allowedRoot: root, defaultCwd: repo, log })
    pid = started.pid
    const state: E2EState = { ...started, root, home, data, repo, log }
    const file = join(root, 'state.json')
    writeState(file, state)
    process.env[STATE_ENV] = file
    console.log(`conductor e2e: server ${state.baseURL} (pid ${pid}), scratch ${root}`)
  } catch (e) {
    if (pid !== undefined) await stopServer(pid)
    rmSync(root, { recursive: true, force: true })
    throw e
  }
}
```

`web/e2e/global-teardown.ts`:
```ts
import { rmSync } from 'node:fs'
import { stopServer } from './server'
import { readState } from './state'

/** Stops the server by its pid and removes the scratch root; CONDUCTOR_E2E_KEEP=1 keeps the root (its server.log) to read. */
export default async function globalTeardown(): Promise<void> {
  const state = readState()
  await stopServer(state.pid)
  if (process.env.CONDUCTOR_E2E_KEEP === '1') {
    console.log(`conductor e2e: kept ${state.root}`)
    return
  }
  rmSync(state.root, { recursive: true, force: true })
}
```

`web/e2e/fixtures.ts` (no TypeScript parameter properties anywhere in `web/e2e`: under Node 23 and later Playwright 1.44 loads the specs through Node's own type stripping, which refuses them with `TypeScript parameter property is not supported in strip-only mode`, found here on Node 25):
```ts
import { test as base, expect, type Page } from '@playwright/test'
import { git } from './server'
import { readState, type E2EState } from './state'

export { expect, git }

export interface RunMember {
  name: string
  agentId: string
  status: 'pending' | 'starting' | 'running' | 'ended'
  sessionId?: string
  branch?: string
  startedAt?: string
  needsInput?: boolean
  error?: string
}

export interface Run {
  id: string
  crewId: string
  name: string
  state: string
  stoppedAt?: string
  members: RunMember[]
  log: Array<{ at: string; type: string; message?: string }>
}

export interface Session {
  id: string
  name: string
  status: string
  crew?: { runId: string; crewId: string; member: string }
  attention?: { state: string; message?: string }
}

/**
 * The admin API of a test server: fetch with the token in the Authorization header. (No parameter properties: Node 23+ loads the
 * specs with its own type stripping, which refuses them.)
 */
export class Api {
  readonly server: { baseURL: string; token: string }

  constructor(server: { baseURL: string; token: string }) {
    this.server = server
  }

  async call<T>(method: string, path: string, body?: unknown): Promise<{ status: number; body: T }> {
    const res = await fetch(this.server.baseURL + path, {
      method,
      headers: { Authorization: `Bearer ${this.server.token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    return { status: res.status, body: (text ? JSON.parse(text) : undefined) as T }
  }

  /** call, failing on a status of 300 or more with what the server said. */
  async ok<T>(method: string, path: string, body?: unknown): Promise<T> {
    const r = await this.call<T>(method, path, body)
    if (r.status >= 300) throw new Error(`${method} ${path}: ${r.status} ${JSON.stringify(r.body)}`)
    return r.body
  }

  crew(id: string) {
    return this.ok<{ crew: { id: string; cwd: string; members: Array<{ name: string }> } }>('GET', `/api/crews/${encodeURIComponent(id)}`).then((r) => r.crew)
  }

  launchCrew(id: string) {
    return this.ok<{ run: Run }>('POST', `/api/crews/${encodeURIComponent(id)}/launch`).then((r) => r.run)
  }

  run(id: string) {
    return this.ok<{ run: Run }>('GET', `/api/runs/${encodeURIComponent(id)}`).then((r) => r.run)
  }

  stopRun(id: string) {
    return this.call('POST', `/api/runs/${encodeURIComponent(id)}/stop`)
  }

  session(id: string) {
    return this.ok<{ session: Session }>('GET', `/api/sessions/${encodeURIComponent(id)}`).then((r) => r.session)
  }
}

/** Whether a line of the run's log contains text. */
export function logged(run: Run, text: string): boolean {
  return run.log.some((e) => (e.message ?? '').includes(text))
}

/** The member of a run with the given name. */
export function member(run: Run, name: string): RunMember {
  const m = run.members.find((x) => x.name === name)
  if (!m) throw new Error(`run ${run.id} has no member ${name}`)
  return m
}

/** The hrefs of the session and run links of the full sidebar's list, in the order they show. */
export function sidebarLinks(page: Page): Promise<string[]> {
  return page
    .locator('[data-session-list] a[href^="/sessions/"], [data-session-list] a[href^="/runs/"]')
    .evaluateAll((els) => els.map((el) => el.getAttribute('href') ?? ''))
}

export const test = base.extend<{ api: Api }, { state: E2EState }>({
  state: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      await use(readState())
    },
    { scope: 'worker' },
  ],
  api: async ({ state }, use) => {
    await use(new Api(state))
  },
  baseURL: async ({ state }, use) => {
    await use(state.baseURL)
  },
  // Every page starts with the admin token where the workbench keeps it (useAdminToken: localStorage conductor.adminToken).
  page: async ({ page, state }, use) => {
    await page.addInitScript((token) => {
      try {
        localStorage.setItem('conductor.adminToken', token)
      } catch {
        /* storage refused: the page asks for the token, and the test fails on what it cannot find */
      }
    }, state.token)
    await use(page)
  },
})
```

`web/e2e/tsconfig.json` (the e2e files only; Playwright transpiles them itself, this is for the type check):
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ES2022", "DOM"],
    "types": ["node"],
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["./**/*.ts", "../playwright.config.ts"]
}
```

`web/playwright.config.ts`:
```ts
import { defineConfig, devices } from '@playwright/test'

// The end-to-end suite (web/e2e): a built server (bin/conductor, which embeds the generated workbench) with stub agents, driven
// through Chromium 1117, the revision @playwright/test 1.44.1 installs. make test-e2e builds both first. One worker: the specs
// share one server and its runs.
export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]] : 'list',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  outputDir: 'test-results',
  use: {
    ...devices['Desktop Chrome'],
    viewport: { width: 1440, height: 900 },
    launchOptions: { args: ['--no-sandbox'] },
    trace: 'retain-on-failure',
  },
})
```

And `.gitignore`, after `web/.output/` (`:7`):
```text
web/.output/
web/playwright-report/
web/test-results/
```

Run: `npm --prefix web run typecheck:e2e`
Expected: exit 0, no output from `tsc`.

- [ ] **Step 6: The specs.** `web/e2e/examples.spec.ts`: the todo app through the workbench, then the other three examples through the API. The merges are proven through git, not through the agents' messages: core's message changes when it answers the tester's handoff, but a branch holds a member's file only through a merge that went through.
```ts
import { expect, git, logged, member, sidebarLinks, test, type Run } from './fixtures'

// The example crews, run by the stub agent (web/e2e/stub-agent.sh) in place of Claude Code and Codex: the todo app through the
// workbench, every step of it checked where a person would see it and through the API, then the other three examples launched.
// The tests share the server and the run, so they run in order.
test.describe.configure({ mode: 'serial' })

const MEMBERS = ['lead', 'core', 'cli', 'tester']
let runId = ''
const runs: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of runs) await api.stopRun(id)
})

test('the examples load on the empty Crews page, at the scratch repository', async ({ page, api, state }) => {
  await page.goto('/crews')
  await page.locator('[data-crews-empty]').getByRole('button', { name: 'Load the examples' }).click()
  await expect(page.locator('[data-crew-item]', { hasText: 'Example: todo app' })).toBeVisible()
  for (const id of ['example-todo-app', 'example-test-fixer', 'example-docs-writer', 'example-dependency-upgrade']) {
    // The examples work in the server's default working directory: the scratch repository.
    expect((await api.crew(id)).cwd).toBe(state.repo)
  }
})

test('the todo app launches and opens its crew view, a tile per member', async ({ page }) => {
  await page.goto('/crews/example-todo-app')
  await page.locator('[data-launch]').click()
  await page.waitForURL(/\/runs\/example-todo-app-[0-9a-f]{8}$/)
  runId = decodeURIComponent(new URL(page.url()).pathname.split('/').pop() ?? '')
  runs.push(runId)
  const grid = page.locator('[data-run-grid]')
  for (const name of MEMBERS) await expect(grid.locator(`[data-member="${name}"]`)).toBeVisible()
  // Every member gets a live tile, the tester last: it starts once cli is done.
  await expect(grid.locator('[data-member="tester"] [data-session-tile]')).toBeVisible({ timeout: 60_000 })
})

test('each prompt runs without a person, the members start in order, the handoff is delivered and the merges succeed', async ({ page, api, state }) => {
  let run: Run = await api.run(runId)
  await expect
    .poll(async () => {
      run = await api.run(runId)
      return ['typed lead', 'lead is done: starting core, cli', 'cli is done: starting tester', 'handoff delivered from tester to core'].filter((t) => !logged(run, t))
    }, { timeout: 90_000, message: 'run log' })
    .toEqual([])
  for (const name of MEMBERS) expect(logged(run, `typed ${name}'s prompt`), `${name}'s prompt`).toBe(true)
  const at = (name: string) => Date.parse(member(run, name).startedAt ?? '')
  expect(at('lead')).toBeLessThan(at('core'))
  expect(at('lead')).toBeLessThan(at('cli'))
  expect(at('cli')).toBeLessThan(at('tester'))
  // Each stub ran its prompt (no one pressed Enter): it merged the branches its prompt names and committed on its own. A branch
  // holds a member's file only through a merge that went through.
  const holds = (branch: string, name: string) => {
    try {
      git(state.repo, 'cat-file', '-e', `crew/${runId}/${branch}:stub-${name}.txt`)
      return true
    } catch {
      return false
    }
  }
  for (const [branch, names] of [['lead', ['lead']], ['core', ['lead', 'core']], ['cli', ['lead', 'cli']], ['tester', ['lead', 'cli', 'core', 'tester']]] as const) {
    for (const name of names) await expect.poll(() => holds(branch, name), { timeout: 60_000, message: `crew/${runId}/${branch} holds stub-${name}.txt` }).toBe(true)
  }
  // core answered the tester's handoff, typed into it as a line of its own.
  await expect
    .poll(() => git(state.repo, 'show', `crew/${runId}/core:stub-core.txt`), { timeout: 30_000 })
    .toContain('Handoff from tester: stub tester: over to core')
  // The feed shows the same steps.
  await page.goto(`/runs/${encodeURIComponent(runId)}`)
  const feed = page.locator('[data-crew-feed]')
  for (const text of ["typed lead's prompt", 'lead is done: starting core, cli', 'cli is done: starting tester', 'handoff delivered from tester to core']) {
    await expect(feed).toContainText(text)
  }
})

test('the Crews page lists the run under its crew, with each member and its status', async ({ page }) => {
  await page.goto('/crews/example-todo-app')
  const row = page.locator(`[data-crew-runs] [data-run="${runId}"]`)
  await expect(row).toBeVisible()
  await expect(row).toHaveAttribute('data-state', 'running')
  for (const name of MEMBERS) await expect(row.locator(`[data-run-member="${name}"]`)).toHaveAttribute('data-status', 'running')
  await expect(row.getByRole('link', { name: /open/i })).toHaveAttribute('href', `/runs/${encodeURIComponent(runId)}`)
})

test('the full sidebar groups the run: its header, then its four members', async ({ page, api }) => {
  await page.goto('/crews')
  const run = await api.run(runId)
  const sessions = new Set(run.members.map((m) => `/sessions/${m.sessionId}`))
  await expect.poll(async () => (await sidebarLinks(page)).filter((h) => sessions.has(h)).length).toBe(4)
  const links = await sidebarLinks(page)
  const header = links.indexOf(`/runs/${encodeURIComponent(runId)}`)
  expect(header, 'the run header').toBeGreaterThanOrEqual(0)
  expect(new Set(links.slice(header + 1, header + 5))).toEqual(sessions)
})

test('typing into a tile on the wall reaches the session', async ({ page, api }) => {
  const lead = member(await api.run(runId), 'lead').sessionId ?? ''
  await page.goto('/wall')
  const tile = page.locator('[data-session-tile]').filter({ has: page.getByText('lead', { exact: true }) })
  await expect(tile).toHaveCount(1)
  await tile.locator('.xterm-screen').click()
  await page.keyboard.type('ping-from-wall')
  await page.keyboard.press('Enter')
  // The click focused the terminal in place: no navigation, and the stub got the line.
  await expect(page).toHaveURL(/\/wall$/)
  await expect.poll(async () => (await api.session(lead)).attention?.message ?? '', { timeout: 30_000 }).toContain('got: ping-from-wall')
})

for (const [crew, first, second] of [
  ['example-test-fixer', 'triage', 'fixer'],
  ['example-docs-writer', 'reader', 'writer'],
  ['example-dependency-upgrade', 'scout', 'upgrader'],
] as const) {
  test(`${crew} launches: ${first}, then ${second} on ${first}'s branch`, async ({ api, state }) => {
    const id = (await api.launchCrew(crew)).id
    runs.push(id)
    await expect
      .poll(async () => {
        const run = await api.run(id)
        return [`typed ${first}'s prompt`, `${first} is done: starting ${second}`, `typed ${second}'s prompt`].filter((t) => !logged(run, t))
      }, { timeout: 60_000 })
      .toEqual([])
    // The second member merged the first's branch: its own branch holds the first's file.
    await expect
      .poll(() => {
        try {
          git(state.repo, 'cat-file', '-e', `crew/${id}/${second}:stub-${first}.txt`)
          return true
        } catch {
          return false
        }
      }, { timeout: 30_000 })
      .toBe(true)
  })
}
```

`web/e2e/live.spec.ts`: skipped unless asked for; when asked, a server of its own with the real HOME and the built-in catalog, one one-member crew per agent with a worktree, and the agent's answer as the proof:
```ts
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test as base, expect } from '@playwright/test'
import { Api, member, type Run } from './fixtures'
import { git, renderConfig, startServer, stopServer, type Started } from './server'

// The live check: the real Claude Code and Codex, launched as one-member crews by a server of their own, take a typed prompt and
// answer it without a person pressing Enter. Skipped unless CONDUCTOR_E2E_LIVE=1, CONDUCTOR_E2E_LIVE_REPO names a git repository
// both agents already trust (a worktree of a trusted repository needs no trust question), and the agent is on the PATH. It runs with
// the real HOME, where the agents keep their logins; it writes nothing there itself (the agents update their own files as they
// always do). The worktrees and branches it makes in the repository are removed afterwards.

const here = dirname(fileURLToPath(import.meta.url))
const repo = process.env.CONDUCTOR_E2E_LIVE_REPO ?? ''
const live = process.env.CONDUCTOR_E2E_LIVE === '1' && repo !== ''
const PROMPT = 'Reply with the single word READY and nothing else'

function onPath(program: string): boolean {
  try {
    execFileSync('sh', ['-c', 'command -v "$1"', 'sh', program], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}

const test = base.extend<object, { server: Started & { root: string } }>({
  server: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const root = mkdtempSync(join(tmpdir(), 'conductor-e2e-live-'))
      const config = join(root, 'conductor.json')
      renderConfig(join(here, 'conductor.e2e-live.json'), config)
      mkdirSync(join(root, 'data'))
      const started = await startServer({
        config,
        home: homedir(),
        data: join(root, 'data'),
        allowedRoot: repo,
        defaultCwd: repo,
        log: join(root, 'server.log'),
        passEnv: ['USER', 'LOGNAME', 'SHELL', 'TMPDIR', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'CODEX_HOME'],
      })
      try {
        await use({ ...started, root })
      } finally {
        await stopServer(started.pid)
        if (process.env.CONDUCTOR_E2E_KEEP !== '1') rmSync(root, { recursive: true, force: true })
      }
    },
    { scope: 'worker' },
  ],
})

test.skip(!live, 'set CONDUCTOR_E2E_LIVE=1 and CONDUCTOR_E2E_LIVE_REPO to an already-trusted git repository')

for (const agent of ['claude', 'codex']) {
  test.describe(agent, () => {
    test.skip(!onPath(agent), `${agent} is not on the PATH`)

    test('answers a typed prompt without a person pressing Enter', async ({ server }) => {
      test.setTimeout(180_000)
      const api = new Api(server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
        name: `Live ${agent}`,
        goal: '',
        cwd: repo,
        where: 'server',
        isolation: 'worktree',
        openAfterLaunch: false,
        members: [{ name: 'solo', agentId: agent, prompt: PROMPT, start: { when: 'immediately' } }],
      })
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        // Claude Code's Stop hook reports done with its answer; Codex's notify reports its turn as needs_input with it.
        await expect
          .poll(async () => {
            const id = member(await api.run(run.id), 'solo').sessionId
            return id ? ((await api.session(id)).attention?.message ?? '') : ''
          }, { timeout: 150_000, intervals: [1_000] })
          .toContain('READY')
        const r = await api.run(run.id)
        expect(r.log.some((e) => (e.message ?? '').includes("typed solo's prompt"))).toBe(true)
      } finally {
        await api.stopRun(run.id)
        const solo = member(await api.run(run.id), 'solo')
        if (solo.branch) {
          try {
            git(repo, 'worktree', 'remove', '--force', join(repo, '.conductor', 'worktrees', run.id, 'solo'))
            git(repo, 'branch', '-D', solo.branch)
          } catch {
            /* left for a person to remove: the run's id names them */
          }
        }
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })
  })
}
```

Run: `cd web && npm run typecheck:e2e && npx playwright test --list; cd ..`
Expected: `Total: 11 tests in 2 files` (nine in `examples.spec.ts`, two in `live.spec.ts`).

- [ ] **Step 7: `make test-e2e`, the pinned versions and the CI job.** In `Makefile`, `.PHONY` (`:10`) gains `test-e2e` after `test-web`:
```make
.PHONY: help deps check-tools build build-go web-install web-build web-typecheck web-dev run dev test test-web test-e2e lint fmt generate docker clean
```

and after `test-web: web-typecheck ## frontend checks` (`:66`) a blank line and:
```make
test-e2e: web-build build-go ## Playwright suite (web/e2e): the built server with stub agents, in Chromium 1117
	cd web && npm run typecheck:e2e && npm run test:e2e
```

(The recipe line starts with a tab. `web-build` comes before `build-go`, which embeds whatever `internal/web/dist` holds; run it without `-j`.)

`AGENTS.md`, Pinned versions, after `| @iconify-json/lucide (icon client bundle) | 1.2.137 |` (`:76`):
```text
| @playwright/test (e2e; Chromium revision 1117, 125.0.6422.26) | 1.44.1 |
| @types/node (e2e type check) | 22.20.5 |
```

and in the Checks block, after `make build-go                  # embeds whatever is in internal/web/dist` (`:57`):
```text
make test-e2e                  # Playwright (web/e2e): builds both, then a server with stub agents in Chromium 1117
```

`.github/workflows/ci.yml`, after the `go` job's last step (`:47-50`, the `conductor-linux-amd64` upload), a blank line and the job:
```yaml
  e2e:
    runs-on: ubuntu-latest
    needs: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: npm
          cache-dependency-path: web/package-lock.json
      - uses: actions/download-artifact@v4
        with:
          name: web-dist
          path: internal/web/dist
      - run: make build-go
      - run: npm ci
        working-directory: web
      - run: npx playwright install --with-deps chromium
        working-directory: web
      - run: npm run typecheck:e2e
        working-directory: web
      - run: npm run test:e2e
        working-directory: web
      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: playwright-report
          path: |
            web/playwright-report
            web/test-results
          if-no-files-found: ignore
```

Run: `make -n test-e2e | tail -1 && python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok')"`
Expected: `cd web && npm run typecheck:e2e && npm run test:e2e` and `yaml ok`.

- [ ] **Step 8: Run the suite.**

Run: `make test-e2e`
Expected: 9 passed, 2 skipped (`live.spec.ts`); the global setup prints `conductor e2e: server http://127.0.0.1:184xx (pid N), scratch /tmp/conductor-e2e-…`; afterwards no `/tmp/conductor-e2e-*` directory is left and no `bin/conductor serve` process (`ps -eo pid,args | grep '[b]in/conductor serve'` prints nothing). On a failure, `CONDUCTOR_E2E_KEEP=1 npm --prefix web run test:e2e` keeps the scratch root with its `server.log`; `web/test-results` holds the trace.

The harness has been run against HEAD 2c9c220's binary (built with HEAD's workbench): the first three examples tests and the three other-example launches pass there (6 tests, 36 s: the examples load, the todo app runs lead → core + cli → tester with the handoff delivered and every merge on the tester's branch); the Crews page, sidebar and wall tests need Tasks 6 and 7 and fail at HEAD on their hooks, as they should.

- [ ] **Step 9: The live check (by a person, optional).** With `claude` and `codex` logged in and a repository both trust (`git init` one with a commit, run `claude` and `codex` in it once and accept their trust questions, or use one already trusted):

Run: `cd web && CONDUCTOR_E2E_LIVE=1 CONDUCTOR_E2E_LIVE_REPO=/path/to/trusted/repo npx playwright test e2e/live.spec.ts; cd ..`
Expected: 2 passed, each in well under 180 s: the session's attention message contains `READY` and the run log has `typed solo's prompt`; no one pressed Enter. The worktrees and `crew/<run>/solo` branches it made are removed. Without the variables both tests are skipped.

- [ ] **Step 10: Commit**
```bash
git add web/e2e/stub-agent.sh web/e2e/conductor.e2e.json web/e2e/conductor.e2e-live.json web/e2e/state.ts web/e2e/server.ts \
  web/e2e/global-setup.ts web/e2e/global-teardown.ts web/e2e/fixtures.ts web/e2e/examples.spec.ts web/e2e/live.spec.ts \
  web/e2e/tsconfig.json web/playwright.config.ts web/package.json web/package-lock.json Makefile .gitignore \
  .github/workflows/ci.yml AGENTS.md
git commit -m "e2e: Playwright runs the example crews against a built server with a stub agent; make test-e2e and a CI job"
```

**Done when:**

- Every test hook of Step 1 is found; a missing one was added on the element named, and nothing else.
- `web/e2e/stub-agent.sh` passes `bash -n` and behaves as Step 2's PTY drive expects (it waits for core's commit, merges only this run's branches, hands off once, reports working then done).
- `@playwright/test` 1.44.1 and `@types/node` 22.20.5 are pinned exactly in `web/package.json` and `web/package-lock.json`, with the `test:e2e` and `typecheck:e2e` scripts.
- `npm --prefix web run typecheck:e2e` exits 0 and `npx playwright test --list` shows 11 tests in 2 files.
- `make test-e2e` exists, `AGENTS.md` has the pinned version and the Checks line, and the CI job `e2e` parses (`yaml ok`).
- `make test-e2e` gives 9 passed and 2 skipped, and leaves no `/tmp/conductor-e2e-*` directory and no `bin/conductor serve` process.
- Committed: "e2e: Playwright runs the example crews against a built server with a stub agent; make test-e2e and a CI job".

---

### Task 10: Docs

**Order:** last: it documents everything the round built. Every edit below is quoted text replaced by text written out in full, plus one Go test and three string constants.

**Files:**
- Modify: `internal/catalog/defaults.go:29` (codex `Site`), `:120` (goose `Site`), `:143` (dsh `Site`); `internal/catalog/catalog_test.go:737-752` (`TestDefaultsHaveSites`, replaced whole). Task 3 leaves every `Site` alone, dsh's included; this task owns the three values and their test.
- Modify: `README.md` — The workbench (`:52-64`), The wall (`:93-105`), Sidebar and keyboard shortcuts (`:128-130`), Crews (`:418-428`, `:448-463`, `:464-470`, `:479-484`, `:504-505`, and two new paragraphs after `:470`), two new sections between Crews and Shell completion (after `:512`), Shell completion (`:516-517`), Configuration (`:554`), Agent catalog (`:622-629`, `:654-666`), Security model (`:691-692`), Development (`:701-707`).
- Modify: `docs/protocol.md` — Attention (`:150-151`, `:172-175`, after `:192`, `:194-196`, `:214-217`, `:219-227`), HTTP API rows (`:404`, `:405`, `:412`, `:418`, `:419`, `:421`, `:422` and a new row after it, `:424`, `:431`, a new row after `:433`, `:438`), the catalog paragraph (`:449-463`), the crew limits (`:494-496`), Crew runs (`:531-543`, `:549-553`, `:562-575`, `:582-598`, the run-log table `:620-641`, the limits `:651-658`, and a new paragraph at the end).
- Modify: `docs/architecture.md` (`:23-26`, `:69-82`, the Packages table `:91`, `:94`, `:97`, after `:101`, the Security model after `:124`, Persistence `:129-136`), `docs/features.md` (the adapter matrix `:113-129`, Open verification (round 2) `:138-152`, Open verification (round 3) `:303-305`, Open verification (round 4) `:553-555` and a new Deferred list after it), `AGENTS.md` (Map rows `:12`, `:17`, `:20`).

Line numbers are at `2c9c220`, except `docs/features.md`, whose Round 4 section is the uncommitted spec the plan starts from: its anchors are of the working tree as the plan begins (`:379-555`). The quoted text is the anchor; where an earlier task of this plan touched a nearby line, the quote still holds, because:
- **Task 1** writes `docs/protocol.md`'s `hello` row (`:36`), the `resize` row (`:37`) and the Resize policy paragraph (`:63-67`).
- **Task 2** writes the client → owner `submit` row in the control-message table (after `:39`) and the relay sentence at `:88` (view-role `submit` dropped like INPUT and `resize`).
- **Task 4** writes the `run` event into the `GET /api/events` row (`:440`) and the SSE sentence at `:232-236`.
- **Task 9** writes `AGENTS.md`'s pinned-versions table and its Checks block.

This task edits none of those lines.

**Interfaces:**
- Consumes (names only, to describe them; nothing is called): `session.SubmitPause` (250 ms), `session.ConfirmWait` (3 s), `session.MaxSubmitText` (32756), the `submit` control message (Task 2); `catalog.Agent.Yolo`, `.TrustPrompt`, `.Session` and their limits, `config.Yolo`/`CONDUCTOR_YOLO`/`serve --yolo`, `GET /api/catalog`'s `yoloDefault`, `POST /api/sessions`' `yolo`, `Info.Yolo`, Codex's per-launch trust override (Task 3); run `state`/`needsInput`/`yolo`, member `needsInput`, the trust hold, the 3 s re-Enter, the run-log lines, broadcast's `no_enter`, the run event (Task 4); `Info.AgentSession`/`ResumedFrom`, member `agentSession`, the attention route's `agentSession`/`turn`, the two resume routes and their errors (Task 5); interactive tiles and the `(0,0)` hello (Tasks 1 and 6); the Crews page runs list, the sidebar's run groups, broadcast to everyone (Task 7); the Yolo and Resume controls (Task 8); `make test-e2e` (Task 9).
- Produces: the three built-in sites `https://learn.chatgpt.com/docs/codex/cli` (codex), `https://goose-docs.ai` (goose), `https://github.com/deepseek-ai/deepseek-harness` (dsh), pinned by `TestDefaultsHaveSites`.

Assumptions this task's text makes of earlier tasks (check each against the merged code; where one differs, change the text, not the code):
- Task 4 makes `Member.checkTypedPrompt` hold a prompt to `session.MaxSubmitText` (32756 bytes) instead of `proto.MaxInput - 1`; the README and `docs/protocol.md` give 32756.
- Task 5's mappers fill `agentSession` from Claude Code (`session_id`), Codex's notify (`thread-id`), Copilot (`sessionId`), Cursor (`conversation_id`) and Antigravity (`conversationId`), the five the built-in recipes with `idFrom: "hook"` name.

- [ ] **Step 1: Write the failing site test**

Replace the whole `TestDefaultsHaveSites` in `internal/catalog/catalog_test.go` (`:737-752`, from `func TestDefaultsHaveSites(t *testing.T) {` to its closing brace) with:

```go
// Every built-in but Shell names its website, held to the site rule. The
// three a check on 2026-10-02 found moved are pinned where they live now:
// developers.openai.com/codex/cli redirects to learn.chatgpt.com, Goose's
// docs left block.github.io, and github.com/deepseek-ai/dsh is a 404.
func TestDefaultsHaveSites(t *testing.T) {
	moved := map[string]string{
		"codex": "https://learn.chatgpt.com/docs/codex/cli",
		"goose": "https://goose-docs.ai",
		"dsh":   "https://github.com/deepseek-ai/deepseek-harness",
	}
	for _, a := range defaults() {
		if a.ID == "shell" {
			if a.Site != "" {
				t.Fatalf("shell has a site: %q", a.Site)
			}
			continue
		}
		if a.Site == "" {
			t.Errorf("%s: no site", a.ID)
		}
		if want, ok := moved[a.ID]; ok && a.Site != want {
			t.Errorf("%s: site %q, want %q", a.ID, a.Site, want)
		}
		if err := validate(a); err != nil {
			t.Errorf("%s: %v", a.ID, err)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/catalog -run TestDefaultsHaveSites`
Expected: FAIL, three lines: `codex: site "https://developers.openai.com/codex/cli", want "https://learn.chatgpt.com/docs/codex/cli"`, `goose: site "https://block.github.io/goose/", want "https://goose-docs.ai"`, `dsh: site "https://github.com/deepseek-ai/dsh", want "https://github.com/deepseek-ai/deepseek-harness"`.

- [ ] **Step 3: Move the three sites**

In `internal/catalog/defaults.go`:
- `:29` `			Site:        "https://developers.openai.com/codex/cli",` becomes `			Site:        "https://learn.chatgpt.com/docs/codex/cli",`
- `:120` `			Site:        "https://block.github.io/goose/",` becomes `			Site:        "https://goose-docs.ai",`
- `:143` `			Site:        "https://github.com/deepseek-ai/dsh",` becomes `			Site:        "https://github.com/deepseek-ai/deepseek-harness",`

Run: `go test -race ./internal/catalog`
Expected: PASS.

```bash
git add internal/catalog/defaults.go internal/catalog/catalog_test.go
git commit -m "catalog: the codex, goose and dsh sites where they live now, pinned by their test"
```

- [ ] **Step 4: `README.md`**

**The workbench** (`:59-64`). Replace

```
connects. The session page shows the terminal, a reply bar whenever the agent
is waiting (type an answer, or press the numbered buttons a Claude Code
permission prompt offers), and an inspector with **People** (who is attached,
their role and link, who is typing), **Files** (the file browser) and
**Activity** (joins, answers, signals and link changes). The header names the
agent, where it runs, the working directory and the git branch.
```

with

```
connects. The session page shows the terminal, a reply bar whenever the agent
is waiting (type an answer, or press the numbered buttons a Claude Code
permission prompt offers), and an inspector with **People** (who is attached,
their role and link, who is typing), **Files** (the file browser) and
**Activity** (joins, answers, signals and link changes). The header names the
agent, where it runs, the working directory and the git branch, shows a
**yolo** badge for a session launched with its agent's yolo recipe (see
[Yolo](#yolo)) and the agent's own session id when Conductor knows it (click
to copy); an ended session offers **Resume** or **Relaunch** (see
[Resume and relaunch](#resume-and-relaunch)). A reply typed in a reply bar
goes in as Conductor types a crew prompt: the text, then Enter on its own a
quarter of a second later, so that an agent that reads a fast burst as a paste
still runs it.
```

**The wall** (`:93-105`). Replace the whole section body

```
`/wall` is a grid of live tiles, one per active session, sized so that every
session fits on screen without scrolling; tiles shrink as sessions are added.
Each tile shows the session's whole screen scaled down. The chips in the
header filter tiles (**All**, **Needs you**, **Running**). A queue on the left
lists every session waiting for input with its prompt: answer from there
(**J**/**K** select, **Enter** types a reply) without opening the session,
and see who answered what under **Answered**. Click a tile and it expands in
place to a full-size, typeable terminal (`/wall?focus=<id>`, so the view is
linkable); **Esc**, the back arrow or the browser's Back button return to the
grid, and **Open page** goes to the full session page. The fullscreen button
turns a spare monitor into a status wall.
```

with

```
`/wall` is a grid of live tiles, one per active session, sized so that every
session fits on screen without scrolling; tiles shrink as sessions are added.
Each tile is the session's terminal at the tile's size, filling it: the last
viewer that attaches or resizes sets a session's size, so opening the wall
sizes each session to its tile, opening a session's page sizes it to that
page, and coming back to the grid sizes it to its tile again. Click into a
tile and type: the keys go to that session, the plain-key shortcuts pause
while the tile has focus and the **Alt** chords still work. The chips in the
header filter tiles (**All**, **Needs you**, **Running**). A queue on the left
lists every session waiting for input with its prompt: answer from there
(**J**/**K** select, **Enter** types a reply) without opening the session,
and see who answered what under **Answered**. The expand button in a tile's
header, a double-click on the header, or **Enter** on a tile whose frame has
keyboard focus expands it in place to a full-size terminal (`/wall?focus=<id>`,
so the view is linkable); **Esc**, the back arrow or the browser's Back button
return to the grid, and **Open page** goes to the full session page. The
fullscreen button turns a spare monitor into a status wall.
```

**Sidebar and keyboard shortcuts** (`:128-130`). Replace

```
chip), and every session as its agent's initials with the amber dot when it
needs you, the members of a running crew together under its name, which links
to the crew view. Click one to open it. The panel button at the bottom of the
```

with

```
chip), and every session as its agent's initials with the amber dot when it
needs you, the members of a running crew together under its name, which links
to the crew view. The full sidebar groups them the same way: inside each
section (**Needs you**, **Running**, **Exited**) the sessions of no crew come
first, then one group per crew run under a header naming the run, which links
to its crew view. A session launched with yolo carries the **yolo** badge
there, and an ended one a **Resume** (or **Relaunch**) button. Click one to
open it. The panel button at the bottom of the
```

**Crews: role prompts** (`:418-428`). Replace

```
A member starts `immediately` at launch, `after <member>` once that member,
with its own prompt typed, first reports it is done (idle), or `manual`, when
you press **Start now** on its tile. A member's role prompt is typed into its
terminal as one line (line breaks and tabs become spaces, as in a handoff or a
broadcast; the crew keeps the prompt as you wrote it), with Enter, once the
agent is ready for it: it reports that it waits
for input or is done, or, at least two seconds after the start, its output has
been quiet for a second. After 60 seconds the prompt is typed anyway and the
run's log says so. Every member's process sees `CONDUCTOR_CREW` (the crew's id),
`CONDUCTOR_RUN` (the run's id), `CONDUCTOR_MEMBER` (its name) and `GOAL`, next
to the usual `CONDUCTOR_SESSION_ID` and notify variables.
```

with

```
A member starts `immediately` at launch, `after <member>` once that member,
with its own prompt typed, first reports it is done (idle), or `manual`, when
you press **Start now** on its tile. A member's role prompt is typed into its
terminal as one line (line breaks and tabs become spaces, as in a handoff or a
broadcast; the crew keeps the prompt as you wrote it) once the agent is ready
for it: it reports that it waits for input or is done, or, at least two
seconds after the start, its output has been quiet for a second. An agent that
only animates a spinner in its window title counts as quiet, and one between
two screens of its start (Claude Code turns bracketed paste off while it
loads) is waited for. Conductor types the prompt as a terminal pastes text:
the text, as a bracketed paste when the agent asks for one, then Enter on its
own 250 ms later, so that Codex (which reads a fast burst of keys with Enter
in it as a paste) and Claude Code (which collapses a long one) both run it;
handoffs, broadcasts and the reply bars go in the same way. Claude Code reports
taking a prompt: when it has not within 3 seconds, Enter is pressed once more
and the run's log says so. A question that comes up in the 250 ms before the
Enter keeps the Enter back: the text waits in the agent's input, nothing is
typed twice, and the run's log tells you to press Enter once you have answered.
After 60 seconds the prompt is typed anyway and the run's log says so, except
while a trust question shows (see below). Every member's process sees
`CONDUCTOR_CREW` (the crew's id), `CONDUCTOR_RUN` (the run's id),
`CONDUCTOR_MEMBER` (its name) and `GOAL`, next to the usual
`CONDUCTOR_SESSION_ID` and notify variables.

**Trust the repository once.** Claude Code and Codex ask whether to trust a
folder the first time they start in it, yolo or not, and a worktree of a
repository they trust asks nothing. Before a crew's first run in a repository,
including the fresh one the todo-app example wants, open `claude` and `codex`
there once and accept. A member that meets the question anyway is held, never
typed into: its tile and the sidebar show that it needs you, with the
question's words, the run's log says so once, and its prompt is typed after
you answer it in its terminal with Enter (an arrow key that moves the
selection does not count as an answer). With yolo on, Codex is trusted for that
launch alone, through `-c projects={…}` naming the repository, and nothing is
written to `~/.codex/config.toml`; Claude Code has no such option, so trust it
once yourself.
```

**Crews: the crew view and broadcast** (`:448-463`). Replace

```
**The crew view.** `/runs/<id>` shows a live tile for every member, with its
branch and a diff count (`+12 −3`), a feed of the members' events and the run's
own log, and how many members need input. The diff counts the lines of tracked
files the member's worktree adds and removes against the commit it began from,
committed or not; untracked files are not counted, and the numbers refresh at
most every 10 seconds. A member that has not started yet shows a placeholder,
and a pending one offers **Start now**. The header has **Add agent** (a member
joins the run), **Share crew** and **Stop all**; stopping ends every session
and leaves the worktrees.

- **Broadcast.** Tick the tiles of the members you want and type one line into
  all of them at once. A member that waits on a prompt is skipped, so the line
  cannot answer it by accident, as is one that is not running; the toast names
  who got the line and who was skipped, and why. Each line is recorded as an
  input in the member's activity under your display name, or the server's
  user when you have not set one.
```

with

```
**The crew view.** `/runs/<id>` shows a live tile for every member, with its
branch and a diff count (`+12 −3`), a feed of the members' events and the run's
own log, and how many members need input. A tile is the member's terminal at
the tile's size, as on the wall: click into it and type; its expand button, or
a double-click on its header, opens the session's page. The diff counts the
lines of tracked files the member's worktree adds and removes against the
commit it began from, committed or not; untracked files are not counted, and
the numbers are read again when the run changes, at most every 10 seconds. The
page follows the run through the event stream and polls nothing. A member that
has not started yet shows a placeholder, a pending one offers **Start now**,
and an ended one **Resume** (see [Resume and relaunch](#resume-and-relaunch)).
The header has **Add agent** (a member joins the run), **Share crew** and
**Stop all**; stopping ends every session and leaves the worktrees.

- **Broadcast.** Every member with a session is ticked, and one that starts
  later is ticked when its tile appears; untick the ones to leave out (the
  ticks stay when the page reads the run again). Type one line and it goes into
  all of them at once. The button counts the members the line will reach and
  says apart how many it will skip because they wait on a prompt: such a member
  is skipped, so the line cannot answer it by accident, as is one that is not
  running, and one where a question comes up in the 250 ms before its Enter
  keeps the line in its input without Enter. The toast names who got the line
  and who was skipped, and why. Each line is recorded as an input in the
  member's activity under your display name, or the server's user when you
  have not set one.
```

**Crews: Share crew** (`:464-466`). Replace

```
- **Share crew** creates a run link with the **View** or **Control** role. It
  grants that role on the session of every member of the run, members added
  later included, and on no other session; the join page lists the members.
```

with

```
- **Share crew** creates a run link with the **View** or **Control** role. It
  grants that role on the session of every member of the run, members added
  later included, and on no other session; the join page lists the members.
  With a **Control** link their tiles take keys as the crew view's do; with a
  **View** link each tile shows the member's whole screen scaled to fit, and
  never resizes the session.
```

**Crews: the runs on the Crews page.** After the Share crew bullet (after `:470`, `  up`; nothing shows it again.`), insert a new paragraph, with a blank line before it:

```
**Runs on the Crews page.** Each crew in the list shows its runs, newest
first: the run's name and age, its state (**Running**; **Needs input** with
how many members wait; **Stopped** after a stop; **Finished** once every member
has ended and none is pending; a member that reports it is done keeps its run
running, for a done agent is idle, not gone), a member's error beside it, the
members as their agents' avatars with a dot for each one's status (pending,
starting, running, needs input, ended), **Open** for the crew view and **Stop**
while it runs; five at first, **Show more** for the rest. The crew's badge
reads **Running** while one of its runs is running or needs input
(**Draft changes** wins while you edit). The page follows the runs through the
event stream, as the crew view and the sidebar do. A crew's **Yolo** setting
(the server's default, on or off; see [Yolo](#yolo)) is fixed on the run when
it is launched, so every member, one started or added later included, follows
it.
```

**Crews: handoffs** (`:479-481`). Replace

```
Conductor types `Handoff from core: /v1/users is ready` into the member named
by `--to`, on one line, as soon as that member is running and not waiting on a
prompt. Up to 10 handoffs wait for a member; past that the oldest is dropped.
```

with

```
Conductor types `Handoff from core: /v1/users is ready` into the member named
by `--to`, on one line and as a prompt is typed (the text, then Enter on its
own), as soon as that member is running and not waiting on a prompt; a
question that comes up before the Enter keeps the line in its input without
Enter, noted in the run's log. Up to 10 handoffs wait for a member; past that
the oldest is dropped.
```

**Crews: limits** (`:504-505`). Replace

```
- Name 60 characters, goal 2000, role prompt 4000; with the goal in it a prompt
  is at most 32 KiB, the most a session takes in one write.
```

with

```
- Name 60 characters, goal 2000, role prompt 4000; with the goal in it a prompt
  is at most 32756 bytes, what one write carries with the paste markers around
  it.
```

**Two new sections** after the Crews section's last line (`:512`, `  (the worktrees stay). Saved crews survive.`) and before `## Shell completion`, with a blank line around each:

````
## Yolo

With yolo on (`"yolo": true` in the config, `CONDUCTOR_YOLO` set to `1` or
`true`, or `conductor serve --yolo`), every agent the server launches skips its
permission prompts, and the server says so in a warning at startup.
`CONDUCTOR_YOLO` set to `0` or `false` turns the config file's `yolo` off. A
launch overrides the server: the Launch dialog has a **Yolo** switch, and
`POST /api/sessions` takes `yolo: true` or `false`; so does a crew (the
editor's **Yolo** select: the server's default, on or off). Each built-in
carries a yolo recipe, arguments put after its command and your extra
arguments, and variables set in its environment through the same filtered
environment as its own `env` (a recipe cannot set `CONDUCTOR_*`):

| Agent | Recipe | What it turns off |
|---|---|---|
| Claude Code | `--dangerously-skip-permissions`; the warning Claude Code shows before its first such launch is skipped through the hooks settings file | every permission prompt; protected paths such as `.git` and `.claude` become writable; deny rules still apply |
| Codex CLI | `--dangerously-bypass-approvals-and-sandbox`, and the repository trusted for this launch | every approval **and the sandbox**: the agent reaches the network and the whole filesystem as the server's user |
| Antigravity | `--dangerously-skip-permissions` | every tool permission request |
| Copilot CLI | `--yolo`, `COPILOT_ALLOW_ALL=true` | every tool, path and URL prompt, and its folder-trust question |
| Cursor CLI | `--yolo --trust` | command and MCP approvals, and its workspace-trust question |
| OpenCode | `--auto` | every permission that is not explicitly denied |
| oh-my-pi | `--yolo` | approvals, unless a deny policy matches (already its default) |
| aider | `--yes-always` | every confirmation, shell commands included |
| Goose | `GOOSE_MODE=auto` | tool approvals (already its default) |
| Amp | `--dangerously-allow-all` | every tool approval |
| DeepSeek Harness | `DSH_PERMISSION_MODE=danger-full-access` | its sandbox and every approval |

pi has no permission system to skip and Shell has nothing to ask, so they have
no recipe. The recipes of Claude Code, Codex and Copilot were checked against
the real CLIs; the others come from their documentation (the adapter matrix in
[docs/features.md](docs/features.md) says which). A session launched with its
agent's recipe shows a **yolo** badge in its header, its tile and the sidebar:
the badge means Conductor applied the recipe, not that no question can appear
(a trust question still can; see Crews). An agent without a recipe is launched
as it would be without yolo, with a note in its activity (and, for a crew
member, in the run's log) and no badge. Edit a recipe on the Agents page
(**Yolo recipe**) or in the catalog (`"yolo": {"args": [...], "env": {...}}`):
a saved agent that replaces a built-in and leaves `yolo` out keeps the
built-in's, and `"yolo": {}` gives it none. Sessions started with
`conductor host` take no yolo in this version.

## Resume and relaunch

An ended session (exited or stopped) offers **Resume** in its header, on its
tile on the crew view (the wall's grid shows only running sessions, so on the
wall it is in the expanded view's header), on its row in the sidebar's
**Exited** section, and on an ended member's tile on the crew view. Resume
starts a new session with the same agent, name, working directory, arguments
and yolo choice and, for a crew member, the same run, branch and worktree,
launched with the agent's resume arguments for its own session id:

| Agent | Its session id | Resumed with |
|---|---|---|
| Claude Code | chosen by Conductor at launch: `--session-id <uuid>` | `--resume <id>` |
| Codex CLI | reported by its notify payload (`thread-id`) | `codex resume <id>` |
| Copilot CLI | chosen by Conductor at launch: `--session-id <uuid>` | `--session-id <id>` |
| Antigravity | reported by its hooks (`conversationId`), once they are installed | `--conversation <id>` |
| Cursor CLI | reported by its hooks (`conversation_id`), once they are installed | `--resume <id>` |
| pi | chosen by Conductor at launch: `--session-id <uuid>` | `--session-id <id>` |
| Goose | chosen by Conductor at launch: `goose session --name cdr-<uuid>` | `goose session --resume --name <id>` |

The agent's conversation comes back; its permission mode and model come from
the new launch, which applies the yolo recipe again when the session had it.
An agent without a recipe (OpenCode, oh-my-pi, aider, Amp, DeepSeek Harness,
Shell), or one that has had no turn yet (Claude Code keeps nothing to resume
until it has been prompted), offers **Relaunch** instead: a fresh session, and
the toast says it started anew; a crew member that is relaunched gets its role
prompt again, while a resumed one gets none, since its conversation has it. The
original arguments are passed again, so a prompt given as an argument
(`claude "fix the tests"`) is sent again as well. One running session holds a
conversation: resuming it a second time while that one runs is refused. Resume
is there while the ended session is listed (`exitedRetention`, 10 minutes by
default) and, for a crew member, while the server keeps the run; a member of a
stopped run, and a hosted session, cannot be resumed in this version.
````

**Shell completion** (`:516-517`). Replace

```
`conductor completion zsh` or `conductor completion bash` prints a completion
script: subcommands, flags and their values, and for `conductor up` the crew
```

with

```
`conductor completion zsh` or `conductor completion bash` prints a completion
script: subcommands, flags (`conductor serve`'s `--examples` and `--yolo`
among them) and their values, and for `conductor up` the crew
```

**Configuration** (`:554`). After the row

```
| — | `CONDUCTOR_EXAMPLES` | off | `1` or `true` seeds the example crews once at startup, as `conductor serve --examples` does; not a config-file key |
```

add

```
| `yolo` | `CONDUCTOR_YOLO` (`1`/`true` on, `0`/`false` off) | `false` | launch every agent with its yolo recipe, skipping its permission prompts, unless a launch or a crew says otherwise; `conductor serve --yolo` turns it on (see [Yolo](#yolo)) |
```

**Agent catalog: inheritance** (`:622-626`). Replace

```
configured catalog at startup: an agent with the ID of a built-in or configured
one replaces it, and deleting that entry brings the original back. Such an
entry inherits what it leaves out: the original's `adapter`, `signal` and
`site`, and every `env` value it holds as `***`, which the Agents page stores
```

with

```
configured catalog at startup: an agent with the ID of a built-in or configured
one replaces it, and deleting that entry brings the original back. Such an
entry inherits what it leaves out: the original's `adapter`, `signal`, `site`,
`yolo` recipe, `trustPrompt` and `session` recipe (an explicit `"yolo": {}` or
`"session": {}` says it has none), and every `env` value it holds as `***`,
which the Agents page stores
```

**Agent catalog: limits** (`:662-666`). Replace

```
200 bytes, and a signal `pattern` (a regular expression) at most 200 bytes that
does not match an empty line. An `adapter`, if an agent names one, matches
`[a-z0-9-]{1,32}` and must be one Conductor has, in the config file (or the
catalog file) as on the Agents page: the server refuses to start with an
unknown one.
```

with

```
200 bytes, and a signal `pattern` (a regular expression) at most 200 bytes that
does not match an empty line. An `adapter`, if an agent names one, matches
`[a-z0-9-]{1,32}` and must be one Conductor has, in the config file (or the
catalog file) as on the Agents page: the server refuses to start with an
unknown one. A `yolo` recipe has at most 16 `args` of 1 to 4096 bytes without
NUL and at most 16 `env` variables, each named like an `envPassthrough` name
but never `CONDUCTOR_*`, with a value of at most 4096 bytes; its values are
shown as they are, not masked: they are switches, not secrets. A `trustPrompt`
(the words of the agent's workspace-trust question, which holds a crew prompt)
is held to the rules of a signal pattern. A `session` recipe has at most 16
`startArgs` and 16 `resumeArgs` of 1 to 4096 bytes, `{id}` a whole argument and
once in each list that has any, `newId` `uuid` or `name`, `idFrom` `hook` or
nothing, `idPolicy` `latest` or `lowest`, startArgs or `idFrom` to say how the
id is known, and an `idPattern` anchored with `^` and `$`, at most 200 bytes,
that matches neither an empty id nor one that begins with a dash.
```

**Security model** (`:691-692`). Replace

```
- Server sessions run with an allowlisted environment and a working directory
  under `allowedRoots`. Hosted sessions run as you, with your environment.
```

with

```
- Server sessions run with an allowlisted environment and a working directory
  under `allowedRoots`. Hosted sessions run as you, with your environment.
- Yolo launches agents without their permission prompts, as the user running
  `conductor serve`, and Codex's recipe turns its sandbox off too: turn it on
  only on a server whose allowed roots you would let an agent change unasked.
  Recipes are catalog data, held to the catalog's limits, passed as arguments
  and through the filtered environment. An agent session id that Resume passes
  to an agent must match its agent's `idPattern` and begin with a letter or a
  digit, so it can never read as a flag, and goes in as one argument.
```

**Development** (`:701-707`). Replace

```
make run        # Go API on :8080 with --dev (CORS and origins for localhost:3000)
make web-dev    # Nuxt dev server on :3000 proxying /api and /ws to :8080
make test       # go test -race ./...
make lint       # gofmt + go vet
npm --prefix web run typecheck && npm --prefix web test
```

with

```
make run        # Go API on :8080 with --dev (CORS and origins for localhost:3000)
make web-dev    # Nuxt dev server on :3000 proxying /api and /ws to :8080
make test       # go test -race ./...
make lint       # gofmt + go vet
npm --prefix web run typecheck && npm --prefix web test
make test-e2e   # builds bin/conductor, then the Playwright suite (web/e2e) against a stub agent
```

and after the fence's closing line add, with a blank line before it:

```
The end-to-end suite starts its own server with its own home, data directory
and port, and a catalog whose `claude` and `codex` are a stub script; its
live test, which drives the real `claude` and `codex` with a tiny prompt,
runs only with `CONDUCTOR_E2E_LIVE=1` and both on the `PATH`.
```

(Task 9 adds `make test-e2e` to the Makefile and leaves the README alone; if Task 9's own text already added these lines, keep one copy.)

- [ ] **Step 5: `docs/protocol.md`**

**Attention: sources** (`:150-151`). Replace

```
Sources: `api` (the agent's own token), `admin`, `bell`, `osc`, `pattern` (the
screen-pattern detector, below), `input` (cleared by a controller typing).
```

with

```
Sources: `api` (the agent's own token), `admin`, `bell`, `osc`, `pattern` (the
screen-pattern detector, below), `trust` (the agent's workspace-trust
question, below), `input` (cleared by a controller typing).
```

**Attention: what clears it** (`:172-175`). Replace

```
does not. Bursts are limited to one change per 500 ms. Successful input from a
`control` client clears the `needs_input` state that was showing when the input
began. One raised while the input was being written (a process that answers at
once, an echo that rings the bell) is left for the next input.
```

with

```
does not. Bursts are limited to one change per 500 ms. Successful input from a
`control` client clears the `needs_input` state that was showing when the input
began. One raised while the input was being written (a process that answers at
once, an echo that rings the bell) is left for the next input. An input that is
only a terminal's own automatic reports, at most 256 bytes of them, is written
and clears nothing, nor stamps its viewer as typing: a cursor position report
(`ESC[<row>;<col>R`, with or without `?`), a status report (`ESC[0n`…`ESC[3n`),
device attributes (`ESC[?…c`, `ESC[>…c`), a mode report (`ESC[?<mode>;<n>$y`),
a window report (`ESC[<n>;…t`), a colour query's reply (`ESC]4;…`, `ESC]10;`
to `ESC]12;` with `rgb:…`, ended by BEL or ST), a DCS reply such as XTVERSION
(`ESC P >|… ESC \`), or focus in and out (`ESC[I`, `ESC[O`): a browser's xterm
answers Codex's cursor position query after every turn, and that answer is not
a person answering Codex. A trust question (`source:"trust"`) is cleared only
by an input with a carriage return in it, so an arrow key that moves its
selection leaves it showing. When a session's process exits or is stopped, its
attention state is cleared in the step that makes it `exited` or `stopped`, and
viewers get an `attention` message with the empty state: an ended session never
shows `needs_input`, and its activity log keeps the entries. A hosted session's
state is cleared when its host reports it ended; a host that disconnects leaves
it as it was, since the host may come back.
```

**Attention: the trust watcher.** After the screen-pattern paragraph (after `:192`, `host reports the change to the server like any other attention change.`), insert, with a blank line before it:

```
An agent's catalog entry can carry a `trustPrompt`, the words of its
workspace-trust question (RE2, at most 200 bytes, not matching an empty text).
A server session of that agent keeps the end of its screen's text, each escape
sequence and control character read as one space and every run of white space
as one, its last 1024 bytes, so that words a TUI draws with cursor moves
between them still read as a sentence. After 500 ms without output, output
that only sets the window title (OSC 0 or 2) not counting, the text is matched
against the pattern; a match marks the session `needs_input` with
`source:"trust"`, `kind:"prompt"` and the matched words as `message`, unless
it is `needs_input` already. Watching ends with the session's first
submission (below). Once a controller answers the question with Enter, only the
question drawn again raises it again. The built-ins carry the questions of
Claude Code 2.1.287 (`Is this a project you created or one you trust?`) and
Codex 0.159.0 (`Trust this folder?`). It exists for crew runs: a member's
prompt is never typed while it shows (see Crew runs).
```

**Attention: explicit updates** (`:194-196`). Replace

```
Explicit updates: `POST /api/sessions/{id}/attention` with
`{state: "needs_input"|"working"|"done"|"clear", message?, kind?, options?}` and
`Authorization: Bearer <agent token>` (or the admin token). Every session's
```

with

```
Explicit updates: `POST /api/sessions/{id}/attention` with
`{state: "needs_input"|"working"|"done"|"clear", message?, kind?, options?, agentSession?, turn?}` and
`Authorization: Bearer <agent token>` (or the admin token). Every session's
```

and after that paragraph's last line (`:212`, `` `attention` activity entry: a state that shows has its entry (see Events). ``) insert, with a blank line before it:

```
`agentSession` is the agent's own session id as its hook payload names it, at
most 128 bytes (`400 invalid_request` past that), and `turn` says the payload
reports a turn: a prompt taken or a turn finished. They change no attention:
for a server session whose agent's `session` recipe takes ids from its hooks
(`idFrom: "hook"`, see HTTP API), an id that begins with a letter or a digit,
holds only letters, digits and `._:-`, and matches the recipe's `idPattern`
becomes the session's `agentSession.id` when it has none, and afterwards as the
recipe's `idPolicy` says: `latest` takes each new id, `lowest` keeps the
smallest (Codex's hidden title thread reports with a later id than the
conversation's). `turn` makes it `resumable`. Any other id is dropped without
an error, and a hosted session ignores both fields. `conductor notify` fills
them from Claude Code's `session_id` (with `turn` on `Stop` and
`UserPromptSubmit`), Codex's notify `thread-id` (every
`agent-turn-complete`; the turn of the hidden thread that names a Codex
conversation, whose first input begins `Generate a concise, single-line task
title`, is not reported at all), Copilot's `sessionId`, Cursor's
`conversation_id` and Antigravity's `conversationId`. A server that refuses
the two fields as unknown (an older one) gets the report again without them.
```

**Attention: session `Info`** (`:214-217`). Replace

```
Session `Info` also carries `branch` (the git branch of the working
directory, read from `.git/HEAD` at launch; server and host alike) and, for
hosted sessions, `hostUser`. `GET /api/sessions/{id}/links` adds `active`
to every link: the number of viewers currently attached through it.
```

with

```
Session `Info` also carries `branch` (the git branch of the working
directory, read from `.git/HEAD` at launch; server and host alike) and, for
hosted sessions, `hostUser`. A server session carries `yolo: true` when its
launch applied its agent's yolo recipe (absent otherwise, an agent without a
recipe included), `agentSession{id, resumable, source}` once Conductor knows
the agent's own session (`source` `set` when Conductor chose the id at launch,
`hook` when the agent reported it, `resumed` when this session resumed it;
`resumable` once the agent has reported a turn, and always for a resumed one),
and `resumedFrom`, the session it resumed or relaunched.
`GET /api/sessions/{id}/links` adds `active` to every link: the number of
viewers currently attached through it.
```

**Attention: what Conductor types** (`:219-227`). Replace

```
When a controller's input clears `needs_input`, the session records
`lastAnswer{by, byName, at, message}` in its `Info` (the prompt that was
answered and who answered it) and an `input` activity entry. What Conductor
types itself (a crew member's prompt, a handoff or a broadcast), at most 32
KiB with its line break as an `INPUT` frame, clears `needs_input` the same way
and always records an `input` entry, `byName` `crew` (for a broadcast, the
admin's display name) and the text typed, less its line break, as `message`,
cut to the 500 bytes of Events (the terminal gets all of it); a handoff or a
broadcast is never typed while the session is `needs_input` (see Crew runs).
```

with

```
When a controller's input clears `needs_input`, the session records
`lastAnswer{by, byName, at, message}` in its `Info` (the prompt that was
answered and who answered it) and an `input` activity entry.

What Conductor types itself (a crew member's prompt, a handoff or a
broadcast) and a reply sent with the `submit` control message are submitted as
a terminal sends a paste and then a key press, one submission at a time per
session. The text has each line break, carriage return and tab made a space,
every other control character (escape among them) and any invalid UTF-8
dropped, and its surrounding space trimmed; once cleaned it is at most 32756
bytes. It is written wrapped in `ESC[200~` and `ESC[201~` while the program
has bracketed paste on (the session follows `ESC[?2004h` and `ESC[?2004l` in
the output, as a terminal does, a sequence split between two reads included),
and as it is otherwise; then, 250 ms later, a carriage return is written on its
own. A TUI that reads a text and its carriage return in one read takes the
carriage return as part of a paste (Codex's paste-burst detector makes it a
newline; Claude Code collapses a read of over 800 bytes into a pasted block),
so written apart it is Enter. The carriage return answers the `needs_input`
that showed when the submission began, as typing does; when a new one comes up
during the 250 ms, it is left out, and the text, already written, waits in the
program's input and is never written again. No lock of the session is held
across the pause, and a person's keystrokes (INPUT) are written as they come,
not held back. What Conductor submits always records one `input` entry as the
text is written, `byName` `crew` (for a broadcast, the admin's display name)
and the text as `message`, cut to the 500 bytes of Events (the terminal gets
all of it); a `submit` reply records the answer as a controller's input does.
A handoff or a broadcast is never submitted while the session is
`needs_input` (see Crew runs).
```

**HTTP API: `GET /api/catalog`** (`:404`). In the row, replace `` `{agents, hidden}`: the launchable agents, `` with `` `{agents, hidden, yoloDefault}`: the launchable agents, ``, and at its end replace

```
and `site` when the agent has a website (an `https` URL: a built-in's, or the saved agent's) |
```

with

```
and `site` when the agent has a website (an `https` URL: a built-in's, or the saved agent's); each agent also carries, when it has them, its `yolo` recipe `{args?, env?}` (its `env` values shown as they are: switches, not secrets), its `trustPrompt` and its `session` recipe `{startArgs?, newId?, idFrom?, idPolicy?, resumeArgs?, idPattern?, resumeNeedsCwd?}` (limits below); `yoloDefault` is the server's `yolo` setting, which a launch or a crew without `yolo` follows |
```

**HTTP API: `POST /api/catalog`** (`:405`). In the row, replace `a saved agent that leaves out `` `adapter` ``, `` `signal` `` or `` `site` `` takes those of the agent it replaces;` — the exact text is

```
a saved agent that leaves out `adapter`, `signal` or `site` takes those of the agent it replaces;
```

— with

```
a saved agent that leaves out `adapter`, `signal`, `site`, `yolo`, `trustPrompt` or `session` takes those of the agent it replaces, and `"yolo": {}` or `"session": {}` says it has none;
```

**HTTP API: `GET /api/crews/{id}`** (`:412`). In the row, replace `` `{id, name, goal, cwd, where, isolation, openAfterLaunch, viewLinkTtlSeconds?, members, createdAt, updatedAt}` `` with `` `{id, name, goal, cwd, where, isolation, openAfterLaunch, viewLinkTtlSeconds?, yolo?, members, createdAt, updatedAt}` (`yolo` absent: the server's default; `true` or `false`: the crew's own choice) ``.

**HTTP API: `POST /api/crews/{id}/launch`** (`:418`). In the row, replace `` reply `201 {run}` once the session of every member that starts immediately exists, each member `starting` until its prompt is typed; `` with `` reply `201 {run}` once the session of every member that starts immediately exists, each member `starting` until its prompt is typed; the run's `yolo` is the crew's, else the server's, fixed for every member of the run, one started or added later included; ``.

**HTTP API: `GET /api/runs`** (`:419`). Replace the row

```
| `GET /api/runs` | admin | `{runs}`: the runs in the server's memory, newest first |
```

with

```
| `GET /api/runs` | admin | `{runs}`: the runs in the server's memory, newest first, each with its `state` and `needsInput` (see Crew runs) |
```

**HTTP API: `POST /api/runs/{run}/members`** (`:421`). In the row, replace `a prompt over 32767 bytes with the run's goal in it,` with `a prompt over 32756 bytes with the run's goal in it,`.

**HTTP API: member resume** — after the row of `POST /api/runs/{run}/members/{name}/start` (`:422`) insert the row:

```
| `POST /api/runs/{run}/members/{name}/resume` | admin | resume an ended member of the run (no body), in its working directory, its worktree and branch kept: a session tagged with the run that resumes the member's last `agentSession` with its agent's `session` recipe when the agent has one and the session was `resumable` (`resumed: true`; no prompt is typed, its conversation has it), and otherwise a fresh one whose role prompt is typed again once it is ready (`resumed: false`, with a `notice`); the member is `running` with the new `sessionId` (or `starting` until that prompt), noted in the run log; reply `201 {session, resumed, notice?}`, the new session's `Info` with `resumedFrom`; `404` for an unknown run or member; `409 still_running` for a member that is pending, starting or running; `409 run_stopped`; `409 already_resumed` when a running session holds that agent session; `400 invalid_agent` when the member's agent is no longer in the catalog; a session that cannot be created answers as at launch, the member `ended` with its `error` |
```

**HTTP API: broadcast** (`:424`). In the row, replace `` with `reason` `needs_input`, `not_running` or `unknown` (see Crew runs); `` with `` with `reason` `needs_input`, `not_running`, `unknown` or `no_enter` (typed, its carriage return left out because a question came up; see Crew runs); ``.

**HTTP API: `POST /api/sessions`** (`:431`). Replace the row

```
| `POST /api/sessions` | admin | launch a server session: `{agentId, name?, cwd?, args?, cols?, rows?}`, reply `201` with the session `Info` |
```

with

```
| `POST /api/sessions` | admin | launch a server session: `{agentId, name?, cwd?, args?, cols?, rows?, yolo?}`, reply `201` with the session `Info`; `yolo` overrides the server's `yolo` for this launch: with it on, the agent's yolo recipe is applied (its `args` after the command and `args`, its `env` over the agent's own, through the filtered environment; for Codex also `-c projects={"<dir>"={trust_level="trusted"}}` naming the launch directory's repository and, for a worktree, its main repository), and `Info.yolo` says so; an agent with no recipe is launched without one and its activity says so; an agent with a `session` recipe and `startArgs` gets a fresh id there, right after its command (`Info.agentSession`, `source:"set"`) |
```

**HTTP API: session resume** — after the row of `DELETE /api/sessions/{id}` (`:433`) insert the row:

```
| `POST /api/sessions/{id}/resume` | admin | resume an ended server session (no body), while it is listed: a new session with the same agent, name, working directory, arguments, environment and yolo choice, launched with its agent's `resumeArgs` for the stored `agentSession`, right after the command, when the agent has a `session` recipe and the session was `resumable` (`resumed: true`), and plainly otherwise, with a `notice`: `<agent> has no session recipe: started anew`, or `nothing to resume yet (the agent had no turn): started anew`; reply `201 {session, resumed, notice?}`, the new session's `Info` with `resumedFrom`; a crew member's session is resumed as its member, as `POST /api/runs/{run}/members/{name}/resume` does; `404` when unknown; `400 hosted_session` for a hosted session; `409 still_running`; `409 already_resumed` when a running session holds that agent session; `400 invalid_agent` when its agent is no longer in the catalog; `400 invalid_cwd` when its working directory no longer passes the check; otherwise as `POST /api/sessions` answers |
```

**HTTP API: attention route** (`:438`). Replace the row

```
| `POST /api/sessions/{id}/attention` | agent token or admin | report an attention state, see Attention |
```

with

```
| `POST /api/sessions/{id}/attention` | agent token or admin | report an attention state, and with it the agent's own session id (`agentSession`, at most 128 bytes) and whether the report is of a turn (`turn`), see Attention |
```

**HTTP API: the catalog's limits** (`:449-463`). Replace

```
`catalog.json` with the ID of a built-in or configured agent replaces it but
inherits what it leaves out (`adapter`, `signal`, `site`, `***` env values and
env values equal to the replaced agent's); env keys it does not list are not
```

with

```
`catalog.json` with the ID of a built-in or configured agent replaces it but
inherits what it leaves out (`adapter`, `signal`, `site`, `yolo`,
`trustPrompt`, `session`, `***` env values and env values equal to the
replaced agent's; `"yolo": {}` and `"session": {}` say it has none); env keys it does not list are not
```

and replace

```
Conductor has, an `env` key or `envPassthrough` name at most 128 bytes and an
`env` value at most 16384 bytes.
```

with

```
Conductor has, an `env` key or `envPassthrough` name at most 128 bytes and an
`env` value at most 16384 bytes. A `yolo` recipe: at most 16 `args` of 1 to
4096 bytes without NUL, at most 16 `env` variables named like an
`envPassthrough` name and never `CONDUCTOR_*`, each value at most 4096 bytes
without NUL. A `trustPrompt`: as a signal `pattern`. A `session` recipe: at
most 16 `startArgs` and 16 `resumeArgs` of 1 to 4096 bytes without NUL, `{id}`
only as a whole argument, once in `resumeArgs` and once in `startArgs` when it
has any; `newId` `uuid` (the default) or `name` (`cdr-` and a UUID); `idFrom`
`hook` or absent; `idPolicy` `latest` (the default) or `lowest`; `startArgs`
or `idFrom: "hook"`, so that the id can be known; `idPattern` anchored with `^`
and `$`, at most 200 bytes, matching neither an empty id nor one that begins
with a dash; `resumeNeedsCwd` documents an agent that resumes only in the
directory its session ran in (a resume always runs there). The recipe's
arguments go right after the agent's command, before the launch's `args`, so
that Codex resumes with its subcommand. The empty recipe `{}` passes and means
none.
```

**HTTP API: the crew limits** (`:494-496`). Replace

```
`$GOAL` and `${GOAL}`, is at most 32767 bytes (it is typed as one line, which
changes no length, with a carriage return, and a session takes at most 32 KiB
at once), and at most 32 `args` of at most 4096 bytes, 8 KiB in all; `start.when` is `immediately`, `after` or
```

with

```
`$GOAL` and `${GOAL}`, is at most 32756 bytes (it is submitted as one line,
which changes no length, inside the 12 bytes of the bracketed-paste markers,
and a session takes at most 32 KiB at once), and at most 32 `args` of at most
4096 bytes, 8 KiB in all; `yolo`, when present, is `true` or `false`;
`start.when` is `immediately`, `after` or
```

**Crew runs: the prompt** (`:531-543`). Replace

```
A launch, a start or an added member answers once the member's session exists;
the member is `starting` until its prompt is typed, then `running`. A member is
ready for its prompt when its agent reports `needs_input` or `done`, or after
one second without output that follows its first output and at least two seconds
after its start, checked every 250 ms; after 60 seconds the prompt is typed
anyway and the run log says so. The prompt, `$GOAL` and `${GOAL}` replaced by
the goal, is typed as one line, as a handoff and a broadcast are (each line
break, carriage return and tab, in the prompt or in the goal, becomes a space;
the crew keeps the prompt as written), with a carriage return at the end; a
`done` the member reports as its prompt is written counts for the members
after it, and one whose `attention` entry is stamped no later than the prompt
was typed never does, however late the entry reaches the run: it is the idle
state the prompt answered. A member whose process ends first gets no prompt: it
```

with

```
A launch, a start or an added member answers once the member's session exists;
the member is `starting` until its prompt is typed, then `running`. A member is
ready for its prompt when its agent reports `needs_input` or `done`, or after
one second without output that follows its first output and at least two seconds
after its start, checked every 250 ms. Output that only sets the window title
(OSC 0 or 2: a spinner in the title) is not output for this. A program that
has turned bracketed paste on and then off again is between screens (Claude
Code does so while it loads, and what is typed then loses its Enter) and is not
ready until it turns it on again or reports `needs_input` or `done`. After 60
seconds the prompt is typed anyway and the run log says so. A trust question on
the member's screen (`source:"trust"`, see Attention) holds the prompt
instead: the member's session shows `needs_input` with the question's words,
the run log says once that the member asks it, the 60 seconds do not run, and
once a person has answered it with Enter the wait for readiness starts over.
The prompt, `$GOAL` and `${GOAL}` replaced by the goal, is one line, as a
handoff and a broadcast are (each line break, carriage return and tab, in the
prompt or in the goal, becomes a space; the crew keeps the prompt as written),
submitted as Attention describes: the text, as a bracketed paste while the
program has that mode on, then a carriage return 250 ms later. For an agent
whose hooks report `working` as it takes a prompt (Claude Code with the `hook`
signal), a prompt not taken within 3 seconds of its carriage return (no change
of attention, other than typing's, stamped after it) gets one more carriage
return, unless the session is `needs_input` by then; the run log says so. The
member is prompted as its carriage return is written: a `done` whose
`attention` entry is stamped after that counts for the members after it, and
one stamped before it never does, however late the entry reaches the run: it
is the idle state the prompt has not reached. When a question comes up during
the 250 ms (a new `needs_input`), the carriage return is left out: the text
waits in the agent's input, is never typed again, the member is `running` and
prompted as of then, and the run log says to press Enter in its terminal once
the question is answered. A member whose process ends first gets no prompt: it
```

**Crew runs: the run's shape** (`:549-553`). Replace

```
`ended`, and keeps its name: `POST /api/runs/{run}/members`
with that name is refused as a name used twice. A run is `{id, crewId, name,
goal, cwd, isolation, startedAt, stoppedAt?, members, log}`, a member `{name,
agentId, start, sessionId?, branch?, worktree?, status, startedAt?, endedAt?,
error?, diff?}` with `status` `pending`, `starting`, `running` or `ended`;
```

with

```
`ended`, and keeps its name: `POST /api/runs/{run}/members`
with that name is refused as a name used twice. A run is `{id, crewId, name,
goal, cwd, isolation, startedAt, stoppedAt?, members, log, state, needsInput,
yolo}`, a member `{name, agentId, start, sessionId?, branch?, worktree?,
status, startedAt?, endedAt?, error?, needsInput?, agentSession?, diff?}` with
`status` `pending`, `starting`, `running` or `ended`. A run's `state` is
derived as it is read: `stopped` once stopped; `finished` when every member
has ended and none is pending; `needs_input` while the session of a starting
or running member is `needs_input` (`needsInput` counts them, and each such
member has `needsInput: true`); `running` otherwise (a member's `done` keeps it
running: a done agent is idle, not gone). `yolo` is the run's yolo choice,
fixed at launch. A member's `agentSession` is the one its latest session last
showed (see Attention), kept after that session leaves the server, for
`POST /api/runs/{run}/members/{name}/resume`;
```

**Crew runs: handoffs** (`:562-575`). Replace

```
A `handoff` event a member reports (see Events) goes to the member of its run
that `to` names. When that member is `running` and not `needs_input`,
Conductor types `Handoff from <member>: <message>` and a carriage return into
its session, recorded as an `input` entry by `crew`, as a prompt is; the line
breaks and tabs the message keeps become spaces, so a handoff is one line.
```

with

```
A `handoff` event a member reports (see Events) goes to the member of its run
that `to` names. When that member is `running` and not `needs_input`,
Conductor submits `Handoff from <member>: <message>` into its session (the
text, then its carriage return 250 ms later, as a prompt is), recorded as an
`input` entry by `crew`; the line breaks and tabs the message keeps become
spaces, so a handoff is one line.
```

and replace

```
running. The session looks at `needs_input` in the same step in which it
finds the prompt an input would answer, so a handoff never answers a prompt:
one the session shows when a handoff is about to be typed sends the handoff
back to the head of the queue, and one raised while it is written stays.
```

with

```
running. The session looks at `needs_input` in the same step in which it
finds the prompt an input would answer, so a handoff never answers a prompt:
one the session shows when a handoff is about to be typed sends the handoff
back to the head of the queue, and one raised in the 250 ms before its
carriage return leaves the handoff typed without it, noted in the run log and
never typed again.
```

**Crew runs: broadcast** (`:586-598`). Replace

```
invalid_request`). It is typed with a carriage return and recorded as an
`input` entry by `byName`, cleaned as a viewer's display name is (`guest` when
```

with

```
invalid_request`). It is submitted to the members together, each as a prompt
is (the text, then its carriage return 250 ms later), and the submissions go on
for up to 15 s when the client goes away, so that none is cut between its text
and its carriage return; each is recorded as an
`input` entry by `byName`, cleaned as a viewer's display name is (`guest` when
```

and replace

```
`ended`), or when no member of the run has the name (`unknown`). A write to a
member's process that fails, as it does when the process has just exited, is
reported as `not_running` too; the server logs it, without the text. `sent`
and `skipped` keep the order of `members`, or of the run.
```

with

```
`ended`), or when no member of the run has the name (`unknown`). A write to a
member's process that fails before the text is written, as it does when the
process has just exited, is reported as `not_running` too; the server logs it,
without the text. A member where a question comes up in the 250 ms before the
carriage return, or whose carriage return cannot be written, gets the text
without it (`no_enter`). `sent` and `skipped` keep the order of `members`, or
of the run.
```

**Crew runs: the run log** (`:620-641`). After the row

```
| prompt typed | `status` | `typed <member>'s prompt` |
```

add

```
| trust question | `status` | `<member> asks "<question>": answer it in <member>'s terminal; its prompt waits`, once each time the question holds the prompt |
| prompt without its Enter | `error` | `typed <member>'s prompt without its Enter: <member> waits on a question; answer it, then press Enter in its terminal` |
| Enter again | `status` | `pressed Enter again for <member>: it did not report taking its prompt within 3s` |
| no yolo recipe | `status` | `<member>: yolo is on, but <agent name> has no yolo recipe: launched without one` |
| resumed | `status` | `<member> resumed its conversation` |
| relaunched | `status` | `<member> started anew` (its prompt is typed again once it is ready) |
```

and after the row

```
| handoff delivered | `status` | `handoff delivered from <a> to <b>` |
```

add

```
| handoff without its Enter | `error` | `handoff from <a> to <b> typed without its Enter: <b> waits on a question; answer it, then press Enter in its terminal`, or `: <the error>` when the run stopped or the write failed during the pause |
```

**Crew runs: limits** (`:655-659`). Replace

```
at most 10 handoffs wait for a member; a broadcast is at most 4096 bytes once
made one line; and whatever Conductor
types into a session, a prompt, a handoff or a broadcast, is one write of at
most 32 KiB with its carriage return. A launch, a start or an added member has
2 minutes to make its worktrees and start its sessions, and a stop 30 seconds.
```

with

```
at most 10 handoffs wait for a member; a broadcast is at most 4096 bytes once
made one line; and whatever Conductor
submits into a session, a prompt, a handoff or a broadcast, is one write of at
most 32756 bytes (32 KiB with the paste markers) and then its carriage return,
250 ms later. A launch, a start, an added or resumed member has 2 minutes to
make its worktrees and start its sessions, a broadcast's submissions 15
seconds, and a stop 30 seconds.
```

**Crew runs: resume.** After the section's last line (`:661`, `not ready for its prompt gets it after 60 s.`), add, with a blank line before it:

```
An ended member can be resumed (`POST /api/runs/{run}/members/{name}/resume`,
or `POST /api/sessions/{id}/resume` on its session): a new session in its
working directory, its worktree and branch untouched, tagged with the run and
launched with the run's yolo choice. It resumes the member's agent session
when the agent has a `session` recipe and that session had a turn, and gets no
prompt; otherwise it starts anew and its role prompt is typed again once it is
ready, and only a `done` after that prompt counts for the members after it. A
member of a stopped run is not resumed (`409 run_stopped`).
```

- [ ] **Step 6: `docs/architecture.md`**

After the `Local` paragraph (`:23-26`, ending `client reconnects to get a fresh replay.`), insert, with a blank line before it:

```
`Local.Submit` is the one way Conductor types into a session (a crew member's
prompt, a handoff, a broadcast) and the way a reply box's `submit` message is
typed: the text, as a bracketed paste while the program has that mode on (the
pump follows `ESC[?2004h`/`ESC[?2004l`), then, 250 ms later, a carriage return
written on its own, one submission at a time per session, with no lock held
across the pause, cancellable, and never written twice. A person's INPUT stays
raw. The same pump treats output that only sets the window title as no output,
watches the screen's text for the agent's workspace-trust question
(`Options.TrustPattern`), and an INPUT that is only a terminal's automatic
reports answers nothing. A session that ends clears its attention as it ends.
```

Crew runs (`:69-80`). Replace

```
sessions exist; a goroutine per member, on the run's own context, then waits
until the session is ready (its agent reports `needs_input` or `done`, or its
output goes quiet) and types the role prompt with `Local.Type`. The engine
```

with

```
sessions exist; a goroutine per member, on the run's own context, then waits
until the session is ready (its agent reports `needs_input` or `done`, or its
output goes quiet, and it is not between two screens of its start), held while
a trust question shows, and submits the role prompt with `Local.Submit`,
stamping the member prompted just before the carriage return. The engine
```

and replace

```
Handoffs and broadcasts are typed with `Local.TypeUnlessWaiting`, which looks
at the attention state in the same step that finds the prompt a write would
answer, so neither ever answers a prompt. Runs live in memory, as sessions do;
run links are share-store links scoped to a run (`Link.RunID`), which the
server resolves to the run's member sessions through `Engine.MemberOf`.
```

with

```
Handoffs and broadcasts are submitted unless the session waits, a look at the
attention state made in the same step that finds the prompt a write would
answer, and a prompt raised during the pause keeps their carriage return back,
so neither ever answers a prompt. What changes in a run without a session
change (a member reserved, started, prompted or ended, an entry in its log, a
stop) is reported through `Engine.OnRunChange`, under the engine's lock and
without waiting, as a `run` event on `/api/events`, and a forgotten run as one
with `removed`; the browser's live store reads the run again, so no page
polls. A run's state (running, needs input, stopped, finished) is derived each
time it is read. `Engine.ResumeMember` starts an ended member again in its
worktree, resuming its agent's own session through the agent's session recipe
when it can. Runs live in memory, as sessions do;
run links are share-store links scoped to a run (`Link.RunID`), which the
server resolves to the run's member sessions through `Engine.MemberOf`.
```

Packages: replace the row (`:91`)

```
| `internal/catalog` | launchable agents (argv arrays, never shell strings) |
```

with

```
| `internal/catalog` | launchable agents (argv arrays, never shell strings), their yolo, trust and session recipes |
```

replace the row (`:94`)

```
| `internal/crew` | saved crews, one file each in `dataDir/crews/`: members, role prompts, start conditions, validation; runs: member sessions through the server's launch path, git worktrees, readiness, prompts, start conditions, handoffs between members (an activity sink and a change hook of `internal/api`) |
```

with

```
| `internal/crew` | saved crews, one file each in `dataDir/crews/`: members, role prompts, start conditions, validation; runs: member sessions through the server's launch path, git worktrees, readiness and the trust hold, prompts, start conditions, handoffs between members (an activity sink and a change hook of `internal/api`), run state, change reports, member resume |
```

replace the row (`:97`)

```
| `internal/session` | ring buffer, fan-out hub, `Local` session, registry, bounded file reads |
```

with

```
| `internal/session` | ring buffer, fan-out hub, `Local` session (submissions, attention, trust watcher, the agent's own session), registry, bounded file reads |
```

and after the row (`:101`) of `internal/hostagent` add

```
| `internal/notify` | `conductor notify`: reports from inside a session; hook payload mappers, the agent's own session id among what they read |
```

Security model: after the bullet that ends `reaches a child, and Conductor sets only the few a session needs itself.` (`:119`), add

```
- Yolo recipes are catalog data, applied as argv and through the filtered
  environment; they run agents without their permission prompts (Codex's
  without its sandbox) as the server's user. An agent session id is passed to
  an agent only as one argument, after matching its agent's pattern and
  beginning with a letter or a digit.
```

Persistence (`:129-136`). Replace

```
server restart ends server sessions and forgets links and runs (worktrees and
their branches stay on disk). Hosted sessions survive a brief server outage
```

with

```
server restart ends server sessions and forgets links and runs (worktrees and
their branches stay on disk), and with them what Resume needs: an ended session
can be resumed while it is listed, a crew member while its run is kept. Hosted
sessions survive a brief server outage
```

- [ ] **Step 7: `docs/features.md`**

**The adapter matrix** (`:113-129`). Replace the whole table, from the header line

```
| Agent | Command | Waiting-for-input signal | Turn done | Tool events | Wiring | Verify before shipping |
```

through the Shell row

```
| Shell / anything | any argv | bell / OSC / pattern | exit | none | — | — |
```

with this table (the first seven columns as they are; the three new ones from the round 4 research, `docs/round4/yolo-flags.md` and `docs/round4/session-resume.md`: **live** checked against the real CLI on 2026-10-01/02, **help** from its `--help`, **docs** or **source** from the vendor's documentation or repository):

```
| Agent | Command | Waiting-for-input signal | Turn done | Tool events | Wiring | Verify before shipping | Yolo recipe (what it turns off) | Trust prompt | Session recipe (id; resume) |
|---|---|---|---|---|---|---|---|---|---|
| Claude Code | `claude` | `Notification`/`PermissionRequest` hooks (with options); `idle_prompt` (a minute at rest) is done, not a question (round 4) | `Stop` | `PostToolUse`, `PermissionDenied`, `SubagentStop` | launch: `--settings <hooks/claude.json>` | — | `--dangerously-skip-permissions`, `skipDangerousModePermissionPrompt` in the `--settings` file (every permission prompt; `.git`, `.claude` writable; deny rules hold; the trust question stays) — live, 2.1.287 | `Is this a project you created or one you trust?` — live; no per-launch trust: trust the repository once (its worktrees follow) | set `--session-id {uuid}`, then hook `session_id` (latest); `--resume {id}` — live |
| Codex CLI | `codex` | the bell (`tui.notification_method="bel"`) and the `PermissionRequest` hook; the end of a turn is done, not a question (round 4) | `notify` (agent-turn-complete) → done; the hidden title thread's turn is ignored | experimental `hooks.json` (`features.hooks`), not on Windows | launch: `-c notify=[…]`; file: `~/.codex/config.toml` + `~/.codex/hooks.json` | `-c` array syntax; PermissionRequest hook shape | `--dangerously-bypass-approvals-and-sandbox` (every approval and the sandbox) and per launch `-c projects={"<repo>"={trust_level="trusted"}}` — live, 0.159.0 | `Trust this folder?` (Enter accepts and saves it) — live | not settable; notify `thread-id` (lowest: the title thread reports too); `resume {id} -c tui.resume_cwd="session"`, same directory — live |
| Antigravity | `agy` | bell (no notification event) | `Stop` | `PreToolUse`/`PostToolUse` (`toolCall.name`) | file: `~/.gemini/config/hooks.json` (global) or `.agents/hooks.json` (project) | exact global path | `--dangerously-skip-permissions` (tool permission requests; its trust dialog stays, trusted per exact path) — help | — | not settable; hook `conversationId` (installed hooks only); `--conversation {id}`, same directory — live (`-p`) |
| Copilot CLI | `copilot` | `notification` (`permission_prompt`) | `agentStop` | `postToolUse`, `errorOccurred` | file: `~/.copilot/hooks/conductor.json` (own file, no merge) | permission prompt key bindings for options | `--yolo`, `COPILOT_ALLOW_ALL=true` (every tool, path and URL; the env var also skips its trust question; deny rules win) — live, 1.0.59 | — (the recipe skips it) | set `--session-id {uuid}`, hook `sessionId`; `--session-id {id}` — live (the hook's `sessionId` not live) |
| Cursor CLI | `cursor-agent` | bell / pattern (CLI fires no waiting event) | `stop` | `postToolUse`, `afterFileEdit` | file: `~/.cursor/hooks.json` (merge) | CLI binary name; which events the CLI fires today | `--yolo --trust` (command and MCP approvals; `--trust` skips workspace trust) — docs | — (the recipe skips it) | not settable; hook `conversation_id` (installed hooks only); `--resume {id}`, same directory — docs |
| OpenCode | `opencode` | plugin `permission.asked`, `session.idle` | `session.idle` | `tool.execute.after`, `session.error` | launch: `OPENCODE_CONFIG_DIR=<hooks/opencode>` if additive, else file: `~/.config/opencode/plugins/conductor.ts` | whether `OPENCODE_CONFIG_DIR` adds to or replaces the default dir | `--auto` (every permission not explicitly denied) — docs | — | none this round (plugin capture deferred): Relaunch |
| pi | `pi` | `agent_end` | `agent_end` | `tool_call`/`tool_result` | launch: `--extension <hooks/pi-conductor.ts>` | — | none: pi has no permission system | — | set `--session-id {uuid}`; `--session-id {id}`, same directory — source |
| oh-my-pi | `omp` | as pi | as pi | as pi | file: `extensions:` entry in `~/.omp/agent/config.yml` | pi extension API compatibility (issue #2166) | `--yolo` (approvals unless a deny policy matches; its default already) — docs | — | none this round (plugin capture deferred): Relaunch |
| aider | `aider` | `--notifications-command` | same | none | launch: `AIDER_NOTIFICATIONS=true`, `AIDER_NOTIFICATIONS_COMMAND` | confirmations (`(Y)es/(N)o`) need the pattern detector | `--yes-always` (every confirmation, shell commands included) — docs | — | none this round (its handle is a chat-history file): Relaunch |
| Goose | `goose` | bell / pattern (no waiting event yet) | `Stop` | `PostToolUse` | file: `~/.agents/plugins/conductor/hooks/hooks.json` | — | `GOOSE_MODE=auto` (tool approvals; its default already) — docs | — | set `session --name cdr-{uuid}`; `session --resume --name {id}`, same directory — source |
| Amp | `amp` | `agent.end` (in-process plugin) | `agent.end` | `tool.call`/`tool.result` | file: `~/.config/amp/plugins/conductor/` | plugin API surface | `--dangerously-allow-all` (every tool approval; the flag in the rebuilt CLI unconfirmed) — docs | — | none this round (plugin capture deferred): Relaunch |
| DeepSeek Harness | `dsh` (with the TUI plugin) | plugin events `question-asked`, `permission-requested` | `session completed/failed` | none | file: dsh plugin | developer preview; plugin API changes | `DSH_PERMISSION_MODE=danger-full-access` (its sandbox and every approval) — source | — | none (unverified): Relaunch |
| Shell / anything | any argv | bell / OSC / pattern | exit | none | — | — | none | — | none: Relaunch |
```

**Open verification (round 2)** (`:138-152`). After the item that ends

```
  prompt. Launch a crew of `claude` members with worktrees in a repository
  Claude Code has not trusted and see whether the prompts land.
```

add the line

```
  Answered in round 4: the dialog appears even with the bypass flag, and the
  typed Enter answered it (Claude Code chose "No, exit"); the run now holds
  the prompt while the question shows (Crew runs in `docs/protocol.md`), and a
  worktree of a trusted repository asks nothing.
```

and after the item that ends

```
  burst of input as a paste; bracketed paste stays the deferred fix for agents
  that need it.
```

add the line

```
  Answered in round 4: Codex took every one-write prompt as a paste (a
  newline, not Enter) and Claude Code any over 800 bytes; prompts, handoffs,
  broadcasts and replies now go in as a bracketed paste and a separate Enter.
```

**Open verification (round 3)** (`:303-305`). Replace

```
- The built-in agents' `site` URLs (twelve, in `internal/catalog/defaults.go`),
  each opened in a browser by hand: they ship as best known and a test checks
  their shape only.
```

with

```
- The built-in agents' `site` URLs (twelve, in `internal/catalog/defaults.go`),
  each opened in a browser by hand: they ship as best known and a test checks
  their shape only. On 2026-10-02 three were found moved and fixed (codex,
  goose, dsh, now pinned by the test); the other nine are still to open.
```

**Open verification (round 4)** (`:553-555`). Replace

```
### Open verification (round 4)

Filled in by Task 10 of `docs/round4-plan.md`: what only a person can check.
```

with

```
### Open verification (round 4)

What only a person can check, beside the plan's automated checks:

- The live run of Task 4 (a one-member crew per agent, a handoff and a
  broadcast, short and long, in a fresh worktree) passed against Claude Code
  2.1.287 and Codex 0.159.0. Repeat it against Codex 0.160 (already on this
  machine's daemon) and the next Claude Code: their paste handling and their
  trust questions' words (`trustPrompt`) can change.
- The interactive tiles with the WebGL renderer in a real browser: the
  headless checks run without WebGL2, so they measured the DOM renderer.
- The yolo recipes taken from documentation, each against the real CLI:
  Amp's `--dangerously-allow-all` in the rebuilt CLI, OpenCode's TUI `--auto`,
  Cursor's `--yolo --trust` together, Antigravity's exact-path trust on 1.2.x,
  aider's `--yes-always`, Goose's `GOOSE_MODE=auto`, oh-my-pi's `--yolo` and
  DeepSeek Harness's `DSH_PERMISSION_MODE`.
- The session recipes taken from documentation or source: Goose's
  `session --name` in the current Block CLI, Cursor's resume of a chat it
  reported, pi's `--session-id`, Antigravity's interactive `--conversation`, and
  Copilot's hook `sessionId` (its launch-time `--session-id` was checked live).
- Codex's hidden title thread: its notify is now ignored by its first input;
  check on a long first turn that no false "needs input" shows meanwhile, and
  that the resumed conversation is the user's, not the title thread's.
- Codex's "Hooks need review" dialog: with Conductor's `~/.codex/hooks.json`
  installed and not yet trusted, Codex 0.159 opens it at start; a crew
  member's prompt is held only if its words match the trust prompt, which they
  do not. Trust the hooks once in Codex before a crew run.
- The trust hold's flow in the workbench: a member in a never-trusted
  repository shows the question, a person answers in its terminal, the prompt
  then runs. Esc (Codex quits) ends the member, as an early exit.
- A member of a stopped run cannot be resumed in this round (`409
  run_stopped`); decide whether Resume should reopen the run.
```

and after that list add, with a blank line before it:

```
### Deferred (round 4)

- Capturing the agent's session id through the plugin agents (OpenCode,
  oh-my-pi, pi's mid-process changes, Amp, DeepSeek Harness): their plugins
  would pass it to `conductor notify`; until then they relaunch.
- A resume record that outlives `exitedRetention` (a bounded store in the data
  directory), so that a session can be resumed after it leaves the list.
- Yolo and Resume for hosted sessions (`conductor host`).
- Resuming a member of a stopped run, which would reopen the run.
- `conductor up --resume <run>`.
- aider's chat-history file as its session handle.
```

- [ ] **Step 8: `AGENTS.md`** (Map only; Task 9 owns the pinned versions and the Checks)

Replace the row (`:12`)

```
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog |
```

with

```
| `internal/config`, `internal/catalog` | JSON config with `CONDUCTOR_*` overrides; argv-based agent catalog with each agent's yolo, trust and session recipes |
```

replace the row (`:17`)

```
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads, viewer roster, activity log, attention |
```

with

```
| `internal/pty`, `internal/session` | process lifecycle; ring buffer, fan-out, roles, resize policy, bounded file reads, viewer roster, activity log, attention, submissions (a paste, then Enter: the one way Conductor types into a session), the agent's own session |
```

and replace the row (`:20`)

```
| `internal/notify` | `conductor notify`: attention and event reports from inside a session; hook payload mappers (Claude Code, Codex, agy, Copilot, Cursor, Goose) |
```

with

```
| `internal/notify` | `conductor notify`: attention and event reports from inside a session; hook payload mappers (Claude Code, Codex, agy, Copilot, Cursor, Goose), the agent's own session id among what they read |
```

- [ ] **Step 9: An agent at rest is done (Task 4).** In `README.md`, at the end of the "Wired at launch." paragraph (after "…since those files choose the commands agents run.", `:182`), add the paragraph:

```
**At rest is done, not a question.** An agent that finished its turn and waits
for its next line reports `done`, whatever its hooks call it: Codex's
`agent-turn-complete` and Claude Code's `idle_prompt` notification (sent after
a minute at rest) both do, as Claude Code's `Stop` does. Needs input is kept
for a real question: a permission request, a dialog, the bell. A crew relies
on this: a handoff or a broadcast is typed only into a member that is not
waiting on a question, and an idle member is not.
```

- [ ] **Step 10: Check the docs and commit**

Run: `python3 scripts/brand_assets.py --check && make lint`
Expected: both exit 0 (nothing in Go changed in this step; the brand check keeps the README's asset references honest).

Run: `grep -n "32767\|TypeUnlessWaiting\|Local.Type\b" README.md docs/protocol.md docs/architecture.md`
Expected: no output (every mention of the old one-write typing is gone).

```bash
git add README.md docs/protocol.md docs/architecture.md docs/features.md AGENTS.md
git commit -m "docs: round 4 tiles, prompts as a paste and an Enter, trust, yolo, run states, resume"
```

**Done when:**

- `TestDefaultsHaveSites` pins the three moved sites and passes; committed: "catalog: the codex, goose and dsh sites where they live now, pinned by their test".
- `README.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/features.md` (the adapter matrix, Open verification for rounds 2, 3 and 4, and Deferred (round 4)) and `AGENTS.md`'s Map carry every edit of Steps 4–9, and none of the lines Tasks 1, 2, 4 and 9 wrote was touched.
- Each assumption listed under Interfaces held against the merged code, or the text was changed to match the code; "Open verification (round 4)" says what Task 4's live check really showed (versions and date, or that it could not run).
- `python3 scripts/brand_assets.py --check && make lint` pass, and the `grep` of Step 10 prints nothing.
- Committed: "docs: round 4 tiles, prompts as a paste and an Enter, trust, yolo, run states, resume".

---

## Verification

1. The full gate on the merged branch: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`, `make test-e2e` (Chromium 1117 from `~/.cache/ms-playwright`, or `npx playwright install chromium`). Name any check that could not run (no Chromium, no network, no git) when the round is handed over: it is a limitation, not a pass.
2. Task 4's live check (Step 11) once more against the merged branch's binary: `PASS live4`, the five words answered by both agents, no "asks" or "without its Enter" line in the run log, no trust entry for the repository in `~/.codex/config.toml`. `CONDUCTOR_E2E_LIVE=1 CONDUCTOR_E2E_LIVE_REPO=<the same repository> npm --prefix web run test:e2e -- live.spec.ts` passes too.
3. The headless checks of Tasks 6, 7 and 8 against the merged build (the test server of Headless checks, under File Structure): every tile's unused width under one cell at 1440×900 and 1920×1080 on the wall and the crew view; a session seeded at 148×57 sized by the tile that last fitted it, never 80×24, and untouched by the view-only join page; typing into a tile reaches the session; the Crews page lists the run under its crew with its state and member statuses; the sidebar groups by run in every section; the broadcast bar reads "Send to N" with every live member ticked, and an untick survives a run event; the yolo switch and badge; Resume of an ended session.
4. With a person, in a scratch repository neither agent has trusted (`git init` and one commit in a new temporary directory): a one-member crew of `claude` with yolo off shows "needs you" with "Is this a project you created or one you trust?"; pressing the arrow keys leaves it held; answering it (Down, then Enter, to trust; the person's choice) lets the prompt be typed and run. The same with `codex` (yolo off) shows "Trust this folder?"; answering with Enter saves the trust in `~/.codex/config.toml` (the person's choice: remove that entry afterwards, or choose Quit, which ends the member, "ended before its prompt was typed"). With yolo on, Codex shows no question in that repository and `config.toml` gains nothing.
5. With a person: a Claude Code session launched from the Launch dialog, prompted once, stopped, then **Resume** from its header: the new session continues the conversation (asked "what did I ask you?", it answers); an unprompted one shows **Relaunch** and starts anew with the notice. A Codex session the same way (its id captured from its notify). A crew member resumed from the crew view keeps its branch and worktree and gets no prompt.
6. The wall on a real browser with WebGL (the headless Chromium has none): tiles fill their width; clicking a tile and typing reaches the agent; Alt+W and the other Alt chords still work with a tile focused; plain `j`/`k` go to the agent, not the queue.

## Coverage check

Run against the spec on 2026-10-02:

- **Coverage.** Every Round 4 decision maps to a task. *Runs on the Crews page*: run state in Go (Task 4: `runState`, `refresh`, `TestRunState`, `TestGetDerivesTheRunState`) and its mirror, the runs list, the "Running" badge (Task 7); run events for the changes no session change carries — a member starting or failing to start, an all-manual launch, a stop of a sessionless run, eviction (OnForget → `removed`), a log entry (Task 4: `OnRunChange`, `TestRunChangesAreReported`, `TestRunEventsReachTheStream`, `TestEventHubRunEvent`); one live store, coalesced re-reads, stale replies dropped, the crew view's poll removed (Task 7). *The sidebar groups by run everywhere* (Task 7: `sidebarGroups`, `railGroups` rebuilt on it, run names from the store). *A typed prompt runs*: the routine (Task 2), every automatic path and the reply boxes on it (Tasks 2 and 4), the readiness rules (Task 4), the re-Enter for Claude Code (Task 2 `TestSubmitPressesEnterAgainWithoutConfirmation`, Task 4 `Confirm`), markPrompted at the Enter (Task 4 `TestADoneBeforeTheEnterDoesNotCount`, `TestADoneAsThePromptIsTypedCounts`), the trust dialog (Tasks 2–4), Codex's per-launch trust with yolo (Task 3 `TestCodexTrustGoesWithYolo`, `TestRepoRoots`), the terminal-report rule (Task 1), the live verification (Task 4 Step 11). *Tiles are interactive and fill*, the hello rule on both transports, scaled view-only tiles (Tasks 1 and 6). *A global yolo flag* (Task 3; UI Task 8; docs Task 10). *Playwright* (Task 9). *An ended session needs nothing* (Task 1, server and hosted). *Agent sessions are named and resumable* (Task 5; UI Task 8). *Broadcast goes to everyone by default* (Task 7: `broadcastSelection` and its vitest, the bar's count and "N waiting skipped", the headless assertion). The dsh `Site` and the two other stale sites (Task 10).
- **Decisions this plan adds, and why.** (1) An agent at rest reports `done`, not needs input (Task 4, Step 8): without it a handoff to an idle Codex member would wait for a person forever and a broadcast would skip it, so the spec's live check of handoffs and broadcasts could not pass; a real question still reports needs input. (2) The trust question is answered only by a write with Enter in it (Task 2): an arrow key moving the dialog's selection must not release the hold. (3) The Claude Code and Codex trust prompts are the words their screens showed in the investigation's captures, not the investigation's paraphrase ("Do you trust the files in this folder?" was not on Claude Code 2.1.287's screen). (4) `crew.RepoRoots` works on git before 2.31 (this machine has 2.25.1, which has no `--path-format=absolute`). (5) Resume works while the ended session is listed or the run is kept, refuses a member of a stopped run, and leaves plugin-captured agents without a recipe: each is listed as deferred in Task 10.
- **Placeholders.** Each step carries its code or its exact edit; the Go of Tasks 1–5 is the code that passed `go test -race` on a copy of the repository, copied into the steps and diffed against it. Where a step changes a hunk an earlier task wrote, it names the earlier task and the hunk.
- **Type consistency.** `Submission`/`SubmitResult`/`Submit` (Task 2) are what Tasks 4 and 5 call; `Type`/`TypeUnlessWaiting`/`write` live until Task 4 removes them, with `answer(promptSince, by, byName, record, enter)` shared from Task 2 on. `LaunchSpec` (Task 3) gains `Resume`/`ResumedFrom` in Task 5; `fakeLauncher.Launch(spec)` and `launchCall{…, yolo, resume}` follow. `Engine.await` takes `held func(string)` from Task 4 on. `InjectFor(…, yolo bool)` and `TrustArgsFor(id, func() []string)` (Task 3) are used by `createLocalSession` and `hostagent/hooks.go`. The wire shapes Tasks 1–5 fix (`state`, `needsInput`, `yolo`, `agentSession`, `resumedFrom`, `yoloDefault`, `no_enter`, `run` event, the resume replies, `submit`, `FOLLOW_SIZE`) are what Tasks 6–9 read.
- **Risks to verify first.** Each of the five has its test in its owning task: Task 2 `TestATrustQuestionNeedsInput`, `TestATrustQuestionIsAnsweredByEnter`, Task 4 `TestATrustQuestionHoldsThePrompt`; Task 2 `TestSubmitLeavesTheEnterOutForAPromptRaisedDuringThePause`, Task 4 `TestAHandoffTypedWithoutItsEnterIsNotTypedAgain`; Task 1 `TestAHelloOfZeroFollowsTheSessionsSize`, `TestAPeersHelloOfZeroFollowsTheSize`, Task 6's headless check; Task 5 `TestAReportedIDOfTheWrongShapeIsDropped`, `TestSessionRecipeValidation`; Task 3 `TestYoloRecipeIsValidated`, `TestCreateSessionAppliesTheYoloRecipe`, `TestACrewsYoloIsTheRuns`.
- **What only a person can check** is Task 10's "Open verification (round 4)" list, with Verification steps 4–6 above.
