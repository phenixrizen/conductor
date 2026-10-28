# Project recipes: a fresh attempt worktree made usable, predictably

Program item EN1 (the Program, section 4.3). Main at b6d017d (PR #381,
2026-10-28). A plan reviewed before its code; the PRs below are cut into
issues after the review.

The words used here. Yardmaster is Conductor's agent harness (#334): a crew
with tasks launches a *managed run*, whose engine (`internal/crew`)
dispatches each task as an *attempt*, a fresh member session in a git
worktree of its own, `<cwd>/.conductor/worktrees/<run>/<task>.<n>`, and
decides a submitted result by its *checks*, run in a detached *checkout* of
the result commit, `<worktree>.check`. A *tree* below is either of those two
directories. A *project recipe* is what this plan adds: what makes a fresh
tree of a project usable, how it is checked and how it is cleaned up. It is
not an *agent recipe*: those are the catalog's (`internal/catalog`: an
agent's yolo, trust and session recipes), and docs and UI say "project
recipe" every time (decision D6 of the Program, section 3.1). "Workspace"
names something else in the Program (section 1.2) and is not used here for
a tree.

## Articles it serves

The twelve articles of the Program, section 2.1:

1. No premium code, no licence check: a recipe is crew data or a file in
   the repository, and nothing around it asks who the user is or what is
   registered.
2. The open-source build is fully useful on its own: recipes are one of the
   things the article names; every phase, receipt, route and screen here
   lands in rocksolidlabs/conductor.
3. Paid features gated by hosted dependencies: not touched; nothing here is
   paid and nothing reaches a service.
4. Stop, inspect, revoke, export and approvals never degrade: a stop gains
   work (it ends a tree's services and runs its cleanup) and loses none;
   inspect gains the receipt routes; the receipts live in the ledger file in
   the data directory, so an export of that directory (item C6) carries
   them; revoke and approvals are not touched.
5. Dependencies point one way: no new import anywhere (a readiness probe
   over HTTP uses `net/http`).
6. No feature build tag: none added; `internal/proc` keeps its unix/other
   platform split and the service runner follows it.
7. The free default is what main does today: a crew with `setup` and no
   `recipe` runs exactly as on main, a crew without either as before;
   `Crew.Setup` stays readable and launchable.
8. Outage and lapse: not touched; no provider is involved.
9. `CGO_ENABLED=0`: no new dependency; the ledger stays modernc.org/sqlite
   and the one migration is SQL the existing migrator runs.
10. The repository contract: this plan before code; one issue, branch
    `issue-N` and PR per row of "The PRs"; the checks of every affected
    surface before every push; no wire protocol change (`internal/proto`,
    `web/app/utils/protocol.ts` are not touched, so the three-places rule
    does not apply; `docs/protocol.md`'s HTTP API section is updated with
    each PR that changes a body); no new dependency, so no `make notices`.
11. Names: "project recipe" beside the catalog's "agent recipes"; a tree is
    a worktree or a checkout, never a workspace. The stored event kind
    `attempt.workspace` keeps its name: it is a ledger kind, not a doc.
12. Execution behind one seam: every step and service runs through
    `internal/proc` where the attempt's tree is; the receipt rows are the
    `checks` rows C10 gives a `where` column, and nothing here reads a pid
    past what those rows record today.

## Why and what happens

On main, a managed crew can name `setup` commands (`Crew.Setup`, at most 8
argv arrays, `internal/crew/crew.go`), run in every new attempt worktree
before the member's session (`Engine.dispatch`, `internal/crew/task.go`) and
again in the checks' checkout before the task's checks (`Engine.verify`,
`internal/crew/verify.go`). Four things are wrong with it for real projects:

- A setup command's output is thrown away (`runSetup` passes `io.Discard`):
  a failed install leaves 200 characters of its tail in the attempt's error
  and no log anywhere. The only receipt is an `attempt.setup` or
  `check.setup` event with the exit code.
- A setup that fails in the attempt's worktree closes the attempt as failed
  and counts it (`closeAttempt`): a host without `npm`, or a registry that
  is down, spends a task's three attempts and fails the task with the same
  words a bad commit by the agent would get. Nothing tells the two apart.
- Nothing can hold a port, start a database or a dev server the task
  needs, wait for it, or point a person at it. Two attempts of one run
  share the host's ports and whatever state a service keeps, since a
  worktree isolates files and nothing else.
- Nothing runs after an attempt ends, however it ends. Nothing is checked
  before the first attempt starts.

After this item, a person who opens a crew's Setup tab finds a **Project
recipe** section under the tasks (the Setup commands box is its `setup`
phase), or ticks "read `conductor.recipe.json` from the repository" and
commits that file beside the code, so every commit carries the recipe that
builds it. A recipe says what the host must have (prerequisites, and the
names of environment variables the host passes on), what a fresh tree needs
(setup), which services each tree runs and on which ports, how to know they
are ready, where the preview is, what to verify beyond the tasks' own
checks, and what to clean up.

A run then does this: before the first attempt, the prerequisites run once
in the crew's directory; a missing tool or variable pauses the run's
dispatch with the step named, and nothing is spent. Each attempt's worktree
is made usable before its agent types a line: setup, then the services with
ports of its own, then the readiness probes; the task file names the ports,
the preview URL and the attempt's state directory. The checks' checkout gets
the same, then the task's checks, then the recipe's verify steps. Every step
is a receipt in the ledger: its command, where it ran, which recipe it came
from, its exit, timing, log and SHA-256, read from the run page and the API.
When a tree ends, however it ends (a result taken, a check failed, a cancel,
a stop, a withdrawal, the server shutting down), its services are ended, the
cleanup steps run and its ports are released. A step that fails in a tree the
agent has not touched is the host's or the recipe's problem: the attempt is
not counted, the task says which step failed and why, and the dispatch pauses
for a person. A step that fails in the checkout of the agent's result is the
result's problem, as a failing check is today.

## The changes

### `internal/crew`: the recipe (new file `recipe.go`)

```go
// Recipe is a project recipe: what makes a fresh tree of this project
// usable, how it is checked and how it is cleaned up. Not an agent's
// recipes (internal/catalog: yolo, trust, session).
type Recipe struct {
	Schema        int      `json:"schema,omitempty"`   // 1; required in conductor.recipe.json, absent in a crew's
	File          bool     `json:"file,omitempty"`     // read conductor.recipe.json at the tree's commit; every other field empty then
	Revision      int64    `json:"revision,omitempty"` // the server's: bumped by every save that changes the recipe
	Prerequisites []Check  `json:"prerequisites,omitempty"`
	Env           []string `json:"env,omitempty"`
	Ports         []string `json:"ports,omitempty"`
	Setup         []Check  `json:"setup,omitempty"`
	Services      []Check  `json:"services,omitempty"`
	Ready         []Probe  `json:"ready,omitempty"`
	Preview       string   `json:"preview,omitempty"`
	Verify        []Check  `json:"verify,omitempty"`
	Cleanup       []Check  `json:"cleanup,omitempty"`
}

// Probe is a readiness check: a command run until it exits 0, or a URL on
// this machine fetched until it answers 2xx, every IntervalSeconds, for at
// most TimeoutSeconds.
type Probe struct {
	ID              string   `json:"id"`
	Argv            []string `json:"argv,omitempty"`
	URL             string   `json:"url,omitempty"`
	TimeoutSeconds  int      `json:"timeoutSeconds,omitempty"`  // 1–3600, 60 when 0
	IntervalSeconds int      `json:"intervalSeconds,omitempty"` // 1–60, 2 when 0
}
```

`Crew` gains `Recipe *Recipe` (`json:"recipe,omitempty"`). `Check` is the
existing `{id, argv, timeoutSeconds}`. The limits, enforced by
`Recipe.validate` from `Crew.checkTasks`, the first problem returned as an
`ErrInvalid` the API answers `400 invalid_crew`:

- A recipe needs tasks, as `setup` does. `setup` and `recipe` are exclusive:
  a crew with both is refused ("setup is the recipe's setup phase: put it
  there"). `Crew.effectiveRecipe()` lifts a legacy `setup` into a `Recipe`
  with that one phase, so the engine sees one shape.
- Lists: `prerequisites`, `setup`, `verify`, `cleanup` and `ready` at most 8
  each; `services` at most 4; `env` at most 32 names; `ports` at most 8.
- A step's `id`, `argv` and `timeoutSeconds` are held to a check's bounds
  (`Check.validate`: id `^[a-z0-9][a-z0-9_-]{0,39}$`, at most 32 argv
  entries of 4096 bytes, 8 KiB in all, timeout 1–3600). Defaults when 0:
  prerequisites 60 s, setup 600 s, verify 600 s, cleanup 60 s; a service has
  no timeout (it lives as long as its tree) but its command must start.
- Ids are unique across every list of the recipe and distinct from every
  task's check ids: a step row shares the ledger's `checks` table and its
  key with the task's checks (below), so "tests" cannot be both.
- `env`: names `^[A-Za-z_][A-Za-z0-9_]{0,127}$`, never `CONDUCTOR_*`, never
  a blocked loader (`pty.Blocked`). The recipe holds names; values are the
  host's (below).
- `ports`: names `^[a-z][a-z0-9_]{0,15}$`, unique. Each becomes
  `CONDUCTOR_PORT_<NAME>` (upper case) in a tree.
- Placeholders `${CONDUCTOR_PORT_<NAME>}` and `${CONDUCTOR_STATE_DIR}` are
  expanded literally inside one argv element, inside a probe's URL and
  inside `preview`, and nowhere else (`expand`); a placeholder naming no
  port of the recipe is refused at validation. There is no shell: a step
  that wants one names it in its argv, as checks do today.
- A probe has `argv` or `url`, not both. A URL is `http` or `https` with a
  host of `127.0.0.1`, `localhost` or `[::1]`, at most 2048 bytes;
  `preview` follows the same rule. A probe over HTTP is a `GET` with the
  probe's interval as its timeout; any status under 300 passes.
- `schema` must be 1 in `conductor.recipe.json`; a larger one is refused
  with "needs a newer Conductor"; in a crew's recipe it is left out: a crew
  file is read with `store.LoadLimit`, which decodes strictly
  (`store.DecodeStrict`, unknown fields refused), so an older binary refuses
  a crew with a field it does not know, as for every crew field. `revision`
  in a request body is ignored: the store owns it.
- The recipe as JSON is at most 64 KiB (`maxRecipeJSON`); the file
  `conductor.recipe.json` at most 256 KiB as a blob (`maxRecipeFile`),
  read and decoded strictly with `DisallowUnknownFields`.

The file form (`File: true`): the recipe of a tree is
`<prefix>conductor.recipe.json` as the tree's commit holds it, read with
`git -C <repo> show <commit>:<path>` through `gitcli.Run` (the C locale, no
hooks or filters), its size checked first with `cat-file -s`, its identity
the blob id from `rev-parse <commit>:<path>` (`resolveRecipe(ctx, repo,
commit, prefix)`). A launch with `File` reads and validates the base commit's
file in `Engine.prepare`, so a broken file refuses the launch (`400
invalid_crew`, the file and the commit named) before a run exists. The
attempt's worktree uses the file at the attempt's start commit; the checkout
uses the file at the result commit (so a task that changes the recipe is
checked by the recipe it changed); the prerequisites use the base's. A tree
whose commit has no such file is a named recipe failure ("the commit does
not hold conductor.recipe.json"), attributed as any step of that tree would
be. A recipe's identity is recorded with every step it ran: `crew:<revision>`
or `file:<blob>`.

`internal/crew/store.go`: `Save` compares the incoming recipe with the stored
one as JSON (`revision` left out) and sets `Revision` to the old one plus
one when they differ, else keeps it; a new crew's is 1. A legacy crew's JSON
on disk is unchanged (the existing round-trip test keeps proving it).

### `internal/crew`: trees, steps and receipts (new file `steps.go`)

A `tree` is the engine's record of one directory it makes usable: its kind
(`worktree` or `checkout`), its directory and the crew's working directory
in it (`<dir>/<prefix>`), its commit, the recipe resolved for it, its ports
(`map[string]int`), its state directory, its services (`[]*proc.Service`),
its preview URL once ready, and the attempt it belongs to.

`prepareTree(ctx, r, a, t)` does, in order, stopping at the first failure
with the step named:

1. `recipe.resolved` and `recipe.ports` recorded in one transaction: the
   recipe's identity, the ports allocated (each name: bind `127.0.0.1:0`,
   take the number, close; a number another live tree of this server holds
   is drawn again; the lease is kept on the engine, `e.ports`, until the
   tree ends) and the state directory, `<dir>.state` beside the tree (so
   `<task>.<n>.state` and `<task>.<n>.check.state`, under
   `.conductor/worktrees`, which git ignores), made with `os.MkdirAll`.
2. The `setup` steps, each through `runStep` (below), in the tree's working
   directory.
3. The `services`, each started through `proc.Serve` with the tree's
   environment, its row put as `running` with the process's pid, group and
   start time before the next one starts; a service that exits before the
   tree is ready fails the tree at "service <id>".
4. The `ready` probes, each one row with its tries counted: a command run
   again every interval until it exits 0, or a URL fetched until it answers
   under 300, failing at the probe's timeout with the last try's output as
   the error.
5. The preview URL expanded and set on the tree, in the attempt's state and
   its environment (`CONDUCTOR_PREVIEW_URL`), and `tree.ready` recorded
   before the launch or the first check rests on the tree.

`runStep(ctx, r, a, t, phase, ch) proc.Result` is today's `runSetup` with a
receipt: a `ledger.CheckRow` with `Phase`, `Recipe`, `Ports` and the tree's
directory, put as `started` before the process (event `step.started`),
`running` with the pid once it exists (`step.running`, through
`proc.Spec.Started`, as `verify` does for a check) and `finished` with the
exit, timing, head and tail, the log (at most `ledger.MaxLog`, its SHA-256,
dropped past `MaxRunLogs` as a check's is) and the words of its failure
(`step.finished`). The ledger refusing the row fails the tree at that step,
as it fails a check today ("the ledger could not record the step").

`endTree(r, t, why)` runs on a goroutine of the tree's own, which the run's
stop waits for (`r.trees`, a `sync.WaitGroup` beside `r.checks`), under
`context.Background()` bounded by the steps' own timeouts, never under the
attempt's context, which is ended by then: it stops every service of the
tree (`Service.Stop`: `SIGTERM` to the group, `SIGKILL` after
`proc.ServeGrace`, 5 s; `service.ended` with its exit or signal), runs the
`cleanup` steps through `runStep` (phase `cleanup`), releases the tree's
ports and records `tree.ended` with why and the first cleanup step that
failed, if one did. A cleanup failure is a receipt and a line in the run log
("task build: attempt 2: cleanup <id> exited 1: …"), never a change to the
attempt's state. `r.cleaning` counts the trees ending; `Engine.idle` is false
while it is above zero, so `evict` never forgets a run mid-cleanup, and the
run reports it (`Run.Cleaning`). `endTree` runs exactly once per tree
(`t.ended`, under `e.mu`).

`prerequisites(ctx, r)` runs the recipe's prerequisites once per run, in the
crew's working directory, before the first dispatch (`startManaged`, after
`run.created`), and once per continuation (`startContinued`): each a
`step.finished` event with its exit, timing and the last 2 KiB of output
(no row: the `checks` table keys rows by attempt, and these have none; the
event and the run log are the receipt). It also checks the recipe's `env`
names: each must be set in the server's environment and allowed by the
server's `envPassthrough` (`config.EnvPassthrough`, the host-managed list;
the recipe names what the project needs, the host decides what leaves it);
a name missing on either side is a prerequisite failure naming it ("the
host has no NPM_TOKEN", "NPM_TOKEN is not in the server's envPassthrough").
A failure pauses the run's dispatch (`pauseRecipe`, below) with the step
named; `prerequisites.passed` is recorded before the first
`attempt.reserved` of the run. The values are never recorded.

### `internal/crew/task.go` and `verify.go`: where the phases run

`Engine.dispatch` replaces its setup loop with `prepareTree` on the attempt's
worktree, after the task files are written and `attempt.workspace` is
recorded, before `Launch`. A tree that fails there closes the attempt with
`fail`, stage `<phase> <id>` (`setup deps`, `service db`, `ready web`, or
`recipe` for a recipe that could not be read), and the new argument
`uncounted`: `closeAttempt` then grants the task one more attempt
(`t.extra++`, as a withdrawal does), leaves the task `ready` with the reason
"attempt 2 could not be made usable at setup deps: deps exited 1: …" and
pauses the dispatch with the same words (`pauseRecipe`), in the one
transaction that closes the attempt. The tree's `endTree` runs after the
close. Failures at the stages main has (`worktree`, `task file`, `launch`,
`prompt`) keep today's accounting.

The attempt's session gets the tree's names through `LaunchSpec.Inject`
(`CONDUCTOR_PORT_<NAME>`, `CONDUCTOR_STATE_DIR`, `CONDUCTOR_PREVIEW_URL`;
the API merges them after `crewInject`'s, unfiltered as `pty.Inject`'s are)
and the recipe's `env` names through `LaunchSpec.Allow` (appended to the
passthrough the session builds from `cfg.EnvPassthrough` and the agent's).
The task file (`taskfile.go`) gains `TaskFile.Recipe` (`{source, revision,
digest, ports, preview, stateDir, services}`, at most 2 KiB, never cut: the
save-time bound of 40 KiB on the crew's text leaves room) and "Where you
are" in `task.md` gains the sentences: which recipe made the tree usable,
the services and their ports, the preview, the state directory and that
`CONDUCTOR_PORT_<NAME>` names each port. The attempt's tree ends when the
attempt closes, whatever closed it: `closeAttempt` hands the tree to
`endTree` once the close is committed (for a cancel, with the session's
stop, from `pendingStops`' commit), so a cancelled or withdrawn attempt
mid-step has its step killed by its context and its cleanup run after.

`Engine.verify` makes the checkout a tree too: `prepareTree` after the
checkout's `checkMemberDir` and `MkdirAll` (so what the worker had, the
checks have, with services and ports of the checkout's own), then
`checkoutIntact` once (a tree phase that changes tracked content fails the
attempt at that phase, as setup does today), then the task's checks and the
recipe's `verify` steps as one list, each under the server's check slot and
compared after (`verify` steps are checks of the crew's: rows of phase
`verify`, stage `verify <id>` on failure, the next attempt starting at the
result with the step's tail in its task file as for a failed check), then
`endTree`, then `accept`. A failure in the checkout keeps today's attribution:
it is the result's, counted, and a failure before the checks (`setup`,
`service`, `ready`, `recipe`) is a refused result, the next attempt starting
where the refused one started (#370's rule, unchanged). A continuation's
`dispatchCandidate` verifies through the same path, so a candidate's checkout
gets its recipe.

`pauseRecipe(r, why)` is `pauseDispatch` with words of its own: `r.paused`
set and marked, `r.pausedWhy` kept, the run log's line "dispatch paused:
<why>", the event `run.paused {why}`. An unpause (`PauseRun(false)`) clears
`pausedWhy` and runs the prerequisites again before the pump dispatches,
so a host fixed in between is seen; failing again, it pauses again. The
API's run gains `pausedWhy` and `cleaning`; `Run.Recipe` (`{source,
revision, digest, prerequisites: [{id, state, exit, error, duration}]}`)
says which recipe the run has and how its prerequisites went;
`AttemptState` gains `preview` (in the run's read) and `steps` (in the task's
read alone, `Engine.TaskOf`, as `submitted` is): `[{phase, id, k, state,
exit, timedOut, duration, error, ports, tries, startedAt, finishedAt,
logBytes, logSha256}]`.

### `internal/crew/ledger`: one migration

`Version` becomes the number after main's when the PR is cut: the Program
gives C10 (the Runtime seam's `where` column) version 5; whichever of the two
lands second takes 6. The migration, in `migrations`, is SQL alone and
conservative: `ALTER TABLE checks ADD COLUMN phase TEXT NOT NULL DEFAULT
'check'`, `recipe TEXT NOT NULL DEFAULT ''`, `ports TEXT NOT NULL DEFAULT
''` (a JSON object, name to number), `tries INTEGER NOT NULL DEFAULT 1`.
Every row a version-4 database holds is a task's check, so the defaults
are true of it; no Go backfill. The key stays `(run_id, task_id, n, id,
k)`: `k` counts the runs of one id on one attempt, so a setup step run in
the attempt's worktree (k 1) and again in the checkout (k 2), and again in
a continuation's fresh checkout (k 3), are told apart, and `dir` says which
tree. `CheckRow` gains `Phase`, `Recipe`, `Ports map[string]int` and
`Tries`; `PutCheck` and `Checks` carry them; the phase constants are
`PhaseCheck`, `PhaseSetup`, `PhaseService`, `PhaseReady`, `PhaseVerify`,
`PhaseCleanup`. `Recover` already turns a `started` or `running` row into
`interrupted`; it gains `Recovered.Services`, the rows of phase `service`
so concluded, and the `recovered` event names them with their pid, group
and start time: a service that outlived the owner is reported, never killed
(a pid after a restart is nobody's to signal). A newer database still opens
read-only in an older binary, as today.

### `internal/proc`: a service runner, and the engine's own names

`Spec.Inject map[string]string`: Conductor's own per-tree names, set after
`BuildEnv`'s filtered environment as `pty.Inject`'s are; `Run` refuses any
name but `CONDUCTOR_RUN`, `CONDUCTOR_TASK`, `CONDUCTOR_ATTEMPT`,
`CONDUCTOR_STATE_DIR`, `CONDUCTOR_PREVIEW_URL` and `CONDUCTOR_PORT_<NAME>`
(`injectable`), so no token can come in by this door; `Spec.Env` keeps
refusing every `CONDUCTOR_*` name. `Spec.Allow` already exists and carries
the recipe's `env` names.

`Serve(ctx, Spec) (*Service, error)` starts the command as `Run` does (its
own process group, the same environment, stdin from nothing, the output's
head and tail and the optional log, `Started` once it exists, the exit
watched without reaping) and returns once the process exists; `Service.Done`
is closed when it exits; `Service.Result` is the `Result` after that;
`Service.Stop(ctx)` sends `SIGTERM` to the group and `SIGKILL` after
`ServeGrace` (5 s) or when ctx ends, then reaps and closes the pipe as `Run`
does at an exit. `lifecycle.signal` gains the signal to send; it stays the
one place in `proc` that signals a process (`run_unix.go`), and the non-unix
file's `Serve` returns "no service runner on this platform", as its exit
watch does. The constants: `ServeGrace`; the timeout of a `Serve` spec is
the start's, not a lifetime (a service lives until `Stop`).

### `internal/api`

`runs.go`: `Launch` passes `LaunchSpec.Inject` and `Allow` into
`createSessionRequest` (two new unexported fields); `sessions.go` appends
`allow` to the passthrough and merges `inject` after `crewInject`. New
`recipe.go`: `GET /api/runs/{run}/tasks/{task}/attempts/{n}/steps`
(`requireAdmin`; `{steps: [...]}` as `AttemptState.Steps`, from
`ledger.Checks` without logs, ordered by `started_at`, so a run the engine
has let go still answers) and `GET …/steps/{id}/{k}/log` (`text/plain`, the
row's log, at most 1 MiB, `X-Conductor-Log-SHA256`; `404` for a row without
one, `410 log_dropped` for one dropped past `MaxRunLogs`). Both registered
in `server.go` beside `GET /api/runs/{run}/tasks/{task}`. No new config key:
`envPassthrough` and `checks.maxConcurrent` are reused.

### `web/app`

- `composables/useSessions.ts`: `CrewRecipe`, `CrewProbe`, `RunStep`,
  `RunAttempt.preview` and `.steps`, `RunInfo.recipe`, `.pausedWhy`,
  `.cleaning`; `runSteps(run, task, n)` and `stepLogUrl(run, task, n, id,
  k)`.
- `utils/crews.ts`: `toCrewInput` sends `recipe` (and `setup` only when
  the draft still has one), `liftSetup` turns a loaded crew's `setup` into
  `recipe.setup` for the editor, `recipeProblems` mirrors every rule above
  (ids across lists and against the tasks' checks, the names, the URLs,
  the placeholders, the counts) and `TASK_LIMITS` gains the recipe's
  numbers.
- `components/CrewRecipeEditor.vue` (new), shown by `CrewEditor.vue` under
  the tasks of a managed crew and taking over `CrewTasksTable.vue`'s Setup
  foot: a "Project recipe" heading with one line saying what it is and that
  the catalog's agent recipes are something else; the file switch ("Read
  `conductor.recipe.json` from the repository at each attempt's commit",
  which hides the lists); the phases as `CrewCheckList` lists
  (`what="prerequisite"`, `"setup step"`, `"service"`, `"verify step"`,
  `"cleanup step"`), the probes as a list of their own (id, argv or URL,
  interval, timeout), `env` and `ports` as chip inputs, the preview URL;
  each row's first problem under it, the Save gate and the Launch button's
  reason as the tasks table has them (`taskProblems` gains the recipe's).
- `components/RunAttemptSteps.vue` (new) in `RunTasks.vue`'s attempt detail:
  the attempt's steps in order with phase, id, outcome in words
  (`stepWords` in `utils/tasks.ts`: "exited 0 in 4.2 s", "timed out after
  60 s", "ready after 3 tries", "running since …", "interrupted"), the
  ports and a log link; "Open preview" on a task row and in its → actions
  and the palette row when the attempt has one (a new browser tab: the
  preview is on the server's loopback and is not proxied; the editor's URL
  iframe would be mixed content under TLS).
- `CrewRunHeader.vue`: the `paused` badge carries `pausedWhy` as its title
  and the words beside it; a `cleaning up` badge while `cleaning` is above
  zero; `taskWords` says "ready, the dispatch is paused: <why>" from the
  task's own reason.
- `pages/crews/[[id]].vue`: nothing but the editor; `pages/runs/[run].vue`:
  the palette's task rows gain Open preview.

### Docs, skill and examples

`docs/protocol.md`: the crew body (`recipe`, its rules, `setup` as its
legacy), "Managed runs" (the phases and where they run, the attribution
rule, the receipt routes, the events), the run and task JSON.
`docs/architecture.md`'s crew-runs section and `docs/repository-map.md`
(`internal/crew`: `recipe.go`, `steps.go`; `internal/proc`: `Serve`).
`internal/agents/skill.go`'s "Your task" gains one sentence on the ports,
the preview and the state directory. The example crew run by tasks
(`internal/crew/examples.go`, `example-todo-app-tasks`) gains a recipe
with one prerequisite, `python3 --version`, so the feature is seen on the
first real run (`Seed` skips crews loaded before, so nobody's copy changes).
`docs/features.md` gets the item's entry in the last PR, a bullet beside
the Yardmaster entries, where #380's and #377's are (after the row-11
entry, #350). CLI and MCP change nothing: `conductor
task context` and `task_context` carry the task file, which carries the
rest.

## Events, orderings and invariants

The attempt's states and stages (`internal/crew/ledger/rows.go`) are not
changed; new stage words are `<phase> <id>` for the five recipe phases and
`recipe` for one that could not be resolved, beside today's `worktree`,
`task file`, `setup <id>`, `launch`, `prompt`, `checkout`, `check <id>` and
`withdrawn`. A step row runs `started → running → finished | interrupted`,
as a check's does; a service row stays `running` until `finished` at its
end. The run's `paused` is one bool, as today; `pausedWhy` is in memory and
in the record, and a continuation inherits `paused` as it does now (a person
unpauses; the prerequisites run again).

The order of a tree, each line a transaction committed before the next
side effect, in the run's own order (`transact`):

1. `recipe.resolved {tree, task, attempt, source, revision | digest}` and
   `recipe.ports {tree, task, attempt, ports, stateDir}`, the ports leased
   on the engine in the same change.
2. For each setup step: the row `started` → the process → the row
   `running` with its pid → the row `finished`, then the next step; a
   failure stops the list and is acted on only once its row is committed.
3. For each service: the row `running` with pid, group and start time,
   committed before the next service starts; `service.ended {id, exit,
   signal, why}` with the row `finished` when it ends, whether it was
   stopped or died on its own.
4. For each probe: one row; `tries` counted; `finished` when it passed or
   its timeout passed.
5. `tree.ready {tree, task, attempt, preview}`, then the side effect that
   rests on the tree: the launch (`attempt.launched`) or the first check
   (`check.started`).
6. At the end: the services stopped, each cleanup step as in 2 (phase
   `cleanup`), then `tree.ended {tree, task, attempt, why, cleanupFailed?}`
   with the ports released in that change.

The run's: `run.created` → `prerequisites.passed` (or `step.finished` for
the failing prerequisite and `run.paused {why}` in one transaction) → the
first `attempt.reserved`. An attempt's tree failure: `attempt.failed
{stage, error, state, uncounted: true}` and `run.paused {why}` in one
transaction, the task row's `extra` one more. An unpause:
`run.paused {paused: false}` → the prerequisites again → the pump. Every
payload is bounded as today's are (`writable`: a tail of at most 2 KiB, an
error of at most 2,000 characters through `session.CleanMessage`, lists of
at most 8 names), so none is replaced by the oversized note.

Invariants, each with a test in "Tests per surface":

- A tree's process never outlives the tree's end, except across the
  owner's own death, after which it is reported at the next start
  (`recovered` with its pid and start time) and never signalled.
- A port is leased to at most one live tree of a server at a time, and
  released only in the `tree.ended` transaction. Two trees of one attempt
  (its worktree and its checkout) never share a port or a state directory.
- Every row of a phase other than `check` names the recipe that ran it;
  the identity is recorded (`recipe.resolved`) before the first step of the
  tree starts.
- The checkout's tracked content equals the result commit after the
  recipe's `setup`, `services` and `ready` phases (compared once before
  the first check, as after `setup` today) and after each check and verify
  step; a phase that changed it fails the attempt at that phase.
- An attempt that fails in a recipe phase of its own worktree is never
  counted against its task, and the run dispatches nothing after it until a
  person unpauses; an attempt that fails in a recipe phase of its checkout
  is counted and attributed as a failing check or refused result is today.
- `endTree` runs exactly once per tree, under its own context, after the
  attempt's close is committed; a stop and the server's shutdown wait for it
  as they wait for the checks (`waitFor(ctx, &r.trees)`), and `Run.Outcome`
  set means every attempt is closed, not that every cleanup is done
  (`cleaning` says).
- A continuation reruns the prerequisites, verifies a candidate in a
  checkout with the candidate's recipe, and makes nothing of a tree the run
  it continues left (its services are the recovered rows above).
- The migration changes no existing value's meaning: a version-4 row reads
  as a task's check with no recipe, no ports and one try.

No wire protocol is touched; no replication, so no conflict rules.

## Out of scope

- Removing a worktree, a checkout or a state directory: Conductor removes
  nothing it made (`worktree.go`'s rule stands); cleanup is the recipe's own
  commands, the services ended and the ports released. A `conductor crew
  clean` is a later item.
- A preview reachable from another machine (the phone on the LAN, a shared
  link) and the in-app capture of it: item PV1 builds on `preview`.
- Any sandbox for steps and services: they run as the user, like checks;
  the Boxcar seam (item C10, F) is where that would come.
- Sharing caches between trees: the user's own caches (`HOME` is passed to
  every step and session) are shared as today; nothing inside a tree is
  shared, and no symlinking of `node_modules` or the like is offered.
- Recipes for crews that start by rules (legacy crews): a recipe needs
  tasks, as `setup` does.
- Recipe templates, a recipe catalog, a CLI that runs a recipe by hand.
- Hosted crews: `Crew.Launchable` keeps refusing them; the recipe runner
  lives in the engine, so a host launch site gets it with the engine.
- Linking receipts to tickets (DS2) and the Runtime `where` column (C10).

## Tests per surface

Per `docs/checks.md`. Server: `make lint`, `go test -race -count=1 ./...`,
`make lint-static`; the new crew tests proven with `-race -count=30`. Web:
`npm --prefix web run typecheck`, `npm --prefix web test`, `make test-e2e`
(the new specs alone with `--repeat-each 10` first). No Python, desktop or
workflow change: those suites are not run for these PRs. The tests wait on
the mechanism (a ledger row's state, a file a step writes, `Service.Done`),
never on the clock, and use a bound only as the sum of the code's own caps.

Go, `internal/crew` (`recipe_test.go`, `steps_test.go`, the `managedEngine`
harness with its fake ledger and `sessiontest.FakeProc`):

- The validation table: every limit above, each list's count, ids across
  lists and against a task's checks, a bad env name, a `CONDUCTOR_` name, a
  loader, a bad port name, a placeholder naming no port, a probe with both
  argv and url, a URL off the loopback, `setup` beside `recipe`, a recipe
  without tasks, `schema` 2 in a file, a file over 256 KiB, a crew's recipe
  over 64 KiB; `effectiveRecipe` lifting `setup`; `expand` on argv, URL and
  preview, never splitting an element.
- `store_test.go`: the revision bumped on a changed recipe and kept on an
  unchanged save, a client's `revision` ignored, a legacy crew's JSON
  unchanged.
- The attempt's worktree: setup, services (a shell loop writing its pid to
  the state directory), a probe by file and a probe by URL (an `httptest`
  server on the loopback), the ports as `CONDUCTOR_PORT_*` in a step's
  environment (a step that writes `env` to a file) and in the session's
  (`FakeProc`'s spec), exactly the injectable `CONDUCTOR_` names and no
  token, the task file's section and `task.json`'s `recipe`, every row in
  the fake ledger with its phase, recipe identity, ports and log.
- A setup step that fails in the worktree: the attempt `failed` at `setup
  deps` with `uncounted`, the task `ready` with the words, `extra` one
  more, the run paused with `pausedWhy`, no dispatch until unpause, the
  prerequisites run again at unpause, the next attempt numbered after.
- A setup step that fails in the checkout: counted, the next attempt where
  the refused one started (the existing `refused_result_test.go` cases,
  kept, with rows now).
- A verify step that fails: as a failing check (the next attempt at the
  result with the tail in its task file).
- Cleanup after a result taken, after a failed check, after a cancel
  mid-step (the step killed by the cancel's commit, the cleanup run after
  under its own context), after a stop mid-service (the stop waits for it),
  after a withdrawal; a cleanup step that fails leaves the attempt's state
  as it was and is a row and a log line; `cleaning` above zero holds off
  `evict`; `endTree` once per tree.
- Prerequisites: pass before the first dispatch; a failing one pauses with
  its words and spends nothing; a missing `env` name and one not in
  `envPassthrough` each named; a continuation reruns them.
- The file recipe: read at the start commit for the worktree and at the
  result for the checkout (a result that changes the file is checked by the
  new one), the blob id recorded, a result that breaks the file refused at
  `recipe`, a launch with a broken file at the base refused before a run
  exists, a commit without the file a named failure.
- Ports: two trees in flight never share one; a lease released at
  `tree.ended`; a number held by a live tree drawn again.
- A service left running across a restart (a real ledger, the engine
  ended, `Recover`): the row `interrupted`, `Recovered.Services` naming its
  pid, the process not signalled.
- The task file's section at the size bound beside the largest cuttable
  parts (an extension of the existing largest-of-each-part test).

Go, `internal/crew/ledger`: the migration from a version-4 fixture (the
rows read back as phase `check`, no recipe, no ports, one try, the version
bumped, the `recovered` event unchanged); `PutCheck` and `Checks` with the
new columns; a `service` row concluded by `Recover` and counted.

Go, `internal/proc` (`serve_test.go`): a service started, its `Started`
pid, `Done` on its own exit with the `Result`, `Stop` ending a process that
ignores `SIGTERM` after the grace and one that handles it before, the group
killed with it, `Inject` names accepted and a token name refused, a
`CONDUCTOR_` name in `Env` still refused, the non-unix refusal.

Go, `internal/api` (`recipe_test.go`): the steps route and the log route
(`200`, the hash header, `404`, `410`, admin only, a run the engine let go),
`Launch` carrying `Inject` and `Allow` into the session's environment
(`sessions_test`-style with a stub agent).

vitest: `crews.test.ts` (`recipeProblems` item for item against the Go
table, `toCrewInput` with a recipe and with a lifted `setup`), `tasks.test.ts`
(`stepWords`, `taskActions.preview`, the paused words).

Playwright: `recipe-editor.spec.ts` (a crew saved with every phase, read
back as sent; a Setup commands box of an old crew shown as the recipe's
setup and saved as `recipe`; an id used twice blocks Save with its words;
the file switch saves `file: true` and hides the lists; `tasks-editor.spec.ts`
updated for the moved Setup box); `recipe.spec.ts` (the stub workers as in
`tasks.spec.ts`: a recipe whose setup writes under `.conductor/`, a
`python3 -m http.server` service on `${CONDUCTOR_PORT_WEB}` with a URL probe
and a preview; the Tasks view's steps and their logs; Open preview answering
from the test; the service's pid gone and the `tree.ended` row present once
the task succeeded; a prerequisite on a file the spec creates later: the run
paused with the step named, Resume continuing it; Cancel mid-step followed by
the cleanup receipt). One Playwright run per worktree, `stopStrays` first,
rows found by id.

By hand, which no test reaches: a real project on the WSL box and the Mac
(the live-check pattern of #350): a Node project with `npm ci` as setup,
its dev server as a service with a preview opened in the browser, a
`conductor.recipe.json` committed and a task that edits it; the host broken
on purpose (a variable unset, the registry unreachable) and the run's words
read; a server killed mid-service and the next start's log; a run stopped
from the UI with its cleanup seen in the receipts.

## Boxcar constraints

The eight of the Program, section 2.5:

1. An attempt starts only through `crew.Launcher`: unchanged; a service is
   not an attempt and starts through `internal/proc`, as a check runs.
2. `session.Local` drives a `session.Process`: not touched. `proc` gains a
   signal argument on its one existing signal site and no new one; the
   engine sees a pid only in receipts, as it does for checks.
3. An attempt's context is files relative to its worktree: the task file
   gains the ports, the preview, the state directory and the recipe's
   identity; the state directory is beside the worktree under
   `.conductor/worktrees`; nothing a tree needs lives only in the server's
   memory (the port lease is a server-side guard, the numbers are in the
   file and the rows).
4. The agent talks to the server over the network: not touched.
5. Checks go through `internal/proc`: every step and service does
   (`proc.Run`, `proc.Serve`), with a `Spec` (argv, directory, timeout,
   head and tail), and every receipt carries its directory; the `where`
   column is C10's and these rows will carry it the same way.
6. Platform splits stay platform tags: `Serve` follows `proc`'s unix/other
   split; nothing registers a provider.
7. The ledger's evidence rows assume no native process beyond what checks
   rows record today (a pid, a group, a start time, reported after a crash,
   never acted on).
8. hostagent is a launch site as much as api: not touched; managed runs
   are refused on a host today (`Crew.Launchable`), and the recipe runner is
   the engine's, so a host launch site gets it with the engine, not from
   `internal/api`.

## Open-core checklist

- [x] Article 1: no premium code and no licence check; a recipe is data,
      and the only question asked of the host is whether a tool or a
      variable is there.
- [x] Article 2: project recipes, every phase, receipt, route and screen,
      ship in the open-source repository.
- [x] Article 3: nothing here is gated; nothing here is paid.
- [x] Article 4: stop ends services and runs cleanup; inspect gains the
      receipt routes; export carries the ledger; revoke and approvals are
      untouched; none can lapse.

## The PRs

Each PR is one issue and one branch `issue-N` from main, leaves main
working and green on its own, ticks the checklist above and names its
checks. Sizes are changed lines including tests. `internal/crew` tests use
the existing harness; no PR touches `internal/proto`, `web/app/utils/protocol.ts`,
`.github` or `scripts`.

**PR-1 — crew: the project recipe model.** `Recipe`, `Probe`, the
validation table, `effectiveRecipe`, `expand`, `resolveRecipe` (the file
form read at a commit, bounded, decoded strictly, its blob id), the store's
revision, `Crew.Recipe` with `setup` as its legacy; the crew body in
`docs/protocol.md`; the web types and `toCrewInput`, `liftSetup`,
`recipeProblems` and `TASK_LIMITS`. Nothing runs a recipe yet: the engine
reads `effectiveRecipe().Setup` where it read `Setup`, so behaviour is
main's. Touches `internal/crew`, `docs`, `web/app/composables`,
`web/app/utils`. Tests: `recipe_test.go`, `store_test.go`, `crews.test.ts`.
Depends on nothing. Parallel with nothing (every later PR builds on it).
About 900 lines.

**PR-2 — ledger and engine: every step a receipt, every failure named and
attributed.** The migration (`phase`, `recipe`, `ports`, `tries`),
`CheckRow`'s fields, `Recover`'s services; `steps.go` with `tree`,
`prepareTree` for the `setup` phase alone, `runStep`, `endTree` with no
cleanup steps yet (the ports and services come later; here it records
`tree.ended`), the `uncounted` close and `pauseRecipe`, the prerequisites
(commands only; `env` comes in PR-3), `Run.Recipe`, `pausedWhy`,
`AttemptState.Steps`; the two receipt routes; the task file's section
with the recipe's identity. Touches `internal/crew`, `internal/crew/ledger`,
`internal/api`, `docs`, `web/app/composables` (types only). Tests: the
migration fixture, `steps_test.go` (receipts, the attribution both ways,
the pause and the unpause, the prerequisites), the existing
`refused_result_test.go` and `managed_test.go` cases updated for rows,
`api/recipe_test.go`. Depends on PR-1. About 1,400 lines.

**PR-3 — the host's names, the ports and the state directory.**
`proc.Spec.Inject` with `injectable`, `Spec.Allow` carried from the
recipe's `env`; `LaunchSpec.Inject` and `Allow` through `createLocalSession`;
the `env` checks in the prerequisites; port allocation and the engine's
leases; the state directory; `expand` applied to steps; the task file's
ports and state directory; `CONDUCTOR_PORT_*` and `CONDUCTOR_STATE_DIR` in
steps and sessions. Touches `internal/proc`, `internal/crew`,
`internal/api`, `internal/agents` (the skill's sentence), `docs`. Tests:
`proc/run_test.go` (inject accepted and refused), `steps_test.go` (the
environment of a step and of a session, the leases, the state directory,
the env prerequisites), `api` (Launch's inject and allow). Depends on PR-2.
About 1,000 lines.

**PR-4 — services, readiness and the preview.** `proc.Serve`, `Service`,
`ServeGrace`, the signal argument; the `services` and `ready` phases in
both trees; the preview URL on the tree, the attempt and the session; the
services ended at `endTree`; `Recovered.Services`; a service that dies
before ready. Touches `internal/proc`, `internal/crew`,
`internal/crew/ledger` (the recovery words), `docs`. Tests: `serve_test.go`,
`steps_test.go` (services by file and by URL probe, a dying service, the
pid gone after the tree, the restart case against a real ledger). Depends on
PR-3. About 1,300 lines.

**PR-5 — verify, cleanup, cancellation and the stop.** The `verify` steps
in the checks loop; the `cleanup` phase in `endTree` under its own context;
`r.trees` waited for by the stop and so by the server's shutdown; `cleaning`
on the run and in `idle`; cleanup after a cancel, a withdrawal, a stop and a
failed step; a cleanup failure as a receipt and a log line. Touches
`internal/crew`, `docs`. Tests: `steps_test.go` (every way a tree ends, the
cancel mid-step, the stop mid-service, the shutdown, `cleaning` holding off
`evict`, once per tree), `-race -count=30`. Depends on PR-4. About 900
lines.

**PR-6 — the crew editor's Project recipe section.** `CrewRecipeEditor.vue`,
the probes list, the chip inputs, the file switch, the lifted `setup`, the
Save gate and Launch reason; `tasks-editor.spec.ts` updated for the moved
box. Touches `web/app/components`, `web/app/utils`, `web/e2e`. Tests:
`crews.test.ts` additions, `recipe-editor.spec.ts`. Depends on PR-1.
Parallel with PR-2, PR-3, PR-4 and PR-5 (they touch no component, util or
spec; the shared `useSessions.ts` types are PR-1's and PR-2's, and PR-6
reads PR-1's alone). About 1,100 lines.

**PR-7 — the run page's receipts and preview, the docs and the entry.**
`RunAttemptSteps.vue`, the log link, Open preview on the row, in → and in
the palette, the header's `paused: why` and `cleaning up`, `stepWords`,
`taskActions.preview`; `docs/architecture.md`, `docs/repository-map.md`,
the example crew's prerequisite, the `docs/features.md` entry. Touches
`web/app/components`, `web/app/utils`, `web/app/pages`, `web/e2e`,
`internal/crew` (`examples.go` and its test), `docs`. Tests:
`tasks.test.ts`, `examples_test.go`, `recipe.spec.ts`. Depends on PR-5 and
PR-6. About 1,200 lines.

## Owner decisions

Dated 2026-10-28. None of the Program's open decisions is this item's to
resolve (O10 is DS1's, O11 and O13 are B's, section 3.2). The defaults
below are this plan's; the owner overturns any of them with a status-log
row, in the Program's convention (section 3.1).

- **R1, two forms.** A recipe is either the crew's (`crew.recipe`, edited
  in the UI, revisioned by the server) or a file, `conductor.recipe.json`
  in the crew's directory, read at each tree's commit, so it is versioned
  with the code and a task may change it; a crew has one or the other.
  Reason: `Crew.Setup` is crew data and stays so (article 7); "versioned"
  and "checked in" in the item's words want the file.
- **R2, attribution by tree.** A step that fails in a tree at a commit the
  agent has not touched (the attempt's worktree at its start) is the host's
  or the recipe's failure: not counted, the dispatch paused; one that fails
  in the checkout of the agent's result is the result's, counted, as a
  failing check is. Reason: it is the one line the engine can draw without
  guessing, and it keeps #370's refused-result rule whole.
- **R3, pause, do not retry.** A recipe failure pauses the run's dispatch
  with the step named rather than retrying by itself. Reason: a missing
  tool or a dead registry needs a person, and a loop would spend nothing
  but time; an unpause reruns the prerequisites so a fix is seen.
- **R4, a tree's services live as long as the tree.** The attempt's end
  ends its services and runs its cleanup, also when the task succeeded and
  the session stays idle for inspection. Reason: one predictable rule;
  looking at a result afterwards is PV1's business.
- **R5, the host manages the values.** A recipe names its `env`; a name
  reaches a step or a session only when the server's `envPassthrough`
  allows it, and a value is never stored. Reason: a checked-in file must
  not be able to pull a secret the server's operator did not allow.
- **R6, one table.** Receipts are rows of the ledger's `checks` table with
  a `phase`, in one migration of four columns, not a table of their own;
  step ids are unique across the recipe and distinct from the tasks'
  check ids. Reason: the row is already the receipt shape, `Recover`,
  `Prune` and the readers already handle it, and C10's `where` lands on it
  once for every kind of step.
- **R7, Conductor removes nothing.** Cleanup is the recipe's commands, the
  services ended and the ports released; worktrees, checkouts and state
  directories stay for inspection; a process that outlived the owner is
  reported at the next start and never signalled. Reason: `worktree.go`'s
  rule today, and a pid after a restart is nobody's.
- **R8, Conductor's own names are `CONDUCTOR_*`.** The ports, the state
  directory and the preview reach steps and sessions as
  `CONDUCTOR_PORT_<NAME>`, `CONDUCTOR_STATE_DIR` and
  `CONDUCTOR_PREVIEW_URL`, through an allowlisted `Inject`, as the task's
  names do. Reason: one prefix for everything Conductor puts in an
  environment, and the existing refusal of `CONDUCTOR_*` in a recipe's or
  catalog's own `env` stays what it is.
- **R9, the preview opens in a new tab**, on the server's loopback, not
  proxied and not in the editor's iframe. Reason: mixed content under TLS,
  and PV1 owns the richer preview.
- **R10, no cache sharing.** Nothing inside a tree is shared; the user's
  caches under `HOME` are reached as today. Reason: every sharing scheme
  (symlinks, copies) breaks the isolation a worktree gives; a project that
  needs one names its cache directory in its own tool's environment.
- **R11, the migration's number** is the one after main's when the PR is
  cut; the Program gives C10 version 5, and the second of the two to land
  takes 6.
- **R12, the file's name** is `conductor.recipe.json`, in the crew's
  directory, not under `.conductor/` (a commit tracking `.conductor/` is
  refused by dispatch today, and that stays).

## Acceptance

The item's boundary, in the Program's words: a fresh attempt worktree
becomes usable predictably; a failing step is named; cleanup runs after
failure and cancellation. It is observed, never ticked, on the open-source
build with no cloud account:

1. On a real project with a committed `conductor.recipe.json` (setup,
   two services with ports, a probe, a preview, a cleanup), a managed crew
   launched from the UI: every attempt's task file names the recipe's blob,
   its ports and its preview before the agent's first line; the receipts
   for every phase are read from the run page with their logs.
2. The host broken on purpose (a required variable unset, then the package
   registry unreachable): the run pauses with the prerequisite, then the
   setup step, named in the task's words and the run log; no attempt is
   spent; the host fixed and the run unpaused, it continues.
3. A task cancelled mid-setup and a run stopped mid-service, from the UI:
   the services' pids are gone, the cleanup rows and `tree.ended` are in
   the ledger, the ports are free, and the next launch gets fresh ones.
4. A result that breaks the setup: refused at the named step, counted, the
   next attempt where the refused one started with the refused result named
   in its task file.
5. The server killed mid-service and started again: the next start's log
   names the service's pid as possibly alive; the continuation reruns the
   prerequisites and verifies the candidate with its recipe.

Done means all five seen on the WSL box and 1 to 3 on the Mac (the owner's
machines; the owner runs or watches 1 to 3, and reads the receipts), with
the run recorded in `docs/features.md`. The owner decides R1 to R12 at the
plan's review and merges every PR; CI is the only gate.
