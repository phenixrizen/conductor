# Round 4: agent session identity and Resume for the thirteen built-in agents

Research only: nothing in the repository changed apart from this file. Date 2026-10-02.

How each claim was checked:
- **Live**: I ran the real CLI with the user's HOME (or a scratch `CODEX_HOME` where noted), in throwaway directories, and read its output, its screen (through a PTY driver) or its session store.
- **Help**: the CLI's own `--help`.
- **Docs/source**: official docs or the vendor repository, cited.
- **Lead**: Codex answering about its own `rust-v0.159.0` source. A lead is confirmed live where marked.

Scripts (`sr_pty.py`, `t_codex_int.py`): evidence captured during the investigation, not kept.

## Per agent

Placeholders: `{id}` is the agent's own session id. `{new}` is a fresh id that Conductor generates.

| Agent (bin) | Set at launch | Capture (when it cannot be set, or changes) | Resume argv (after `Command`) | State restored | cwd requirement | Id shape | Version | Verified |
|---|---|---|---|---|---|---|---|---|
| claude | `--session-id {new}` (UUID; a UUID already used fails with "Session ID … is already in use") | `session_id` in **every** hook payload (SessionStart `source` is startup/resume/clear/compact). Transcript: `~/.claude/projects/<cwd with / and . as ->/<id>.jsonl`. `/clear` switches to a new id (SessionStart `clear`), so keep the **latest** value. | `--resume {id}` (also takes a title, or an absolute `.jsonl` path). `-c` resumes the latest in the cwd. `--fork-session` mints a new id. `--resume X --session-id Y` is refused without `--fork-session`. | The conversation, and the system prompt recorded on the first request until compaction (docs). Permission mode, model and effort come from the new launch: the session was recorded `mode: normal` and resumed showing the user's default "auto mode on". | **None**. An id from another directory resumed in print mode and in the TUI, and the new turns ran in the **new** cwd. The search order is this project and its worktrees, then every project (docs; v2.1.223 and later). | UUID | 2.1.287 | **Live** + help + https://code.claude.com/docs/en/cli-reference |
| codex | **Not possible.** No flag chooses the id or the name (help; lead). | **notify JSON carries `"thread-id"`** (live), alongside `turn-id`, `cwd`, `client` (`codex-tui`/`codex_exec`), `input-messages` and `last-assistant-message`. Hooks (`hooks.json`, now stable) carry `session_id`, with SessionStart `source` startup or resume (live, but only with hook trust; see the findings). `codex exec --json` gives `thread.started.thread_id`. On a clean exit the TUI prints `To continue this session, run: codex resume <uuid>`. Rollout: `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl`. **Catch:** the notify program also fires for a hidden **title-generation thread** with its own thread-id and no rollout. Keep the lowest (earliest UUIDv7) id. | `resume {id}`, a subcommand: `codex resume {id} <flags>`. It accepts `-c`, `-a`, `-s`, `--dangerously-bypass-approvals-and-sandbox` after the id (live). `resume --last`; `fork {id}`. | The conversation. Model, provider and effort come from the thread unless set explicitly. Approval and sandbox come from the current flags (lead; the resumed TUI showed "permissions: YOLO mode" from the flag, live). | **Must match, or a dialog blocks:** "Working directory · resume: 1. Use session directory / 2. Use current directory / 3./4. Always…" (live TUI). `-c tui.resume_cwd="session"` skips it (live); `-C <dir>` is said to as well (lead). `codex exec resume` from another dir ran in the new cwd. | UUIDv7 (`01a0fc72-f99b-7331-…`) | 0.159.0 (daemon 0.160.0) | **Live** + help + lead |
| agy | **Not possible** (help) | Hook payload `conversationId` is a common field of all events (docs). Conductor's agy hooks are install-only. `-p --output-format json` returns `conversation_id` (live). The TUI's `~/.gemini/antigravity-cli/history.jsonl` has `{conversationId, workspace}`. Store: `conversations/<id>.db` (older `.pb`). | `--conversation {id}`; `-c`/`--continue` for the latest | The conversation (live: it repeated the first message) | **None** in print mode: resumed from another dir (live). The TUI is not checked. The workspace trust dialog applies per directory (yolo report). | UUID | 1.2.14 (was 1.2.13) | **Live** (`-p`) + help + https://antigravity.google/docs/hooks/ |
| copilot | `--session-id {new}`: "Resume an existing session or task by ID, **or set the UUID for a new session**" (live) | Hook payload `sessionId` for camelCase events, which Conductor uses (`session_id` for the VS Code-style names) (docs; Conductor's test fixtures already carry `sessionId`). Store: `~/.copilot/session-state/<id>/` (`events.jsonl`, `workspace.yaml` with `cwd`). | `--session-id {id}`: the same flag resumes (live). Also `--resume={id}` (the value needs `=`), live. `--continue` resumes the latest. | The conversation (live: "reply READY" repeated). A `session.resume` event records the new cwd and `alreadyInUse`. The model comes from the new launch. | **None**: resumed from another directory (live), and the context cwd became the new one | UUID | 1.0.59 | **Live** + help + https://docs.github.com/en/copilot/reference/hooks-configuration |
| cursor (`cursor-agent`) | **Mint, not choose**: `cursor-agent create-chat` prints a new empty chat id; then `--resume {id}` (docs; that the TUI opens on an empty minted chat is inferred) | Hook payload `conversation_id` on every hook, `session_id` on start/end, `transcript_path`. Conductor's cursor hooks (install-only) receive it. | `--resume {id}`; `--continue`; `resume` (latest) | The conversation and sub-agent checkpoints; the model probably (docs). The mode is not documented. | Since 2026-07-06 `--resume` finds chats from all workspaces (docs). Whether the cwd switches is undocumented: keep the cwd. | UUID | docs to 2026-08-26 (not installed) | Docs: https://cursor.com/docs/cli/reference/parameters , /docs/agent/hooks , /docs/cli/changelog |
| opencode | **Not possible** (no flag; `POST /session` takes no id) | Plugin events carry `properties.sessionID`. Conductor's plugin already handles `session.idle`, which has it; child sessions (opencode's sub-agents) have their own ids, so take the root from `session.created` where `info.parentID` is absent (inferred). On exit the TUI prints `Continue opencode -s <id>`. Store: SQLite `~/.local/share/opencode/opencode.db`. | `--session {id}` (`-s`); `-c` for the latest; `--fork` copies to a new id | The messages. Agent/model on the session record; picking up the model is inferred. | The id lookup is global (source), and the TUI runs in the launch cwd (or `[project]`). Keep the cwd. | `ses_` + 12 hex + 14 base62 | v1.18.34 (not installed) | Docs + source: https://opencode.ai/docs/cli/ , anomalyco/opencode `packages/opencode/src/cli/cmd/tui.ts`, `packages/schema/src/session-id.ts` |
| pi | `--session-id {new}` opens that id in this project **or creates it** (warns on stderr). Custom ids match `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`. The file is written lazily, at the first turn. | Extension `ctx.sessionManager.getSessionId()` and the `session_start` event (`reason` startup/new/resume/fork). `/new`, `/resume` and `/fork` change the id mid-process, so keep the latest. Tool env `PI_SESSION_ID`. | `--session-id {id}` (same flag). `--session <abs .jsonl>`; `-c` latest in the cwd. | The message tree, model and thinking level. **Not** extensions: Conductor's `--extension` is injected again at every launch, which is fine. | **Must match**: sessions are per cwd, and `--session-id X` elsewhere silently creates a new X | UUIDv7 by default | 1.0.0 (not installed) | Source + docs: github.com/earendil-works/pi `packages/coding-agent/docs/cli.md`, `src/core/session-manager.ts` |
| omp | **Not possible**. `--resume /abs/new.jsonl` picks the file but not the id. | Extension `ctx.sessionManager.getSessionId()`, and `session_start`/`session_switch`. Store: `~/.omp/agent/sessions/<enc-cwd>/<ts>_<uuid>.jsonl`. | `--resume {id}` (id or prefix; aliases `-r`, `--session`). `-c` uses a per-terminal breadcrumb. | Transcript, model and thinking. Runtime flags (`--yolo`, `-e`) are not kept (inferred), so pass them again. | **None**: omp chdirs to the session's recorded cwd itself, and prompts if that directory is gone. **Watch:** the `autoResume` setting makes a plain `omp` continue the last session. | UUIDv7 | 18.4.10 (not installed) | Source + docs: github.com/can1357/oh-my-pi `docs/cli-reference.md`, `docs/session-operations-export-share-fork-resume.md` |
| aider | No id. The handle is a file: `--chat-history-file <path>` (env `AIDER_CHAT_HISTORY_FILE`); the default is `<git root>/.aider.chat.history.md`. | n/a (path known) | `--restore-chat-history` (+ the same `--chat-history-file`) | **Messages only**, summarized if long. Not the files in the chat or the model (`/save` + `--load` for files). | Via the default path: same git root | path | 0.86.2 (not installed) | Docs + source: https://aider.chat/docs/config/options.html , Aider-AI/aider `aider/coders/base_coder.py` |
| goose (Block) | `session --name {new}` creates a **new** session even if the name exists (names are not unique; a uuid-bearing name is effectively unique). `--session-id` is rejected without `-r`. | Hook payload `session_id` and `working_dir`. Env `AGENT_SESSION_ID` for extensions and shell. Banner `<id> · <cwd>`. Store: `~/.local/share/goose/sessions/sessions.db`. | `session --resume --name {id}` or `session --resume --session-id {id}`. `--fork` copies. | Messages, the saved extensions and provider/model. **Not the mode**: `GOOSE_MODE` is read again, so the yolo env must be set again. | A different cwd prompts "switch back to the original working directory?" (default yes): **keep the cwd** | `YYYYMMDD_N` (`^\d{8}_\d+$`) | 1.53.0 (the `goose` on PATH here is the SQL migration tool) | Source + docs: github.com/aaif-goose/goose `crates/goose-cli/src/cli.rs`, `session/builder.rs`; https://goose-docs.ai/docs/guides/sessions/session-management |
| amp | **Mint, not choose**: `amp threads new` prints a new thread id (SDK source) | Plugin events (`session.start`, `agent.end`, `tool.result`, …) carry `event.thread.id`; Conductor's plugin would forward it. Threads are server-side; no local store. | `threads continue {id}`, a subcommand | The whole thread, kept server-side. `--mode` is ignored on continue. | Not documented (threads are not tied to a device). Keep the cwd. | `T-<uuid>` | SDK 0.1.0-20260918 (not installed) | Docs + SDK: https://ampcode.com/docs/threads , /docs/cli , /docs/plugin-api ; unpkg @ampcode/sdk |
| dsh | Not via a flag (`agents[].sessionId` in a `--patch` overlay; unverified) | Plugin `session/created` etc.; `~/.dsh/sessions/--<cwd>--/<id>/` | `--profile tui --resume {id}`, but the TUI profile is not in the open repo (archived notes only) | Transcript, title, todos (notes) | Keep the cwd; `DSH_PERMISSION_MODE` must be set again | opaque (`session-<uuid>`) | 0.2.0-rc.2 (not installed) | Source/notes only: github.com/deepseek-ai/deepseek-harness `apps/cli/reference/README.md`; **unverified** |
| shell | n/a | n/a | none: a plain relaunch (a new login shell in the same cwd) | none | n/a | n/a | n/a | n/a |

**Re-passing flags** (all agents): yolo, mode and permission flags are per launch everywhere. Resume must re-apply the same yolo recipe and the adapter's injection.
- Claude: `--settings`.
- Codex: `-c notify=…`, which works after `resume {id}` (live).
- pi: `--extension`.
- aider: env.

Positional prompts in the original `args` (`claude "query"`, `codex "query"`, `copilot -i "…"`) would be **sent again** if re-passed. Crew members never carry one (their prompt is typed in), but ad-hoc sessions can.

## Live check results

**Claude Code 2.1.287** (scratch dirs; real HOME; print mode cost about $0.37 + $0.29 + $0.02 on the user's default model):
1. `claude --session-id 3f80c8bd-… -p "reply READY"` printed `READY`. The transcript is at `~/.claude/projects/-tmp-…-claude-live/3f80c8bd-….jsonl` (the project directory is the run's throwaway directory, `/` as `-`).
2. `claude --resume 3f80c8bd-… -p "what did I ask"` answered "You asked me to reply with the word "READY"." The same file was appended to under the same `sessionId`.
3. From **another directory**, `--resume` of the same id worked. The new turn recorded `cwd` = the new directory and was still appended to the original project's file.
4. Interactive (PTY, Conductor-like env allowlist, `--settings` with dump hooks):
   - `--session-id U3` with **no prompt** then exit. SessionStart (`source: startup`, `session_id: U3`) and SessionEnd fired, but **no transcript was written**. `claude --resume U3` then printed `No conversation found with session ID: …` and quit.
   - Interactive `--resume <id>` from another directory loaded the 3 earlier turns. SessionStart fired with `source: resume` and the same `session_id`, so `--settings` hooks fire on resume.
5. Errors (free):
   - Reusing an id: `Session ID … is already in use`.
   - A bad UUID: `Invalid session ID. Must be a valid UUID.`
   - `--resume X --session-id Y`: refused without `--fork-session`.
   - `--resume 'foo bar' -p`: "not a UUID and does not match any session title".
6. Gotcha: launched from inside a Claude Code session, the child showed "Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker". Conductor's `pty.BuildEnv` allowlist drops that variable, so server sessions are safe. A `CONDUCTOR_ENV_PASSTHROUGH` that lets `CLAUDE_CODE_*` through would silently make every session unresumable.

**Codex CLI 0.159.0**:
1. `codex exec --json … "reply READY" < /dev/null` (without the `/dev/null` it waits on stdin) printed `{"type":"thread.started","thread_id":"01a0fc72-f99b-7331-aec3-818001309536"}`. The injected notify program received `{"type":"agent-turn-complete","thread-id":"01a0fc72-…","turn-id":…,"cwd":…,"client":"codex_exec",…}`, the same id. Rollout: `~/.codex/sessions/2026/10/02/rollout-…-01a0fc72-….jsonl`.
2. `codex exec resume 01a0fc72-… "what did I ask"` **from another directory** answered `You asked me: "reply READY"` and reported the new directory as its cwd. Same `thread_id`.
3. Interactive TUI in a scratch `CODEX_HOME` (`--no-daemon`, notify injected):
   - On exit it printed `To continue this session, run: codex resume 01a0fc76-3ab9-…`.
   - notify fired **twice**: the main thread `01a0fc76-3ab9-…` and a hidden title thread `01a0fc76-4817-…`, whose input was "Generate a concise, single-line task title…" and which has no rollout file.
4. `codex resume <id>` in **another** directory stopped at the "Working directory · resume" dialog. In the **recorded** directory with `--dangerously-bypass-approvals-and-sandbox -c notify=… -c tui.notification_method="bel"` there was no dialog: it showed the history, "permissions: YOLO mode", and accepted the injected flags after the id. `-c tui.resume_cwd="session"` from another directory: no dialog.
5. Hooks:
   - A `hooks.json` in the scratch `CODEX_HOME` fired **nothing** under `codex exec` until `--dangerously-bypass-hook-trust`. Then SessionStart (`session_id` = thread id, `source: resume`), UserPromptSubmit and Stop arrived, all with `session_id`.
   - In the TUI the same file raised "**Hooks need review**: 3 hooks are new or changed … 1. Review hooks 2. Trust all and continue 3. Continue without trusting".

**Copilot 1.0.59** (2 premium requests, ~2.3 credits each):
- `copilot --session-id <new uuid> -p "reply READY"` gave `READY` and created `~/.copilot/session-state/<uuid>/`.
- `--session-id <same>` from another directory resumed: a `session.resume` event with the new cwd, `alreadyInUse:false`. The haiku model answered badly because a user skill intervened.
- `--resume=<uuid>` in the original directory: "reply READY" (correct).
- The repo-level `.github/hooks/*.json` dump did not fire in `-p`, so the hook `sessionId` is not live-checked.

**agy 1.2.14**: `agy -p "reply READY" --output-format json` gave `{"conversation_id":"94395bec-…","status":"SUCCESS",…}`. `agy --conversation 94395bec-… -p "repeat my first message"` from another directory quoted "reply READY".

**What the CLIs wrote outside the throwaway directories:**
- Claude: `~/.claude/projects/-tmp-…-claude-live/3f80c8bd-….jsonl` (the interactive no-prompt run wrote no transcript).
- Codex: `~/.codex/sessions/2026/10/02/rollout-…-01a0fc72-….jsonl`, a `session_index.jsonl` line and its sqlite state. `~/.codex/config.toml` is unchanged (md5 checked); the TUI runs used a scratch `CODEX_HOME`, whose auth symlink is removed.
- Copilot: `~/.copilot/session-state/0cf49f77-…/` and `session-store.db` rows.
- agy: `~/.gemini/antigravity-cli/conversations/94395bec-….db` and `brain/94395bec-…`.
- The yolo research left an old app-server daemon from its throwaway HOME `fakehome2` running (pid 2180217).

## Findings that touch existing code

1. **Codex's title thread fires notify** (live). `MapCodex` maps every `agent-turn-complete` to `needs_input` with the last message. The title thread's report ("Reply READY") arrives seconds after the first prompt, while a long main turn is still running, so the tile likely shows a false "needs input". This is inferred from the timing: in the tiny live run the title notify came second. The fix rides on capture: once the main thread id is known, ignore notifies for other thread ids.
2. **Codex hooks need trust.** Conductor's `Install` writes `~/.codex/hooks.json`. Codex 0.159 skips untrusted hooks, and its TUI then opens a "Hooks need review" dialog. A crew member's prompt would be typed into that dialog. This belongs with the trust work of the yolo plan. Inline `-c hooks.<Event>=[…]` is subject to the same trust (lead).
3. Claude resumes **cross-directory**, but tools then run in the new cwd. Conductor should always resume in the stored cwd anyway.

## Model proposal (one page)

**Catalog** (`catalog.Agent`, next to `Yolo`): `Session *SessionRecipe` (`json:"session,omitempty"`):

```go
type SessionRecipe struct {
    StartArgs  []string `json:"startArgs,omitempty"`  // sets the id; "{id}" is a whole element, filled with a fresh id
    NewID      string   `json:"newId,omitempty"`      // "uuid" | "name" (cdr-<uuid>) – how Conductor mints {id}
    IDFrom     string   `json:"idFrom,omitempty"`     // capture: "hook" (mapper field) | "exit" (screen line) | "" (set only)
    IDPolicy   string   `json:"idPolicy,omitempty"`   // "latest" (default) | "lowest" (codex: earliest UUIDv7)
    ResumeArgs []string `json:"resumeArgs"`           // "{id}" placeholder, whole element
    IDPattern  string   `json:"idPattern"`            // anchored RE2, first char alnum, ≤128 bytes
    NeedsCwd   bool     `json:"resumeNeedsCwd"`
    Note       string   `json:"note,omitempty"`
}
```

Built-ins:

| Agent | startArgs | idFrom / policy | resumeArgs | needsCwd |
|---|---|---|---|---|
| claude | `--session-id {id}` (uuid) | hook `session_id` / latest | `--resume {id}` | false (true in practice) |
| codex | none | hook `thread-id` (notify) / lowest; exit line `codex resume <uuid>` overrides | `resume {id} -c tui.resume_cwd="session"` | true |
| copilot | `--session-id {id}` (uuid) | hook `sessionId` / latest | `--session-id {id}` | false |
| cursor | none | hook `conversation_id` / latest | `--resume {id}` | true |
| agy | none | hook `conversationId` / latest | `--conversation {id}` | true |
| opencode | none | plugin `sessionID` (root only) / latest | `--session {id}` | true |
| pi | `--session-id {id}` (uuid) | plugin `getSessionId()` / latest | `--session-id {id}` | true |
| omp | none | plugin `getSessionId()` / latest | `--resume {id}` | false |
| goose | `session --name {id}` (name) | none | `session --resume --name {id}` | true |
| amp | none | plugin `thread.id` / latest | `threads continue {id}` | true |
| aider | none (path) | none | `--restore-chat-history` (relaunch-like, same cwd) | true |
| dsh, shell | none: **Relaunch** | | | |

Recipe args sit **right after `Command`**, because codex, goose and amp use subcommands: `Command ++ recipe ++ req.Args ++ yolo.Args ++ adapter extra`. For goose, consider making the built-in `Command` `["goose","session"]`. `validate` holds a recipe to the bounds of `Command` (NUL, 32 elements, 4096 bytes). It also requires `{id}` only as a whole element and compiles `IDPattern` like a signal pattern.

**Capture path.** This is a protocol change in three places.
- `notify.Request` gains `AgentSession string` (`json:"agentSession,omitempty"`, ≤128 bytes, size-limited and tested).
- The mappers fill it from `session_id` / `thread-id` / `sessionId` / `conversation_id` / `conversationId`.
- Plugin adapters (amp, pi, omp, opencode, dsh) pass `--agent-session <id>` to `conductor notify`.
- The server validates the value against the agent's `IDPattern` and applies `IDPolicy` in `session.Local` (`SetAgentSession`). Then it publishes.
- For Codex the same check filters notifies whose thread id is not the main one (finding 1).
- `exit` capture scans the last screen of a cleanly exited codex/opencode session for its "resume" line.

**Session state.** `session.Info` gains:
- `agentSession *AgentSession {id, resumable, source:"set"|"hook"|"exit"}`. It goes into `/api/sessions`, SSE and `protocol.ts`.
- `resumedFrom string`.

`resumable` turns true only on evidence of a turn: the first UserPromptSubmit, Stop or turn-complete report. Claude writes nothing until then, live.

Exited sessions leave the registry after `exitedRetention` (10 min). Resume records therefore go to a bounded `dataDir/agent-sessions.json` through `internal/store`: last 200, 7 days. Each record holds `{sessionId, agentId, name, cwd, args, yolo, crew, agentSession, endedAt}`, and the sidebar's exited section reads it.

**Route.** `POST /api/sessions/{id}/resume` (`requireAdmin`), with body `{args?, cols?, rows?, plain?}` decoded with `DisallowUnknownFields`.
- **Errors:** 404 unknown; 409 `still_running`; 409 `already_resumed` (a live session holds the same agent id: one at a time); 409 `not_resumable` (recipe but no captured id, so the UI offers Relaunch with `plain:true`); 400 `invalid_cwd` (through `resolveCwd`, e.g. a removed worktree).
- **What it does:** it goes through `createLocalSession` with the same agent, name, cwd, args (editable, default the original), env, yolo choice and `CrewRef`. `ResumeArgs` is expanded with the stored id. It answers 201 with the new `Info` (`resumedFrom` set).
- A test goes in `api_test.go`, a client call in `useSessions.ts`, and a row in `docs/protocol.md`.
- Hosted sessions: not in this round. Show the copyable `conductor host --agent X -- <resumeArgs>` instead.

**Crews.** `Engine.ResumeMember(run, member)` launches into the member's **existing** worktree and branch, with no `AddWorktree`, and `m.state.Branch`/`Worktree` unchanged. It repoints `SessionID`, sets the status to running and logs "X resumed". It types **no** prompt, because the conversation already has it. A stopped run reopens.
- `POST /api/runs/{run}/members/{name}/resume` reuses the session route's checks.
- `MemberState` gains `agentSession` so the crew view can resume after the session left the registry.
- `conductor up` always makes a new run. A `conductor up --resume <run>` is deferred.

**UI.** A **Resume** button appears in four places: an ended session's header, the wall tile, the sidebar's exited rows, and a crew member's card. The tooltip shows the agent id and the argv preview.
- Agents without a recipe, or with no captured id, get **Relaunch**, named as such ("starts a new conversation").
- The header shows the agent id (copyable) when known.
- A badge uses the brand palette, never the junction mark.

**Security and bounds.**
- Ids pass `IDPattern` and are anchored with a first alnum character, so an id can never become a flag such as `--dangerously…`. They are at most 128 bytes, contain no NUL, and are substituted only as whole argv elements, never as shell text.
- Recipes are operator data: they apply even with `allowArgs:false`, like yolo.
- The notify token already restricts who can set the id. The agent itself can set it too (through the skill), so the worst case is resuming another of the same user's conversations with the same CLI.
- One live resumed session per agent id.
- The resume store is bounded by count and age, and records hold no secrets (env values are re-derived, not stored).

**Verify before shipping:**
- Copilot hook `sessionId` live with the user-level hooks.
- The cursor `create-chat` → `--resume` TUI.
- opencode root-session filtering.
- amp `threads continue` with `--dangerously-allow-all`.
- goose `session --name` in the current Block CLI.
- agy interactive `--conversation`.
- The timing of Codex's title-thread notify on a long turn.
