# Project manifests: a fresh attempt worktree made usable, predictably

Program item EN1 (the Program, section 4.3). Main at b6d017d (PR #381,
2026-10-28). A plan reviewed before its code; the PRs below are cut into
issues after the review.

The words used here. Yardmaster is Conductor's agent harness (#334): a crew
with tasks launches a *managed run*, whose engine (`internal/crew`)
dispatches each task as an *attempt*, a fresh member session in a git
worktree of its own, `<cwd>/.conductor/worktrees/<run>/<task>.<n>`, and
decides a submitted result by its *checks*, run in a detached *checkout* of
the result commit, `<worktree>.check`. A *tree* below is either of those two
directories. A *project manifest* is what this plan adds: what makes a fresh
tree of a project usable, how it is checked and how it is cleaned up. It is
not an *agent recipe*: those are the catalog's (`internal/catalog`: an
agent's yolo, trust and session recipes), and docs and UI say "project
manifest" every time (decision D6 of the Program, section 3.1, under the
name the owner gave it on 2026-10-29; it was "project recipe"). "Workspace"
names something else in the Program (section 1.2) and is not used here for
a tree.

## Articles it serves

The twelve articles of the Program, section 2.1:

1. No premium code, no licence check: a manifest is crew data or a file in
   the repository, and nothing around it asks who the user is or what is
   registered.
2. The open-source build is fully useful on its own: manifests are one of the
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
7. The free default is what main does today: a crew without a manifest or
   `setup` runs as before, untouched; a crew with `setup` and no `manifest`
   keeps running, readable and launchable as it is stored, its `setup`
   lifted into the manifest's `setup` phase, so it gains what the item adds
   (its output kept, every run of it a receipt, a failure in a fresh
   worktree uncounted and named) and loses nothing. "The changes" says
   what a legacy `setup` keeps (its ids' rule) and "The PRs" names the
   existing tests that change with it.
8. Outage and lapse: not touched; no provider is involved.
9. `CGO_ENABLED=0`: no new dependency; the ledger stays modernc.org/sqlite
   and the one migration is SQL the existing migrator runs.
10. The repository contract: this plan before code; one issue, branch
    `issue-N` and PR per row of "The PRs"; the checks of every affected
    surface before every push; no wire protocol change (`internal/proto`,
    `web/app/utils/protocol.ts` are not touched, so the three-places rule
    does not apply; `docs/protocol.md`'s HTTP API section is updated with
    each PR that changes a body); no new dependency, so no `make notices`.
11. Names: "project manifest" beside the catalog's "agent recipes"; a tree is
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
manifest** section under the tasks (the Setup commands box is its `setup`
phase), or ticks "read `conductor.manifest.json` from the repository" and
commits that file beside the code, so every commit carries the manifest that
builds it. A manifest says what the host must have (prerequisites, and the
names of environment variables the host passes on), what a fresh tree needs
(setup), which services each tree runs and on which ports, how to know they
are ready, where the preview is, what to verify beyond the tasks' own
checks, and what to clean up. The item's "preview commands" are a pair of
those: the command that serves the preview is a `services` entry, and
`preview` is the URL it answers on, expanded from its port
(`http://127.0.0.1:${CONDUCTOR_PORT_WEB}`).

A run then does this: before the first attempt, the prerequisites run once
in the crew's directory; a missing tool or variable pauses the run's
dispatch with the step named, and nothing is spent. Each attempt's worktree
is made usable before its agent types a line: setup, then the services with
ports of its own, then the readiness probes; the task file names the ports,
the preview URL and the attempt's state directory. The checks' checkout gets
the same, then the task's checks, then the manifest's verify steps. Every step
is a receipt in the ledger: its command, where it ran, which manifest it came
from, its exit, timing, log and SHA-256, read from the run page and the API.
When a tree ends, however it ends (a result taken, a check failed, a cancel,
a stop, a withdrawal, the server shutting down), its services are ended, the
cleanup steps run and its ports are released. A step that fails in a tree the
agent has not touched is the host's or the manifest's problem: the attempt is
not counted, the task says which step failed and why, and the dispatch pauses
for a person. A step that fails in the checkout of the agent's result is the
result's problem, as a failing check is today; one that fails in a worktree
at a checkpoint the agent saved, a commit of its own that nothing verified,
refuses the checkpoint as a bad result is refused, and the next attempt
starts where the checkpointed one did.

## The changes

### `internal/crew`: the manifest (new file `manifest.go`)

```go
// Manifest is a project manifest: what makes a fresh tree of this project
// usable, how it is checked and how it is cleaned up. Not an agent's
// recipes (internal/catalog: yolo, trust, session).
type Manifest struct {
	Schema        int      `json:"schema,omitempty"`   // 1; required in conductor.manifest.json, absent in a crew's
	File          bool     `json:"file,omitempty"`     // read conductor.manifest.json at the tree's commit; every other field empty then
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

`Crew` gains `Manifest *Manifest` (`json:"manifest,omitempty"`) and
`ManifestRevision int64` (`json:"manifestRevision,omitempty"`): the server's
count of the crew's manifest changes, on the crew and not inside the optional
`Manifest`, so a save that removes the manifest keeps it (the store, below).
`Check` is the existing `{id, argv, timeoutSeconds}`. The limits, enforced by
`Manifest.validate` from `Crew.checkTasks`, the first problem returned as an
`ErrInvalid` the API answers `400 invalid_crew`:

- A manifest needs tasks, as `setup` does. `setup` and `manifest` are exclusive:
  a crew with both is refused ("setup is the manifest's setup phase: put it
  there"). `Crew.effectiveManifest()` lifts a legacy `setup` into a `Manifest`
  with that one phase, so the engine sees one shape and a legacy `setup`
  gets the receipts and the attribution below (article 7).
- Lists: `prerequisites`, `setup`, `verify`, `cleanup` and `ready` at most 8
  each; `services` at most 4; `env` at most 32 names; `ports` at most 8.
- A step's `id`, `argv` and `timeoutSeconds` are held to a check's bounds
  (`Check.validate`: id `^[a-z0-9][a-z0-9_-]{0,39}$`, at most 32 argv
  entries of 4096 bytes, 8 KiB in all, timeout 1–3600). Defaults when 0:
  prerequisites 60 s, setup 600 s, verify 600 s, cleanup 60 s; a service has
  no timeout (it lives as long as its tree) but its command must start.
- Ids are unique across every list of the manifest and distinct from every
  task's check ids: a step row shares the ledger's `checks` table and its
  key with the task's checks (below), so "tests" cannot be both. A legacy
  `setup` keeps main's rule (its ids unique within `setup`; a task's check
  may share one: the rows are then told apart by `k`, which the attempt
  allocates across phases, below, and `phase` says which is which), so no
  stored crew becomes invalid; the editor's lift shows such a clash as the
  row's problem, Save held until the id is changed, and the crew as stored
  stays launchable.
- `env`: names `^[A-Za-z_][A-Za-z0-9_]{0,127}$`, never `CONDUCTOR_*`, never
  a blocked loader (`pty.Blocked`). The manifest holds names; values are the
  host's, and whether a name may leave the host is the server's
  `envPassthrough`'s, checked for every manifest resolved (`allowedEnv`,
  below), never the manifest's own say.
- `ports`: names `^[a-z][a-z0-9_]{0,15}$`, unique. Each becomes
  `CONDUCTOR_PORT_<NAME>` (upper case) in a tree.
- Placeholders `${CONDUCTOR_PORT_<NAME>}` and `${CONDUCTOR_STATE_DIR}` are
  expanded literally inside one argv element, inside a probe's URL and
  inside `preview`, and nowhere else (`expand`); a placeholder naming no
  port of the manifest is refused at validation, and so is any placeholder
  in a prerequisite's argv: the prerequisites run once per run in the
  crew's working directory before any tree exists, with no ports and no
  state directory to expand, so `expand` is never applied to them, and a
  `${CONDUCTOR_STATE_DIR}` there, which would otherwise be passed on as
  a literal, is refused with "a prerequisite runs before any tree: no
  placeholders" (the editor's `manifestProblems` says the same under the
  row). There is no shell: a step
  that wants one names it in its argv, as checks do today. The bounds above
  hold on the expanded form too: `resolveTree` expands every argv, URL and
  `preview` of the manifest once, when the ports and the state directory are
  known, and holds each expanded argv element to 4096 bytes and each argv
  to 8 KiB, an expanded URL to 2048 and the expanded `preview` to 512; one
  past its bound fails the tree at `manifest` ("preview expands to 61,440
  bytes, more than 512"), attributed by the tree's rule, before anything is
  written or run. A port expands to at most five digits, shorter than its
  placeholder, but the state directory is a path of up to the platform's
  limit, so a `preview` of twenty `${CONDUCTOR_STATE_DIR}` (457 bytes,
  valid) could expand past 60 KiB were the template alone bounded, and
  defeat the task file's uncut section (`taskfile.go` would refuse the
  files at that stage, a failure counted and named `task file`), the
  receipt's `url` column and a step's argv row.
- A probe has `argv` or `url`, not both. A URL is `http` or `https` with a
  host of `127.0.0.1`, `localhost` or `[::1]`, at most 2048 bytes;
  `preview` follows the same rule at most 512 bytes (a loopback URL needs
  no more, and the task file carries it uncut). A probe over HTTP is one
  `GET` by an `http.Client` of the probe's own (`probeClient`): the probe's
  interval as its `Timeout`, `CheckRedirect` answering
  `http.ErrUseLastResponse` so no redirect is ever followed (a `3xx` is
  the try's status, not under 300, and the probe tries again: a service,
  or a checked-in manifest at a result commit, answering `302 Location:
  http://<anywhere>` makes the server fetch nothing off the loopback), and
  the body closed unread, so nothing of it is kept or bounded by its size;
  any status under 300 passes.
- `schema` must be 1 in `conductor.manifest.json`; a larger one is refused
  with "needs a newer Conductor"; in a crew's manifest it is left out: a crew
  file is read with `store.LoadLimit`, which decodes strictly
  (`store.DecodeStrict`, unknown fields refused), so an older binary refuses
  a crew with a field it does not know, as for every crew field.
  `manifestRevision` is the server's: a request body that carries it is
  refused as an unknown field, as one that carries `id`, `createdAt` or
  `updatedAt` is (`crewInput`'s rule, `internal/api/crews.go`, below), and
  `toCrewInput` never sends it, as it never sends `id`; the store sets it
  on what it saves whatever the `Crew` value handed to `Update` carries.
- The manifest as JSON is at most 64 KiB (`maxManifestJSON`); the file
  `conductor.manifest.json` at most 256 KiB as a blob (`maxManifestFile`),
  read and decoded strictly with `DisallowUnknownFields`.

The file form (`File: true`): the manifest of a tree is
`<prefix>conductor.manifest.json` as the tree's commit holds it, read with
`git -C <repo> show <commit>:<path>` through `gitcli.Run` (the C locale, no
hooks or filters), its size checked first with `cat-file -s`, its identity
the blob id from `rev-parse <commit>:<path>` (`resolveManifest(ctx, repo,
commit, prefix)`). A launch with `File` reads and validates the base
commit's file in `Engine.prepare`, so a broken file refuses the launch (`400
invalid_crew`, the file and the commit named) before a run exists. The base
is pinned first: `LaunchNamed` reads HEAD once for a managed crew
(`headCommit`, which `startManaged` reads today, moved before `prepare`) and
hands it down, `prepare(ctx, c, base)` validating the file at that commit
and `startManaged` recording it as `r.base` instead of reading HEAD a second
time, so the commit validated, the commit recorded and the commit the
prerequisites and the first attempts resolve their manifest at are one,
whatever HEAD does meanwhile; `ContinueRun` passes `from.Run.Base`, the
commit `startContinued` restores, so a continuation is prepared at its own
base and never at the directory's HEAD. The attempt's worktree uses the file
at the attempt's start commit; the checkout uses the file at the result
commit (so a task that changes the manifest is checked by the manifest it
changed); the prerequisites use the base's. A tree whose commit has no such
file is a named manifest failure ("the commit does not hold
conductor.manifest.json"), attributed as any step of that tree would be. A
manifest's identity is recorded with every step it ran: `crew:<revision>` or
`file:<blob>`.

`internal/crew/store.go`: `Update` compares the incoming crew's
`effectiveManifest()` with the stored one's as JSON and sets `ManifestRevision`
to the old one plus one when they differ, a removal included (a manifest taken
off the crew is a change the counter keeps, so the next manifest put on the
crew is one past the removed one's number and never `crew:1` again: a
number names one manifest of a crew for good, which is what a receipt's
identity promises); an unchanged manifest keeps it. `Create` gives a new crew
whose effective manifest is not empty 1, else 0; a legacy crew's `setup` never
changed since is 0 and its steps record `crew:0`, one content as any number
is. A legacy crew's JSON on disk is unchanged (the existing round-trip test
keeps proving it: the counter is `omitempty` and an unchanged save leaves it
0).

### `internal/crew`: trees, steps and receipts (new file `steps.go`)

A `tree` is the engine's record of one directory it makes usable: its kind
(`worktree` or `checkout`), its directory and the crew's working directory
in it (`<dir>/<prefix>`), its commit, the manifest resolved for it, its ports
(`map[string]int`), its state directory, its services (`[]service`, an
interface of the engine's own, `Done() <-chan struct{}`, `Result()
proc.Result` and `Stop(ctx) error`, which `*proc.Service` satisfies), its
preview URL, the attempt it belongs to, `busy` (a `sync.WaitGroup` held
while one of its phases or checks runs) and `ending` and `ended` (set under
`e.mu`).

Everything a tree does on the machine that runs it goes through one value
on the engine, the tree executor (`treeExec`: `run`, `serve`, `probe` and
`port`), native by default (`proc.Run`, `proc.Serve`, one `GET` through
`probeClient`, one bind of `127.0.0.1:0`) and replaced whole by the test
harness: today's `runCommand` becomes its `run`, and C10 (the Runtime seam)
wraps the four together, so a probe or a port is never the server's own
network for a tree that is not on the server's machine.

`prepareTree(ctx, r, a, t)` does, in order, stopping at the first failure
with the step named; its first step is `resolveTree`, which the dispatch
runs before the task files are written, so the files carry what it made:

1. `resolveTree`: the manifest resolved (until PR-5, one naming a phase this
   build does not run yet, or a placeholder it does not expand yet, is
   refused here with `prepare`'s words, so a result commit or a
   continuation's candidate cannot bring in a `verify` or a `cleanup` the
   engine would skip, or a `${CONDUCTOR_STATE_DIR}` it would pass on as
   it is); its `env` names checked, each for
   permission and for presence (`allowedEnv`: a name the host does not allow
   fails the tree at `manifest`, "NPM_TOKEN is not in the server's
   envPassthrough", and one allowed but not set in the server's environment
   fails it there too, "the host has no NPM_TOKEN", since `pty.BuildEnv`
   passes silently over an absent name; both before any step or session
   starts, in the checkout as in the worktree, so a result's manifest cannot
   add a name the operator did not allow or the host does not have, and the
   failure is attributed by its tree's rule); the ports allocated (each
   name: `treeExec.port`, natively a bind of `127.0.0.1:0` whose number is
   taken and closed; a number another live tree of this server holds is
   drawn again; the lease is kept on the engine, `e.ports`, until the tree
   ends); the preview URL expanded, now that the ports are known; and the
   state directory made, `.conductor/ state` under the tree's working
   directory, through an `os.Root` at that directory as the task files are
   written (their `.gitignore` beside it in a checkout too), so it is inside
   what a guest would be handed (Boxcar constraint 3) and `.conductor/`'s
   exclusion keeps it out of the status. `manifest.resolved` and
   `manifest.ports` are recorded in one transaction.
2. The `setup` steps, each through `runStep` (below), in the tree's working
   directory.
3. The `services`, each: its row put as `started` (event `step.started`)
   before anything runs, as a step's is; then `treeExec.serve` with the
   tree's environment, under `r.teardown` (below) and never the attempt's
   context, whose end `cmd.Cancel` (`proc`'s `attr`) would turn into a
   `SIGKILL` of the group at the attempt's close, before `endTree`'s
   `SIGTERM` and grace ever ran: a service ends by `service.Stop`, from
   `endTree` or from a stop, and by its context only at the shutdown's
   cut-off; the handle appended to the tree under `e.mu` the
   moment it exists (a tree already `ending` has the new service stopped at
   once and the phase fails at "service <id>"); then its row `running` with
   the process's pid, group and start time (`step.running`), committed
   before the next service starts. A `running` the ledger refuses stops and
   reaps the service and fails the tree at "service <id>", as a refused
   step row does; a service that exits before the tree is ready fails the
   tree at "service <id>".
4. The `ready` probes, each one row with its tries counted: a command run
   again (`treeExec.run`) every interval until it exits 0, or a URL fetched
   (`treeExec.probe`) until it answers under 300, failing at the probe's
   timeout with the last try's output as the error.
5. `tree.ready` recorded, with the preview, before the launch or the first
   check rests on the tree. The preview URL is on the tree, in the task
   file and in the environment (`CONDUCTOR_PREVIEW_URL`) from step 1, for
   the agent, whose session starts only after this step; the attempt's own
   `preview` (`AttemptState.Preview`, the run's read, which the UI's Open
   preview follows) is the worktree's URL, set in the worktree's
   `tree.ready` transaction and cleared in the first of two: the attempt's
   close transaction (the one that hands the worktree to `endTree`, below:
   the teardown begins there, before any service is signalled, so no
   cleanup step, however long it runs, runs behind an offered URL) and a
   `service.ended` of the worktree's own that nobody asked for (a service
   that exited on its own after `tree.ready`: the tree no longer answers
   whole), so the read offers a preview only while the tree answers behind
   it: never before the probes passed, never once a service is gone or the
   teardown has begun, and never once the number may be another tree's,
   the port lease being released only in `tree.ended`, after the clear.
   The checkout's preview is its own rows' alone. The URL stays for the
   record in the `trees` row, the `manifest.ports` and `tree.ready` events
   and the task file.

`runStep(ctx, r, a, t, phase, ch) proc.Result` is today's `runSetup` with a
receipt: a `ledger.CheckRow` with `Phase`, `Manifest`, `Ports`, `Error` and
the tree's directory (a `PrerequisiteRow` when there is no attempt), put as
`started` before the process (event `step.started`), `running` with the pid
once it exists (`step.running`, through `treeExec.run`'s `Started`, as
`verify` does for a check) and `finished` with the exit, timing, head and
tail, the log (at most `ledger.MaxLog` as stored, the dropped line and
the tail inside that bound, which `proc.Spec.LogLimit` promises from PR-2
on, below; its SHA-256 over the bytes stored; dropped past `MaxRunLogs`
as a check's is) and the words of its failure, in the row's
`error` and the event (`step.finished`). The ledger refusing the row fails
the tree at that step, as it fails a check today ("the ledger could not
record the step").

`endTree(r, t, why)` runs on a goroutine of the tree's own, counted in
`r.trees` (a `sync.WaitGroup`), under the run's teardown context,
`r.teardown`, never the attempt's, which is ended by then: a context made
with the run beside `r.ctx` and outliving it (a stop cancels `r.ctx` at once
and `r.teardown` never), each step under it bounded by its own timeout, and
cut only by the server's shutdown (`Engine.Drain`, below), so a teardown
already running when the server goes down, after a result taken or a stop
that returned early, has an owner that can bound it. It first waits for the
tree to be quiet: `t.busy` (the phase or check running when the attempt
closed has returned; the attempt's context killed its process, and
`proc.Run` returns only once the group is reaped) and, for a worktree whose
session is being stopped (a cancel, a withdrawal, a stop, a failure that
stops it), the session ended (`Local.Ended()` after `pendingStops`' commit,
bounded by `stopTimeout` as the dispatch's wait on a member's previous
session is), so no cleanup runs beside a process still writing in the tree;
a succeeded attempt's session stays idle (R4) and its tree is cleaned beside
it, the result being committed already. `t.ending` is set under `e.mu`
before the wait, and no service is registered after it. Then it stops every
service of the tree (`service.Stop`: `SIGTERM` to the group, `SIGKILL` after
`proc.ServeGrace`, 5 s; `service.ended` with its exit or signal), runs the
`cleanup` steps through `runStep` (phase `cleanup`), releases the tree's
ports and records `tree.ended` with why and the first cleanup step that
failed, if one did. A cleanup failure is a receipt and a line in the run log
("task build: attempt 2: cleanup <id> exited 1: …"), never a change to the
attempt's state. `r.cleaning` counts the trees ending; `Engine.idle` is
false while it is above zero, so `evict` never forgets a run mid-cleanup,
and the run reports it (`Run.Cleaning`). `endTree` runs exactly once per
tree (`t.ended`, under `e.mu`).

The hand-off is `endAttempt`'s, the bookkeeping every close shares
(`closeAttempt`, and through it `recordStop`, `cancelTask`, `managedEnded`,
`accept` and `dispatch`'s `fail`; the withdrawal's direct call in
`withdrawFrom`): it queues the attempt's tree with the attempt's cancel,
and the commit that makes the close durable starts `endTree` as it ends
the contexts (`pendingCancels`), so a withdrawn attempt's tree ends as a
cancelled one's does. A stop's order is then: `r.cancel()`, the starts;
the sessions and, beside them in the same wait, every service of the run's
live trees (each tree marked `ending` under `e.mu` first, so no service is
registered after; then `service.Stop` under the stop's context: `SIGTERM`,
the grace, `SIGKILL`); the deliveries and the checks waited for as today;
`recordStop` committed (it closes the attempts in flight and so hands their
trees over, whose `endTree` finds the services ended and records each
`service.ended` with the exit or signal the stop gave it, why "the run was
stopped"); `waitFor(ctx, &r.trees)`; the end told. The stop ends the
services itself because the hand-off is the commit's: `recordStop` has no
undo, a refusal returns from `stop` with the run stopped and its attempts
put back by the snapshot, never closed, so no tree is handed over, and
`Server.Shutdown` passes over a run with `StoppedAt` set. After a refused
`recordStop`, then, no service is running (the stop ended them before it);
the trees' cleanup steps are not run, their `trees` rows stay as they were
and `Recover` concludes them at the next start, and their port leases are
held until the server exits (the lease is the engine's own guard, nothing
on the host). A stopping run (`r.stopping`, set under `e.mu` before
anything is signalled) reads with no `preview`, the teardown having begun.
A stop cut short by its context leaves `cleaning` above zero and the end
is told all the same; the teardown goes on under `r.teardown`, which no
stop cuts, and with a cleanup step's default timeout of 60 s (above) a
stop with one slow cleanup step returning so is the normal case, not the
cut-short one: the stop's `200` carries `cleaning`, and the header's badge
shows it. The stop's context is `handleStopRun`'s own for a stop from the
UI, 30 s detached from the request (`internal/api/runs.go`; the CLI has no
stop: `conductor up` launches and resumes, and a stop is `POST
/api/runs/{run}/stop`), and the shutdown's for the server's, which is the
one context that cuts the teardown, and arms the cut before it waits for
anything:
`Server.Shutdown` first calls `Engine.Draining(ctx)` (new), which sets the
teardown's cut-off on the engine, the shutdown's deadline less
`teardownReserve` (`proc.WaitDelay` for the reaping and one second for the
rows: 3 s; ctx's end when it has no deadline), and starts the goroutine
that cancels `r.teardown` of every run the engine holds when the cut-off
comes; then it stops every run not yet stopped, as today, a finished run
among them (its `StoppedAt` is nil until a stop sets it, so the stop loop
stops it as any other and its stop waits for its trees); then it calls
`Engine.Drain(ctx)` (new) before the ledger closes, which waits for
`r.trees` of every run the engine holds until the deadline, the runs
stopped before the shutdown included (one cleaning after a stop from the
UI that returned early, its `StoppedAt` set, which the stop loop passes
over). The cut-off armed first is what gives the stops their room: a
cleanup step that would outlast the budget is killed by its context at the
cut-off (`proc.Run` returns once its group is reaped) and recorded
`interrupted`, and no step starts after it (`proc.Run` under an ended
context returns at once, `Canceled`, and the row is not put), so each
tree's `tree.ended` is committed and each stop returns before the
deadline, with the reserve left for the rows and the records; a cut-off
reached only after the stops, as a `Drain` alone would reach it, would
hold every stop to the full deadline behind one long cleanup and leave the
cut nothing to reap and record in. The CLI's budget is 10 s and the app's
12 s, both unchanged: under a shutdown a tree's services are ended first
(the grace fits the budget), each cleanup step runs until its own timeout
or the cut-off, and a `tree.ended` the ledger's close beat all the same is
finished by `Recover` at the next start from the tree's `trees` row
(below: the row `ended` and `interrupted`, its open step rows
`interrupted`, its services reported), the ports being nobody's by then. A
manifest whose cleanup needs longer keeps its receipts at a stop from the UI
and loses the cut step's at a shutdown, which the run log says.

`prerequisites(r)` runs the manifest's prerequisites once per run, in the
crew's working directory, on a goroutine of the run's own counted in
`r.starts` (so a stop waits for it as for a dispatch, and `r.ctx` ends it,
its row `interrupted`), gated by `r.prereq`, a run flag the pump checks:
`pending` → `running` → `passed` | `failed`; the pump dispatches nothing
while it is not `passed`, whatever pumped it (another attempt's close
included). The goroutine is counted in `r.prereqs` too, a count beside the
wait group as `cleaning` is beside `r.trees` (0 or 1: one generation at a
time, below), set under `e.mu` in the commit that starts it and cleared
under `e.mu` after its last commit, dropped or not; `Engine.idle` is false
while it is above zero, as it is while `cleaning` is, since `idle` reads
neither `r.starts` nor the run's context and a prerequisite is no member
and no tree: so `evict` never forgets a run with a generation in flight,
which would cancel the run's context under the step without joining it,
take the run out of the shutdown's inventory and leave the step's reaping
and its last row racing the ledger's close. A run that becomes terminal
while a generation is in flight (its last open task cancelled before they
first passed, or an attempt's close finishing the run while an unpause's
generation runs; a stop cancels `r.ctx` itself) ends the generation in the
commit that sets its outcome (`settleRun`, whichever close called it): the
goroutine runs under a context of the generation's own beneath `r.ctx`
(`r.prereqCancel`), which that commit cancels when `r.prereqs` is above
zero, so the step's process is killed and reaped (`proc.Run` returns once
its group is), its row `interrupted` with why "the run finished before the
prerequisite ended", and the goroutine's last commit clears the count;
until then the run stays held, and the shutdown's stop loop stops it as
any other. `startManaged` sets `r.prereq` `pending` after `run.created`
and starts the goroutine, and the launch answers at once with the run
(`Run.Manifest.prerequisites[].state` says `running`); `startContinued` does
the same for a continuation; an unpause's commit sets it back to `pending`
and starts the goroutine again, so a host fixed in between is seen, and
failing again they pause again. One generation at a time: each start
(`startPrerequisites`, under `e.mu` in the commit) bumps `r.prereqGen` and
hands the number to the goroutine, whose `running` and final commits carry
it and are dropped, their rows kept, when it is no longer the run's; an
unpause while a generation is in flight (`r.prereq` is `pending` or
`running`: a person paused and resumed while they ran, which `setPaused`
allows) starts none, the one running being the current generation, whose
result stands; only a `passed` or a `failed` is run again. So no older
generation can enable the dispatch while a newer one is blocked, and a stop
ends the one in flight through `r.ctx`. Each prerequisite is a receipt of
the same class as a step: a row of the ledger's new `prerequisites` table
(the `checks` columns without `task_id` and `n`, keyed `(run_id, id, k)`,
`k` counting its runs in the run: 2 at the first unpause; a continuation is
a run of its own and starts at 1), put `started`, `running` and `finished`
through `runStep` as a step is, with its log, SHA-256, words and manifest
identity; the events are `step.started`, `step.running` and `step.finished`
with `phase: prerequisite`. It also checks the manifest's `env` names: each
must be set in the server's environment and allowed by the server's
`envPassthrough` (`config.EnvPassthrough`, the host-managed list; the manifest
names what the project needs, the host decides what leaves it); a name
missing on either side is a prerequisite failure naming it ("the host has no
NPM_TOKEN", "NPM_TOKEN is not in the server's envPassthrough"), and the
first says what fixes it, "set it where the server starts, start it again
and continue the run": the server's environment is read once, at its start
(`pty.ParentEnv` is `os.Environ`), `envPassthrough` holds names, not values,
and nothing reloads either while it runs, so an unpause alone finds the same
host (R13). A failure pauses the run's dispatch (`pauseManifest`, below) with
the step named, `r.prereq` left `failed`; `prerequisites.passed {manifest}` is
recorded, with the manifest's identity, before the first `attempt.reserved` of
the run. The values are never recorded.

### `internal/crew/task.go` and `verify.go`: where the phases run

`Engine.dispatch` replaces its setup loop with `prepareTree` on the attempt's
worktree: the worktree made and checked as today, then `resolveTree` (the
manifest, the `env` names, the ports, the preview and the state directory),
then the task files written once, carrying them (`writeTaskFiles` refuses
a second write, so nothing is written before the tree's facts are known),
then `attempt.workspace` recorded, then the `setup`, `services` and
`ready` phases and `tree.ready`, then `Launch`. A tree that fails there
closes the attempt with `fail`, stage `<phase> <id>` (`setup deps`,
`service db`, `ready web`, or `manifest` for a manifest that could not be read
or names an `env` the host does not allow), and the new argument
`uncounted`: `closeAttempt` then grants the task one more attempt
(`t.extra++`, as a withdrawal does; the task row's `Extra` is persisted, so
a continuation counts it too), leaves the task `ready` with the reason
"attempt 2 could not be made usable at setup deps: deps exited 1: …" and
pauses the dispatch with the same words (`pauseManifest`), in the one
transaction that closes the attempt. The next attempt starts where the
uncounted one would have: `startCommit` passes over an uncounted attempt
(one failed at a manifest stage with no result: its `StartCommit` is on its
row from its reservation and its stage from its close, so a continuation's
`last` passes over the same rows, as it reads a refusal) and decides from
the kept attempt before it, as if the uncounted one had not been made:
its result, its refused result's start, its checkpoint unless refused
(below), else the inputs; and the task file
carries the last counted attempt's words as the uncounted one's did, with
a line that the attempt before could not be made usable (a continuation
reads the uncounted row into `t.uncounted`, a record beside `t.refused`
with its number, stage and words, and the checkpoint commit and branch
when it refused one, so its first attempt's task file says the same); so
attempt 3, after an attempt 2 made at attempt 1's refused result B and
failed at `setup deps`, starts at B, not at the task's input.

One start commit no checkout verified is a checkpoint's: `startCommit`
starts the attempt after one that failed without a result at that
attempt's checkpoint, and a continuation's first attempt at the task's
(`t.checkpoint`), a commit the agent made at its worktree's HEAD and saved
(`checkpoints.go`), which no manifest ran at. A manifest failure in a worktree
at the task's checkpoint commit (`a.start == t.checkpoint.commit`) is not
the host's by R2: it refuses the checkpoint as #370 refuses a result. The
attempt closes failed at `<phase> <id>`, uncounted as above (the agent
typed nothing), but the run is not paused: in the close's transaction
`t.checkpoint` and the checkpointed attempt's own are dropped
(`attempt.failed` carries `checkpoint: <commit>` beside `uncounted: true`;
the rows are not rewritten, the saved checkpoint staying on its attempt's
row and in `task.checkpoint` for the record), so `startCommit`, passing
over the uncounted attempt, finds the attempt before it failed with no
result and no checkpoint and starts the next attempt where that attempt
started, at once; a continuation refuses it from the rows the same way
(an uncounted row whose `StartCommit` is the checkpoint commit of the
kept row before it leaves `t.checkpoint` nil). The next attempt's task
file has no "Where the last attempt got to", and its `TaskPrevious` gains
`Checkpoint` and `Branch`, rendered "Attempt 1's checkpoint, the commit C
on the branch B, could not be made usable: attempt 2 failed at setup
deps: …. It is not where you start: take from it what holds up". A second
failure there, at a commit a checkout verified or the task's input, is
the host's and pauses as above, so a task never loops on a commit the
agent made. The tree's `endTree`
runs after the close. Failures at the stages main has (`worktree`, `task
file`, `launch`, `prompt`) keep today's accounting.

The attempt's session gets the tree's names through `LaunchSpec.Inject`
(`CONDUCTOR_PORT_<NAME>`, `CONDUCTOR_STATE_DIR`, `CONDUCTOR_PREVIEW_URL`;
the API merges them after `crewInject`'s, unfiltered as `pty.Inject`'s are)
and the manifest's `env` names through `LaunchSpec.Allow`, the names
`allowedEnv` approved and no other (`pty.BuildEnv` takes `extraAllow` as
permission, so the engine intersects first; appended to the passthrough
the session builds from `cfg.EnvPassthrough` and the agent's). A step's
`proc.Spec.Allow` carries the same approved names.
The task file (`taskfile.go`) gains `TaskFile.Manifest` (`{source, revision,
digest, ports, preview, services}`: the identity, the ports by name, the
preview URL of at most 512 bytes expanded (the bound above) and the service
ids; the state directory is not in it, being `.conductor/state` under the
working directory the file already names, and `CONDUCTOR_STATE_DIR` in the
environment), never
cut and at most 2 KiB as the sum of its parts at their largest (a 512-byte
preview, 8 ports, 4 ids, the identity: about 1.2 KiB), which the extended
largest-of-each-part test proves (`TestTheTaskFileIsCutToFit`). "Where you
are" in `task.md` gains the sentences: which manifest made the tree usable,
the services and their ports, the preview, that the state directory is
`.conductor/state` and that `CONDUCTOR_PORT_<NAME>` names each port. The
attempt's trees end when the attempt closes, whatever closed it:
`endAttempt` hands them to `endTree` once the close is committed (for a
cancel or a withdrawal, with the session's stop, from `pendingStops`'
commit), so a cancelled or withdrawn attempt mid-step has its step killed
by its context, its process reaped and its cleanup run after.

`Engine.verify` makes the checkout a tree too: `prepareTree` after the
checkout's `checkMemberDir` and `MkdirAll` (so what the worker had, the
checks have, with services and ports of the checkout's own), the checkout
compared with the result (`checkoutIntact`) where the comparison can name
an author: after each `setup` step, which runs alone in the tree, so a
change is that step's, stage `setup <id>` and "setup deps changed tracked
files: …" (main compares once after the whole phase and says `setup`;
`refusedBeforeChecks` keeps that word for the rows main wrote and gains
`ready`, below); and once after the `ready` phase, when the services have
been running beside the probes, where a change is the phase's and no one
step's: stage `ready` with no id, the words naming what could have made
it, "tracked files changed while the services db, web ran and the probe
web was tried: …", since the engine cannot tell a service's write from a
probe's. Then the task's checks and the manifest's `verify` steps as one
list, each under the server's check slot and compared after (`verify`
steps are checks of the crew's: rows of phase `verify`, stage `verify
<id>` on failure, the next attempt starting at the result with the step's
tail in its task file as for a failed check); a change found after a check
or a verify step is attributed to it, as today, and in a tree with
services the words name them as the other possible author ("check unit,
or a service of the tree (db, web), changed tracked files: …"), the
accounting being the result's either way, since the services are the
result's manifest's. Then `accept`, as today.
The trees end after the decision, never before it: an
attempt owns the trees it made (`a.trees`: its worktree from `dispatch`, its
checkout from `verify`; a candidate's attempt the checkout alone), and the
close `accept` commits (`closeAttempt`, through `endAttempt`) hands every
one of them to `endTree`, each on a goroutine of its own, as any close does;
a tree added after the close (a checkout `verify` makes while a cancel
commits) is handed over by `addTree` itself, which sees `a.closedAt` under
`e.mu`. `verify` runs the checkout's phases and checks on its own goroutine
under the checkout's `busy`, so when it calls `accept` the checkout is quiet
and its `endTree` waits for the commit alone; the worktree's finds the
succeeded attempt's session idle (R4) and cleans beside it. A failure in the
checkout keeps today's attribution: it is the result's, counted, and a
failure before the checks (`setup`, `service`, `ready`, `manifest`) is a
refused result, the next attempt starting where the refused one started
(#370's rule): `refusedBeforeChecks` gains the words `service <id>`, `ready
<id>`, `ready` (the phase's comparison, above) and `manifest` beside `setup
<id>`, so `startCommit` and a
continuation's refusal (`continue.go` reads the same function) see them. The
same `npm ci` against a dead registry is so uncounted in a fresh worktree
and counted in a checkout, and the plan accepts the one attempt that costs:
the checkout's manifest ran at a commit the agent changed, the worktree's at
one it had not, which is the only line the engine can draw without guessing
(R2); the next attempt tells the two apart by itself, failing uncounted and
pausing in its own fresh worktree when the host is at fault; and a pause on
every refused result would have a person click through each bad lockfile an
agent commits. A continuation's `dispatchCandidate` verifies through the
same path, so a candidate's checkout gets its manifest, in an attempt of its
own (`reverify` numbers it after the task's, uncounted).

`pauseManifest(r, why)` is `pauseDispatch` with words of its own: `r.paused`
set and marked, `r.pausedWhy` kept and on the run row (`paused_why`,
below, so the ledger holds the words, not the record alone), the run log's
line "dispatch paused: <why>", the event `run.paused {why}`. An unpause
(`setPaused(false)`, which `Pause`, the command the UI's route calls, and
`PauseRun` both go through) clears `pausedWhy`, in memory and on the row,
and, unless a generation is in flight, sets `r.prereq`
to `pending` and starts the prerequisites again in the same commit (one
generation at a time, above), so the pump, which runs under
`e.mu` and cannot wait on a process, dispatches nothing until they pass,
however it is pumped meanwhile; a host fixed in between is seen, and
failing again they pause again. The API's run gains `pausedWhy` and
`cleaning`; `Run.Manifest` (`{source, revision, digest, prerequisites: [{id,
k, state, exit, error, duration}]}`) says which manifest the run has and how
its prerequisites go (`running` while they do); `AttemptState` gains
`preview` (in the run's read: the worktree's URL from its `tree.ready` to
its clear, the attempt's close or an earlier `service.ended` of the
worktree's own, empty otherwise and empty for a stopping run, step 5 of
`prepareTree`) and
`steps` (in the task's read alone,
`Engine.TaskOf`, as `submitted` is): `[{phase, id, k, state, argv, url,
dir, subject, manifest, exit, timedOut, duration, error, ports, tries,
startedAt, finishedAt, logBytes, logSha256}]`, the receipt whole but for
the log: the command (a probe over HTTP: `GET` and its expanded URL), where
it ran, the commit, which manifest produced it (the worktree's and the
checkout's may differ), its outcome and its words.

### `internal/crew/ledger`: one migration

`Version` becomes the number after main's when the PR is cut: the Program
gives C10 (the Runtime seam's `where` column) version 5; whichever of the
two lands second takes 6. The migration, in `migrations`, is SQL alone and
conservative: `ALTER TABLE checks ADD COLUMN phase TEXT NOT NULL DEFAULT
'check'`, `manifest TEXT NOT NULL DEFAULT ''`, `ports TEXT NOT NULL DEFAULT
''` (a JSON object, name to number), `tries INTEGER NOT NULL DEFAULT 1`,
`error TEXT NOT NULL DEFAULT ''` (the words of the failure, bounded as the
events' are, so a step whose program could not start, which has no output,
still says why after the engine has let the run go), `url TEXT NOT NULL
DEFAULT ''` (a probe over HTTP has no argv: its expanded URL, at most 2048
bytes, is the receipt's command, the method `GET` by the rule above, so the
receipt still says what was asked of what after the engine has let the run
go); `ALTER TABLE runs ADD COLUMN paused_why TEXT NOT NULL DEFAULT ''`
(the words of a pause, bounded as the event's are: `RunRow` gains
`PausedWhy`, `PutRun` writes it beside `paused`, and the row the engine
puts in every transaction that marks the run, `task.go`'s `RunRow` from
`r.paused`, carries `r.pausedWhy`, so the words are committed in the
transaction that commits `run.paused {why}` and cleared in the one that
commits the unpause; `Recovered` reads them into the record beside
`Paused`, `rec.PausedWhy = from.Run.PausedWhy`, so a record that lags the
ledger, the record being written after the commit and a crash between the
two losing it, or one missing altogether is rebuilt with the words by
`syncRecords`, and a continuation inherits them with `paused`; a
version-4 row reads with none); `CREATE TABLE prerequisites`, the
`checks` columns without `task_id`,
`n`, `phase`, `ports` and `tries`, keyed `(run_id, id, k)` with `run_id`
referencing `runs` on delete cascade: a prerequisite has no attempt to hang
a `checks` row on (the table's foreign key), and gets a row of the same
shape; and `CREATE TABLE trees`, a tree's lifecycle, which no receipt row
holds: keyed `(run_id, task_id, n, kind)`, `run_id` referencing `runs` on
delete cascade, with `dir`, `commit_id`, `manifest`, `ports`, `preview`,
`state` (`resolved`, put in the `manifest.resolved` transaction; `ready`, in
`tree.ready`'s; `ended`, in `tree.ended`'s), `why`, `interrupted` and
`ended_at`, so a tree the owner left between its attempt's close and its
`tree.ended` is found at the next start whatever its run's state. Every row
a version-4 database holds is a task's check, so the defaults are true of
it; no Go backfill. The key stays `(run_id, task_id, n, id, k)`: a receipt
is keyed by its attempt, its id and `k`, which counts the runs of one id on
one attempt whatever their phase: the attempt keeps one counter per id
(`a.ks`, under `e.mu`, bumped when a row is first put), every row of the
attempt takes the next number, a task's check included, and PR-2 replaces
`verify.go`'s `K: 1` by that allocation (`phase` is not in the key, and
`PutCheck` upserts on it: a check put at k 1 would overwrite a legacy
setup step's row of the same id). A setup step run in the attempt's
worktree (k 1) and again in the checkout (k 2) are so told apart, and
`dir` says which tree; a legacy `setup tests` beside a check `tests` is
three rows, `setup` k 1 and k 2 and `check` k 3; a check's `k` stays 1
where no step shares its id, so the existing tests' assertions on it
change only where one does (`refused_result_test.go`, `managed_test.go`,
"The PRs"); a
continuation's checkout is an attempt of its own (`reverify`), so its rows
start at k 1 under that attempt. `CheckRow` gains `Phase`, `Manifest`, `Ports
map[string]int`, `Tries`, `Error` and `URL`; `PutCheck` and `Checks` carry
them (`Checks` orders by task, attempt, id and `k`, as today; the route
sorts by `started_at` itself); `PrerequisiteRow`, `PutPrerequisite` and
`Prerequisites` are their counterparts; `TreeRow`, `PutTree` and `Trees`
carry the tree rows; the phase constants are `PhaseCheck`, `PhaseSetup`,
`PhaseService`, `PhaseReady`, `PhaseVerify`, `PhaseCleanup` and
`PhasePrerequisite`. `Recover` already turns a `started` or `running` row
into `interrupted`; it does the same for a prerequisite's row. Its selection
(`transition.go`'s query: a run running, or with an open attempt, check or
command row) gains two predicates. A run with a `prerequisites` row
`started` or `running` (an `EXISTS` on the new table, as the attempts',
checks' and commands' are): a stop waits for the prerequisites' goroutine
only as long as its context lasts (`waitFor(ctx, &r.starts)`), so a stop
cut short commits `run.stopped` while the prerequisite it ended is still
being reaped, and a server that dies before the row's `interrupted` commit
leaves a stopped run with no attempt, no check and no tree, which no other
predicate selects; its row would say `running` for good. And a run with a
`trees` row not `ended`: a succeeded attempt can be closed, every service
row finished and the run finished when the server dies before the first
cleanup row or the `tree.ended`, and nothing else selects that run. Each
such tree it concludes `ended` and
`interrupted`, why "the owner stopped before the tree was ended", its open
step rows `interrupted`, counted in `Recovered.Trees`, with a `tree.ended
{interrupted: true, recovered: true}` event per tree under the run, whose
own state it leaves as it was when terminal (nothing runs after a restart:
the ports are nobody's, the cleanup steps are not run, and `Run.Cleaning` is
0). It also gains `Recovered.Services`, one descriptor per row of phase
`service` so concluded, `RecoveredService {Task, N, Tree, ID, Pid, Group,
StartedAt, Confirmed}` and nothing of the row's argv, log or words (the
row holds them): one that was `running` with its pid, group and start
time, `Confirmed`; one still `started` as a service whose start was not
confirmed, which may be alive and cannot be named. The `recovered` event
names them by count alone, `services` and `unconfirmed`, as it counts
`checks` and `commands`, and each concluded row gets a `service.ended
{task, attempt, tree, id, pid, group, startedAt, confirmed, interrupted:
true, recovered: true}` event of its own in the same transaction, a
payload of a fixed shape near 200 bytes. `Recover` writes through
`Tx.Event`, not the engine's `writable`, so an oversized payload would
roll the whole recovery back: nothing it writes may carry a row's argv (a
recovery can meet 32 open service rows, 4 attempts in flight × 2 trees ×
4 services, 8 KiB of argv each, four times `MaxPayload` in one list), and
each of its payloads is bounded by its shape, never by the rows. A
process this server started that outlived it is reported, never killed (a
pid after a restart is nobody's to signal), and a tree the server did not
run natively has no pid to report. A newer database still opens read-only
in an older binary, as today. `Prune` refuses a run with a `trees` row not
`ended` or a
`prerequisites` row `started` or `running` as it refuses an open run
(`ErrNotPrunable`: "a tree of it is still ending", "a prerequisite of it is
still running"): a finished run can be mid-cleanup (`Run.Outcome` set,
`cleaning` above zero), and deleting it then would cascade the live tree's
rows and receipts away, fail the cleanup's remaining transactions and lose
the row `Recover` concludes; `Prune` has no caller on main but the ledger's
tests, and this is the precondition its first caller gets. Otherwise it
deletes the prerequisites and the trees with the run's rows, and the
prerequisites' logs count toward `MaxRunLogs` as the checks' do.

### `internal/proc`: a service runner, and the engine's own names

`Spec.Inject map[string]string`: Conductor's own per-tree names, set after
`BuildEnv`'s filtered environment as `pty.Inject`'s are; `Run` refuses any
name but `CONDUCTOR_RUN`, `CONDUCTOR_TASK`, `CONDUCTOR_ATTEMPT`,
`CONDUCTOR_STATE_DIR`, `CONDUCTOR_PREVIEW_URL` and `CONDUCTOR_PORT_<NAME>`
(`injectable`), so no token can come in by this door; `Spec.Env` keeps
refusing every `CONDUCTOR_*` name. `Spec.Allow` already exists and carries
the manifest's `env` names.

`Spec.LogLimit` bounds everything the `Log` writer receives, the dropped
line and the tail included: `logSink` keeps the tail to `LogTail` or a
quarter of the limit, whichever is less, reserves 64 bytes for the dropped
line and gives the head the rest, so a writer given `ledger.MaxLog` holds
at most `ledger.MaxLog` bytes when `Run` returns (the tail stays 256 KiB
at that limit). On main the limit bounds the head alone and the sink
appends the line and up to `LogTail` more (`TestRunKeepsHeadAndTail`
asserts it), while `PutCheck` refuses a log past `MaxLog`: a check whose
output passes 1 MiB by more than the tail ends its attempt with "the
ledger could not record the check's outcome" instead of its own exit, a
defect on main that every receipt here would inherit through the same
`runCommand`. It is fixed in PR-2, where the first step rows land, for
the checks' path with the steps'; the hash stays over the bytes stored.

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
the start's, not a lifetime (a service lives until `Stop`). The engine
reaches `Serve` through `treeExec.serve` alone and holds what it returns
behind its `service` interface, so C10 wraps `Serve` with `Run`.

### `internal/api`

`crews.go`: `crewInput`, the body of `POST /api/crews` and `PUT
/api/crews/{id}`, gains `Manifest *crew.Manifest` (`json:"manifest,omitempty"`)
and `readCrew` maps it into the `Crew` beside `Tasks` and `Setup`: the
body is decoded strictly and the `Crew` is built field by field, so a
manifest reaches the store by this one door and by no other, and
`manifestRevision` stays out of `crewInput`, refused in a body as `id` is.
This lands with the model in PR-1, so the API accepts what PR-1 lets the
web app send.

`runs.go`: `Launch` passes `LaunchSpec.Inject` and `Allow` into
`createSessionRequest` (two new unexported fields); `sessions.go` appends
`allow` to the passthrough and merges `inject` after `crewInject`. New
`manifest.go`: `GET /api/runs/{run}/tasks/{task}/attempts/{n}/steps`
(`requireAdmin`; `{steps: [...]}` as `AttemptState.Steps`, from
`ledger.Checks` without logs, sorted by `started_at` in the handler, so a
run the engine has let go still answers, each step's `argv`, `url`, `dir`,
`subject`, `manifest` and `error` from its row), `GET …/steps/{id}/{k}/log`
(`text/plain`, the row's log, at most 1 MiB, `X-Conductor-Log-SHA256`;
`404` for a row without one, `410 log_dropped` for one dropped past
`MaxRunLogs`), and the same pair for the prerequisites, `GET
/api/runs/{run}/prerequisites` (`{steps: [...]}`, phase `prerequisite`,
from `ledger.Prerequisites`) and `GET …/prerequisites/{id}/{k}/log`. All
four `requireAdmin` (a bearer header, as every admin route: the app fetches
them through `useApi`, never as a link the browser follows) and registered
in `server.go` beside `GET /api/runs/{run}/tasks/{task}`. No new config
key: `checks.maxConcurrent` is reused as it is, and `envPassthrough`
reaches the engine, which holds no config, through a setter of its own,
`Engine.SetEnvPassthrough(names)` (new, PR-3), which `server.go` calls
beside `SetCheckConcurrency` when it gives the engine the ledger, before
the first launch; `allowedEnv` intersects a manifest's names with what it
was given, and allows none before the call.
`middleware`'s CORS block (`server.go`) adds `Access-Control-Expose-Headers:
X-Conductor-Log-SHA256`, so the hash reaches the app's script where the
server answers cross-origin (a dev origin under `cfg.Dev`,
`crossOriginAllowed`), the one case the app and the API are not one origin.

`tasks.go`: `handleGetTask`, the task read `RunTasks.vue` lists attempts
from, answers from the ledger when the engine answers `ErrRunNotFound`
(the run evicted, or the server started again; today that is a `404` and
the page has no attempt row to open, though the receipts are in the
ledger): `crew.LoadedTask(from, taskID)` builds the `TaskState` and every
`AttemptState` of the task from `Loaded`'s rows through the one
row-to-state code `Recovered` uses (`taskStateOfRow`,
`attemptStateOfRow`, factored out of `continue.go`), each attempt with its
number, state, stage, error, commits, worktree, branch, session id, times,
`submitted` (the row's result decoded, as `Recovered` decodes it) and
`steps` from `ledger.Checks` without logs, as the engine's read carries
them, and no `preview` (nothing answers behind a tree the engine let go);
`404 not_found` when the ledger has no such run or task, `503
ledger_unavailable` with no ledger, as the commands route answers. So a
historical run page has attempt rows to open, and the receipts under each
read as they do for a held run; the run read already answers from the
record (`syncRecords`), and this is its counterpart for the task.

### `web/app`

- `composables/useApi.ts`: `requestText(path)` beside `request<T>`, which
  returns the body alone: `$fetch.raw` with the bearer header and the same
  error normalisation, returning `{ text, headers }`, so a caller can read
  a response header.
- `composables/useSessions.ts`: `CrewManifest`, `CrewProbe`, `RunStep`,
  `RunAttempt.preview` and `.steps`, `RunInfo.manifest`, `.pausedWhy`,
  `.cleaning`; `runSteps(run, task, n)`, `runPrerequisites(run)`,
  `stepLog(run, task, n, id, k)` and `prerequisiteLog(run, id, k)`, the
  last two fetching the log as text through `requestText` (the bearer
  header: a link the browser followed would carry no token and get `401`)
  and returning it with `X-Conductor-Log-SHA256`.
- `utils/crews.ts`: `toCrewInput` sends `manifest` (and `setup` only when
  the draft still has one), `liftSetup` turns a loaded crew's `setup` into
  `manifest.setup` for the editor, `manifestProblems` mirrors every rule above
  (ids across lists and against the tasks' checks, the names, the URLs,
  the placeholders, the counts) and `TASK_LIMITS` gains the manifest's
  numbers.
- `components/CrewManifestEditor.vue` (new), shown by `CrewEditor.vue` under
  the tasks of a managed crew and taking over `CrewTasksTable.vue`'s Setup
  foot: a "Project manifest" heading with one line saying what it is and that
  the catalog's agent recipes are something else; the file switch ("Read
  `conductor.manifest.json` from the repository at each attempt's commit",
  which hides the lists); the phases as `CrewCheckList` lists
  (`what="prerequisite"`, `"setup step"`, `"service"`, `"verify step"`,
  `"cleanup step"`), the probes as a list of their own (id, argv or URL,
  interval, timeout), `env` and `ports` as chip inputs, the preview URL;
  each row's first problem under it, the Save gate and the Launch button's
  reason as the tasks table has them (`taskProblems` gains the manifest's).
- `components/RunAttemptSteps.vue` (new) in `RunTasks.vue`'s attempt detail,
  for a run the engine holds and one it has let go alike (the attempt rows
  come from the task read, which answers from the ledger for the latter):
  the attempt's steps in order with phase, id, outcome in words (`stepWords`
  in `utils/tasks.ts`: "exited 0 in 4.2 s", "timed out after 60 s", "ready
  after 3 tries", "running since …", "interrupted"), the command (a probe
  over HTTP: `GET` and its URL), where it ran, the manifest that produced it
  (`crew:3`, `file: <short blob>`) and the ports, and a "Log" button that
  fetches the log (`stepLog`) and shows it in a `UModal` with its SHA-256
  and a Save action that downloads it from a blob URL; the run's
  prerequisites the same way at the top of `RunTasks.vue` while the run has
  a manifest; "Open preview" on a task row and in its → actions and the
  palette row while the attempt's `preview` is set, between its worktree's
  `tree.ready` and the attempt's close, or a worktree service's own exit
  before it, and never before or after (step 5 of `prepareTree`); it
  opens a new browser tab: the preview is on the
  server's loopback and is not proxied, and the editor's URL iframe would
  be mixed content under TLS.
- `CrewRunHeader.vue`: the `paused` badge carries `pausedWhy` as its title
  and the words beside it; a `cleaning up` badge while `cleaning` is above
  zero; `taskWords` says "ready, the dispatch is paused: <why>" from the
  task's own reason.
- `pages/crews/[[id]].vue`: nothing but the editor; `pages/runs/[run].vue`:
  the palette's task rows gain Open preview.

### Docs, skill and examples

`docs/protocol.md`: the crew body (`manifest`, its rules, `setup` as its
legacy), "Managed runs" (the phases and where they run, the attribution
rule, the receipt routes, the events), the run and task JSON.
`docs/architecture.md`'s crew-runs section and `docs/repository-map.md`
(`internal/crew`: `manifest.go`, `steps.go`; `internal/proc`: `Serve`;
`internal/crew/ledger`: the schema number, which PR-2 sets when it bumps
`Version`, the row saying 3 on main).
`internal/agents/skill.go`'s "Your task" gains one sentence on the ports,
the preview and the state directory. The example crew run by tasks
(`internal/crew/examples.go`, `example-todo-app-tasks`) gains a manifest
with one prerequisite, `python3 --version`, so the feature is seen on the
first real run (`Seed` skips crews loaded before, so nobody's copy changes).
`docs/features.md` gets the item's entry in the last PR, a bullet beside
the Yardmaster entries, where #380's and #377's are (after the row-11
entry, #350). CLI and MCP change nothing: `conductor
task context` and `task_context` carry the task file, which carries the
rest.

## Events, orderings and invariants

The attempt's states and stages (`internal/crew/ledger/rows.go`) are not
changed; new stage words are `<phase> <id>` for the five manifest phases and
`manifest` for one that could not be resolved, beside today's `worktree`,
`task file`, `setup <id>`, `launch`, `prompt`, `checkout`, `check <id>` and
`withdrawn`. A step row runs `started → running → finished | interrupted`,
as a check's does; a service row stays `running` until `finished` at its
end. The run's `paused` is one bool, as today; `pausedWhy` is in memory,
on the run row (`paused_why`, written in the transaction that commits
`run.paused {why}` and cleared in the unpause's) and in the record, which
is rebuilt from the row when it lags or is missing, and a continuation
inherits `paused` and its words as it inherits `paused` now (a person
unpauses; the prerequisites run again).

The order of a tree, each line a transaction committed before the next
side effect, in the run's own order (`transact`):

1. `manifest.resolved {tree, task, attempt, source, revision | digest, env}`
   and `manifest.ports {tree, task, attempt, ports, preview}`, the ports
   leased on the engine and the tree's `trees` row put `resolved` in the
   same change; for a worktree, the task files are written after this
   commit and `attempt.workspace` after them.
2. For each setup step: the row `started` → the process → the row
   `running` with its pid → the row `finished`, then the next step; a
   failure stops the list and is acted on only once its row is committed.
3. For each service: the row `started` → the process, its handle on the
   tree at once → the row `running` with pid, group and start time,
   committed before the next service starts (a `running` the ledger
   refuses stops and reaps the service: the tree fails at that service);
   `service.ended {id, exit, signal, why}` with the row `finished` when it
   ends, whether it was stopped or died on its own; a worktree's service
   that exits on its own after `tree.ready` clears the attempt's `preview`
   in that transaction and puts a line in the run log ("task build:
   attempt 2: service web exited 1 while the tree was in use; the preview
   is withdrawn"), the attempt's state untouched (the exit is a receipt;
   its session goes on, and `endTree` finds that service ended and stops
   the rest when the attempt closes).
4. For each probe: one row; `tries` counted; `finished` when it passed or
   its timeout passed.
5. `tree.ready {tree, task, attempt, preview}` with the row `ready`, then
   the side effect that rests on the tree: the launch (`attempt.launched`)
   or the first check (`check.started`).
6. At the end, once the attempt's close is committed and the tree is quiet
   (`busy` returned; the session ended when it was stopped): the services
   stopped, each cleanup step as in 2 (phase `cleanup`), then `tree.ended
   {tree, task, attempt, why, cleanupFailed?, interrupted?}` with the ports
   released and the row `ended` in that change.

The run's: `run.created` → each prerequisite's `step.started`,
`step.running` and `step.finished` (phase `prerequisite`, a row each) →
`prerequisites.passed {manifest}` (or `run.paused {why}` in one transaction
with the failing one's `step.finished`) → the first `attempt.reserved`;
`r.prereq` is `pending` from `run.created`, `running` from the first
`step.started` and `passed` or `failed` with the last commit, and the pump
dispatches only on `passed`. An attempt's tree failure: `attempt.failed
{stage, error, state, uncounted: true}` and `run.paused {why}` in one
transaction, the task row's `extra` one more; at a checkpoint commit,
`attempt.failed {…, uncounted: true, checkpoint}` alone, the checkpoint
refused and the pump going on. An unpause: `run.paused
{paused: false}` with, when no generation is in flight, `r.prereq` back to
`pending` → the prerequisites again (`k` and `r.prereqGen` one more) →
`prerequisites.passed` → the pump. A stop: `run.stopped` (the attempts in
flight closed, their trees handed to `endTree`) → each tree's
`service.ended`, cleanup rows and `tree.ended` → the end told, after
`r.trees` or the stop's context. Every payload is bounded as today's are
(`writable`: a tail of at most 2 KiB, an error of at most 2,000 characters
through `session.CleanMessage`, lists of at most 8 names), so none is
replaced by the oversized note.

Invariants, each with a test in "Tests per surface":

- A tree owns the process groups of its steps and services: none outlives
  the tree's end, except across the owner's own death, after which it is
  reported at the next start (`recovered` with its pid and start time) and
  never signalled; a stop ends the run's services itself, beside its
  sessions and before `recordStop`, so a close the ledger refuses, which
  hands no tree over, leaves none running. What a step or service puts
  outside its group (a
  `setsid` child, a daemon) is not the tree's: `proc.Run` leaves it alone
  by design (`TestRunLeavesAloneAChildThatLeftTheGroup`), nothing native
  can find it, and it is neither stopped nor reported, except that one
  still holding the step's output fails the step with `proc`'s words ("a
  process that left the group still holds the output"), as a check fails
  today (`checkWords`, `runSetup`), so a manifest that daemonizes reads it in
  its own receipt. A service is a foreground process: one whose command
  exits before the tree is ready, a daemonizer included, fails the tree at
  `service <id>` with "exited 0 before the tree was ready: a service must
  stay in the foreground", and `docs/protocol.md` says the rule. Containing
  what leaves a group is the Runtime seam's (item F, a guest), not this
  item's.
- A port is leased to at most one live tree of a server at a time, and
  released only in the `tree.ended` transaction. Two trees of one attempt
  (its worktree and its checkout) never share a port or a state directory.
  An attempt's `preview` is set in its worktree's `tree.ready` transaction
  and cleared in the first of the attempt's close and a `service.ended` of
  the worktree's own, both before the teardown signals a service and
  before the lease is released in `tree.ended`, and a stopping run
  (`r.stopping`) reads with none, so a read never offers a
  URL behind which a service has been stopped or has died, nor one whose
  port the lease has let go.
- Every row of a phase other than `check` names the manifest that ran it;
  the identity is recorded (`manifest.resolved`) before the first step of the
  tree starts. A receipt's key is its attempt, its id and `k`, and `dir`
  names its tree; a prerequisite's is the run, its id and `k`, a row of the
  same class with its log and hash, read by a route after the engine has
  let the run go.
- A manifest's `env` name reaches a step or a session only when the server's
  `envPassthrough` allows it and the host has it set, in every tree,
  whichever commit's manifest named it; a name failing either test fails its
  tree at `manifest` before anything runs.
- The pump dispatches nothing while `r.prereq` is not `passed`, whatever
  pumps it; the launch and the continuation answer before the
  prerequisites have run; at most one generation of them is in flight,
  and a commit from one that is not the run's current is dropped. A run
  with a generation in flight is never forgotten (`idle` is false while
  `r.prereqs` is above zero), and one that becomes terminal with one in
  flight ends it in the commit that sets its outcome, the step's process
  reaped and its row `interrupted` before the count is cleared.
- The attempt after an uncounted one starts where the uncounted one
  started, after a continuation too, unless the uncounted one started at a
  checkpoint, which it refuses: then where the checkpointed attempt
  started, the task's checkpoint gone from `startCommit`, from a
  continuation's `t.checkpoint` and from the task file, which names the
  commit as not usable; no run is paused for a failure at a checkpoint,
  and a task never loops on a commit the agent made.
- The checkout's tracked content equals the result commit after each
  `setup` step (compared after each, as after each check), after the
  `ready` phase (compared once, the services having run beside the
  probes) and after each check and verify step; a comparison that fails
  fails the attempt at the step or phase it follows, `setup <id>`,
  `ready`, `check <id>` or `verify <id>`, and its words name only what
  the evidence can: the step alone when it ran alone, the services and
  probes together at `ready`, the check or a running service after a
  check; no stage is asserted that the comparison before it cannot
  establish.
- An attempt that fails in a manifest phase of its own worktree is never
  counted against its task, and the run dispatches nothing after it until a
  person unpauses; an attempt that fails in a manifest phase of its checkout
  is counted and attributed as a failing check or refused result is today.
- `endTree` runs exactly once per tree, under the run's teardown context,
  after the attempt's close is committed and the tree is quiet (no step,
  check or stopped session of it still running), for a withdrawn attempt
  as for a cancelled one, and for the checkout and the worktree of a
  result taken as for a refused one (both `tree.ended` rows follow
  `task.succeeded` in the ledger); a stop waits for it after `recordStop`,
  bounded by its context and cutting nothing, and the server's shutdown
  arms the teardown's cut-off before its first wait (`Draining`), stops
  every run not yet stopped, the finished ones too, and waits for the rest
  through `Drain`, so every stop returns before the deadline with the
  reserve left; `Run.Outcome` set means every
  attempt is closed, not that every cleanup is done (`cleaning` says).
- A continuation reruns the prerequisites, verifies a candidate in a
  checkout with the candidate's manifest, and makes nothing of a tree the run
  it continues left (its services are the recovered rows above).
- The migration changes no existing value's meaning: a version-4 row reads
  as a task's check with no manifest, no ports and one try, and a version-4
  run row with no pause words.
- Every tree the engine resolved has a `trees` row from the
  `manifest.resolved` transaction on, `ended` only in the `tree.ended` one; a
  row not `ended` at the next start is concluded by `Recover` whatever the
  run's state, a finished run's included, so no tree is left half ended
  and unreported.

No wire protocol is touched; no replication, so no conflict rules.

## Out of scope

- Removing a worktree, a checkout or a state directory: Conductor removes
  nothing it made (`worktree.go`'s rule stands); cleanup is the manifest's own
  commands, the services ended and the ports released. A `conductor crew
  clean` is a later item.
- A preview reachable from another machine (the phone on the LAN, a shared
  link) and the in-app capture of it: item PV1 builds on `preview`.
- A preview that is not an HTTP server on the loopback (a static build
  opened from disk, a screenshot): a static build is served by a service
  such as `python3 -m http.server`, on a port of the manifest's; PV1 owns the
  rest.
- Any sandbox for steps and services: they run as the user, like checks;
  the Boxcar seam (item C10, F) is where that would come.
- Sharing caches between trees: the user's own caches (`HOME` is passed to
  every step and session) are shared as today; nothing inside a tree is
  shared, and no symlinking of `node_modules` or the like is offered.
- Manifests for crews that start by rules (legacy crews): a manifest needs
  tasks, as `setup` does.
- Manifest templates, a manifest catalog, a CLI that runs a manifest by hand.
- Hosted crews: `Crew.Launchable` keeps refusing them; the manifest runner
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

Go, `internal/crew` (`manifest_test.go`, `steps_test.go`, the `managedEngine`
harness with its fake ledger and `sessiontest.FakeProc`):

- The validation table: every limit above, each list's count, ids across
  lists and against a task's checks, a bad env name, a `CONDUCTOR_` name, a
  loader, a bad port name, a placeholder naming no port, a probe with both
  argv and url, a URL off the loopback, `setup` beside `manifest`, a manifest
  without tasks, `schema` 2 in a file, a file over 256 KiB, a crew's manifest
  over 64 KiB; `effectiveManifest` lifting `setup`; `expand` on argv, URL and
  preview, never splitting an element; the expanded bounds: twenty
  `${CONDUCTOR_STATE_DIR}` in a `preview` of 457 bytes, which validation
  accepts, expand past 512 with any real state directory (the test's
  temporary one is near 100 bytes; no long directory is needed to show it)
  and fail the tree at `manifest` with the expanded size in its words,
  uncounted in the worktree and counted in a checkout, nothing written or
  run; the same for an argv element past 4096 and a probe URL past 2048
  (more placeholders).
- `store_test.go`: the revision bumped on a changed manifest and kept on an
  unchanged save, a `ManifestRevision` on the value handed to `Update`
  ignored, a legacy crew's JSON
  unchanged; the counter kept by a save that removes the manifest and, the
  store reopened, one past it when a different manifest is added again
  (update, remove, reopen, re-add: 2, 3 and 4, never 1 again).
- The attempt's worktree: setup, services (a shell loop writing its pid to
  the state directory, `.conductor/state` in the tree), a probe by file and
  a probe by URL (an `httptest` server on the loopback), the ports as
  `CONDUCTOR_PORT_*` in a step's environment (a step that writes `env` to a
  file) and in the session's (`FakeProc`'s spec), exactly the injectable
  `CONDUCTOR_` names and no token, the task file's section and
  `task.json`'s `manifest` as the files are on disk before the launch (the
  ports and the preview in them), every row in the fake ledger with its
  phase, manifest identity, ports, words and log; a service's `started` row
  before its process and the service stopped when its `running` row is
  refused.
- Escaped descendants: a service whose command `setsid`s a child and exits
  fails the tree at `service <id>` with the foreground words, the tree
  cleaned after, the child not the tree's (the test ends it, as `proc`'s
  test does); a setup step whose `setsid` child holds its output fails at
  that step with `proc`'s words in its `error`, uncounted in a worktree;
  one whose child redirected its output finishes as its exit says and the
  child is left alone.
- The tree executor: the native one binds and releases a port and probes
  an `httptest` server, and against one that answers `302` to a second
  `httptest` server its try fails on the `302` and the second server sees
  no request (its handler counts them); the harness's fake hands out
  numbers from a range and answers probes from a map, and every engine
  test runs on it, so the engine reaches no network but through the
  executor.
- The `env` rule in every tree: a file manifest at a result commit that adds
  a name not in `envPassthrough` is refused at `manifest`, counted; the same
  name in the worktree's manifest fails uncounted and pauses; a name allowed
  but unset in the server's environment the same way in a result's
  checkout (refused, counted) and in the next attempt's worktree
  (uncounted, paused); a session's and a step's environment carry the
  approved names alone.
- A setup step that fails in the worktree: the attempt `failed` at `setup
  deps` with `uncounted`, the task `ready` with the words, `extra` one
  more, the run paused with `pausedWhy`, no dispatch until unpause, the
  prerequisites run again at unpause (`k` 2), the next attempt numbered
  after and started where the uncounted one started (attempt 1's result B
  refused at a check, attempt 2 at B failed at `setup deps`, attempt 3 at
  B), and the same after a continuation; at a checkpoint: attempt 1 saves
  the checkpoint C and its session ends, attempt 2 at C fails at `setup
  deps`, uncounted, the run not paused, `attempt.failed` naming C, attempt
  3 dispatched at once at attempt 1's start with `TaskPrevious` naming the
  checkpoint as not usable and no checkpoint section, and the same after a
  continuation made between attempts 2 and 3 (its `t.checkpoint` nil from
  the rows); attempt 3 failing at the same step there pauses the run.
- A setup step that fails in the checkout: counted, the next attempt where
  the refused one started (the existing `refused_result_test.go` cases,
  kept, with rows now); a checkout refused at a service, the same.
- Tracked content changed in the checkout, each author its own case: a
  setup step that writes a tracked file, failed at `setup <id>` with the
  step's words (the existing
  `TestTheSetupRunsInTheChecksCheckoutAndMayChangeNothingTracked`, its
  stage now the step's); a service that writes one before the tree is
  ready, and a probe command that writes one, each failed at `ready` with
  the services and probes named and refused before the checks (the next
  attempt where the refused one started); a check beside a running
  service that writes one during it, failed at `check <id>` with the
  service named as the other possible author, counted as a failing check.
- A step whose program could not start: its `error` on the row and in the
  route after the engine let the run go (`ForgetForTest`); two manifest
  identities on one attempt (the worktree's blob and the checkout's) read
  from the route the same way; a probe over HTTP read there with its
  expanded URL.
- A verify step that fails: as a failing check (the next attempt at the
  result with the tail in its task file).
- Cleanup after a result taken (the ledger's order: `check.finished`,
  `task.succeeded`, then the checkout's and the worktree's `tree.ended`),
  after a failed check, after a cancel mid-step (the step, a shell that
  ignores `SIGTERM`, killed by the cancel's commit; the cleanup's row starts
  only after the step's row is `interrupted`, under its own context), after
  a stop mid-service (the stop returns after the service's pid is gone and
  `tree.ended` is committed; under a context already short, it returns with
  `cleaning` above zero and the tree ends after), after a withdrawal (the
  tree ended, its ports released); a stop whose `recordStop` the fake
  ledger refuses: the stop returns the refusal with the run stopped, the
  service's pid gone before it returns (so none is alive once
  `Server.Shutdown`, which passes over the stopped run, returns), no
  `tree.ended` row, and the run's read with no `preview` from the stop's
  first step on; a cleanup step that fails leaves the
  attempt's state as it was and is a row and a log line; `cleaning` above
  zero holds off `evict`, and `Prune` refuses the finished run while its
  cleanup step is held at a gate (a real ledger); `endTree` once per tree.
- Prerequisites: on a goroutine of the run's, the launch answering while one
  is held at a gate (a file the test creates), the pump dispatching nothing
  while another attempt's close pumps it, a stop during one ending it with
  its row `interrupted`; the sole task cancelled while one is held at its
  gate: the run finished in that commit and the generation ended by it,
  the step's process gone and its row `interrupted` with the words, the
  run held under eviction pressure (the harness's `maxRuns` at 1 and a
  second launch) until the goroutine's last commit and forgotten only
  after, and, in `api/shutdown_test.go`, the same run under
  `Server.Shutdown`, the row committed before the ledger closes; a pause
  and an unpause while a generation is held
  at its gate starting no second one (one run of the id in the executor's
  count, `k` 1), the gate released, its `passed` standing and the pump
  dispatching; a stale generation's commit (the harness bumping
  `r.prereqGen` under the gate) dropped with its rows kept; they pass before
  the first dispatch; a failing one pauses with its words and spends
  nothing; a missing `env` name and one not in `envPassthrough` each named;
  a continuation reruns them (`k` 1 in the new run); each a row with its log
  and hash, read by the route after the run is let go.
- A legacy crew with a `setup` id equal to a task's check id still
  launches, with three rows of that id on the attempt, `setup` k 1 (the
  worktree) and k 2 (the checkout) and `check` k 3, each with its phase
  and none overwritten; `effectiveManifest` lifts it with the receipts and
  the uncounted close.
- The file manifest: read at the start commit for the worktree and at the
  result for the checkout (a result that changes the file is checked by the
  new one), the blob id recorded, a result that breaks the file refused at
  `manifest`, a launch with a broken file at the base refused before a run
  exists, a commit without the file a named failure; a launch prepared at
  the pinned base when HEAD moves between `prepare` and the launch
  (`afterAdd`), the run's base and its manifest the pinned commit's; a
  continuation whose recorded base holds a valid file while HEAD holds
  none or a broken one continues; a result commit that adds a phase this
  build does not run yet (PR-2 to PR-4), or `${CONDUCTOR_STATE_DIR}` in a
  step's argv before PR-3, refused at `manifest`, counted.
- Ports: two trees in flight never share one; a lease released at
  `tree.ended`; a number held by a live tree drawn again; the attempt's
  `preview` empty before `tree.ready` (a probe held at a gate), the URL
  after it, empty once the attempt's close is committed, read while a
  cleanup step is held at a gate after the services ended (not after
  `tree.ended` alone), empty after `tree.ended`, and still empty when the
  next tree draws the released number; a worktree service the test kills
  after `tree.ready`: `service.ended` with its exit, the `preview` empty in
  that transaction, the run log's line, the attempt still running and its
  other services stopped only when it closes.
- A service left running across a restart (a real ledger, the engine
  ended, `Recover`): the row `interrupted`, `Recovered.Services` naming its
  pid, the process not signalled; a crash between the services' end and
  the first cleanup row (the engine ended at the seam after
  `service.ended`, the attempt closed, the run finished, the tree row
  `ready`): `Recover` concludes the tree `ended` and `interrupted`, the
  `tree.ended` event recorded, `Recovered.Trees` 1, and the run read after
  has `cleaning` 0.
- The task file's section at the size bound beside the largest cuttable
  parts (an extension of the existing largest-of-each-part test).

Go, `internal/crew/ledger`: the migration from a version-4 fixture (the
rows read back as phase `check`, no manifest, no ports, one try, no error,
the version bumped, the `recovered` event unchanged); `PutCheck` and
`Checks` with the new columns; `PutPrerequisite`, `Prerequisites`, a
prerequisite row concluded by `Recover`, a stopped run whose only open row
is a prerequisite `running` selected and concluded by it (the row
`interrupted`, the run left `stopped`, one `recovered` event), pruned with
the run and counted toward `MaxRunLogs`; a `service` row concluded by
`Recover` and described, a `started` one reported as unconfirmed, and the
most one recovery can meet, 32 open service rows (4 attempts × 2 trees × 4
services) each with an argv of 8 KiB, concluded in one transaction, the
`recovered` event under `MaxPayload` with its counts and one
`service.ended` per row; `paused_why` written by `PutRun`, read back by
`Load` and `Runs`, empty on a version-4 row; `PutTree`
and `Trees`, a tree row
left `ready` under a finished run selected and concluded by `Recover`
(`ended`, `interrupted`, the `tree.ended` event written) and one `ended`
left alone, pruned with the run; `Prune` refusing a finished run whose tree
row is `ready` and one whose prerequisite row is `running`, and deleting
each once the row is `ended` or `finished`; `url` on a probe's row read
back.

Go, `internal/proc` (`serve_test.go`): a service started, its `Started`
pid, `Done` on its own exit with the `Result`, `Stop` ending a process that
ignores `SIGTERM` after the grace and one that handles it before, the group
killed with it, `Inject` names accepted and a token name refused, a
`CONDUCTOR_` name in `Env` still refused, the non-unix refusal;
`run_test.go` (PR-2): `TestRunKeepsHeadAndTail` holds the whole log to
`LogLimit`, the dropped line and the tail inside it.

Go, `internal/api` (`manifest_test.go`): the steps route and the log route
(`200`, the hash header, `404`, `410`, admin only, a run the engine let
go, the hash header exposed to a dev origin), the prerequisites pair the
same, the task read for a run the engine let go (`ForgetForTest`) and,
against a real ledger, after a restart (a second server opened on the
same data directory after the first's `Shutdown`, through the harness's
`restart`, which `New`s one on the first's config and store, as
`crews_test.go` reads the crews back after one; `shutdown_test.go` reopens
no ledger: its one test lists the records after a spent budget): a task
with two attempts answered whole, each attempt's state, commits,
`submitted` and `steps` as the rows have them, no `preview`, each
attempt's log read through the log route, `404` for a task the ledger has
not; `ledger_test.go`: `syncRecords` rebuilding a missing record and one
whose revision lags a `run.paused` commit, the run read after it carrying
the row's `pausedWhy`, and empty again after an unpause; `Launch`
carrying `Inject` and `Allow` into the session's environment
(`sessions_test`-style with a stub agent); `shutdown_test.go`,
beside `TestShutdownWritesEveryRunsRecordWhenItsBudgetIsSpent`, three
cases:
`Server.Shutdown` under the CLI's 10 s budget with a run whose service
ignores `SIGTERM`, the service's pid gone when it returns (PR-4); and a
run whose task succeeded and which nobody stopped (finished, its
`StoppedAt` nil, so the stop loop stops it as any other and its stop waits
for its trees) and whose cleanup step, begun under `r.teardown` before the
shutdown, sleeps past the budget: the cut-off armed at the shutdown's
entry ends it at the deadline less the reserve, the stop returns before
the deadline, the step's pid is gone when `Shutdown` returns, and its row
is `interrupted` and the tree's row `ended` once the ledger is reopened,
by the cut when the reaping and the rows fit the reserve (the code's own
cap) or by `Recover` when the machine missed it; and the same run stopped
from the UI before the shutdown, its stop returned early under a short
context with `cleaning` 1, which the stop loop passes over and `Drain`
waits for: the same rows (PR-5); and a third, a run whose sole task was
cancelled while a prerequisite was held at its gate (the run finished,
the generation ended by that commit): the stop loop stops it as any
other, the step's pid gone and its row `interrupted` before the ledger
closes (PR-2).

vitest: `crews.test.ts` (`manifestProblems` item for item against the Go
table, `toCrewInput` with a manifest and with a lifted `setup`), `tasks.test.ts`
(`stepWords`, `taskActions.preview` offered by the attempt's `preview`
alone, the paused words),
`composables/useApi.test.ts` (`requestText` from a stubbed `$fetch.raw`:
the text, the header, an error normalised as `request`'s).

Playwright: `manifest-editor.spec.ts` (a crew saved with every phase, read
back as sent; a Setup commands box of an old crew shown as the manifest's
setup and saved as `manifest`; an id used twice blocks Save with its words;
the file switch saves `file: true` and hides the lists; `tasks-editor.spec.ts`
updated for the moved Setup box); `manifest.spec.ts` (the stub workers as in
`tasks.spec.ts`: a manifest whose setup writes under `.conductor/`, a
`python3 -m http.server` service on `${CONDUCTOR_PORT_WEB}` with a URL probe
and a preview; the Tasks view's steps and their logs; Open preview answering
from the test while the attempt runs, and gone from the row, the → actions
and the palette once the task succeeded, which closes the attempt, its
tree ending after; the service's
pid gone and the `tree.ended` row present once
the task succeeded; a prerequisite on a file the spec creates later: the run
paused with the step named, Resume continuing it; Cancel mid-step followed by
the cleanup receipt; the Log action opening the modal with the step's text
and the hash the response header carried, which the spec reads itself
through the route and compares). One Playwright run per worktree,
`stopStrays` first, rows found by id.

By hand, which no test reaches: a real project on the WSL box and the Mac
(the live-check pattern of #350): a Node project with `npm ci` as setup,
its dev server as a service with a preview opened in the browser, a
`conductor.manifest.json` committed and a task that edits it; the host broken
on purpose (a variable unset: the server started again with it set and the
run continued, which no test with `t.Setenv` can show; the registry
unreachable: the run unpaused) and the run's words read; a server killed
mid-service and the next start's log; a run stopped from the UI with its
cleanup seen in the receipts.

## Boxcar constraints

The eight of the Program, section 2.5:

1. An attempt starts only through `crew.Launcher`: unchanged; a service is
   not an attempt and starts through `internal/proc`, as a check runs.
2. `session.Local` drives a `session.Process`: not touched. `proc` gains a
   signal argument on its one existing signal site and no new one; the
   engine sees a pid only in receipts, as it does for checks, and holds a
   service behind its own interface (`service`), never as a
   `*proc.Service`.
3. An attempt's context is files relative to its worktree: the task file
   gains the ports, the preview and the manifest's identity; the state
   directory is `.conductor/state` inside the tree's working directory, so
   a tree mounted or copied into a guest carries it; nothing a tree needs
   lives only in the server's memory (the port lease is a server-side
   guard, the numbers are in the file and the rows).
4. The agent talks to the server over the network: not touched.
5. Checks go through `internal/proc`: every step and service does
   (`proc.Run`, `proc.Serve`), with a `Spec` (argv, directory, timeout,
   head and tail), and every receipt carries its directory; the `where`
   column is C10's and these rows will carry it the same way. The engine
   reaches `proc`, the probe's `GET` and the port's bind through the tree
   executor alone (`treeExec`: `run`, `serve`, `probe`, `port`), native
   today and the one value C10 puts behind Runtime, so a probe or a port
   of a tree that is not on the server's machine is never the server's
   own loopback.
6. Platform splits stay platform tags: `Serve` follows `proc`'s unix/other
   split; nothing registers a provider.
7. The ledger's evidence rows assume no native process beyond what checks
   rows record today (a pid, a group, a start time, reported after a crash,
   never acted on).
8. hostagent is a launch site as much as api: not touched; managed runs
   are refused on a host today (`Crew.Launchable`), and the manifest runner is
   the engine's, so a host launch site gets it with the engine, not from
   `internal/api`.

## Open-core checklist

- [x] Article 1: no premium code and no licence check; a manifest is data,
      and the only question asked of the host is whether a tool or a
      variable is there.
- [x] Article 2: project manifests, every phase, receipt, route and screen,
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

**PR-1 — crew: the project manifest model.** `Manifest`, `Probe`, the validation
table, `effectiveManifest`, `expand`, `resolveManifest` (the file form read at a
commit, bounded, decoded strictly, its blob id), the store's revision,
`Crew.Manifest` with `setup` as its legacy; the crew body in
`docs/protocol.md`; the web types and `toCrewInput`, `liftSetup`,
`manifestProblems` and `TASK_LIMITS`. Nothing runs a manifest yet: the engine
reads `effectiveManifest().Setup` where it read `Setup`, so behaviour is
main's, and `Engine.prepare` refuses a launch whose manifest names a phase,
form or placeholder the engine does not run yet (`400 invalid_crew`, "this
build does not run a manifest's services yet"; prerequisites, `env`, `ports`,
services, `ready`, `preview`, `verify`, `cleanup`, the file form, and
`${CONDUCTOR_STATE_DIR}` in any argv, "this build does not expand a manifest's
placeholders yet": `expand` lands in PR-3 and the runner passes argv as it
is (`runCommand`, `verify.go`), so a `mkdir ${CONDUCTOR_STATE_DIR}/cache`
in a `setup` step, which needs no port and no refused field, would make a
directory of that literal name; a `${CONDUCTOR_PORT_<NAME>}` needs `ports`,
refused here; so a manifest saved early is never accepted and silently
skipped or half run); each later PR lifts the refusal for what it runs,
PR-2 applies the same check in `resolveTree` to every manifest it resolves (a
result commit's, a candidate's, a file manifest's), and PR-5 removes both.
Touches `internal/crew`, `internal/api` (`crewInput` and `readCrew`,
above), `docs`, `web/app/composables`, `web/app/utils`.
Tests: `manifest_test.go` (the refusal per phase, for a setup-only manifest
with `${CONDUCTOR_STATE_DIR}` in a step's argv, and the validation table's
refusal of a placeholder in a prerequisite), `store_test.go`,
`api/crews_test.go` (a create and an update round trip with a manifest, read
back as sent with `manifestRevision` 1 and then 2; a body carrying
`manifestRevision` refused `400 invalid_request` as an unknown field;
`setup` beside `manifest` refused `400 invalid_crew`), `crews.test.ts`
(`manifestProblems` refusing the prerequisite's placeholder among the
rest). Depends on nothing. Parallel with nothing
(every later PR builds on it). About 1,000 lines.

**PR-2 — ledger and engine: every step a receipt, every failure named and
attributed.** The migration (`phase`, `manifest`, `ports`, `tries`, `error`,
`url`, the `prerequisites` and `trees` tables), `CheckRow`'s,
`PrerequisiteRow`'s and `TreeRow`'s fields, `Recover`'s services,
prerequisites and trees; `steps.go` with `tree`, the tree executor with
`run` alone, `resolveTree` for the identity, `prepareTree` for the `setup`
phase alone, `runStep` and the attempt's `k` allocation (`verify.go`'s
`K: 1` replaced), `endTree` with no cleanup steps yet (the ports and
services come later; here it waits for the tree to be quiet and records
`tree.ended`) and its hand-off from `endAttempt`, the `uncounted` close, the
successor's start and `pauseManifest`, the prerequisites on their goroutine
with `r.prereq` and the pump's gate (commands only; `env` comes in PR-3),
`r.prereqs` in `idle` and the terminal run's end of a generation,
`refusedBeforeChecks`'s new words, `Run.Manifest`, `pausedWhy` on the run
row and in `Recovered`, `AttemptState.Steps`; the four receipt routes and
the task read's ledger fallback (`handleGetTask`, `crew.LoadedTask`); the
task file's section with
the manifest's identity; the refusal lifted for prerequisites, `setup` and the
file form, and applied by `resolveTree` to every resolved manifest; the base
pinned through `prepare` and `startManaged`, `ContinueRun` passing its own;
the `Access-Control-Expose-Headers` line; `proc.Spec.LogLimit` bounding
the log whole, so a step's or a check's 2 MiB of output is a row the
ledger takes; `docs/repository-map.md`'s `internal/crew/ledger` row saying
the schema number this PR writes (it says 3 on main, which writes 4).
Touches `internal/crew`,
`internal/crew/ledger`, `internal/api`, `internal/proc` (the log bound),
`docs` (`protocol.md`, `repository-map.md`), `web/app/composables` (the
types, `requestText` and the four fetchers, which PR-7's components use).
Tests: the migration fixture, `proc/run_test.go`
(`TestRunKeepsHeadAndTail`: the log whole at most `LogLimit`, the dropped
count the difference, the tail the output's last bytes),
`ledger_bounds_test.go` (a setup step and a check that write 2 MiB,
against a real ledger: each row `finished` with its own exit, its log at
most `MaxLog` holding the dropped line and the output's last bytes, and
the log route's hash header the SHA-256 of the body it serves),
`steps_test.go` (receipts, the attribution
both ways, the pause and the unpause, the prerequisites and their gate, one
generation at a time, the successor's start, the pinned base, a result
commit or a file manifest bringing in an unsupported phase or the
state-directory placeholder, refused at `manifest`, the tree rows and the
crash before the first cleanup row), `composables/useApi.test.ts`, the
existing cases that change with the lift: `refused_result_test.go` (rows
now, and a check's `k` asserted as allocated: past a legacy setup step's
rows of the same id, 1 otherwise),
`managed_test.go`
(`TestTheSetupRunsInTheChecksCheckoutAndMayChangeNothingTracked`: one
`check.setup` event becomes one row of phase `setup` in the checkout, and
the stage `setup` becomes `setup <id>`, the checkout compared after each
step) and
`ledger_bounds_test.go` (`TestALaunchAndASetupThatHappenedKeepTheirEvents`:
`attempt.setup` becomes `step.finished`); `api/manifest_test.go` (the task
read's fallback after `ForgetForTest` and after a restart);
`api/ledger_test.go` (`syncRecords` and the pause words);
`api/shutdown_test.go`'s third case. Depends on PR-1. About 2,100 lines.

**PR-3 — the host's names, the ports and the state directory.**
`proc.Spec.Inject` with `injectable`, `Spec.Allow` carried from the manifest's
`env`; `LaunchSpec.Inject` and `Allow` through `createLocalSession`;
`Engine.SetEnvPassthrough` and its call in `server.go`; the
`env` checks in the prerequisites and, for permission and presence, in
`resolveTree`; port allocation and the engine's leases; the state directory;
`expand` applied to steps, with the bounds on the expanded form; the task
file's ports and state directory; `CONDUCTOR_PORT_*` and
`CONDUCTOR_STATE_DIR` in steps and sessions; `allowedEnv` in every tree;
the executor's `port`; the refusal lifted for `env`, `ports` and the
placeholders. Touches `internal/proc`, `internal/crew`, `internal/api`,
`internal/agents` (the skill's sentence), `docs`. Tests: `proc/run_test.go`
(inject accepted and refused), `steps_test.go` (the environment of a step
and of a session, the leases, the state directory, the expanded bounds, the
env prerequisites,
the `env` rule, permission and presence, in the checkout and the next
worktree), `api` (Launch's inject and allow). Depends on PR-2. About 1,100
lines.

**PR-4 — services, readiness and the preview.** `proc.Serve`, `Service`,
`ServeGrace`, the signal argument; the engine's `service` interface and the
executor's `serve` and `probe`; the `services` and `ready` phases in both
trees; the preview URL on the tree and the session, and on the attempt
between its worktree's `tree.ready` and its clear (the attempt's close,
or a worktree service's own exit); the
services under `r.teardown` and ended at `endTree`, with `r.trees`, the
stop's own ending of the services beside the sessions and its wait after
`recordStop`, `Engine.Draining` and `Engine.Drain` in
`Server.Shutdown`, the cut-off armed at its entry with `teardownReserve`,
and `cleaning` on the run and in `idle`, so no main between two merges
lets a stop return under a service still being signalled;
`Recovered.Services` as descriptors, the counts in `recovered` and the
`service.ended` event per recovered row; a service that dies before
ready, a daemonizer
included; the refusal lifted for services, `ready` and `preview`. Touches
`internal/proc`, `internal/crew`, `internal/crew/ledger` (the recovery
words), `internal/api` (`Draining`, `Drain` and the shutdown test), `docs`.
Tests: `serve_test.go`, `steps_test.go` (services by file and by URL probe,
a dying service and a daemonizing one, the escaped child, the `ready`
comparison with a writing service and with a writing probe, a check
beside a running service that writes, the attempt's `preview` set at
`tree.ready`, empty from the attempt's close on, read while its services
are still being stopped, empty at a worktree service's own exit, and
empty when the number is drawn again, the recovery of 32 service rows at
8 KiB of argv each, the pid
gone
after
the tree, the stop mid-service, the stop whose `recordStop` the fake
ledger refuses, `cleaning` holding off `evict`, the
restart cases against a real ledger), `api/shutdown_test.go`'s first case.
Depends on PR-3. About 1,600 lines.

**PR-5 — verify and cleanup.** The `verify` steps in the checks loop; the
`cleanup` phase in `endTree` under `r.teardown` and the shutdown's armed
cut-off;
cleanup after a cancel, a withdrawal, a stop and a failed step; a cleanup
failure as a receipt and a log line; the launch refusal and
`resolveTree`'s removed. Touches `internal/crew`, `internal/api` (the
shutdown test), `docs`. Tests: `steps_test.go` (every way a tree ends, the
ledger's order on a result taken, the cancel mid-step against a step that
ignores `SIGTERM`, the cleanup cut by `Drain`, the `preview` empty while
a cleanup step is held at a gate after the services ended, once per
tree),
`api/shutdown_test.go`'s second case, `-race -count=30`. Depends on PR-4.
About 800 lines.

**PR-6 — the crew editor's Project manifest section.** `CrewManifestEditor.vue`,
the probes list, the chip inputs, the file switch, the lifted `setup`, the
Save gate and Launch reason; `tasks-editor.spec.ts` updated for the moved
box. Touches `web/app/components`, `web/app/utils`, `web/e2e`. Tests:
`crews.test.ts` additions, `manifest-editor.spec.ts`. Depends on PR-5: the
editor offers every phase, so it lands once the engine runs every phase
and the launch refusal is gone, and after PR-2's edits to
`useSessions.ts`, which its components import (`web/` is one package with
one typecheck, so no two PRs change it side by side). Parallel with
nothing. About 1,100 lines.

**PR-7 — the run page's receipts and preview, the docs and the entry.**
`RunAttemptSteps.vue`, the log link, Open preview on the row, in → and in
the palette, the header's `paused: why` and `cleaning up`, `stepWords`,
`taskActions.preview`; `docs/architecture.md`, `docs/repository-map.md`,
the example crew's prerequisite, the `docs/features.md` entry. Touches
`web/app/components`, `web/app/utils`, `web/app/pages`, `web/e2e`,
`internal/crew` (`examples.go` and its test), `docs`. Tests:
`tasks.test.ts`, `examples_test.go`, `manifest.spec.ts`. Depends on PR-5 and
PR-6. About 1,200 lines.

## Owner decisions

Dated 2026-10-28. The defaults below are this plan's, inside the twelve
articles; the owner decides R1 to R13 at the plan's review, and one
overturned is rewritten here before the PRs are cut.

- **The name, 2026-10-29, the owner's.** "Project recipe" became "project
  manifest", overturning the name D6 kept; the catalog's agent recipes keep
  theirs. The checked-in file is `conductor.manifest.json`, the crew's field
  `manifest`, the Go and TypeScript names follow it (`Manifest`,
  `CrewManifest`, `manifestRevision`), and this plan is
  `docs/plans/project-manifests.md`.
- **R1, two forms.** A manifest is either the crew's (`crew.manifest`, edited
  in the UI, revisioned by the server) or a file, `conductor.manifest.json`
  in the crew's directory, read at each tree's commit, so it is versioned
  with the code and a task may change it; a crew has one or the other.
  Reason: `Crew.Setup` is crew data and stays so (article 7); "versioned"
  and "checked in" in the item's words want the file.
- **R2, attribution by tree.** A step that fails in a tree at a commit the
  agent has not touched (the attempt's worktree at its start: an input, a
  result a checkout verified, a repair commit, the base) is the host's
  or the manifest's failure: not counted, the dispatch paused; one that fails
  in the checkout of the agent's result is the result's, counted, as a
  failing check is; and one that fails in a worktree at a checkpoint
  commit, the agent's own and verified by nothing, refuses the checkpoint
  as #370 refuses a result, uncounted and unpaused, the next attempt where
  the checkpointed one started ("where the phases run"). Reason: it is the
  one line the engine can draw without
  guessing, and it keeps #370's refused-result rule whole.
- **R3, pause, do not retry.** A manifest failure pauses the run's dispatch
  with the step named rather than retrying by itself. Reason: a missing
  tool or a dead registry needs a person, and a loop would spend nothing
  but time; an unpause reruns the prerequisites so a fix is seen.
- **R4, a tree's services live as long as the tree.** The attempt's end
  ends its services and runs its cleanup, also when the task succeeded and
  the session stays idle for inspection. Reason: one predictable rule;
  looking at a result afterwards is PV1's business.
- **R5, the host manages the values.** A manifest names its `env`; a name
  reaches a step or a session only when the server's `envPassthrough`
  allows it, and a value is never stored. Reason: a checked-in file must
  not be able to pull a secret the server's operator did not allow.
- **R6, one table for a tree's steps.** Receipts of a tree are rows of the
  ledger's `checks` table with a `phase`, in one migration of six columns,
  not a table of their own; step ids are unique across the manifest and
  distinct from the tasks' check ids (a legacy `setup`'s keep main's rule).
  The prerequisites, which no attempt owns and the `checks` table's foreign
  key therefore cannot hold, get the one other receipt table,
  `prerequisites`, of the same shape; a tree's lifecycle, which no receipt
  row holds, gets `trees`, one row per tree with its state, read by
  `Recover` and the tests. Reason: the row is already the receipt shape,
  `Recover`, `Prune` and the readers already handle it, and C10's `where`
  lands on both once for every kind of step.
- **R7, Conductor removes nothing.** Cleanup is the manifest's commands, the
  services ended and the ports released; worktrees, checkouts and state
  directories stay for inspection; a process that outlived the owner is
  reported at the next start and never signalled. Reason: `worktree.go`'s
  rule today, and a pid after a restart is nobody's.
- **R8, Conductor's own names are `CONDUCTOR_*`.** The ports, the state
  directory and the preview reach steps and sessions as
  `CONDUCTOR_PORT_<NAME>`, `CONDUCTOR_STATE_DIR` and
  `CONDUCTOR_PREVIEW_URL`, through an allowlisted `Inject`, as the task's
  names do. Reason: one prefix for everything Conductor puts in an
  environment, and the existing refusal of `CONDUCTOR_*` in a manifest's or
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
- **R12, the file's name** is `conductor.manifest.json`, in the crew's
  directory, not under `.conductor/` (a commit tracking `.conductor/` is
  refused by dispatch today, and that stays).
- **R13, a value the host lacks is fixed by a restart.** The server's
  environment is read once, at its start (`pty.ParentEnv` is
  `os.Environ`), and `envPassthrough` is a list of names: exporting a
  variable in another shell changes nothing a running server sees, and no
  reload is added. So a missing variable's words say so ("set it where the
  server starts, start it again and continue the run"), the recovery is a
  restart and a continuation (`continue.go`), whose prerequisites run
  again, and an unpause meanwhile shows the same failure. Reason: a
  reloadable store of host references is another item; the by-hand check
  (a server started without the variable, then with it) proves this one,
  where `t.Setenv` would hide it.

## Acceptance

The item is done when a fresh attempt worktree of a real project becomes
usable the same way every time, a step that fails is named as the step it
is, and a tree is cleaned up after a failure and after a cancellation. That
is observed, never ticked, on the open-source build with no cloud account:

1. On a real project with a committed `conductor.manifest.json` (setup,
   two services with ports, a probe, a preview, a cleanup), a managed crew
   launched from the UI: every attempt's task file names the manifest's blob,
   its ports and its preview before the agent's first line; the receipts
   for every phase are read from the run page with their logs.
2. The host broken on purpose, in two ways. A required variable unset: the
   run pauses with the prerequisite named in the run log, and the words
   say a restart fixes it; the server started again with the variable set
   and the run continued, the continuation's prerequisites pass and it
   goes on (an unpause alone shows the same failure, the environment being
   the server's at its start: R13). The package registry unreachable: the
   run pauses with the setup step named in the task's words and the run
   log; no attempt is spent; the registry back and the run unpaused, it
   continues.
3. A task cancelled mid-setup and a run stopped mid-service, from the UI:
   the services' pids are gone, the cleanup rows and `tree.ended` are in
   the ledger, the ports are free, and the next launch gets fresh ones.
4. A result that breaks the setup: refused at the named step, counted, the
   next attempt where the refused one started with the refused result named
   in its task file.
5. The server killed mid-service and started again: the next start's log
   names the service's pid as possibly alive; the continuation reruns the
   prerequisites and verifies the candidate with its manifest.

Done means all five seen on the WSL box and 1 to 3 on the Mac (the owner's
machines; the owner runs or watches 1 to 3, and reads the receipts), with
the run recorded in `docs/features.md`. The owner decides R1 to R13 at the
plan's review and merges every PR; CI is the only gate.
