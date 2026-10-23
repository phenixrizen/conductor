# The agent harness: the order of work (#334)

Codex's specification is `agent-harness.md` beside this file; its errata say
where the code differs from it. This is the order, in the shape of the
streams before it: one issue and one PR per row, each cut from `main` once
the one before it merged, a draft PR at the first commit, the checks for every
affected surface then the Codex review then one push, the owner merging. The
owner's decisions of 2026-10-22: SQLite through `modernc.org/sqlite` for the
ledger; slice A then slice B, the rest out; a tracking issue (#334) with one
child per row; this plan merged first.

## What the harness is, in the code's own words

A saved crew with a `tasks` list runs managed; a crew without one runs as
today. A task names its member, its kind (`work` or `review`), its
instructions, the tasks it needs and the checks that decide it. The engine
dispatches a task as an attempt: a fresh member session in its own worktree,
made at the commit its inputs produced, with its context in a file. The agent
reads the file (or calls `task_context`), works, commits, and submits once
(`task_result`). The controller runs the task's checks against that commit;
only a passing run moves the task to `succeeded`. A review's `needs_changes`
starts a bounded repair. Every transition is a row committed before the side
effect it precedes, so a run continues after a restart without repeating
finished work, and what cannot be known is shown as uncertain. A `done`, a
quiet terminal and a `submit_ok` finish nothing.

## Decisions

1. **Managed mode is per crew.** `Crew.Tasks` (omitempty) makes a crew
   managed. Its members have `start: {when: "task"}`, required there and
   refused elsewhere; the editor hides the Starts column. Isolation
   `worktree` is required. A task member's agent has an adapter (no `shell`).
   A config key `crews.managed` (default on) refuses managed launches when
   off and leaves managed records readable. Until the paths exist the server
   refuses on its own: PR 4 makes `Launchable()` refuse a crew with tasks,
   and `StartMember`, `AddMember`, `ResumeMember`, `ResumeRun` and
   `ResumeRecord` refuse a managed run or record (`StartMember` starts any
   pending member regardless of its rule today); PR 5 lifts the launch
   refusal, PR 8 the resume refusals.
2. **A task** is `{id, member, kind, instructions, needs[], checks[{id, argv,
   timeoutSeconds}]}`: ids `^[a-z0-9][a-z0-9_-]{0,39}$` (no dot, so
   `<task>.<n>` is unambiguous and git takes the branch); 128 tasks; 8 checks
   per task, argv within the member-args bounds, a timeout of 1–3600 s (600
   default); needs acyclic; a review needs exactly one work task and has no
   checks; every member has a task; instructions at most 8,000 characters. A
   crew-level `setup[]` (argv lists, the same runner and receipt) runs in
   every new attempt worktree, since a worktree holds tracked files only.
3. **An attempt** is a fresh member session in
   `<cwd>/.conductor/worktrees/<run>/<task>.<n>` on branch
   `crew/<run>/<task>.<n>`, created at its input commit under a
   per-repository mutex. The run's base is read once at launch. One live
   session per member: dispatching a member's next attempt stops its previous
   session; a succeeded task's session stays idle for inspection until Stop
   or the member's next attempt. Four attempts in flight per run, three per
   task. A dispatch begins by reserving the attempt's session slot in the
   registry (`Launcher.Reserve`, released if the launch does not happen):
   `createLocalSession` starts the PTY before the registry's capacity check,
   so a refusal at the cap must come before the worktree, `setup` and the
   process exist; a refusal spends no attempt and makes nothing, and the
   next dispatch takes the next attempt number, never a branch name already
   made. Fan-in: the
   worktree roots at the first need's result, the task file names the other
   results' branches to merge, and submit refuses a HEAD that does not
   descend from all of them.
4. **Context** is `.conductor/task.md` (at most 64 KiB: goal, role, task,
   start point and inputs with commits and summaries, findings to address,
   acceptance, how to submit) and `.conductor/task.json`, written through an
   `os.Root` with their own `.gitignore`, refused if the input commit tracks
   `.conductor/`. The typed line is about 300 bytes and names the file, the
   tool and the command.
5. **A result** (`task_result`, the agent token only) needs a clean tree
   apart from `.conductor/`, HEAD on the attempt's branch descending from
   every input (a review's HEAD equal to its candidate); a summary of at most
   4,000 characters, 20 issues, 50 findings `{severity, path?, line?,
   message}`; a repeated call returns the same receipt; stale, dirty or
   mismatched is a 409 naming the paths. The controller then materializes
   the result commit into a checkout of its own,
   `<cwd>/.conductor/worktrees/<run>/<task>.<n>.check`, detached, with no
   agent or editor attached, runs `setup` there and then the checks: a check
   never reads the worker's live checkout, where the agent or a person at a
   shared terminal could change files while it runs and restore them after.
   The receipt names the checkout's HEAD, compared before and after.
   A failing check fails the attempt; the next one starts from that result
   commit with the log tail in its task file. A review that ran succeeds
   whatever its verdict; `needs_changes` reopens its subject (a fresh attempt
   at the reviewed commit, the findings in its task file) and the review runs
   again on the new result. Repairs are fresh sessions, never resumed
   conversations (most recipes need their cwd, so a resumed conversation
   would sit in the old attempt's worktree; a resume types no prompt; not
   every agent can resume); `repair: resume` may come later as an opt-in.
6. **Done, handoff and ask in a managed run.** `done` is recorded, starts no
   one and completes nothing; one reminder line per attempt on a done without
   a result, typed with `UnlessWaiting`. A `handoff` is noted, never typed.
   `ask` holds the task without spending an attempt. `StartMember`,
   `AddMember`, `ResumeMember` and the self add-member route answer 409
   `managed_run`.
7. **The ledger** (`internal/crew/ledger`, the only package that imports
   `modernc.org/sqlite`): one database `<dataDir>/runs/managed.sqlite`, 0600,
   opened with `journal_mode=WAL`, `synchronous=FULL`, `foreign_keys=ON`, a
   busy timeout, and on darwin `fullfsync=ON` and `checkpoint_fullfsync=ON`;
   one connection, so every transition is one `BEGIN IMMEDIATE … COMMIT`.
   Schema v1 in a `schema_version` table: `runs`, `tasks`, `attempts`,
   `checks` (argv, subject, exit, timing, stable, a log blob of at most 1 MiB
   with its SHA-256 and a 2 KiB tail), `commands` (run, actor, id,
   fingerprint, status, receipt) and `events` (run, a gapless `seq`, kind,
   payload); later `messages` and `checkpoints`. Intents commit before side
   effects (`dispatch` before `Launch`, `deliver` before a typed line,
   `check_started` before exec, an acting command before it acts) and facts
   after observations; a commit runs outside the engine's lock. A failed
   commit undoes the intent and refuses new intents until a commit lands;
   `PRAGMA quick_check` at open, a failure or a newer schema opens read-only
   and managed launches answer 503 `ledger_unavailable`. The snapshot
   `runs/<id>.json` stays the API answer (`Run.Managed` omitempty, legacy JSON
   byte-identical) and is a cache of the database with a revision: the
   journal's save is two seconds late and asynchronous, so at start the
   owner rebuilds every managed snapshot that is missing or older than the
   database's revision, and a snapshot write never regresses that revision.
   `reconcile` skips managed records; `evict` keeps open managed runs. A
   managed run's record and rows are pruned from durable state alone, never
   from engine membership: only once its database state is terminal and no
   continuation names it as its source or imports one of its results, and
   only by the owner, in one transaction after the snapshot; an interrupted
   run awaiting a person and a finished run a continuation still cites both
   stay. Logs are capped at 64 MiB per run. No terminal output, no secrets,
   environment names only.
8. **One owner.** `runs/owner.lock` (flock, 10 s retry): SQLite serializes
   writers but elects no scheduler, and two servers would each "recover" the
   other's runs. Every managed mutation needs the lock: launches, recovery,
   snapshot rebuilds and pruning. When another process holds it, or locks
   are unsupported, managed launches answer 503, legacy runs work, and the
   server's legacy pruning (`Records.Save` on every save) leaves every
   managed record (`Run.Managed` set) out, since the engine that holds those
   runs is the other process. A network or 9p filesystem is outside the
   durability claim; the startup log says so.
9. **Resume is a continuation**, as `ResumeRun` is today: an interrupted
   managed run shows read-only from its snapshot; Resume makes the
   continuation under the existing lease, imports accepted results by
   reference, carries attempt counts, re-verifies a candidate that was
   verifying if HEAD and cleanliness still match, and dispatches the rest.
   Uncertain boundaries (a dispatch with no `launched`, a typed line with no
   `entered`, a command that acts) wait for a person; nothing is retried on
   its own.
10. **Receipts.** Mutations are per-action routes (`POST /api/runs/{run}/
    pause|unpause`, `…/tasks/{task}/retry|cancel|accept`,
    `…/sessions/{id}/task/result`) with `{commandId, expectedRevision}`,
    the id `^[A-Za-z0-9._~-]{1,32}$` keyed by (run, actor); a repeat with the
    same fingerprint returns the receipt, a different one 409
    `command_conflict`; `GET /api/runs/{run}/commands/{id}` answers without
    acting; 4,096 commands per run.
11. **The runner** `internal/proc.Run(ctx, Spec{Argv, Dir, Env, Timeout,
    MaxOutput, Log})`: a process-group kill as the probes have, the
    environment from `pty.BuildEnv` with `CONDUCTOR_*` and tokens stripped,
    `TERM=dumb`, stdin null, 64 KiB of head and 16 KiB of tail in memory, a
    1 MiB log with a SHA-256; `checks.maxConcurrent` (default 1). Checks run
    natively as the user; the editor and the docs say so.
12. **Run outcome.** `state` keeps its four values; a managed run adds
    `outcome: succeeded | failed | cancelled` once every task is terminal,
    and `finished` no longer waits for idle sessions to end (#154's outcome).
13. **The UI of slice A.** A Tasks view (a fourth tab, managed runs only,
    `?view=tasks&task=`): rows with a state badge, the member, attempt n of
    m, blockers in words, check badges, the result commit and summary, and
    the next action (Retry…, Accept anyway… on a failed task with a result,
    Show result, Open session, Copy branch, Cancel…); the header shows the
    task count, the failures, who waits on a person and what runs; the grid
    shows one tile per member's current attempt; the graph gets a Tasks |
    Members switch, one node per task, `needs` edges in a third style; the
    palette's `#` run rows gain task counts and a "Tasks waiting on you"
    group, the run page's `>` group one row per task; the editor gets a
    Coordination select (start rules | tasks) and a tasks table with
    `ArgvInput` chips for checks, validated client-side as the server does.

## Order, and why

| # | Issue | PR | Why here |
|---|---|---|---|
| 0 | #334 | The plan on main: `agent-harness.md` with its errata, this file | Every later PR cites it |
| 1 | #335 | records: never prune a run the engine holds | The one change a legacy crew can see; the ledger's prune rule needs it |
| 2 | | `internal/proc`: a bounded argv runner | No callers yet; its own review |
| 3 | | The managed-run ledger in SQLite: the dependency, schema v1, transactions, the owner lock, receipts, recovery | Inert until managed crews; the storage and dependency review on its own |
| 4 | | A crew can define tasks: schema, validation, `CrewRef`, env, the editor's Tasks table; the server refuses to launch, start, add to or resume a managed crew or run | A managed crew can be saved; nothing runs yet, and the legacy paths cannot run it |
| 5 | | A managed run dispatches tasks into attempt worktrees and takes their results; the launch refusal lifted | The engine core, tested by calling the engine and the real registry |
| 6 | | An agent reads its task and submits its result; a review's findings lead to a bounded repair | Ends slice A's vertical: the stub proves `done` finishes nothing |
| 7 | | The run page shows tasks, their checks and results, and what to do next | Needs 5 and 6's data and routes |
| 8 | | A managed run continues after a restart without repeating finished work; the resume refusals lifted | Closes slice A |
| 9 | | Members of a managed run exchange durable messages | Slice B |
| 10 | | A member reports a blocker and saves a checkpoint | Slice B |
| 11 | | The harness by hand on macOS, Linux and WSL with Claude Code and Codex; the docs | Qualification |

## What each PR does

**1. records** (`internal/crew/record.go`, `run.go`): `Records.Protect
func(Run) bool`; the engine publishes the ids it holds in a `sync.Map`
written under its lock and read without it (the save goroutine holds
`RecordEnds`' mutex across `Save`, and `journalForgotten` takes that mutex
under the engine's lock: a hook that took the engine's lock would deadlock);
protected records do not count toward the limit. Tests: a held run survives
a prune of 501; the deadlock regression.

**2. proc** (`internal/proc`): decision 11, with `run_unix.go` and
`run_other.go`. Tests: the timeout kills the group; leftovers after exit 0
and 3 are killed; head and tail kept over 1 MiB; the environment stripped;
exit codes; cancel is not timeout. No config key yet.

**3. ledger** (`internal/crew/ledger`, `record.go`, `run.go`, `api/server.go`,
`go.mod`, `THIRD_PARTY_NOTICES`, `docs/dependencies.md`): decision 7 and 8;
the dependency pinned at its latest release with its reason in the PR,
`make notices` after `npm ci` in `desktop/`, a `docs/dependencies.md` row,
`make vuln`, the four cross targets built, binary growth and cold build time
measured and named in the PR; `Open`, `Transition`, `Recover`, the owner
lock, receipts and `GET /api/runs/{run}/commands/{id}`, `Run.Managed` with
its revision, the snapshot rebuild at start, the `reconcile` and `evict`
exclusions, managed pruning from durable state by the owner alone and the
non-owner's legacy pruning leaving managed records out. Tests: open and
migrate; a newer schema or a failed `quick_check` opens read-only and a
managed launch answers 503; a transition is committed before `Launch`; an
injected commit error undoes the intent and keeps later facts; the crash
matrix (stop before each side effect, reopen, recover, assert the
conclusion); a crash with a stale snapshot on disk is rebuilt at start and a
later legacy save cannot regress it; recovery concludes once; `events.seq`
gapless under 50 goroutines; receipts; the owner lock across processes and
unsupported locks; a second process without the lock saving 501 legacy
records leaves the owner's managed records alone; an interrupted managed
record and a finished one a continuation cites survive a prune; the log
cap; a WAL checkpoint at shutdown.

**4. definitions** (`crew.go`, `store.go`, `session/info.go`,
`api/sessions.go`, `CrewTasksTable.vue`, `CrewEditor.vue`, `utils/crews.ts`,
`docs/protocol.md`): `Task`, `Check`, `StartTask`, `checkTasks`;
`CrewRef.Task` and `Attempt`; `CONDUCTOR_TASK`, `CONDUCTOR_ATTEMPT`,
`CONDUCTOR_TASK_FILE`; `crews.managed`; `Launchable()` refusing a crew with
tasks and the start, add, member-resume, run-resume and record-resume paths
refusing a managed run or record, until PRs 5 and 8 lift them; the editor's
Coordination select and tasks table; `taskProblems()`. Tests: the
validation table; a legacy crew file unchanged through the store; the env
set only for an attempt; a managed crew refused by launch, `conductor up`,
the self-service crew route, start, add and every resume route; vitest for
`taskProblems`; Playwright `tasks-editor.spec.ts`.

**5. engine** (`internal/crew/task.go`, `run.go`, `worktree.go`, `gitcli`):
decisions 3–6 and 12: `pump` under the engine's lock after every launch,
close, result, unpause, cancel or retry; `dispatch` in order (the registry
slot reserved through `Launcher.Reserve`, the `attempt.reserved` row, the
member's previous session stopped, `AddWorktreeAt`, the task files, `setup`,
`Launch` with the reservation, `attempt.launched`, the typed line,
`attempt.prompted`; a refusal at the slot spends nothing and makes
nothing); `SubmitResult` with its 409 rules; `verify` on a goroutine in a
WaitGroup that `stop` waits on, in the result's own detached checkout, with
`setup` run there first; the `OnActivity` guards;
`Run.tasks`, `MemberState.task` and `attempt`; `runState` by mode and its
web mirror. Tests in `managed_test.go` on a real repository, the fake
launcher, a fake ledger and a `runCheck` seam: the caps; a slot refused
before anything is made; the worktree at the input commit; a dependent
seeing the producer's tree; dirty, stale and mismatched refusals; a file
changed and restored in the worker's checkout during a check leaving the
receipt true to the result commit; `done` completing nothing; handoffs not
typed; a session end failing its attempt; Stop draining checks;
`AddWorktreeAt` in `worktree_test.go`; and in `internal/api`, the
reservation against the real registry at `MaxSessions`.

**6. agent surface** (`internal/api/tasks.go`, `internal/cli/mcp.go`,
`task.go`, `internal/agents/skill.go`, `web/e2e/stub-agent.sh`,
`conductor.e2e.json`, `fixtures.ts`, `docs/protocol.md`, `README.md`):
`GET /api/sessions/{id}/task?page=` and `POST …/task/result` (the agent
token only, no self-service gate, 201 with a receipt, 200 on a repeat); the
admin `GET /api/runs/{run}/tasks/{task}` and the per-action routes;
`task_context` (32 KiB pages) and `task_result` with one minted commandId and
one retry; `conductor task context|result` (loud outside a session); the
skill's "Your task" section; the review gate and the repair dispatch; the
stub's worker mode (reads the task over MCP or the CLI, commits, submits,
reviews `needs_changes` once then `approved`, then reports done); catalog
ids `worker` and `worker-cli`; the failing-once check `["git","cat-file",
"-e","HEAD:stub-fixed.txt"]`. Tests: every 4xx, the repeated receipt, the
bounds, `tasks` in both reads, a `run` event per change; the CLI and MCP
tests; Playwright `tasks.spec.ts` and `tasks-review.spec.ts`. By hand:
whether Codex passes `CONDUCTOR_NOTIFY_*` to its stdio MCP child; the fix
forwards names in its config, never the token on argv.

**7. run page** (`RunTasks.vue`, `CrewRunHeader.vue`, `CrewGraph.vue`,
`utils/taskGraph.ts`, `utils/actions.ts`, `usePaletteRows.ts`,
`runs/[run].vue`, `useSessions.ts`, `protocol.ts`): decision 13; `Action`
gains kind `task`; no new timer. Tests: vitest for the rows' words, the task
graph, the actions and `runState`; Playwright assertions in `tasks.spec.ts`
and rows in `crumbs.spec.ts` and `hscroll.spec.ts`.

**8. recovery** (the resume paths in `run.go`, the ledger, the web):
decision 9, the resume refusals of PR 4 lifted. Tests: a succeeded task
never relaunched; a matching candidate verified with no session; a second
resume refused; a predecessor's record and rows kept while a continuation
cites them; Playwright
`tasks-resume.spec.ts` on its own server restarted on the same data, token
and port.

**9–10. slice B**: durable messages (`POST /api/sessions/{id}/messages`,
`GET …/inbox?after=`, `POST …/inbox/ack`, the tools, 50 per recipient with
429 `inbox_full`, delivery as `deliver` intents typed with `UnlessWaiting`, a
hold while a person types, the legacy handoff as a message); `task_blocked
{reason, message}` as attention and `task_checkpoint {done, remaining,
notes}` tied to HEAD, carried into a continuation's task file.

**11. qualification**: a real crew (a Claude Code builder, a Codex reviewer)
on the Mac, on WSL and on a Linux box, with a check that fails once; findings
into issues; the README's crews section, `docs/architecture.md`'s crew runs
and persistence, `docs/repository-map.md` entries for `proc`, `ledger` and
`task`.

## Tests

Go race tests per PR as listed; `make preflight` on the three machines
before every push; `CONDUCTOR_RENDEZVOUS=0` in every launching test;
`make notices` in PR 3. Playwright after `make web-build build-go`. The
specification's acceptance matrix maps onto PRs 3 (crash, prune, receipts),
5 (done, stale, the dependent's tree, caps), 6 (dirty, review, repair, a
symlinked `.conductor`), 7 (overflow, palette), 8 (restart, uncertain, a
duplicate command) and 9 (an inbox full, a message to an unstarted task).

## Not here

The managed-command wire capability, `internal/execution`, packages and
manifests, a blob store beyond check logs, the supervisor, the remote owner
and a machine selector, transcript normalizers, cost estimates, database
backups, repair by resume, host crews (#152, #162), any Boxcar integration or
stub. #151 (progress reported by the agents) stays its own issue.
