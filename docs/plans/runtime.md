# Runtime: where an attempt's process and its checks run (Program item C10)

The Runtime is the one place where the processes of Conductor's sessions
and of a managed run's trees start: native processes on the machine that
runs the session, as the user, unless a provider is registered. This plan
is Program item C10 (the Program, section 4.2), written against `main` at
`293bd7f6` (2026-10-30) after reading the launch paths, the engine's tree
executor, the checks runner, the host agent and the ledger. It is reviewed
before any of its code; the PRs at the end are cut into issues after the
review, one issue, one branch `issue-N`, one PR each (AGENTS.md). Line
numbers are `main`'s at that commit.

The names follow the railroad (the Program, section 1.2): Yardmaster is the
agent harness whose engine changes here, and an attempt's checkout is a
worktree, never a workspace (decision D5).

The other Program items this plan meets, in plain words: C1 makes the
public package `ext` (the eight extension interfaces, a registry, the free
defaults in `ext/local`) and the composition root that reads the registry
once at start, `docs/plans/ext.md`; this plan needs it merged. C6 adds
`conductor export`, a zip of the data directory. C9 adds `GET
/api/capabilities` and `ext/exttest`, a fake and a contract suite per
interface. DS2 shows a ticket's runs, candidates and checks, this item's
`where` among them. EN1 is project manifests: its PR #441 (issue #396), in
review as this is written, gives the engine's tree executor the `serve`
and `probe` this plan puts behind the Runtime, and is to merge first. F is
the Boxcar integration, a Linux microVM running an attempt; it comes after
this item and is not part of it. P0 is the private premium repository's
first PR, the first code outside this repository that imports `ext`.

## Articles it serves

1. No premium code, no licence check: the only question asked is whether a
   Runtime is registered, once, by the composition root; nothing in this
   repository branches on a plan or a subscription.
2. Fully useful alone: the native Runtime is what `main` does today; every
   session, crew, managed run, check, service and probe keeps running with
   nothing registered, which is every build of this repository.
3. Paid features gated by hosted dependencies: not touched. The Runtime is
   no paid feature and has no hosted dependency; Boxcar (item F) is free and
   runs on the user's own machine.
4. Stop, inspect, revoke, export, required approvals: stop still ends a
   session through its process's `Stop` (native: `pty.Process.Stop`, the
   same signals) and a run's checks and services through their contexts;
   inspect gains `where` on the task read and the two receipt routes;
   revoke, export (the ledger file, C6) and the approval paths (`Submit`,
   the control role, the held question) are untouched.
5. Dependencies one way: no new module; the Runtime's part of `ext`
   imports the standard library alone; its part of `ext/local` imports
   `internal/pty` and `internal/proc`, which import nothing of `ext`; C1's
   lint rule (no private module) holds.
6. No feature build tag: none added; `internal/pty` and `internal/proc`
   keep their `unix` and `!unix` splits.
7. The free default is `main`: the native Runtime is `pty.Start`,
   `proc.Run`, `proc.Serve`, the probe and the port draw, moved behind one
   interface unchanged; a registered provider could only add.
8. A provider whose hosted dependency is unreachable: not touched; the
   native Runtime depends on nothing, and the composition root takes what is
   registered once at start, waiting on no service.
9. `CGO_ENABLED=0`: no cgo; the ledger stays `modernc.org/sqlite`; the
   migration is three `ALTER TABLE … ADD COLUMN`.
10. The repository contract: this plan before code; one issue, branch and
    PR per row of The PRs; race tests per package; no WebSocket protocol
    change (`internal/proto` and `web/app/utils/protocol.ts` untouched), the
    HTTP additions in `docs/protocol.md`; no new dependency, so no `make
    notices`.
11. Names: no new cloud name. The new field `where` names a runtime;
    `docs/protocol.md` tells it apart from a crew's `where` (`server` or
    `host`, where its members run) and an integration's (the file an
    adapter installs into).
12. Execution behind one seam: this item is that seam. After it every
    process of a session or of a managed run's trees starts through one
    Runtime, and every attempt and evidence row in the ledger says which.

## Why and what happens

Yardmaster is the execution authority, native execution on macOS, Linux and
WSL stays foundational, and the seam that would let a Linux microVM run an
attempt is built now as code rather than kept as a rule (the Program,
sections 1.3 and 2.5, decision D7). On `main` the places a process of a
session or a run starts are few and known:

- A server session: `createLocalSession` calls `pty.Start`
  (`internal/api/sessions.go:308`). It is every server session's path: a
  person's launch, a resume, a legacy crew member's and a managed attempt's
  (`crew.Launcher.Launch`, implemented at `internal/api/runs.go:55`).
- A hosted session: `hostagent.Run` calls `pty.Start`
  (`internal/hostagent/agent.go:237`).
- A managed run's trees: the engine's tree executor `treeExec`
  (`internal/crew/steps.go:179`) runs a task's checks (`verify.go`), a
  project manifest's steps and prerequisites (`runStep`, through
  `runCommand`, `steps.go:1319`) with `proc.Run`, its services with
  `proc.Serve`, its readiness probes, and draws its ports. Its comment
  already says the Runtime seam (C10) wraps it.

After this item:

- `ext.Runtime` is the one interface behind all three: a terminal process
  (`Start`), a bounded command (`Run`), a service (`Serve`), one try of a
  probe (`Probe`), a port (`Port`), and its name (`Where`). `ext/local`'s
  native Runtime is `main`'s code.
- The composition root (`runServe`, `runHost`) asks the registry for a
  Runtime once and gives the server (its sessions and, through it, the
  engine) and `conductor host` the one it finds, or the native one.
- The ledger's attempts, checks (a task's checks and a manifest's steps
  share the table) and prerequisites rows say where they ran (`where_ran`,
  schema version 6); the task read, the steps route and the prerequisites
  route report it as `where`. Every row a build of this repository writes,
  and every row an older ledger held, says `native`.

What a person sees: nothing different. Sessions open, take input, resize
and stop as they do; crews and managed runs run as they do; Pause, Cancel,
Retry, Accept anyway and Stop act as they do. Reading a task's attempts
(`GET /api/runs/{run}/tasks/{task}`) shows `"where": "native"` on each
attempt and each receipt. The run page shows nothing new: DS2's evidence
view is where a person will read it, and the run page has a reason to show
it only once item F gives it a second value. A developer finds the Runtime
in `ext`, and a test that fails any change starting or signalling a process
outside the runners that already do.

The harness plan asked for a narrow internal seam and ruled out a provider
registry and any schema field for Boxcar (`docs/plans/agent-harness.md`,
sections 5 and 18). The Program, which a plan does not override, puts the
Runtime in `ext`'s registry now (decision D7). Nothing here is Boxcar's:
the registry's Runtime and the `where` column are provider-neutral, and
`native` is their one value; PR-1 adds a line to that plan's errata saying
so.

## The changes

### `ext`: the Runtime's contract

C1 declares the eight interfaces (the Program, section 2.4, item C1); this
plan specifies the Runtime's members, since C10 is the item that calls
them. If C1 ships a narrower Runtime, PR-1 completes it as written here:
nothing outside this repository implements `ext` before P0, so the change
costs no one (decision RT13). The types are `ext`'s own, with no alias to an
internal type. They are in-process Go types (a writer, a callback, an open
directory), not wire DTOs: no hosted service implements a Runtime.

```go
// Where names the runtime a process ran in, as the ledger records it:
// ^[a-z][a-z0-9-]{0,31}$. Native is the free default's.
type Where string

const Native Where = "native"

// Runtime is where the processes of Conductor's sessions and of a managed
// run's trees run: native processes on this machine, as the user, unless a
// provider is registered.
type Runtime interface {
	Where() Where
	Start(ctx context.Context, s TerminalSpec) (Terminal, error)
	Run(ctx context.Context, s CommandSpec) CommandResult
	Serve(ctx context.Context, s CommandSpec) (ServiceProcess, error)
	Probe(ctx context.Context, s ProbeSpec) (status int, err error)
	Port(ctx context.Context, ref ProcessRef) (int, error)
}
```

- `ProcessRef{Session, Run, Task string; Attempt int; Tree string}`: what a
  process is for. A session's terminal names its session, and a managed
  attempt's also its run, task, number and tree `worktree`; a legacy crew
  member's its run alone beside the session; a check names its attempt and
  tree `checkout`; a manifest step its attempt and its tree's kind; a
  prerequisite its run alone; a probe and a port their attempt and tree.
  The native Runtime ignores it; a provider may group an attempt's
  processes by it (item F).
- `TerminalSpec{Argv []string; Dir string; DirFile *os.File; Env []string;
  Cols, Rows uint16; Ref ProcessRef}`: `pty.Spec`'s fields
  (`internal/pty/pty.go:21`). `Env` is the whole environment as Conductor
  built it (`pty.BuildEnv`); `DirFile` is the folder `openCwd` opened and
  checked, which the native Runtime starts the process in (on Linux by its
  descriptor, elsewhere by its path checked to still name it, as `main`).
- `Terminal`: `io.Reader`, `io.Writer`, `Resize(cols, rows uint16) error`,
  `Done() <-chan struct{}`, `Exit() ExitStatus`, `Stop(ctx, grace) error`,
  `Process() ProcessInfo`: `session.Process`'s members
  (`internal/session/local.go:27`) and the process as reported.
- `ExitStatus{Code int; Signal string; Err string}`; `ProcessInfo{PID,
  Group int; StartedAt time.Time}`, recorded and logged, never acted on.
- `CommandSpec{Argv []string; Dir string; Env []string; Timeout
  time.Duration; Head, Tail int; Log io.Writer; LogLimit int64; Started
  func(ProcessInfo); Ref ProcessRef}`: `proc.Spec`'s bounds with the
  environment whole, built by `proc.Prepare` (below), so the filter that
  keeps `CONDUCTOR_*` tokens and blocked loaders out stays in this
  repository whatever runs the command (decision RT8).
- `CommandResult`: `proc.Result`'s fields one for one
  (`internal/proc/run.go:108`).
- `ServiceProcess`: `Done() <-chan struct{}`, `Result() CommandResult`,
  `Stop(ctx) error`: `proc.Service`'s (#441).
- `ProbeSpec{URL string; Interval time.Duration; Ref ProcessRef}`: one try,
  no redirect followed, as #441's `probeURL` makes it.

Each method's doc states the contract, which is what the native one does:
`Start` starts the process in `DirFile`'s directory with exactly `Env` on
a terminal of `Cols` by `Rows`, `ctx` bounding the start, not the
process's life; `Run` and `Serve` keep `proc`'s semantics (argv, never a
shell; a timeout `Run` requires, at most `proc.MaxTimeout`; the output's
head and tail; the log within `LogLimit`; `Started` once the process exists
and before its output is read; what the command left in its process group
ended with it); `Probe` tries once from where the tree's services listen;
`Port` gives a number free there; `Where` is constant for the Runtime's
life. A Runtime receives a session's environment whole, its agent token
among it (the address-based path of constraint 4), and logs and keeps none
of it.

The registry holds one Runtime (section 2.4), read once by the composition
root before `ext.Freeze()`. The Program writes the getter as `ext.X()`; Go
cannot give a function the name of a type in the same package, so C1 names
it, and this plan says "the registry's Runtime".

### `ext/local`: the native Runtime

`local.Native()` (C1 names the defaults; this is the Runtime's): `Where`
native; `Start` is `pty.Start`, its `*pty.Process` wrapped (`Exit`'s error
as words, `Process` from `PID()` with the group the same number, as
`creack/pty`'s setsid makes it, and the time `Start` returned); `Run` is
`proc.Run` with `Spec.Environ`; `Serve` is `proc.Serve` likewise; `Probe`
is `proc.Probe`; `Port` is `proc.FreePort`. `internal/api`,
`internal/crew` and `internal/hostagent` take their default from it, so
`ext/local` must import nothing that imports one of them (Go would refuse
the cycle); its Runtime needs `ext`, `internal/pty` and `internal/proc`
alone. Should one of C1's other defaults need such a package, the native
Runtime moves to a package of its own under `ext/local` (decision RT13).
Go's internal rule lets `ext/local` import `internal/…`; another module
imports `ext/local`, never `internal/…`.

### `internal/proc`

- `Prepare(s Spec, service bool) ([]string, error)`: the checks `bounds`
  makes, in its order (no command, the timeout, a relative directory, a
  `CONDUCTOR_`, blocked or malformed name in `Env`, a name in `Inject` that
  is not one of Conductor's own), then the environment `environment()`
  builds (the session's allowlist and `Allow`, `Env`, `Inject`, `TERM=dumb`,
  `NO_COLOR=1`). Its error is the words `Result.Err` carries for the same
  spec on `main`.
- `Spec.Environ []string`: the whole environment `Prepare` built. With it
  set, `Allow`, `Env` and `Inject` must be empty (refused: "proc: Environ
  beside Allow, Env or Inject"), and each entry must be `NAME=value`, the
  name non-empty and free of `=` and NUL; the names were held to their
  rules when `Prepare` built it (its `CONDUCTOR_RUN` or `CONDUCTOR_PORT_WEB`
  is Conductor's own, which `checkName` would refuse in `Env`). `launch`
  sets `cmd.Env` to it.
- `Probe(ctx, url string, interval time.Duration) (int, error)` and
  `FreePort(ctx) (int, error)`: `crew`'s `probeURL`, `probeClient` and
  `freePort` (`steps.go:191`, and #441's), moved unchanged with their tests;
  `crew`'s `nativeExec` calls them until PR-2 replaces it.

### `internal/session`

- `session.RuntimeProcess(t ext.Terminal) Process`: the adapter a session
  drives; `Exit`'s words become a `pty.ExitStatus` again (`Local` reads only
  its `Code`, `local.go:669`). `Local` stays the only writer to a session's
  process (the guarded submission and a person's input, as today): the
  server and the host hand the `Terminal` to it and keep no other handle,
  so no second raw write path appears (the Program, section 1.5).
- `sessiontest.FakeRuntime`: the contract's fake for every package's tests.
  Its `Where` is its own (`fake` unless set); it records every call with its
  spec and ref; each method answers as the test sets it (a `FakeProc`-backed
  `Terminal`, a scripted result, maps for `Probe` and `Port`) or, with
  `Base` set, passes the call to it (`local.Native()`), so a run's real
  work can be done under a `where` that is not `native`. It imports `ext`,
  `ext/local` and `internal/pty`, never `internal/session`, so the
  session's own tests may use it.

### `internal/crew`

- The engine's executor is the Runtime's: `execFor(rt ext.Runtime)
  treeExec` replaces `nativeExec`. Its `run` builds the environment with
  `proc.Prepare` and calls `rt.Run`; a spec `Prepare` refuses is answered as
  `proc.Run` answers it on `main` (`Exit` -1, the same words); the result
  comes back field for field. `serve`, `probe` and `port` go through
  `rt.Serve`, `rt.Probe` and `rt.Port`, a `ServiceProcess` wrapped as the
  engine's `service`. The executor's functions take the `ProcessRef` first:
  `runCommand`'s callers know the attempt and the tree (`verify.go:176` for
  a check, `runStep` for a step or a prerequisite, the service's start and
  its probe, `resolveTree`'s port draw).
- `Engine.SetRuntime(rt ext.Runtime) error`, beside `SetCheckConcurrency`
  and `SetEnvPassthrough` and set like them before the first launch: it
  refuses nil, a `Where` that is not a name, and any call once the engine
  has launched a run. It writes the executor and `where` under `e.mu`; the
  first launch, which starts every goroutine that reads them, comes after.
  `NewEngine` starts with `local.Native()`, so every engine test runs its
  checks and steps through the adapter, as production does.
- An attempt's `where` is set where the attempt is made, from the engine's
  Runtime (`task.go:1148` in the pump; `continue.go:632` for a candidate
  verified again), and never changed. `attemptRow` (`task.go:1320`) writes
  it, and so does every receipt of the attempt (`verify.go:158` for a
  check, `steps.go:654` for a step); a prerequisite's row (`steps.go:828`)
  takes the engine's.
- `AttemptState.Where` (`json:"where,omitempty"`: a run record saved before
  this item has none) and `StepState.Where` (`json:"where"`), filled by
  `attemptStateOf`, `attemptStateOfRow` (`continue.go:908`), `StepStates`
  and `PrerequisiteStates`.

### `internal/crew/ledger`: schema version 6

`Version` becomes 6: EN1 took 5 for its receipts (#394), and its plan
already says the second of the two to land takes 6 (decision RT1). The
migration from 5 is SQL alone, in `migrate`'s one transaction with the
version's update:

```sql
ALTER TABLE attempts ADD COLUMN where_ran TEXT NOT NULL DEFAULT 'native'
ALTER TABLE checks ADD COLUMN where_ran TEXT NOT NULL DEFAULT 'native'
ALTER TABLE prerequisites ADD COLUMN where_ran TEXT NOT NULL DEFAULT 'native'
```

The `schema` a new database is made with has the same three columns,
default included, so a migrated database and a new one are alike. The
column is `where_ran` because `WHERE` is a keyword, as `pgroup`,
`commit_id` and `from_member` are the ledger's names for theirs; the Go
fields are `AttemptRow.Where`, `CheckRow.Where` and `PrerequisiteRow.Where`
(decision RT2). `PutAttempt`, `PutCheck` and `PutPrerequisite` write it and
refuse a row with none (`ErrNoWhere`) or one past 32 bytes (`ErrTooLarge`)
(decision RT5); `Attempts`, `checkCols` and `prerequisiteCols` read it.
`WhereNative = "native"` is the ledger's constant. `Recover`, `Prune`,
`Events` and the trees table are unchanged.

### `internal/api`

- `Server` keeps its Runtime (`local.Native()` from `New`), under `s.mu`.
  `SetRuntime(rt) error` sets it and the engine's (`Engine.SetRuntime`) and
  refuses once the server has started a session of its own (a hosted one
  runs on its host) or the engine a run; `Runtime()` reads it.
- `createLocalSession(ctx, req, crewRef)` starts the process through the
  Runtime's `Start` instead of `pty.Start`, with the `ProcessRef` its
  `crewRef` gives; a refused start is `502 start_failed` as today. The
  "session started" log line keeps its `pid`, from `Terminal.Process()`
  (`sessions.go:375`). Its three callers pass their context: the request's
  (`handleCreateSession`, the resume at `resume.go:173`) and `Launch`'s
  own (a crew launch's, bounded by `runTimeout`, or an attempt's, which
  ends with the attempt).
- No route is added or changed: the task read, the steps route and the
  prerequisites route report `where` through `crew`'s types.

### `internal/hostagent`

`Options.Runtime ext.Runtime`: nil is `local.Native()`, as a nil `Log` is
`slog.Default()`. `Run` starts its command through it with the ref
`{Session: <registered id>}`; the exit code `Run` returns comes from the
`Terminal`'s `Exit` (`:316`). The `agent.proc` field (`agent.go:327`,
written at `:279` and read nowhere) is dropped rather than made a
`Terminal`, so nothing on the host holds the process but its `Local`.

### `internal/cli`

- `pickRuntime(rt ext.Runtime, ok bool) (ext.Runtime, error)`: the
  registry's answer in, the Runtime out (the registered one, or
  `local.Native()` when there is none), refusing one whose `where` is not a
  name with the words naming it; a function of its arguments, so its tests
  touch no global registry.
- `runServe`: where C1's composition root reads the registry, it reads the
  Runtime too and passes it through `pickRuntime`, then calls
  `srv.SetRuntime` right after `api.New` (`serve.go:173`), before any
  listener is bound; an error stops the server with its words.
- `runHost`: the same, into `Options.Runtime` (`host.go:147`).

### `web`

`web/app/composables/useSessions.ts`: `RunAttempt.where?: string` and
`RunStep.where: string`, so the client's types stay the server's; no
component reads them yet.

### The boundary test

`ext/local/boundary_test.go`, `TestOnlyTheRunnersStartOrSignalAProcess`,
makes constraints 1, 2 and 5 of the Program's section 2.5 a failing test
(decision RT12). It walks the module from its root (`../..`), skipping
`testdata`, every directory whose name starts with `.` or `_` (the
worktrees under `.claude/` among them) and every directory with a `go.mod`
of its own, parses each non-test Go file whatever its build tags
(`go/parser`, the imports matched by path whatever their local names) and
fails, naming the file, the line, the call and article 12, on a call of
`os/exec`'s `Command` or `CommandContext`, `os.StartProcess`,
`os.FindProcess`, `syscall`'s `ForkExec`, `Exec` or `Kill`,
`golang.org/x/sys/unix`'s `Kill`, or `Kill` or `Signal` called on a
selector ending in `.Process` (`cmd.Process.Kill()`), outside the packages
that do so today: `internal/pty` (sessions), `internal/proc` (checks,
steps, services), `internal/gitcli` (git, and `gitclitest`),
`internal/nvim` (the editor), `internal/agents` (the identity probe),
`internal/reach` (the gateway lookup on darwin) and `internal/cli/up.go`
(opening the browser). `exec.LookPath` is allowed anywhere. `main` passes it as it stands; a new site is a reviewed edit of
the allowlist. A second case runs the walker over a planted file in a
temporary tree and expects the report.

### Docs

`docs/protocol.md`: `where` in the task read's attempts and in the steps
and prerequisites routes' receipts (rows at lines 1781 to 1784), its rule
and bound, and the line that tells it from a crew's `where`; the Managed
runs paragraph on what a run reports (line 2541), whose latest attempt
carries it.
`docs/architecture.md`: the server-hosted sessions diagram's start line
("start through the Runtime: native, creack/pty"), a paragraph in Crew runs
on where an attempt and its checks run and what the ledger records, and the
Packages rows. `docs/repository-map.md`: the `ext` row's Runtime and
`ext/local`'s native one, `internal/proc` (`Prepare`, `Environ`, `Probe`,
`FreePort`), `internal/crew` (the executor is the Runtime's),
`internal/pty` and `internal/session` (one implementation of a
`session.Process`; the adapter), `internal/hostagent`, and
`internal/crew/ledger` (schema version 6, `where_ran` on attempts, checks
and prerequisites); the `app` row is C1's and unchanged.
`docs/plans/agent-harness.md`: one errata line under its 2026-10-22
errata, that the execution seam is `ext.Runtime` in `ext`'s registry by
decision D7 (this plan), its section 18 line on Boxcar standing until item
F. `docs/features.md`: the item's entry, beside C1's.

## Events, orderings and invariants

States and transitions: none change. An attempt still runs reserved →
launched → running → submitting → verifying → succeeded or failed (or ends
cancelled, stopped, interrupted, uncertain); a receipt started → running →
finished or interrupted; a tree resolved → ready → ended; a run's states as
they are. No event kind is added and no payload changes: the rows carry
`where`, the events do not.

What is written before what:

1. The Runtime is set before the first launch: `runServe` calls
   `SetRuntime` before a listener is bound, and `Engine.SetRuntime` and
   `Server.SetRuntime` refuse once a run or a session has started, so a
   server's sessions and its engine's trees always run in one Runtime.
2. An attempt's `where` is in memory from the moment the attempt is made
   and is first written in its reservation's transaction (`attempt.reserved`,
   `task.go`), before its worktree, its session or any process of it exists,
   as every row of the attempt is today.
3. A receipt carries its `where` from its first row (`check.started`,
   `step.started`), committed before its process starts, and keeps it
   through its running and finished rows.
4. Nothing rewrites a `where`: `Recover` concludes states and leaves it; a
   continuation's new attempts (a candidate verified again among them) take
   the `where` of the engine that makes them; an imported result keeps its
   source run's rows.

What the ledger holds after the change: `where_ran`, NOT NULL, at most 32
bytes, on every attempts, checks and prerequisites row. The trees table is
unchanged: a tree's lifecycle row is its attempt's, whose `where` covers it
(decision RT3).

The migration, version 5 to 6: three `ADD COLUMN` statements with
`DEFAULT 'native'` in `migrate`'s one transaction with the version's
update, or nothing; one that fails leaves the database at version 5,
opened read-only with why, as every migration does today. It is
conservative in the owner's sense of the version 3 to 4 migration (claim
nothing the rows cannot support; decision RT4): before this item nothing
could run a process of a session or a run but the native code, so `native`
is what happened to every row a version 5 database holds, not a guess. It
says `native` of an attempt that never started a process (reserved, then
uncertain or failed before its launch), which the column means as the
runtime the engine dispatched it to. A version 6 database opened by an
older Conductor opens read-only (`ledger.go`, a later version), and that
Conductor's readers name their columns, so it reads the rows and never
writes one without `where`.

Invariants, each held by a test (Tests per surface):

- I1. Every attempts, checks and prerequisites row has a `where`: the
  ledger refuses one without; the crew tests' fake ledger refuses it too, so
  every engine test checks it.
- I2. An attempt's receipts have the attempt's `where`.
- I3. A server's sessions and its engine's trees use one Runtime, set once
  before the first launch.
- I4. Every refusal, environment, exit code and log line a native start or
  run gives is `main`'s, byte for byte.

Conflict rules: none. The ledger is one writer's (the owner lock); a
replication of it (C6's continuity store, CS1) carries the column as part
of the rows.

## Out of scope

- Boxcar, any VM, KVM or container code, a second Runtime or a placeholder
  of one, and a provenance class on the evidence rows: item F (the
  Program, sections 2.5, 4.6 and 7).
- Amending "checks run natively as the user" and the harness plan's
  section 18 line on Boxcar: item F does both (the Program, section 4.6).
- What a guest would need beyond this seam, each of which works on the
  server's machine today and keeps doing so, for item F to propose: a
  `where` reported per process rather than per Runtime; hooks marking the
  start and end of an attempt's place, and a Runtime's own shutdown; the
  notify URL a guest reaches the server at, and the path of the binary the
  hooks run (`CONDUCTOR_BIN`); the skill installed into the agent's own
  configuration and the agent's plans directory outside its folder (#289);
  the Files tab and the editor (`internal/nvim`) over a guest's tree; the
  preview URL a person opens, an address on the server's machine; the
  engine's comparison of a checkout after each check (`checkoutIntact`, git
  on the server's machine), which sees a guest's changes only through a
  mounted tree; and the catalog's availability lookup and identity probe
  asking the Runtime instead of the server's machine.
- Managed runs on a host: `Crew.Launchable` refuses a crew whose `where` is
  `host` (`internal/crew/crew.go:599`), unchanged.
- `GET /api/capabilities` naming a registered Runtime, and `ext/exttest`'s
  public fake and contract suite: item C9 (decision RT14). `ext.Host`: item
  C1.
- Showing `where` in the web app: DS2's evidence view, then item F.
- Open decisions O9 (where Boxcar's code lives; item F answers it), O12
  (the names `ext` and `app`; item C1) and O16 (paid features' UI; not
  touched): see Owner decisions.

## Tests per surface

Every PR runs, before each push, the checks of `docs/checks.md` for its
surfaces: Go, `make lint`, `go test -race -count=1 ./...` (the whole
module) and `make lint-static`; PR-4 also the web's `npm --prefix web run
typecheck`, `npm --prefix web test` and `make test-e2e`. PR-2 and PR-3 run
`make test-e2e` too, though they change no web file: every session and
check of the e2e stub starts through the Runtime from then on, so the
browser suite is evidence of no behaviour change. The Mac's row
(`python3 scripts/preflight.py --machines mac --only go-test-race`, under
the preflight lock) runs on every PR, since Darwin's PTY start (by path)
and exit watch (kqueue) are not Linux's and CI's race tests run on Linux
alone. No Python script changes, so the scripts suite is not affected. Each
PR carries a test that a plain build registers no Runtime and runs natively,
and every engine, session and host test it touches runs on the native
default. The tests wait on the mechanism, never the clock (AGENTS.md,
"Tests that do not flake"): a process's `Done`, a row the ledger holds, a
call the fake recorded.

Go, by package, the tests the PRs add:

- `ext/local` (PR-1): `TestNativeStartsATerminalInItsFolder` (the folder
  renamed after it was opened, the process's working directory still it on
  Linux, as `internal/pty`'s start tests have it; output, input, resize, exit
  0, a `Process` with a pid), `TestNativeStopEndsTheGroup`,
  `TestNativeRunIsProcRun` (exit, signal, head and tail, a log within its
  limit, `Started` before the output, a timeout told from a cancel),
  `TestNativeServeStartsAndStops`, `TestNativeProbeAndPortAreProcs` (the
  native's `Probe` and `Port` answer as `proc`'s), `TestAPlainBuildRegistersNoRuntime`
  (the registry's Runtime is empty; `Native().Where()` is `ext.Native`),
  `TestOnlyTheRunnersStartOrSignalAProcess` with its planted case.
- `internal/proc` (PR-1): `TestPrepareRefusesAsRunDoes` (for each refusal,
  `Prepare`'s words are the `Result.Err` `Run` gives the same spec, in
  `bounds`' order), `TestRunWithEnvironGivesExactlyIt` (the command prints
  its environment, Conductor's own `CONDUCTOR_RUN` and `CONDUCTOR_PORT_WEB`
  among it, and nothing else), `TestEnvironBesideAllowIsRefused`, and the probe's and
  the port's tests moved from `internal/crew` with the code (#441's "the
  native executor's probe asks the loopback through a client of its own"
  and its redirect case).
- `internal/session` (PR-2): `TestFakeRuntimeRecordsWhatItIsAsked`
  (`sessiontest`); (PR-3) `TestARuntimeTerminalDrivesALocal` (a fake
  terminal's output, input, resize, stop and exit code through
  `RuntimeProcess`).
- `internal/crew` (PR-2): `TestEveryProcessOfARunGoesThroughTheRuntime` (a
  crew whose manifest has a prerequisite, a setup step, a service with a
  ready probe and a port, and a task with a check: the fake records each
  call with its ref, the prerequisite's `{Run}`, the worktree's setup
  `{Run, Task, Attempt, Tree: worktree}`, the checkout's setup and the check
  `{…, Tree: checkout}`), `TestAResultCrossesTheRuntimeWhole` (every
  exported field of `proc.Result` has its `CommandResult` field, by
  reflection, and a scripted result lands in the check's row unchanged),
  `TestARefusedSpecKeepsItsWords` (the executor's `run`, given a spec
  `Prepare` refuses, an `Inject` name that is not Conductor's, answers
  `main`'s result, `Exit` -1 and the same words, and never calls the
  Runtime), `TestSetRuntimeRefusals`
  (nil, a nameless `where`, after a launch), `TestAnEngineRunsNativelyByDefault`;
  the existing tests' executor hooks take the ref, unchanged otherwise.
  (PR-4) `TestAnAttemptAndItsChecksReportTheSameWhere` (with the fake
  Runtime, every attempts, checks and prerequisites row and every
  `AttemptState` and `StepState` say `fake`; with the native, `native`),
  `TestAContinuationsAttemptsTakeTheirEnginesWhere` (a run continued by an
  engine with another Runtime: the old rows keep theirs, the new attempts
  and the candidate verified again take the new), `TestARecordWithoutWhereReadsWithout`
  (a run record saved before PR-4: no `where` on its attempts; the task read
  from the ledger says `native`); the fake ledger refuses a row with no
  `where` (I1 in every engine test).
- `internal/crew/ledger` (PR-4): `TestAVersion5DatabaseIsBroughtToVersion6`
  (a database with an attempt, a check, a step and a prerequisite made
  version 5 by `version6Added`, its three `DROP COLUMN`s: reopened, version
  6, each row's `Where` native, its events as they were);
  `TestAVersion4DatabaseIsBroughtToVersion5` and
  `TestAVersion1DatabaseIsBroughtToTheVersionThisConductorWrites` reach
  `Version` with `version6Added` added to their fixtures (the former's
  `Version != 5` reads `Version`); `TestARowWithNoWhereIsRefused` (each of
  the three puts: empty refused with `ErrNoWhere`, 33 bytes with
  `ErrTooLarge`, the transaction undone); `TestARowKeepsItsWhere`;
  `TestWhereNativeIsExtNative`. The rows the ledger's tests and the crew
  tests that seed a ledger build by hand (about 40 literals) gain `Where:
  WhereNative`.
- `internal/api` (PR-3): `TestASessionStartsThroughTheRuntime` (`POST
  /api/sessions` with the fake: the argv, the directory, the folder, the
  environment's names with no `CONDUCTOR_` from the server's own beside
  Conductor's, the size, `{Session}`), `TestACrewMemberAndAnAttemptStartThroughTheRuntime`,
  `TestAResumeStartsThroughTheRuntime`, `TestARefusedStartIsStartFailed`
  (`502 start_failed`, nothing registered), `TestSetRuntimeReachesTheEngine`
  (an attempt's session and its check in the same fake),
  `TestSetRuntimeIsRefusedOnceASessionStarted`, `TestAPlainServerStartsNatively`.
  (PR-4) `TestAnAttemptAndItsChecksRanWhereTheyReport` (a managed run of the
  stub worker on a real ledger under a `FakeRuntime` with `Base` native and
  `where` `fake`: the fake recorded the attempt's `Start` and its check's
  `Run`, and the task read's attempts and steps, the steps route and the
  prerequisites route say `fake`), `TestTheTaskReadReportsWhere` (the same
  run in a plain build: `native`), `TestAPlainBuildsRowsSayNative`.
- `internal/hostagent` (PR-3): `TestAHostStartsThroughTheRuntime` (the
  fake's spec: argv, directory, environment, `{Session}` the registered id;
  the exit code from its terminal), `TestAHostWithNoRuntimeRunsNatively`.
- `internal/cli` (PR-3): `TestPickRuntime` (none registered: the native;
  one registered: it; a `where` that is not a name: refused with its
  words), `TestAPlainBuildPicksTheNativeRuntime` (the registry's Runtime
  empty, so `runServe` and `runHost` pass the native one on).

Web (PR-4): the type change; `npm --prefix web run typecheck`, `npm
--prefix web test` and `make test-e2e`, the existing suites unchanged (no
component reads the field, so no new spec).

By hand, what the tests do not reach (the Acceptance section's runs): a
real agent's PTY (Claude Code and Codex: the trust question, resize, Stop
ending its group) through the native Runtime on WSL and on the Mac;
`example-todo-app-tasks` run to its end there; an upgrade in place of a
copy of a data directory whose ledger a pre-PR-4 build wrote; `conductor
host` against a `conductor serve` on the same machine (the Mac's own, and
WSL's own at `127.0.0.1`, so nobody configures networking, AGENTS.md); the
Windows desktop app starting its WSL server and a session.

## Boxcar constraints

The Program's section 2.5 lists eight; each, for this item:

1. The engine starts an attempt only through `crew.Launcher`: honoured and
   made narrower. The attempt's session is still `Launcher.Launch`, which
   now starts through the server's Runtime, and everything else a run
   starts (checks, steps, services) goes through the same Runtime as the
   engine's executor. The boundary test fails a new process started outside
   the runners. Not touched: the scheduler client and Brakeman reach
   `ext.Host` (C1, and their own items).
2. `session.Local` drives a `session.Process`: honoured. The Runtime's
   `Terminal` reaches `Local` through `session.RuntimeProcess`, and
   `internal/pty` is the native Runtime's implementation of it. The pid is
   read once, for the log line it is in today, from what the Runtime
   reports; nothing acts on it. The only `syscall.Kill` of a session's
   process stays in `internal/pty` (`pty.go:163` and `:172`); `ext/local`
   adds none, and the boundary test keeps the others (`proc`'s for checks,
   `nvim`'s and the agents probe's, none a session's) where they are.
3. An attempt's context is files under its worktree: not touched. The task
   files are still written through an `os.Root` before the session starts;
   the Runtime receives a directory, never an in-memory context; git (which
   makes and compares the trees) stays Conductor's, on the machine that
   holds the repository (decision RT10).
4. The agent talks to the server over the network: honoured. The Runtime
   receives the session's environment with `CONDUCTOR_NOTIFY_URL` and
   `CONDUCTOR_NOTIFY_TOKEN` as `main` builds them (`pty.Inject`); nothing
   adds a Unix-socket or in-process channel.
5. Checks go through `internal/proc` and carry where they ran: honoured.
   A check is still a `proc.Spec` the engine builds, run by `proc.Run` under
   the native Runtime, with its environment prepared by `proc.Prepare`; its
   row carries `where`. A provider keeps `proc`'s semantics by the contract
   on `Run`.
6. Platform splits stay platform tags: honoured. No tag is added; nothing
   registers a Runtime in this repository; the web app shows nothing of it.
7. The evidence rows gain `where`, native the only value until F: this is
   PR-4. No evidence assuming a native process is added: the existing pid,
   group and start time columns stay as they are, holding what the Runtime
   reports.
8. hostagent is a launch site as much as api: honoured. PR-3 wires
   `conductor host` and the server in the same PR, through the same
   `ext.Runtime`, from the same composition root.

## Open-core checklist

- [x] Article 1: no premium code and no licence check; the composition root
      asks only whether a Runtime is registered, once.
- [x] Article 2: the native Runtime is `main`'s behaviour; everything runs
      with nothing registered.
- [x] Article 3: nothing here is paid or gated; the Runtime has no hosted
      dependency.
- [x] Article 4: stop (a session's `Stop`, a run's stop, pause, cancel,
      retry, accept) acts as before; inspect gains `where`; revoke, export
      and approvals are untouched; none can lapse.

## The PRs

Each PR is one issue and one branch `issue-N` from `main`, cut after the
one it depends on has merged; it leaves `main` working and green on its
own, ticks the checklist above, and names its checks. Every PR updates
`docs/repository-map.md`'s rows it changes and adds its sentences to the
item's `docs/features.md` entry, which PR-1 opens and PR-4 completes.
Sizes are changed lines including tests. None runs beside another: each
edits that entry, and PR-2 to PR-4 each build on the one before.

**PR-1: the Runtime's contract and its native default.** `ext`'s Runtime
and its types as above (or what C1's declaration lacks of them);
`ext/local`'s `Native()`; `internal/proc`'s `Prepare`, `Spec.Environ`, and
`Probe` and `FreePort` moved from `internal/crew/steps.go` with their
tests, `crew`'s `nativeExec` calling them; the boundary test; the errata
line in `docs/plans/agent-harness.md`; `docs/repository-map.md` (`ext`,
`internal/proc`); the features entry opened. Nothing calls the Runtime yet:
behaviour is `main`'s. Touches `ext`, `ext/local`, `internal/proc`,
`internal/crew` (the move), `docs`. Tests: the `ext/local` and
`internal/proc` tests above. Depends on C1 and #441. About 1,000 lines.

**PR-2: a managed run's trees run through the Runtime.** `execFor`, the
executor's functions taking the `ProcessRef`, `Engine.SetRuntime`,
`NewEngine`'s native default, `nativeExec` removed; `sessiontest.FakeRuntime`;
`docs/repository-map.md` (`internal/crew`), `docs/architecture.md` (Crew
runs); the features entry's sentence. Touches `internal/crew`,
`internal/session` (`sessiontest`), `docs`. Tests: the PR-2 tests of
`internal/crew` and `sessiontest` above; every existing crew test, its
checks and steps now through the adapter. Depends on PR-1. About 750 lines.

**PR-3: sessions start through the Runtime, on the server and on a host.**
`session.RuntimeProcess`; `Server`'s Runtime, `SetRuntime` (the engine's
too) and `Runtime()`; `createLocalSession(ctx, …)` and its three callers;
`hostagent.Options.Runtime`; `runServe` and `runHost` taking the
registry's Runtime or the native one; `docs/architecture.md` (the sessions
diagram, Packages), `docs/repository-map.md` (`internal/pty`,
`internal/session`, `internal/hostagent`); the features entry's sentence.
Touches `internal/session`, `internal/api`, `internal/hostagent`,
`internal/cli`, `docs`. Tests: the PR-3 tests of `internal/session`,
`internal/api`, `internal/hostagent` and `internal/cli` above; the
existing PTY, viewer and host tests on the native Runtime. Depends on PR-1
and PR-2 (`SetRuntime` sets the engine's). About 700 lines.

**PR-4: every attempt and its evidence say where they ran (ledger schema
6).** The migration and the columns; the rows' `Where`, the puts' refusals,
the readers; the attempt's `where`, its rows', `AttemptState.Where` and
`StepState.Where`; the fake ledger's refusal; the web types;
`docs/protocol.md`'s rows; `docs/architecture.md` and
`docs/repository-map.md` (`internal/crew/ledger`: schema version 6); the
features entry completed with what the Acceptance runs observed. Touches
`internal/crew/ledger`, `internal/crew`, `internal/api` (its tests),
`web/app/composables`, `docs`. Tests: the PR-4 tests of
`internal/crew/ledger`, `internal/crew` and `internal/api` above; the web
checks. Depends on PR-3. About 850 lines.

## Owner decisions

Dated 2026-10-30. The defaults below are this plan's, inside the twelve
articles; the owner may overturn any at the plan's review, and one
overturned is rewritten here before the PRs are cut.

- **Open decisions.** None is this plan's to answer. O9 (where Boxcar's
  code lives) blocks item F alone, so F's plan answers it; this contract
  serves either answer, since everything a provider needs, in this
  repository under a `linux` tag or in a module of its own, is in `ext`.
  O12 (the names `ext` and `app`) is C1's; this plan uses the Program's
  names. O16 (where paid features' UI lives) is not touched.
- **RT1, schema version 6.** The Program says version 5; it was written
  when `main` wrote 4, and EN1 has since taken 5 (#394), its plan saying
  the second of the two takes 6. Proposed status-log row: "2026-10-30 | C10
  | where lands as ledger schema 6: EN1 (#394) took 5".
- **RT2, the names.** Column `where_ran`, Go field `Where`, JSON `where`.
  Reason: `WHERE` is an SQL keyword, and the ledger already renames
  keyword columns that way (`pgroup`, `commit_id`, `from_member`); Go and
  JSON keep the Program's word.
- **RT3, which rows.** Attempts, checks (a task's checks and a manifest's
  steps) and prerequisites; not trees. Reason: those are the evidence rows
  (constraint 7) and the acceptance compares an attempt with its checks; a
  tree's lifecycle row belongs to its attempt.
- **RT4, the migration.** SQL alone, `DEFAULT 'native'` on every row a
  version 5 database holds, no backfill. Reason: only native code could
  run anything before this item, so the value is what happened; the
  owner's conservative rule (the version 3 to 4 migration) is met because
  nothing is claimed that the rows cannot support.
- **RT5, a row without `where` is refused, not defaulted.** Reason: a
  default would label any future runtime's omission native; a refusal makes
  it a failing test. `phase` keeps its default.
- **RT6, one Runtime per process, `where` per Runtime.** The server and a
  host each have one, set before the first launch; an attempt's `where` is
  fixed when it is made. Reason: an attempt and its checks then report the
  same `where` by construction, and nothing can mix runtimes mid-run; a
  `where` per process is item F's to propose if a guest needs it.
- **RT7, every session starts through the Runtime, not only attempts.**
  Reason: `createLocalSession` is every server session's one path; the
  `ProcessRef` tells a provider what a process is for, and the native
  Runtime is `main`'s either way.
- **RT8, Conductor builds the environment.** A Runtime receives it whole.
  Reason: the filter that keeps tokens and loaders out stays in one place.
- **RT9, the whole tree executor.** `Serve`, `Probe` and `Port` beside
  `Start` and `Run`. Reason: a service is a process of a run (constraint
  1), and a tree's probe or port must be the tree's runtime's, not the
  server's loopback (EN1's executor).
- **RT10, git stays Conductor's.** Reason: the tree is made and compared
  where the repository is, and handed to a runtime mounted or copied
  (constraint 3).
- **RT11, no UI change.** Reason: the acceptance asks for no behaviour
  change; DS2 shows the evidence; a column saying `native` on every row
  tells a person nothing before item F.
- **RT12, the boundary test.** Reason: constraints 1, 2 and 5 become a
  failing test, so the boundary is code (decision D7), and a new process
  site is a reviewed edit.
- **RT13, reconciliation with C1.** This plan specifies `ext.Runtime`'s
  members; if `docs/plans/ext.md` declares them otherwise, the two plans are
  made one before either's first PR is cut, keeping this plan's semantics
  (what each method does and the `where` it reports).
- **RT14, the fakes.** C10's fake is internal (`sessiontest.FakeRuntime`);
  `ext/exttest`'s public fake and contract suite are C9's, covering the
  Runtime with the cases PR-1 tests on the native one.

## Acceptance

The boundary, "an attempt and its checks report the same where; no
behaviour change on any platform", is observed three ways:

1. By test, in CI and on the Mac's row: `TestAnAttemptAndItsChecksRanWhereTheyReport`
   (through the server and a real ledger, the attempt's session and its
   check run in the fake Runtime, and every row and report says its
   `where`), `TestAnAttemptAndItsChecksReportTheSameWhere` (the engine's
   rows and reports, fake and native), `TestTheTaskReadReportsWhere` (a
   plain build: `native`) and `TestAVersion5DatabaseIsBroughtToVersion6`.
2. By hand on WSL and on the Mac, on the build of PR-4: the example crew
   `example-todo-app-tasks` (its prerequisite included), with real agents
   in a scratch repository both trust, run to its end; then `GET
   /api/runs/{run}/tasks/{task}` for each task, every attempt and receipt
   `"where": "native"`, and `sqlite3 runs/managed.sqlite "SELECT DISTINCT
   where_ran FROM attempts UNION SELECT where_ran FROM checks UNION SELECT
   where_ran FROM prerequisites"` printing `native` alone. A copy of a data
   directory whose ledger an earlier build wrote (version 5) opened by the
   same build: version 6, its old runs' attempts and receipts `native`, an
   interrupted run continued.
3. No behaviour change: every existing suite green, unchanged, on each PR
   (CI's Go race tests, e2e, desktop smoke and the build matrix; the Mac's
   race tests); and by hand, a real agent's session on WSL and the Mac
   (trust question, typing, resize, Stop), `conductor host` against a
   server on the same machine (the Mac, and WSL at `127.0.0.1`), and the
   Windows desktop app starting its server and a session, each as before.
   Windows runs the Linux server in WSL through the desktop app (ci.yml's
   build matrix has no windows row), so Linux's tests are its server's.

Done means PR-1 to PR-4 merged and the runs of 2 and 3 recorded in the
item's `docs/features.md` entry; Status moves to Done then, not when the
checklist is ticked. The owner merges each PR, decides RT1 to RT14 (and
the status-log row RT1 proposes) at the plan's review, and the runs on the
Mac and the Windows app use the owner's machines and the agents' logins
there, by the owner or with the owner's word.
