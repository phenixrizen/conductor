# Brakeman: observation and drafting, the operating guide, authorized actions and the background role (Program items BK1, BK0, BK2, BK3)

Status: proposed 2026-10-31, for the owner's review; the owner sets it to
Planned. Main at a4026597 (PR #507, 2026-10-31). Every fact below about the
code was read from that commit on 2026-10-31; file, function and constant
names are main's.

Brakeman is Conductor's sidekick agent: an agent of the person's own
choosing, on the person's own agent subscription, that Yardmaster runs for
bounded turns to read what the crews, the runs and Dispatch hold, explain
it, and draft and propose what a person may then do. It observes,
explains, drafts and proposes; it is never another scheduler, and nothing
it writes becomes work until a person (or, after BK2, a grant the person
made) acts through the same typed operations every client uses. This plan
is four Program items in one file (the Program, sections 4.3 and 4.5):

- **BK1**, observation and drafting: a configured agent, account and model;
  a dedicated identity and session; invoked, bounded turns that brief,
  explain, draft and propose, with facts, interpretation, proposals and
  verified outcomes kept apart.
- **BK0**, capability discovery: a version-matched operating guide that the
  running server serves over MCP, the CLI and HTTP, so an agent learns what
  this build supports without a large static prompt.
- **BK2**, authorized actions: a short list of operations Brakeman may be
  granted, run through the same backend operations as every client, with
  scope, state checks and receipts.
- **BK3**, the background role: Brakeman woken by events, requests and
  configured briefings, batching, cooling down and keeping a budget, and
  rebuilt from Conductor's records after a restart or a change of model.

The Program puts BK0 at `docs/plans/capabilities.md` and BK3 at
`docs/plans/brakeman-role.md`; this one file stands for both, so the four
items share one design and one set of names (Owner decisions, 1). It is
reviewed before any of its code; the PRs at the end are cut into issues
after the review, one issue, one branch `issue-N`, one PR each
(AGENTS.md).

The names follow the railroad (the Program, section 1.2): Conductor is the
application; Yardmaster the agent harness, whose engine (`internal/crew`)
runs every Brakeman turn as a run; Brakeman the sidekick; Switchman the
routing of decisions to people, which arrives with Program item SW1 and
which Brakeman never stands in for; Dispatch the ticketing, whose items
are tickets. "Workspace" is only the paid tenant: a run's checkout is a
worktree. A Brakeman **turn** is one bounded session of Brakeman's agent
doing one **request**; what it hands back is a **brief**. "Recipe" here
always means the catalog's agent recipes (yolo, trust, resume); this plan
touches no project recipe (Program item EN1's project manifests).

The other Program items this plan meets, in plain words:

- **DS1** made Dispatch (`internal/dispatch`, `docs/plans/dispatch.md`):
  projects, tickets with stable keys, comments, relations, links, typed
  operations with receipts. Its last pages are in flight (PR #501 and PR
  #503, issues #410 and #411).
- **DS2** joins a ticket to its evidence (`docs/plans/dispatch-evidence.md`,
  issues #458 to #471): plan revisions a person approves, launches from an
  approved revision with a frozen package, a ticket's runs and their exact
  candidates with check receipts. Its first PRs are being written today
  (PR #515 for #460). DS2 is BK1's prerequisite; the first PRs below build
  on what `main` already has and the DS2 parts wait for DS2 (PR-10, PR-11).
- **C1 to C9**, the extension seams (`docs/plans/ext.md`, issues #473 to
  #489): the public package `ext`, its registry and free defaults, the
  provider's one door into the app (`ext.Host`), and, as C9, `GET
  /api/capabilities` (#488), which names the registered providers. BK0's
  guide reads it once it exists (PR-9); until then the guide says so.
- **C10**, the Runtime seam (`docs/plans/runtime.md`): where an attempt's
  process and checks run, and a `where` on every evidence row.
- **DR1** (durable decision records, the operator as the single approver)
  and **RV1** (review of a candidate and a repair round as a Yardmaster
  transition) have no plan or code yet. BK2 needs both.
- Later items that build on Brakeman: AU1 (a scheduled morning briefing,
  paid, through `ext.Host`), CP1 (a Brakeman comparison of candidates), PV1
  (an "Ask Brakeman" destination for a browser capture), AC1 (Brakeman may
  propose delaying work for capacity). None is part of this plan.

## Articles it serves

1. No premium code and no licence check: Brakeman ships whole in this
   repository; nothing asks for a plan, a tier or an entitlement. The guide
   names the providers C9 reports and branches on nothing else (PR-9).
2. Fully useful alone: every part of the four items runs on the free build
   with the person's own agent CLI and login, the local ledger and the local
   Dispatch store, with no account and no network beyond the agent's own.
3. Paid features gated by hosted dependencies: not touched. Nothing here is
   paid (decision D1); routing a brief to a named person, push delivery,
   graph-enriched context and shared briefings are paid later and live
   behind the providers of `ext`, which this plan neither calls nor defines.
4. Stop, inspect, revoke, export, required approvals: a Brakeman turn is an
   ordinary run that Stop ends; its requests, briefs and the drafts made
   from them are plain files in the data directory (inspected on the page
   and through `GET /api/brakeman/requests`, carried by C6's export); its
   token is its session's, which dies with the session, and turning
   Brakeman off stops the turn in progress; it answers no question,
   approves no plan revision and passes no gate, and its writes never
   reach a route that would (Events, orderings and invariants).
5. Dependencies one way: no new module, no new Go or npm dependency; the
   new package `internal/brakeman` imports the standard library, the
   `internal/store` documents and nothing of `ext`.
6. No feature build tag: none added. The one test tier used is the existing
   `recipes` tag, for the agents' model flags (Tests per surface).
7. The free default is `main`: every route, tool and page added here is new
   behaviour beside `main`'s; no existing route changes its answer, and the
   MCP server's existing tools list byte for byte as before (PR-2's golden
   test). Brakeman is off until a person configures it.
8. A hosted dependency unreachable: not touched; nothing here reaches a
   hosted service.
9. `CGO_ENABLED=0`: no cgo; no SQLite schema changes; Brakeman's records are
   JSON documents through `internal/store`.
10. The repository contract: this plan before code; one issue, branch and
    PR per row of The PRs; Go race tests per package, vitest and Playwright
    for the pages; no WebSocket frame or control message, so
    `internal/proto` and `web/app/utils/protocol.ts` are untouched, and
    `docs/protocol.md` gains the HTTP routes and one Server-Sent Event,
    `brakeman`, with its size limit and its test; no new dependency, so
    `make notices` has nothing to regenerate.
11. Names: Brakeman, Yardmaster, Dispatch and Switchman keep their meaning;
    "brief", "request" and "turn" are new words with one meaning each
    (above); "graph" stays the crew and task view, and Brakeman's reads are
    never called a graph.
12. Execution behind one seam: a Brakeman turn starts only through the
    engine (`Engine.LaunchNamed`, then `crew.Launcher`), as every run's
    member does; Brakeman runs no command itself; Boxcar constraints below
    takes the eight one by one.

## Why and what happens

The person running crews overnight comes back to many runs, tasks,
attempts, check receipts, blockers, questions, messages and tickets. Every
fact is already recorded: the ledger (`runs/managed.sqlite`) keeps each
run's transitions, events, receipts and check logs with their SHA-256;
Dispatch keeps tickets, comments and each field's history; the sessions
keep their attention (OPEN, CLAIMED, RESOLVED) and activity; the touched
index keeps the files agents touched. What is missing is someone who reads
it all and says, with links, what happened, what failed and how we know,
what only looks stuck, what needs the person and what answering would
authorize. The Program asks for that someone to be an agent the person
chooses and pays for already, run by Yardmaster, using the tools, context
and permission mechanisms the other agents use, with operational visibility
granted explicitly (the Program, sections 1.3 and 1.4; decision D1).

What `main` has that this builds on, and what it lacks:

- An agent session reaches the server over HTTP with its own token
  (`CONDUCTOR_NOTIFY_TOKEN`, `Local.AgentTokenOK`): the task tools
  (`internal/api/tasks.go`, `taskSession`), the messages, and DS1's
  `ticket_read` and `ticket_comment` (`internal/api/dispatch_session.go`).
  `conductor mcp` (`internal/cli/mcp.go`) serves sixteen tools on stdio and
  is registered at launch with Claude Code (`--mcp-config`) and Codex (`-c
  mcp_servers.conductor…`, with `env_vars` since #354). Every other read is
  the admin's (`requireAdmin`, `launching`): no agent can read the runs,
  the tickets or the sessions list, which is right for a worker and is
  exactly the visibility Brakeman needs granted.
- The engine launches an unsaved crew as a run (`Engine.LaunchNamed(ctx,
  crew.Crew, label)`, `internal/crew/run.go:623`) through `crew.Launcher`
  (`api.Server.Launch`, `internal/api/runs.go:55`) and types a member's
  prompt once its session is ready, through the guarded submission
  (`Local.Submit`), so a startup question is never typed into.
- Nothing describes the server's own operations to an agent beyond the
  tools' one-line descriptions and the skill (`internal/agents/skill.go`);
  there is no `GET /api/capabilities` (C9, #488, is not started).
- There is no notion of an account: an agent's login is wherever its CLI
  keeps it, which its adapter's `ConfigDirEnv` moves (Claude Code's
  `CLAUDE_CONFIG_DIR`, Codex's `CODEX_HOME`). A model is chosen today only
  by a member's arguments.

After the first slice (PR-1 to PR-8) ships, a person sees:

- **Settings, Brakeman.** Off until set: the agent (an installed catalog
  agent whose adapter registers Conductor's MCP server: Claude Code or
  Codex today), the account (the folder that agent's login lives in, or its
  default login on this machine), the model (for an agent whose model flag
  Conductor knows; empty for its default), the folder its turns run in, the
  yolo choice (off by default), what it may see (runs, tasks and checks;
  Dispatch; sessions; touched files; terminal screens, off by default), its
  scope (all projects and crews, or chosen ones), and its budget (minutes
  a turn, reads a turn, turns a day).
- **Ask Brakeman.** On the Brakeman page (`/brakeman`), from the palette,
  and from a run's or a task's menu: "Brief me since…", "Explain this",
  "Prepare a decision", "Draft tickets", "Propose a crew", "Prepare a
  handoff", "Find…", "Overlapping changes", or a free question. The request
  shows as queued, then running with its run and session (the person can
  watch the terminal, stop it, or cancel the request), then briefed.
- **A brief**, as Conductor renders it: a title, Brakeman's reading (marked
  as its interpretation), the **facts**, each with the evidence it rests on
  as links (a run, a task, an attempt, a check receipt with its log hash, a
  ticket or comment, a session, a question, a file, an earlier brief),
  **interpretations** naming the facts they read, **proposals** naming an
  operation Conductor can run, **verified outcomes** naming a receipt, and
  where asked: **decision cards** (what work occurred, why a person is
  needed, the precise question, the alternatives and their tradeoffs, the
  recommendation and its uncertainty, what is blocked, what answering
  authorizes, the evidence and the revision it was read at) whose controls
  are Conductor's own, read from the live record; **draft tickets** and
  **draft crews** as forms the person edits and creates; **handoff
  packages**; **overlapping changes**. Every brief says which agent, model
  and version produced it, for which request, as of when.
- Nothing Brakeman does takes the focus: a brief arriving adds a count to
  the sidebar's Brakeman row and to the page, with no dialog, no toast that
  takes the keyboard and no navigation.
- After a restart of the server, or with another agent or model chosen,
  "What changed since your last brief?" reads the earlier briefs and their
  cursors from the data directory, not from any conversation: every turn is
  a fresh session.

An agent in any Conductor session (BK0) can call the `guide` tool or run
`conductor guide`: a small overview first (this build's version and commit,
who the caller is and what it may do, the sections and their sizes), then
each section on request: the operations (name, scope, MCP tool, CLI form,
HTTP route, input schema, receipt), the limits, the host (the platform, the
managed-run switch, the ledger's and Dispatch's state, the catalog's agents
and their probed versions), the receipt formats, and the providers (empty,
and said to be unknown, until C9; PR-9).

Later, gated PRs add: DS2's plan revisions, launches and candidates as
Brakeman's evidence and drafts (PR-10, PR-11); the four granted actions of
BK2 (PR-12, PR-13); and BK3's background role (PR-14, PR-15).

## The changes

### `internal/brakeman` (new, PR-1): the configuration, requests, briefs and evidence references

A package with no HTTP, no session and no process, like `internal/dispatch`:
the API, the CLI and the tests are its callers. It imports the standard
library and `internal/store` (atomic JSON documents) alone.

**The configuration** (`config.go`), one document `brakeman/config.json`
in the data directory, written only through the admin's route:

```go
type Config struct {
	Enabled bool   `json:"enabled"`
	AgentID string `json:"agentId"` // a catalog agent
	// Account is the folder the agent's login lives in, set in its
	// session as its adapter's ConfigDirEnv (CLAUDE_CONFIG_DIR,
	// CODEX_HOME): a reference, never read by Conductor beyond a stat.
	// "" is the agent's default login on this machine.
	Account string `json:"account,omitempty"`
	Model   string `json:"model,omitempty"` // "" is the agent's default
	Cwd     string `json:"cwd"`             // the folder its turns run in
	Yolo    bool   `json:"yolo"`            // the agent's yolo recipe; off by default
	// AllowConductorTools lets the agent call Conductor's MCP tools
	// without asking, where its adapter can say so (Claude Code).
	AllowConductorTools bool        `json:"allowConductorTools"`
	Visibility          Visibility  `json:"visibility"`
	Scope               Scope       `json:"scope"`
	Permissions         Permissions `json:"permissions"`
	Wake                Wake        `json:"wake"`
	Budget              Budget      `json:"budget"`
	Rev                 int64       `json:"rev"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}
type Visibility struct {
	Runs      bool `json:"runs"`      // runs, tasks, attempts, check receipts and logs, messages, blockers
	Dispatch  bool `json:"dispatch"`  // projects, tickets, comments, history
	Sessions  bool `json:"sessions"`  // the sessions list: names, states, questions, quiet time
	Touched   bool `json:"touched"`   // the touched index
	Terminals bool `json:"terminals"` // a session's screen text; off by default
}
type Scope struct {
	Projects []string `json:"projects,omitempty"` // Dispatch project keys; none is all
	Crews    []string `json:"crews,omitempty"`    // crew ids; none is all
}
type Permissions struct {
	// Observe, Draft and Propose are true in BK1 and cannot be turned off
	// one by one (Enabled is the switch). Act names the operations BK2
	// grants; it must be empty until PR-12.
	Observe bool     `json:"observe"`
	Draft   bool     `json:"draft"`
	Propose bool     `json:"propose"`
	Act     []string `json:"act,omitempty"`
}
type Wake struct {
	Mode string `json:"mode"` // "invoked" until PR-14 adds "background"
}
type Budget struct {
	TurnMinutes  int `json:"turnMinutes"`  // 1 to 60; 15
	ReadsPerTurn int `json:"readsPerTurn"` // 10 to 2,000; 300
	TurnsPerDay  int `json:"turnsPerDay"`  // 1 to 500; 40, counted by the server's local day
}
```

`Validate(c Config, f Facts) error` holds every field to its bound and
asks `Facts` (an interface the API implements over the catalog and the
adapters) what the package cannot know: the agent exists, is not hidden,
has an adapter with an MCP registration (`agents.Adapter.MCP`, today Claude
Code and Codex), and takes a model only when its adapter has `ModelArgs`
(PR-3). The model is `^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,99}$`, an argument
of the adapter's own flag, never free arguments; the account and the folder
are absolute, at most 4,096 bytes, checked by the API (the account a
directory owned by the server's user, the folder through `resolveCwd`). A
save names the `rev` it read (`409 stale_revision` otherwise), bumps it and
stamps `updatedAt`. The defaults, written on the first read: off, no
agent, the server's default folder, yolo off, visibility on but terminals,
all of scope, the budget above, mode invoked.

**A request** (`request.go`) is one thing a person asked of Brakeman,
kept as `brakeman/requests/br-<n>.json`:

```go
type Request struct {
	Schema    int       `json:"schema"` // 1
	ID        string    `json:"id"`     // br-<n>, n from 1, never reused
	OpID      string    `json:"opId"`   // the client's, the repeat key
	Kind      string    `json:"kind"`
	Subjects  []Ref     `json:"subjects,omitempty"` // at most 10
	Note      string    `json:"note,omitempty"`     // Markdown, at most 16 KiB
	Since     time.Time `json:"since,omitzero"`     // a briefing's start
	By        string    `json:"by,omitempty"`       // the person's display name
	CreatedAt time.Time `json:"createdAt"`
	State     string    `json:"state"`
	Why       string    `json:"why,omitempty"` // a refusal's or an end's words, at most 500 bytes
	RunID     string    `json:"runId,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Agent     Stamp     `json:"agent"`  // agent id, adapter, probed version, model, whether an account was set
	Config    Snapshot  `json:"config"` // visibility, scope, permissions, budget and rev as the turn began
	Deadline  time.Time `json:"deadline,omitzero"`
	Reads     int       `json:"reads"`
	Brief     *Brief    `json:"brief,omitempty"`
	BriefOpID string    `json:"briefOpId,omitempty"`
	BriefAt   time.Time `json:"briefAt,omitzero"`
	Resolved  []Resolved `json:"resolved,omitempty"` // each ref of the brief as Conductor found it
	Made      []Made     `json:"made,omitempty"`     // drafts made work by a person: pending op, outcome
	ClosedAt  time.Time  `json:"closedAt,omitzero"`
}
```

The kinds: `briefing` (what changed since `Since`, else since the last
briefing's cursors, else the last 24 hours), `explain` (one subject: a run,
task, attempt, session, question or ticket), `decision` (a question, a task
that failed or is blocked, a ticket), `draft_tickets` (a ticket or comment,
or the note as the discussion), `propose_crew` (a ticket), `handoff` (a
task, attempt or session), `locate` (the note as the question),
`overlaps`, and `ask` (the note, any subjects). The states and their
transitions are in Events, orderings and invariants. A record is at most
1 MiB (`LoadLimit`); one of a later schema is skipped, logged once and
never rewritten.

**A brief** (`brief.go`), schema `brakeman.brief/1`, is what a turn hands
back, at most 256 KiB as JSON:

```go
type Brief struct {
	Schema      string `json:"schema"`      // "brakeman.brief/1"
	Title       string `json:"title"`       // at most 200 characters
	Summary     string `json:"summary"`     // Brakeman's reading, Markdown, at most 4 KiB
	Uncertainty string `json:"uncertainty,omitempty"` // at most 2 KiB
	Items       []Item `json:"items"`       // at most 100
	AsOf        AsOf   `json:"asOf"`        // the cursors it read to: each run's last event seq, Dispatch's last change, the time
}
type Item struct {
	Kind string `json:"kind"` // fact/1, interpretation/1, proposal/1, outcome/1, decision/1,
	// comparison/1, evidence/1, draft_ticket/1, draft_crew/1, handoff/1, overlap/1
	// … the kind's fields, below
}
```

Each kind's fields and rules, enforced by `Validate`:

| Kind | Fields | Rule |
|---|---|---|
| `fact/1` | `text` (1 KiB), `evidence` (1 to 10 refs) | A fact without evidence is refused. |
| `interpretation/1` | `text` (2 KiB), `facts` (indexes of fact items, at least one) | Reads only facts of the same brief. |
| `proposal/1` | `text` (2 KiB), `op` (an operation name the guide lists), `target` (a ref), `args` (JSON object, 4 KiB), `expectedRevision` (string, optional), `facts` | Rendered as a control the operator presses; Brakeman runs it only with a BK2 grant. |
| `outcome/1` | `text` (1 KiB), `receipt` (a `check:`, `command:` or `op:` ref) | A verified outcome names a receipt, or it is a fact or an interpretation. |
| `decision/1` | `about` (a `question:`, `task:` or `ticket:` ref, later `plan:` and `decision:`), `happened`, `whyHuman`, `question` (500 bytes), `alternatives` (2 to 6, each `label` of 80 characters and `tradeoffs` of 1 KiB), `recommendation` (an alternative's index, or -1 for none), `uncertainty`, `blocked` (refs), `authorizes` (1 KiB), `evidence` (1 to 20 refs), `subjectRevision` | Conductor renders the controls from the live record of `about`, never from these fields. |
| `comparison/1` | `title`, `columns` (2 to 6), `rows` (1 to 20, a label and one cell of 500 bytes per column), `evidence` | |
| `evidence/1` | `title`, `refs` (1 to 50) | An evidence panel. |
| `draft_ticket/1` | `project` (a key), `title` (200 characters), `description` (16 KiB), `acceptance` (8 KiB), `priority` (0 to 4), `blocks` (indexes of other draft tickets of the brief), `from` (refs) | At most 20 a brief; `blocks` has no cycle. |
| `draft_crew/1` | `crew` (the body `POST /api/crews` takes), `gates` (up to 10 notes: a task id and the decision a person makes before it), `from` (refs) | The crew passes `crew.Crew.Validate` at submission. |
| `handoff/1` | `objective`, `constraints`, `decisions` (up to 20), `revision` (a `commit:` ref), `questions` (up to 20), `evidence` | Built from Conductor's records; never an agent's conversation. |
| `overlap/1` | `paths` (1 to 50), `between` (2 to 8 refs of attempts or sessions), `evidence` | |

The four kinds a brief's reader must never confuse are four kinds: a fact
names evidence, an interpretation names facts, a proposal names an
operation, an outcome names a receipt. Text fields are cleaned as session
messages are (`session.CleanMessage`) and bounded in bytes before they are
kept.

**Evidence references** (`refs.go`): a ref is a string of a kind and its
parts, at most 512 bytes, parsed once into a typed `Ref`:

| Ref | Names | Resolved (PR-3) to |
|---|---|---|
| `run:<run>` | a run | the run page; its state and revision |
| `task:<run>/<task>` | a managed task | `?task=` on the run page; its state |
| `attempt:<run>/<task>/<n>` | an attempt | its row; its stage and result commit |
| `check:<run>/<task>/<n>/<id>/<k>` | a check receipt (the ledger's step row) | its log route; exit, subject commit, log SHA-256 |
| `command:<run>/<command>` | a command receipt | `GET /api/runs/{run}/commands/{command}`; its status |
| `event:<run>/<seq>` | a run's ledger event | its kind and time |
| `message:<run>/<seq>` | a members' message | its sender and delivery |
| `session:<id>` | a session | the session page; its state and attention |
| `question:<session>/<question>` | a question the session asks or asked | its state (OPEN, CLAIMED, RESOLVED) and options |
| `ticket:<KEY-n>` | a ticket | the ticket page; its `rev` |
| `comment:<KEY-n>/<id>` | a comment | the ticket page at it; its `rev` |
| `op:<actor>/<opId>` | a Dispatch receipt | its status |
| `commit:<run>/<sha>` | a commit on a run's branches | its short id and subject |
| `file:<session>/<path>` | a touched-index entry | the Files tab; its last op and time |
| `brief:<br-n>/<item>` | an earlier brief's item | the brief |

PR-10 adds `plan:<KEY-n>/<revision>`, `launch:<id>` and
`candidate:<run>/<task>/<n>` for DS2's records; DR1's `decision:<id>` comes
with PR-12.

**The store** (`store.go`): `Open(*store.Store)` takes `brakeman/` under
the data directory (`store.Sub`, 0700). `Config`, `SaveConfig`; `Create`
(the next number from `brakeman/state.json`, written before the request's
own file, so a number is never reused, even after a crash between the two);
`Get`, `List(after, limit)` newest first in pages of at most 100;
`Update(id, func(*Request) error)` under one mutex, the request read, changed
and written atomically; `Prune`, which keeps the newest 500 requests and
never one that is not closed; `TurnsToday(now)`; `Open` concluding what the
last server left open (`queued` stays queued; `starting` and `running`
become `interrupted`, with why). The clock is a field, so the tests drive it.

### `internal/api`: the operating guide (PR-2, BK0's first form)

`guide.go` (new) holds the table of Conductor's operations an agent or a
client calls, one entry each, the single source the guide, the MCP tools'
list and the tests read:

```go
type Operation struct {
	Name    string         `json:"name"`    // task_result, observe, ticket_comment, run_pause…
	Summary string         `json:"summary"` // at most 300 bytes
	Scope   string         `json:"scope"`   // admin | session | task | ticket | observe | draft | propose | act
	Method  string         `json:"method"`
	Path    string         `json:"path"`    // the mux pattern
	MCP     string         `json:"mcp,omitempty"` // the tool's name
	CLI     []string       `json:"cli,omitempty"` // conductor task result …
	Input   map[string]any `json:"input"`   // JSON Schema of the body or the tool's arguments
	Receipt string         `json:"receipt,omitempty"` // ledger | dispatch | brakeman
	Bounds  map[string]int `json:"bounds,omitempty"`  // the body's and the reply's limits
}
func Operations() []Operation
```

The first table lists every agent route and tool of `main` (the sixteen
MCP tools and the session routes they call), the admin operations a
proposal may name (a run's pause and unpause, stop and resume; a task's
cancel, retry and accept; a ticket's create, edit, comment and link; a
crew's create; a crew's launch), and the guide itself; PR-3 to PR-5 add
Brakeman's. `internal/cli`'s `mcpTools()` builds its list from the
entries with an `MCP` name, their `Summary` as the description and `Input`
as the schema, so the tools and the guide cannot drift; a golden test
holds the existing tools' `tools/list` reply byte for byte as it is on
`main`.

The guide (`Guide`, one JSON document, computed per caller, never cached
across a configuration change):

- `overview` (at most 4 KiB): the guide's version (1), this build's
  `version.Version` and commit, the caller (`admin`, or a session with its
  kinds: a plain session, an attempt of a managed run, a member of a run
  linked to a ticket, a Brakeman turn) and its scopes, each section's name
  and size, and three lines that hold for every caller: a fact needs
  evidence; nothing an agent writes becomes work by itself; a question goes
  to a person with `ask`.
- `operations`: the entries the caller's scopes reach (an attempt sees the
  task tools, a plain session does not; the admin sees them all).
- `limits`: the named bounds that refuse a body: a task id's pattern, 128
  tasks a crew, 8 checks a task, a check's 1 to 3,600 s, a comment's 16
  KiB, a message's bound, 50 unacknowledged messages, a brief's 256 KiB and
  its items' bounds; each read from the constant that enforces it.
- `host`: the platform (`runtime.GOOS`, `GOARCH`), switchyard or not,
  `crews.managed`, the ledger's state (owned, read-only and why, absent),
  Dispatch's (open, read-only, absent), `checks.maxConcurrent`, and the
  catalog's agents (id, adapter, installed, the probe's `Identity` version
  when known); for a session caller, only the agents it could name.
- `receipts`: the formats of the three receipts a caller meets: a run
  command's (`internal/crew/commands.go`), a Dispatch operation's
  (`dispatch.Receipt`), a Brakeman request's.
- `providers`: `{"known": false, "providers": []}` until C9; PR-9 fills it
  from `GET /api/capabilities`.

Routes: `GET /api/guide?section=<name>` (the admin's) and `GET
/api/sessions/{id}/guide?section=<name>` (the session's own token, or the
admin's, through `taskSession(w, r, false)`): without `section` the
overview, else that section; `404 no_section` for another name. Each
section is at most 64 KiB and the whole at most 256 KiB, measured by a
test with every entry at its largest. A switchyard answers its own guide
(no ledger, no Dispatch, the sessions published to it).

`internal/cli` (PR-2): the MCP tool `guide` (`section`, optional), listed
for every session, which reads the session's guide from the running server
and, when the server's version differs from this binary's, says so first
("this conductor is X, the server is Y: the guide is the server's");
`conductor guide [--section S] [--json]`, in a session through its token,
else with `--server`, `--token` and `--insecure` as `conductor dispatch`
takes them. The MCP server's `initialize` instructions gain one sentence:
"Call guide to learn what you may do here."

### `internal/api`, `internal/agents`, `internal/crew`: a Brakeman turn (PR-3)

`brakeman.go` (new) holds the **desk**, the server's one owner of
Brakeman's requests: the store, the request in progress, the queue, the
watchdog. `New` opens it beside the ledger and Dispatch when the server
has a data directory (none on a switchyard, which answers `403` on every
Brakeman route, as `launching` does).

**A turn is a run Yardmaster launches.** The desk takes the oldest queued
request when none is starting or running, writes it `starting` with its
deadline and its configuration snapshot, and launches, through the engine,
an unsaved one-member crew:

```go
crew.Crew{
	ID: "brakeman", Name: "Brakeman", Cwd: cfg.Cwd, Where: "server", Isolation: "none",
	Goal: "Brakeman request " + req.ID, Yolo: &cfg.Yolo,
	Members: []crew.Member{{
		// the adapter's ModelArgs(cfg.Model), then AllowTools("conductor")
		// when AllowConductorTools is set; nothing a person typed
		Name: "brakeman", AgentID: cfg.AgentID, Args: args,
		Start: crew.Start{When: "immediately"},
		Prompt: line, // below
	}},
}
```

with `Engine.LaunchNamed(ctx, c, "Brakeman · <kind> " + req.ID)`, the run
taking the configuration's yolo choice as a saved crew's run takes its own.
The engine starts the member through `crew.Launcher` (`api.Server.Launch`)
and types the prompt once the session is ready, as for any member, through
`Local.Submit`: a trust or startup question shows as the session's question
and is answered by a person, never typed into. The line, at most 300 bytes:

> Brakeman request br-12 (briefing): read it with the brakeman_request tool
> or "conductor brakeman request", and submit one brief with brief_submit
> or "conductor brakeman brief". Call guide for what you may read.

`Server.Launch` sets, for a member of a run the desk owns, Conductor's own
names in the session (`createSessionRequest.inject`, unfiltered as an
attempt's are): `CONDUCTOR_BRAKEMAN=<request id>`; and the account as the
adapter's `ConfigDirEnv` in `Env` (the launch already installs the skill
under that folder, `InstallSkillIn`). The run id and the session id are
written into the request (`running`) once `LaunchNamed` returns. A launch
that fails writes `failed` with the engine's words. A Brakeman run is an
ordinary run: the run page, the sidebar's run block (labelled "Brakeman ·
…"), Stop, the records; it is never a managed run, so the ledger holds
nothing of it and the 409 `managed_run` boundary is untouched. The desk
knows its runs by its records (each request names its run), and a run its
records missed (a server that died between the launch and the write) by
the crew the run was launched with, the id `brakeman` with the goal
`Brakeman request br-<n>`, so it knows them after a restart too. Three refusals keep a turn
Brakeman's and never a scheduler: a run the desk's records name is not
resumed and takes no member (`POST /api/runs/{run}/resume`, `…/members` and
`…/members/{name}/resume` answer `409 brakeman_run`, "ask Brakeman again");
the crew store reserves `brakeman` beside `new` (`reservedID`, which no
derived id takes), so a crew a person names Brakeman is saved as
`brakeman-2` (one saved under that
id before PR-3 keeps it, and its runs list then shows the turns too, each
labelled); and a Brakeman session cannot form a crew or add a member to one
(`POST /api/sessions/{id}/crew` and `…/run/members` answer `403
brakeman_cannot`, and the MCP server does not list `form_crew` and
`add_member` there).

`internal/agents` (PR-3): `Adapter.ModelArgs func(model string) []string`,
set for Claude Code (`--model <m>`) and Codex (`--model <m>`), each checked
against the CLI's `--help` on this machine in the PR and nightly by the
`recipes` tier; nil for every other adapter, so a model cannot be set for
them. `Adapter.AllowTools func(server string) []string`, set for Claude Code
alone (`--allowedTools mcp__conductor`, its rule for every tool of one MCP
server), checked the same way; applied only with `AllowConductorTools`.
Codex's handling of MCP tool approvals is read from the CLI in the PR and
written into `docs/features.md`; without a verified flag, Brakeman under
Codex asks before each tool unless yolo is on, and Settings says so.

**The watchdog**: at the deadline (`TurnMinutes` after `starting`) a
running request becomes `timed_out` and the run is stopped (`Engine.Stop`);
a member session that ends with no brief makes it `ended` (the desk reads
the session's end from the server's session change hook, the one that feeds
`Engine.OnChange`); a person's cancel, or Brakeman turned off, makes it
`cancelled` and stops the run. Each is written before the stop.

**Admin routes** (`brakeman.go`), every one `requireAdmin` and a
switchyard's 403:

| Route | Body and answer |
|---|---|
| `GET /api/brakeman` | `{config, current, queue, turnsToday}` |
| `PUT /api/brakeman/config` | `{rev, config}` → `{config}`; `409 stale_revision`; `400 invalid_config` with the field's words |
| `POST /api/brakeman/requests` | `{opId, kind, subjects, note, since}` → `201 {request}`, `200` for the same `opId` and content, `409 op_conflict` for other content; `409 brakeman_off`, `429 queue_full` (8 waiting), `429 budget_spent` (the day's turns), `400` for a bad kind, subject or note |
| `GET /api/brakeman/requests?after=&limit=` | a page of requests, newest first, without their briefs' items |
| `GET /api/brakeman/requests/{id}` | the request whole, with its brief, its resolved refs and what was made from it; at most 1 MiB |
| `POST /api/brakeman/requests/{id}/cancel` | `{opId}` → `{request}`; a closed request answers as it stands |

**Session routes** (`brakeman_session.go`), the Brakeman session's own
token only (`agentTokenOK`) for the request the session's run belongs to,
while it is `running`; the admin's token is refused for the brief (`403
agent_only`, as a task result is: a brief is always Brakeman's own) and
taken for the reads, so a person can inspect what Brakeman was given:

| Route | Body and answer |
|---|---|
| `GET /api/sessions/{id}/brakeman/request` | `{request}`: kind, subjects as resolved, note, since, the last briefing's `asOf`, the scope and visibility it may use, the budget left; `404 not_brakeman` for another session |
| `POST /api/sessions/{id}/brakeman/brief` | `{opId, brief}` → `201 {request}`; the same `opId` and brief `200`; another brief `409 brief_exists`; `422 bad_refs` naming each ref that resolves to nothing or lies outside the scope or visibility; `400 invalid_brief` with the item's index and why; `413 bound` past 256 KiB as JSON |

**Resolution** (`brakeman_refs.go`): each ref of a submitted brief is
looked up in the record it names (the ledger's reads, Dispatch's
`Ticket`, `Comment` and `Receipt`, the registry and the session's
attention, the touched index, git through `internal/gitcli` for a
commit, the desk's own requests) and checked against the request's scope
and visibility; the brief is kept with each ref's `Resolved` snapshot
(`{ref, url, state, rev, sha256, at}`), so a later reader sees what
Brakeman saw and whether it has changed since. A ref to something deleted
since is shown as such, never dropped.

**The `brakeman` event** on `GET /api/events`: `{id, state}` after each
change of a request's state, at most 200 bytes, state a page cannot do
without (like `run` and `dispatch`), never a webhook. `docs/protocol.md`
gains a Brakeman section: the routes above with their bounds and refusals,
the event, the request's states, the brief's schema and the refs.

### `internal/api`: what Brakeman reads, and drafts made work (PR-4)

`brakeman_observe.go` (new): `GET /api/sessions/{id}/brakeman/observe/{what}`,
the Brakeman session's token (or the admin's), counted against the
request's `ReadsPerTurn` (`429 reads_spent` past it), each answer at most
256 KiB and every item carrying the ref a brief cites it by:

| `what` | Query | Answer |
|---|---|---|
| `changes` | `since` (a time) or none (the last briefing's cursors), `cursor`, `limit` (at most 200) | what changed: runs launched, ended, paused or continued; tasks that changed state, with how Conductor knows (below); questions opened and answered; messages; tickets created, edited or closed and comments added; each with its ref and time; `next` for the rest |
| `run` | `run` | a run with its tasks, attempts, check receipts (no logs), messages and blockers, as the admin's reads give them |
| `log` | a `check:` ref, `tail` (at most 64 KiB) | the log's last bytes and its SHA-256 |
| `tickets` | the Dispatch list's query (`project`, `status`, `q`, `sort`) | a page of summaries |
| `ticket` | `key` | the ticket as `GET /api/dispatch/tickets/{key}` reads it |
| `history` | `key`, `after` | a page of its history |
| `sessions` | none | each session in scope: name, agent, state, the question it asks with its options, quiet for how long |
| `screen` | `session`, `lines` (at most 200) | the screen's last lines; `403 not_visible` unless terminals are visible |
| `touched` | `session` (optional) | touched-index entries in scope, at most 500 |
| `overlaps` | none | paths changed by two or more live attempts or sessions in git work trees (`gitcli.GetStatus` against each one's base, at most 32 trees, 2,000 paths), each with the refs |
| `briefs` | `q`, `after` | earlier briefs, title, summary and items' refs, newest first, at most 20 a page |

**How Conductor knows, computed, never inferred by the agent.** Each task
and session in `changes`, `run` and `sessions` carries `status`, one of:
`failed` with the receipt that proves it (a check's non-zero exit or
timeout, a refused result, attempts spent); `blocked` with the blocker the
attempt reported; `waiting_on_person` with the open question; `quiet` with
how long the terminal has written nothing (`LastOutputAt`, at least 10
minutes) and `proof: none`; `running`; `succeeded` with its result commit;
`stopped` or `interrupted` with the record's words. Brakeman's brief can
then tell inactivity from proven failure because Conductor already did, and
its fact cites the receipt or says there is none.

The ledger gains one read, no schema change: `Ledger.LastEvent(ctx, run)
(int64, error)` (`SELECT MAX(seq)`), so `changes` reads each run's events
after the last briefing's cursor, or, given a time, the newest 5,000 of a
run's events (`Events` after `LastEvent - 5000`) filtered by their time in
Go; a run with more since then is marked `truncated`. Dispatch's tickets
since a time are the list sorted by `updated`, descending, read until a
ticket's `updatedAt` is before it.

**Drafts made work, by a person** (admin): `POST
/api/brakeman/requests/{id}/items/{i}/make` with `{opId, by, edit}`, `edit`
the form as the person changed it (the same shape as the draft):

- a `draft_ticket/1` becomes a ticket through Dispatch's own operation
  (`Store.CreateTicket` under the actor `admin`, the person's `by`, the
  person's `opId`), then its `blocks` relations to tickets already made
  from the same brief (`AddRelation`, op ids `<opId>-r<j>`); the request
  records `Made{item, pending: opId}` before the store's operation and
  `{key, receipt}` after it; a repeat with the same `opId` answers from
  Dispatch's receipt, and an item already made answers `409 already_made`
  with its key;
- a `draft_crew/1` becomes a saved crew through the crew store, as `POST
  /api/crews` saves one (validated again), recorded the same way; a crew is
  never launched by this route;
- every other kind answers `400 not_a_draft`.

Brakeman's own token reaches none of these: its routes are under
`/api/sessions/{id}/brakeman/` and are reads, the request and the brief.

### `internal/cli` and `internal/agents`: Brakeman's tools and command (PR-5)

MCP tools, listed only in a session whose environment has
`CONDUCTOR_BRAKEMAN` (set by the server, PR-3): `brakeman_request` (the
request), `observe` (`what` and its query, one tool for the eleven reads,
its schema a `oneOf` from the guide's table), and `brief_submit` (`opId`
minted by the tool when absent, and `brief`; a reply lost to the network
is asked once more with the same `opId`, as the Dispatch writes do).
`conductor brakeman request|observe WHAT [--k v]…|brief --file F` in a
session, the same calls for an agent without MCP; and with the workbench
token, `conductor brakeman ask KIND [--subject REF]… [--note-file F]
[--since T]`, `status`, `list` and `show br-n [--json]`.

`internal/agents`: `codexMCPEnv` gains `CONDUCTOR_BRAKEMAN` (Codex starts
`conductor mcp` with only the names it lists, #354); the skill gains one
short section, "If you are Brakeman" (six lines: `CONDUCTOR_BRAKEMAN` is
set; call `guide` first; read before you write; every fact cites evidence;
nothing you write becomes work; one brief, then your turn ends), and nothing
else, so no agent carries a large static prompt. `web/e2e/stub-agent.sh`
gains `STUB_BRAKEMAN=1`: on its request line the stub reads its request with
`conductor brakeman request`, observes `changes`, and submits a brief whose
fact cites its own session and run (and a check receipt when
`STUB_BRAKEMAN_CHECK` names one), through `STUB_WORKER`'s CLI or MCP path.

### `web/app`: Settings, the Brakeman page and a brief (PR-6)

- `composables/useBrakeman.ts`: the config, the requests (a page, then the
  `brakeman` events), `ask`, `cancel`, `make`; ids minted by the browser and
  a lost reply asked once more with the same id (`utils/tasks.ts`'s rule).
- `utils/brakeman.ts` (pure, vitest): the kinds' words, the states' words
  ("queued", "running in <session>", "timed out after 15 min", "the session
  ended with no brief"), a ref to its route and label, a brief's items
  grouped in the order facts, interpretations, decisions, proposals,
  outcomes, drafts, handoffs, overlaps, evidence; the config form's checks
  mirroring the server's.
- `components/BrakemanSettings.vue` (a card on `pages/settings.vue`): the
  fields of the configuration; agents offered are those whose adapter
  registers MCP; the model field only for an agent with a model flag; the
  account folder through `DirInput`; terminals off with its words ("lets
  Brakeman read what a session's screen shows").
- `pages/brakeman.vue`: the composer (`BrakemanAsk.vue`: a kind, subjects
  picked from runs, tasks, sessions and tickets, a note, a since), the
  request in progress with its session's link and Stop, the queue, and the
  briefs newest first.
- `components/BrakemanBrief.vue`: a brief: the provenance line (agent,
  model, version, request, as of), the title, "Brakeman's reading" (the
  summary), then `BrakemanFact.vue` (text and evidence chips, each chip a
  link labelled by `utils/brakeman.ts`, marked "changed since" when its
  resolved `rev` or state differs from the live one the page has),
  interpretations ("reads facts 2 and 3"), proposals as text with the
  operation named (their controls are PR-7's), outcomes with their receipt,
  comparison and evidence panels.
- The sidebar's foot gains a Brakeman row (`data-sidebar-brakeman`) with
  the count of briefs not yet opened, kept per browser; no toast, no dialog,
  no navigation when a brief arrives.

### `web/app`: decision cards, drafts, handoffs and the entry points (PR-7)

- `components/BrakemanDecision.vue`: the card's text from the brief, and
  the controls from the live record of `about`, read through the existing
  routes: for an open question, its options as the session reports them and
  "Answer in the session", which opens the session page at its reply bar (the
  one place a question is answered, with the control role); for a failed or
  blocked task, Retry, Accept anyway (asked first, as on the run page) and
  Cancel, sent through `useTaskCommands` with `expectedRevision` the run's
  revision the brief resolved, so a run that moved since is refused `409
  stale_revision` and the card says what changed; for a ticket, its page. The
  card says who may answer ("you, the operator"; DR1 and HM1 add named
  people) and what answering authorizes, from the brief.
- `components/BrakemanProposal.vue`: a proposal's text and its operation
  as a button, for the operations the guide lists as the operator's that
  the web app already sends (a run's pause, unpause, stop and resume; a
  task's cancel, retry and accept; a ticket's edit, comment and link),
  sent through the same client calls as their own pages, with the
  proposal's `expectedRevision`, asked first where the page asks first
  (Accept anyway, Cancel, Stop); any other operation is shown as text.
- `components/BrakemanDraftTicket.vue` and `BrakemanDraftCrew.vue`: the
  draft as an editable form, Create (the `make` route), and once made the
  ticket or crew it became, linked; a crew made is opened in the crew
  editor, never launched.
- `components/BrakemanHandoff.vue`: the package as Markdown, Copy, and
  "Comment on the ticket" for a ticket in its refs (Dispatch's comment
  operation as the admin, the person's name).
- Entry points: the palette gains a Brakeman group (`usePaletteRows`: "Ask
  Brakeman: brief me since…", "…explain this run", "…this task", "…draft
  tickets from this ticket", "…propose a crew for this ticket", "Open
  Brakeman"); the run page's task actions (`taskActions`) gain "Ask
  Brakeman why"; the ticket page's menu gains "Draft tickets" and "Propose a
  crew" once DS1's PR #501 and PR #503 have merged (the file they change).

### Docs (PR-8)

`docs/features.md` gains the first slice's entry: what each PR verified and
the by-hand turn (Acceptance). The README gains a Brakeman paragraph;
`docs/repository-map.md` a row for `internal/brakeman` and the desk;
`docs/architecture.md` a Brakeman section (a turn is a run; the guide; the
records). `docs/protocol.md` is each PR's own.

### Later PRs: DS2's records, BK2's actions, BK3's role (PR-9 to PR-16)

**PR-9 (C9's providers).** Once C9's `GET /api/capabilities` (#488) has
merged, the guide's `providers` section is that route's answer (`known:
true`, each provider's kind, name and state) for the admin and for a
session, and `host` gains what C9 reports beside it; a plain build answers
`{"known": true, "providers": []}`. The guide still calls nothing of `ext`
itself and branches on nothing (article 1).

**PR-10 and PR-11 (DS2's records).** Once DS2's plan revisions, launches
and candidates are served (its PR-6 to PR-8, #463 to #465): the refs
`plan:`, `launch:` and `candidate:` resolve; `observe` gains `plans`
(a ticket's revisions, the approved one, whether it still holds) and
`candidates` (a run's candidates with their receipts, as DS2's route
reads them); a `draft_plan/1` item drafts a revision (a saved crew with
tasks, the inputs, the decisions a person will make), which the person
proposes through DS2's own propose route ("Propose as plan revision"),
then approves as DS2 requires of the operator; a `decision/1` about a
`plan:` ref renders DS2's Approve as its control. PR-11 is the pages'
part, once DS2's ticket pages (#467, #468) have merged.

**PR-12 and PR-13 (BK2, authorized actions).** Once DR1 and RV1 serve
their operations and BK1 is Done:

- `Permissions.Act` names grants from a closed list, each one operation:
  `launch_approved_plan` (DS2's launch from a revision the operator
  approved and that still holds), `request_repair` (RV1's Request changes
  on a candidate, a Yardmaster repair transition), `prepare_followup`
  (DS2's proposal of a follow-up revision from the approved one, which a
  person still approves), `route_decision` (DR1's routing of a decision
  record to the operator or to a human member label of HM1's; never its
  answer). Only the admin's configuration route writes grants; no route
  Brakeman's token reaches does.
- `POST /api/sessions/{id}/brakeman/act` `{opId, op, target, args,
  expectedRevision}`: the op must be granted, in scope, and its target in
  the state the operation requires; it runs through the same function the
  admin's route for that operation runs (moved into one function both
  call, as `docs/plans/ext.md` does for `ext.Host.Launch` with
  `launchSaved`), under the actor `brakeman`, with the request's id and the
  op's `opId` as its idempotency key, and answers that operation's own
  receipt, also kept in the request. Approving a plan revision, answering
  a decision, changing a gate, accepting a task, editing the
  configuration and every Dispatch close are not operations it can be
  granted; DS2 refuses an approval by any actor but the operator, DR1 an
  answer by any but the approver.
- Visibility and grants stay two fields checked apart: a grant gives no
  read, a read gives no grant; with visibility narrowed, an act whose
  target is no longer visible is refused.
- PR-13: the MCP tool `act` and `conductor brakeman act`; Settings' grants;
  a brief's proposals that Brakeman ran show "Brakeman did this" with the
  receipt; the guide lists the granted operations.

**PR-14 and PR-15 (BK3, the background role; O2's default, Owner decisions,
2).** Yardmaster's engine gains a role loop (`internal/crew/role.go`): a
role is a record, not a process: an identity (`brakeman`), its preferences,
its durable notes (at most 64 KiB, kept by Conductor: a brief's fact or
interpretation the person chooses to keep, readable and clearable on the
page, never something Brakeman writes there by itself), and its lifecycle,
`idle`, `gathering` (a wake source fired; the batch window open),
`briefing` (a turn running), `cooling` (until the cooldown ends), `paused`
(by a person), `unavailable` (the agent cannot start: not installed, its
login refused, the budget spent), each change written before what it
starts. Wake sources, all event-driven, none a poll of a model: a task
failed with proof, a question open longer than a threshold, a run ended or
paused, a ticket moved to ready, a person's explicit request, and
configured briefing times in the server's local zone. Each wake is a row in
the role's log; the batch window (default 120 s, 10 to 900) gathers the
wakes that fire in it into one request of kind `wake` with the wakes as
subjects; the cooldown (default 30 min, 0 to 1,440) holds the next batch;
the budget (turns a day, minutes a day) refuses a turn and says so. A turn
is BK1's turn, unchanged: a fresh run of a fresh session that rebuilds
everything from the records. After a restart the role's state, its open
batch and its log are read back; a batch open when the server stopped is
fired once. While the role is unavailable or paused nothing else waits on
it: crews, decisions and inspection never depend on Brakeman. PR-15: the
role's controls on the Brakeman page and in Settings (background on or off,
pause, briefing times, window, cooldown, budget), its log, and its
unavailable state in words.

**PR-16** adds the features entries of BK0 to BK3 and the by-hand records
of each acceptance.

## Events, orderings and invariants

No SQLite schema changes: the ledger gains one read (`LastEvent`) and
Dispatch none; Brakeman's records are new JSON documents of their own
(schema 1). A record of a later schema is skipped, logged once and never
rewritten, so an older binary started on newer data loses nothing
(the conservative rule the ledger's migrations follow). No WebSocket frame
or control message changes; the one new Server-Sent Event is `brakeman`.

**A request's states.**

| State | Meaning | Goes to |
|---|---|---|
| `queued` | written, waiting for the desk | `starting`, `cancelled` |
| `starting` | taken by the desk; deadline and configuration snapshot written; the launch under way | `running`, `failed`, `cancelled`, `interrupted` |
| `running` | its run and session recorded; the session may read and submit | `briefed`, `timed_out`, `ended`, `cancelled`, `interrupted` |
| `briefed` | a brief kept | none |
| `timed_out` | the deadline passed with no brief | none |
| `ended` | the session ended with no brief | none |
| `failed` | the launch failed (the engine's words) | none |
| `cancelled` | a person cancelled it, or turned Brakeman off | none |
| `interrupted` | the server stopped while it was starting or running | none |

Every other transition is refused by `brakeman.Request.To` (a table test
walks every pair). The last six are closed: `closedAt` is set and nothing
of the request changes again but its `made` list.

**What is written before what.**

1. A request's number is written to `brakeman/state.json` before the
   request's file, and the file (`queued`) before the reply: a number is
   never reused and a reply never names a request that is not kept.
2. `starting`, with its deadline and snapshot, is written before
   `Engine.LaunchNamed`; `running`, with the run and session, after it
   returns. A server that dies between them leaves `starting`, which the
   next start makes `interrupted`; the engine's record of the run is
   interrupted at start as every legacy run's is, and the run, known by
   its crew as launched, is never resumed (the `brakeman_run` refusal).
3. A brief is validated, its refs resolved, and the request written
   `briefed` with the brief and the resolutions before the reply; the run is
   stopped after the reply, after a grace of 20 s so the agent's last output
   reaches its terminal. A stop that fails is logged; the record is the
   truth.
4. A deadline, a cancel and turning Brakeman off write the closing state
   first and stop the run after.
5. A draft made work: `made {item, pending: opId}` is written before
   Dispatch's operation (or the crew's save), and `{key | crew, receipt}`
   after it. A server that dies between them leaves the pending entry,
   which the next read of the request completes from Dispatch's receipt for
   that `(admin, opId)`; for a crew, from the saved crew of that name and
   content made after the pending stamp.

**Invariants.**

- At most one request is `starting` or `running` (the desk's mutex); the
  queue holds at most 8 and is taken in number order.
- A request's `opId` (the admin's) names one request: the same content
  again answers it (`200`), other content `409 op_conflict`;
  `state.json` keeps the last 1,000 `opId`s with their request.
- A brief is kept once. Its `opId` again with the same brief answers it;
  any other brief `409 brief_exists`. Only the session of the request's
  run submits it, only while `running`, only with its own token.
- Every fact names at least one ref; every ref of a kept brief resolved,
  inside the request's scope and visibility, when it was kept; the
  resolution is kept beside it and never rewritten.
- A turn's reads use the snapshot taken at `starting` intersected with the
  configuration now: a person narrowing what Brakeman may see takes effect
  at once; widening takes effect at the next request.
- Brakeman's token reaches the routes any session's token reaches today
  (its own attention, reports and view link), the guide, and the
  `/api/sessions/{id}/brakeman/` routes, which read and submit a brief; it
  is refused by every admin route (a test walks them), by the self-crew
  routes and, until PR-12's grants, by anything that changes a run, a
  ticket or a crew.
- The day's budget: a request is refused at creation when the turns
  started today plus those queued reach `TurnsPerDay`; a turn counts once
  it is `starting`.
- Nothing Brakeman submits changes a ticket's status, a run's state, a
  task's outcome, a verification or an acceptance: those stay separate
  facts with their own owners (the Program, section 1.4), and a brief only
  cites them.

**The `brakeman` event**: `{id, state}`, sent after each state is written,
at most 200 bytes (an id of at most 12 bytes and a state of at most 12),
measured by `TestTheBrakemanEventIsBounded`. Like a `run` event it is state
a page cannot do without: a client that cannot keep up is dropped and
reads again on its next snapshot.

**BK2 (PR-12).** An act is checked, in this order, against: the session
being the running request's; the grant (snapshot intersected with now);
the scope and visibility of its target; the target's state as the
operation requires (the revision `expectedRevision` names, or the
operation's own state check); then it runs through the operation's one
function under the actor `brakeman` with `<request>-<opId>` as its
idempotency key, whose receipt is written by the operation's own store
(the ledger's commands, Dispatch's ops, DR1's records) and copied into the
request after. A repeat answers the same receipt; the request's copy is
completed from the store's receipt after a crash, as `made` is. No
operation in the grant list changes an approval, a gate, a decision's
answer or the configuration.

**BK3 (PR-14).** The role's lifecycle, written to `brakeman/role.json`
before what each change starts: `idle` → `gathering` when a wake source
fires (the wake appended to the role's log, at most 1,000 kept) → after
the window, a request of kind `wake` is created (rule 1 above) and the role
is `briefing` → when that request closes, `cooling` until the cooldown ends
→ `idle`. `paused` from any state by a person (a request already running
runs to its end); `unavailable` when a turn cannot start (the request
`failed`), until a person's request or the next configured briefing tries
again. Wakes that fire while `briefing` or `cooling` join the next batch.
An invoked request and a wake batch share the one queue. At start the role
is read back: `gathering` keeps its open batch and fires once its window
has passed; `briefing` follows its request, which rule 2 made
`interrupted`, to `cooling`. The engine's loop holds no process between
turns and polls no model: it is woken by the activity sink, the engine's
run changes, Dispatch's `OnChange` and one timer for the next configured
briefing or window end.

## Out of scope

- Brakeman as a scheduler: it launches nothing, adds no member, forms no
  crew and rewrites no task graph; BK2's launch is of a plan revision a
  person approved, through DS2's own launch. Supervisor lifecycles beyond
  BK3's role, dynamic crew reshaping and live plan revision are the
  Program's later work (section 7).
- Answering a question, approving a plan revision, accepting a task or a
  ticket, closing a ticket, passing a gate, changing a required approval:
  never Brakeman's, granted or not.
- An agent's private conversation: Brakeman never reads another agent's
  transcript or session files, and a handoff package never pretends to
  carry one provider's conversation to another; it is built from
  Conductor's records.
- Brakeman listening by default: terminal screens are off until a person
  turns them on, and are read only when Brakeman asks, a bounded number of
  lines; huddles (Program item CL1) are not touched.
- The paid parts (decision D1): routing a brief to a named person, push
  delivery, graph-enriched context, workspace-shared briefings, and a
  briefing scheduled from the cloud while the machine sleeps (Program item
  AU1). BK3's briefing times are local and fire while the server runs.
- An account registry, usage windows and reset times (Program item AC1):
  the account here is a folder reference.
- A2UI, AG-UI and MCP Apps: candidates the Program names; none is adopted
  (Owner decisions, 15).
- More than one Brakeman, a Brakeman per project, Brakeman on a host or a
  switchyard: one per server, on the server.
- Hibernation of idle sessions (Program item HB1).

## Tests per surface

Per `docs/checks.md`: every Go PR runs `make lint`, `go test -race
-count=1 -timeout 30m ./...` and `make lint-static`; every UI PR runs
`npm --prefix web run typecheck`, `npm --prefix web test` and `make
test-e2e` (the PR's specs alone first, with `--repeat-each 10` for a new
spec); a PR touching both runs both. The named tests each PR adds:

**PR-1, `internal/brakeman`** (Go race): `config_test.go`:
`TestConfigBoundsEachField` (every field at its bound and one past),
`TestAModelNeedsAnAdapterThatTakesOne`, `TestAConfigSaveNamesItsRevision`,
`TestTheDefaultsAreOffAndTerminalsHidden`, `TestActMustBeEmptyInThisBuild`.
`request_test.go`: `TestEveryRequestTransition` (the table, every pair),
`TestAClosedRequestOnlyGainsMade`, `TestEachKindTakesItsSubjects`.
`brief_test.go`: `TestBriefBounds` (each field and count at its bound and
one past, the whole at 256 KiB), `TestAFactWithoutEvidenceIsRefused`,
`TestAnInterpretationReadsFactsOfItsBrief`, `TestAnOutcomeNamesAReceipt`,
`TestADecisionCardIsWhole`, `TestDraftTicketsBlockWithoutACycle`,
`TestADraftCrewPassesValidate`, `TestTextIsCleanedBeforeItIsKept`.
`refs_test.go`: `TestRefGrammar` (each kind's valid and invalid forms, the
512-byte bound), `TestARefPrintsAsParsed`. `store_test.go`:
`TestANumberIsNeverReused` (the state written, the request's file failing),
`TestPruneKeepsTheNewest500AndEveryOpenOne`,
`TestOpenInterruptsWhatWasStartingOrRunning`,
`TestALaterSchemaIsSkippedNotRewritten`,
`TestTurnsTodayCountsByTheLocalDay` (a fake clock across midnight),
`TestUpdateIsAtomicUnderConcurrency` (32 goroutines).

**PR-2, `internal/api` and `internal/cli`** (Go race):
`api/guide_test.go`: `TestTheGuideNamesThisBuild`,
`TestEveryOperationIsARegisteredRoute` (each table entry's pattern answers
other than the mux's 404), `TestEverySessionRouteIsInTheTable`,
`TestEachSectionIsBounded` (every entry at its largest),
`TestASessionsGuideListsItsScopesOnly` (a plain session, an attempt, a
member of a run with a ticket), `TestTheGuideRefusesAnotherSessionsToken`,
`TestASwitchyardAnswersItsOwnGuide`, `TestProvidersAreUnknownBeforeC9`.
`cli/mcp_test.go`: `TestToolsListIsUnchanged` (the sixteen existing
tools' entries against a golden file of `main`'s reply),
`TestEveryToolIsAnOperation`, `TestTheGuideToolReadsTheServer`,
`TestTheGuideToolSaysWhenVersionsDiffer`. `cli/guide_test.go`:
`TestGuideInASession`, `TestGuideWithTheWorkbenchToken`, `TestGuideJSON`.

**PR-3, `internal/api`, `internal/agents`, `internal/crew`** (Go race):
`api/brakeman_test.go`: `TestARequestIsRefusedWhileBrakemanIsOff`,
`TestATurnIsAOneMemberRunThatTypesItsLine` (the API tests' scripted
agent, which submits its brief over HTTP with its own token),
`TestTheTurnCarriesItsRequestAndAccount` (`CONDUCTOR_BRAKEMAN` and the
adapter's `ConfigDirEnv` in the session's environment),
`TestARequestRepeatsByItsOpID`, `TestOneTurnAtATimeAndABoundedQueue`,
`TestTheDeadlineEndsTheTurn` (the desk's clock seam, never a sleep),
`TestASessionThatEndsWithNoBriefEndsTheRequest`,
`TestABriefIsKeptBeforeTheRunStops` (a seam between the write and the
stop), `TestABriefNamesOnlyRefsThatResolve` (`422 bad_refs` with each ref),
`TestABriefOutsideTheScopeIsRefused`, `TestTheWorkbenchTokenSubmitsNoBrief`,
`TestABriefFromAnotherSessionIsRefused`, `TestABriefIsKeptOnce`,
`TestARestartInterruptsTheTurn` (a server of its own, stopped and started
on the same data), `TestABrakemanRunIsNotResumed`,
`TestABrakemanSessionFormsNoCrew`, `TestBrakemansTokenReachesNoAdminRoute`
(walks every `requireAdmin` and `launching` route with it),
`TestTheDaysBudgetRefuses`, `TestCancelStopsTheTurn`,
`TestTurningBrakemanOffStopsTheTurn`, `TestTheBrakemanEventIsBounded`,
`TestASwitchyardRefusesBrakeman`. `agents/adapter_test.go`:
`TestOnlyVerifiedAdaptersTakeAModel`, `TestClaudeAllowsConductorsTools`;
under the `recipes` tag (nightly, `make test-recipes`):
`TestModelFlagsMatchTheCLIs`, `TestClaudeAllowedToolsMatchesTheCLI`.
`crew/store_test.go`: `TestBrakemanIsATakenID`.

**PR-4, `internal/api`, `internal/crew/ledger`** (Go race):
`api/brakeman_observe_test.go`: `TestChangesSinceTheLastBriefUseItsCursors`,
`TestChangesSinceATimeAreBoundedAndSayTruncated`,
`TestAFailedTaskCarriesItsReceipt`, `TestAQuietSessionIsNotAFailure`
(`LastOutputAt` through a seam), `TestAnOpenQuestionIsWaitingOnAPerson`,
`TestObserveKeepsToTheScope`, `TestTheScreenNeedsTerminals`,
`TestReadsAreCountedAndBounded`, `TestOverlapsNameBothTreesAndThePath` (two
worktrees of a real repository in `t.TempDir()`), `TestALogTailCarriesItsSHA`
(against the step log route's `X-Conductor-Log-SHA256`),
`TestEachReplyIsBounded`, `TestADraftTicketIsMadeOnce` (the same `opId`
twice; a crash seam between Dispatch's commit and `made`'s write),
`TestDraftTicketsKeepTheirBlocks`, `TestADraftCrewIsSavedNotLaunched`,
`TestBrakemansTokenMakesNothing`. `ledger/ledger_test.go`:
`TestLastEvent`.

**PR-5, `internal/cli`, `internal/agents`** (Go race): `cli/mcp_test.go`:
`TestBrakemansToolsOnlyInABrakemanSession`, `TestNoCrewToolsForBrakeman`,
`TestObservePassesItsQuery`, `TestBriefSubmitResendsTheSameOpID`.
`cli/brakeman_test.go`: `TestBrakemanAskWithTheWorkbenchToken`,
`TestBrakemanBriefFromAFile`, `TestBrakemanOutsideASessionIsLoud`.
`agents/codex_test.go`: `TestCodexMCPEnvNamesBrakeman`;
`agents/skill_test.go`: `TestTheSkillsBrakemanSectionIsShort` (at most six
lines, names `guide`). The stub's Brakeman mode is exercised by PR-6's
spec.

**PR-6, `web/app`, `web/e2e`**: vitest `utils/brakeman.test.ts` (the
kinds' and states' words, every ref kind to its route and label, the
items' order, the config form's checks against the server's bounds),
`composables/useBrakeman.test.ts` (an id kept across a lost reply, the
event folding into the list). Playwright `brakeman.spec.ts`: Settings saves
a configuration with the stub as the agent and terminals off; a briefing
asked from the page is queued, then running with its session's link, then
briefed; the brief shows the provenance line, the reading, the fact with
its evidence chips linking to the run and the session, and the
interpretation naming the fact; the run is stopped; a brief arriving while
a terminal has the focus leaves `document.activeElement` and the URL as
they were and adds the sidebar's count; a server of its own restarted on
the same data shows the same brief.

**PR-7, `web/app`, `web/e2e`**: vitest `utils/brakemanCards.test.ts` (a
card's controls from the live record of each `about` kind, the stale
revision's words). Playwright `brakeman-cards.spec.ts`: a crew whose check
always fails (the `tasks.spec.ts` fixture) gets a decision card with Retry,
Accept anyway and Cancel; Retry with the brief's revision after the run
moved is refused with words; a draft of two tickets, one blocking the
other, edited and made: both tickets exist with the relation, Make again
makes nothing; a draft crew is saved and opens in the editor, not
launched; the handoff copies; the palette's Brakeman rows and the run
page's "Ask Brakeman why". `palette-rows.spec.ts` gains the new rows.

**PR-8** docs only; no check applies.

**PR-9** (Go race): `api/guide_test.go`: `TestTheGuideListsC9sProviders`
(with `ext/exttest`'s fakes registered), `TestAnEmptyRegistryIsKnownAndEmpty`.

**PR-10 and PR-11**: Go race `TestPlanRefsResolve`,
`TestObservePlansSaysWhetherTheApprovalHolds`,
`TestADraftPlanIsProposedByTheOperator` (DS2's route, the actor `admin`),
`TestBrakemanCannotApprove`; Playwright `brakeman-plans.spec.ts` (a draft
plan revision proposed from a brief, approved on the ticket page, the
card's Approve control).

**PR-12 and PR-13**: Go race `TestAnUngrantedActIsRefused`,
`TestAGrantGivesNoRead`, `TestAReadGivesNoGrant`,
`TestAnActRunsTheOperationsOwnFunction` (the same receipt as the admin's
route, actor `brakeman`), `TestAnActIsKeptOnce`, `TestAStaleTargetIsRefused`,
`TestNoGrantApprovesAnswersOrAccepts` (each refused operation),
`TestBrakemanCannotGrantItself` (every route its token reaches, none writes
`Permissions`); Playwright `brakeman-act.spec.ts` (a granted
`launch_approved_plan` from a brief, its receipt on the brief and the run
page; an ungranted one shown as a proposal only).

**PR-14 and PR-15**: Go race `crew/role_test.go`:
`TestAWakeGathersThenBriefsThenCools` (fake clock),
`TestWakesInTheWindowAreOneBatch`, `TestTheCooldownHoldsTheNextBatch`,
`TestTheBudgetStopsTurnsAndSaysSo`, `TestNoModelIsPolled` (no turn without
a wake), `TestARestartFiresAnOpenBatchOnce`,
`TestAnUnavailableRoleHoldsNothingElse` (a crew runs, a question is
answered, while the role is unavailable),
`TestBriefingTimesInTheLocalZone`; Playwright `brakeman-role.spec.ts` (a
task failing wakes Brakeman, one brief, the role cooling, the log; paused,
nothing wakes it).

**What tests cannot cover, by hand** (Acceptance): a real agent on the
owner's own subscription (Claude Code and Codex) doing real turns against a
night of real crews; Claude Code's `--allowedTools` rule and Codex's MCP
approvals as the CLIs of the day behave; the Windows app (WSL) and the Mac
rows of `make preflight` for PR-3, PR-6 and PR-14, the PRs that change a
launch, the pages and the engine.

## Boxcar constraints

1. **The engine starts an attempt only through `crew.Launcher`; Brakeman
   goes through `ext.Host`, never exec.** A turn is a run the engine
   launches (`LaunchNamed`), its member started through `crew.Launcher`;
   Brakeman runs no command. BK2's one launch, of an approved plan, runs
   DS2's launch function, the one `ext.Host.Launch` calls once C1 has
   built it (`launchSaved`, `docs/plans/ext.md`), so a launch by Brakeman
   and one by a provider are the same code.
2. **`session.Local` drives a `session.Process`.** A turn's session is an
   ordinary server session; nothing here reaches a pid or a tty; a turn is
   stopped through `Engine.Stop`.
3. **An attempt's context is files.** Not touched: a turn is not an
   attempt and adds nothing to one. Its request lives in the data
   directory, a file, and reaches the agent over the network (4).
4. **The agent talks to the server over the network.** Every Brakeman tool
   is HTTP with the session's `CONDUCTOR_NOTIFY_TOKEN`, through `conductor
   mcp` or the CLI, as the task tools are.
5. **Checks go through `internal/proc`.** Not touched: Brakeman runs no
   check. Its git reads (`overlaps`, `commit:` refs) go through
   `internal/gitcli`, argv arrays, as the Files tab's do.
6. **Platform splits stay platform tags; the UI shows a capability.** No
   tag added; the guide and the pages show what the server reports, never
   a plan (PR-9 reads C9).
7. **Evidence rows gain `where`.** No evidence row is added; a check
   receipt Brakeman cites is resolved from the ledger as it stands, and
   once C10 lands its `where` is carried in the resolution as it is.
8. **`hostagent` is a launch site.** Not touched: Brakeman runs only on the
   server and starts nothing on a host; a hosted session is observed
   through the registry like any other.

## Open-core checklist

- [x] Article 1, no premium code and no licence check: Brakeman is
  open-source code with no plan, tier or entitlement check; the guide reads
  C9's registry answer and branches on nothing.
- [x] Article 2, the free build is fully useful: briefs, drafts, the guide,
  BK2's actions and BK3's role all work on the free build with the
  person's own agent, the local ledger and Dispatch, offline from any
  Conductor service.
- [x] Article 3, paid features gated by hosted dependencies: nothing here is
  paid; the paid extras of decision D1 are out of scope and would arrive as
  providers.
- [x] Article 4, stop, inspect, revoke, export, required approvals stay free
  and never degrade: a turn stops like any run, every request and brief is
  inspectable and in the data directory export carries, Brakeman's token
  dies with its session, and it answers, approves and accepts nothing.

## The PRs

Each PR is one issue and one branch cut from `main`, a draft PR at its
first commit, the checks for its surfaces before every push, its body
ticking the open-core checklist (articles 1 to 4) and naming the agent
that made it; each leaves `main` working and green on its own (a package
without its routes is reached by its tests, a route without its page by the
CLI and the tests, a page without its later parts says what is not there
yet). Parallel means the two touch no common Go package, no common
directory under `web/`, and no common file under `docs/` or at the root.
Sizes are estimates of the first push's changed lines with tests; DS1's and
DS2's PRs grew past their estimates in review, which is why none here is
planned larger than about 1,500.

**The cut for Monday 2026-11-02.** PR-1 to PR-8 need nothing that is not on
`main` today and can ship before Monday: in waves, PR-1 and PR-2 together,
then PR-3, then PR-4 and PR-5 together (PR-6 joining PR-4 once PR-5 has
merged), then PR-7, then PR-8. After them the owner configures Brakeman
with the owner's own agent and uses it. PR-9 to PR-16 wait, each on the
prerequisite named in its row.

| PR | Title | Touches | Depends on | Parallel with | Size | When |
|---|---|---|---|---|---|---|
| PR-1 | brakeman: the configuration, requests, briefs (schema 1) and evidence references, and their records | `internal/brakeman` | none | PR-2 | ~1,400 | before Monday |
| PR-2 | api, cli, mcp: the operating guide from the running server, and the tools listed from one table (BK0, first form) | `internal/api`, `internal/cli`, `docs/protocol.md` | none | PR-1 | ~1,200 | before Monday |
| PR-3 | api, agents, crew: a Brakeman turn as a one-member Yardmaster run, its requests over HTTP and the brief its session submits | `internal/api`, `internal/agents`, `internal/crew`, `docs/protocol.md` | PR-1, PR-2 | none | ~1,500 | before Monday |
| PR-4 | api, ledger: what Brakeman reads, the status Conductor computes, and a draft made work by the operator | `internal/api`, `internal/crew/ledger`, `docs/protocol.md` | PR-3 | PR-5, PR-6 | ~1,500 | before Monday |
| PR-5 | cli, agents: Brakeman's MCP tools and `conductor brakeman`, the skill's section, the e2e stub's Brakeman | `internal/cli`, `internal/agents`, `web/e2e` (`stub-agent.sh`) | PR-3 | PR-4 | ~900 | before Monday |
| PR-6 | web: Brakeman's settings, the Brakeman page and a brief as Conductor renders it | `web/app`, `web/e2e` | PR-3, PR-5 | PR-4 | ~1,400 | before Monday |
| PR-7 | web: decision cards with Conductor's controls, drafts made work, handoffs, the palette and run page entries | `web/app`, `web/e2e` | PR-4, PR-6 | none | ~1,300 | before Monday |
| PR-8 | docs: the first slice's features entry, the README, the repository map, the architecture | `docs`, `README.md` | PR-1 to PR-7 | none | ~250 | before Monday |
| PR-9 | api, cli: the guide's providers and host capabilities from C9 | `internal/api`, `internal/cli`, `docs/protocol.md` | PR-2; waits for C9 (`GET /api/capabilities`, #488) | PR-11 | ~400 | waits: C9 |
| PR-10 | brakeman, api: plan revisions, launches and candidates as evidence, and a plan revision drafted for the operator | `internal/brakeman`, `internal/api`, `docs/protocol.md` | PR-4; waits for DS2's PR-6 to PR-8 (#463 to #465) | none | ~1,100 | waits: DS2 |
| PR-11 | web: DS2's records in briefs, "Propose as plan revision", Approve on a card, the ticket page's entries | `web/app`, `web/e2e` | PR-7, PR-10; waits for DS2's PR-10 and PR-11 (#467, #468) | PR-9 | ~900 | waits: DS2 |
| PR-12 | brakeman, api: authorized actions: the grants, the act route, four operations with scope, state checks and receipts (BK2) | `internal/brakeman`, `internal/api`, `docs/protocol.md` | PR-8, PR-11; waits for DR1 and RV1 and BK1 Done | none | ~1,400 | waits: DR1, RV1 |
| PR-13 | cli, agents, web: the act tool and command, grants in Settings, actions and their receipts in a brief | `internal/cli`, `internal/agents`, `web/app`, `web/e2e` | PR-12 | none | ~1,100 | waits: DR1, RV1 |
| PR-14 | crew, brakeman, api: the background role in Yardmaster: wake sources, batching, cooldowns, budgets, records (BK3) | `internal/crew`, `internal/brakeman`, `internal/api`, `docs/protocol.md` | PR-13; waits for BK2 Done | none | ~1,500 | waits: BK2 |
| PR-15 | web, cli: the role's controls, its wake log and its unavailable state | `web/app`, `web/e2e`, `internal/cli` | PR-14 | none | ~1,000 | waits: BK2 |
| PR-16 | docs: the features entries of BK0 to BK3 and the by-hand records of their acceptance | `docs`, `README.md` | PR-9, PR-15 | none | ~250 | last |

**PR-1** (`internal/brakeman`): `config.go`, `request.go`, `brief.go`,
`refs.go`, `store.go` as The changes describe them, with `Facts` as an
interface the API implements in PR-3. No caller yet; its tests reach all of
it. Go only.

**PR-2** (`internal/api`, `internal/cli`, `docs/protocol.md`): `guide.go`
with the operations table for `main`'s agent routes and tools and the admin
operations a proposal may name; the two guide routes; `mcpTools()` built
from the table, held byte for byte by the golden test; the MCP tool
`guide`, `initialize`'s one sentence, `conductor guide`; the protocol's
"Guide" rows. Go only.

**PR-3** (`internal/api`, `internal/agents`, `internal/crew`,
`docs/protocol.md`): `brakeman.go` (the desk, the admin routes, the turn
as a run, the watchdog, the restart, the refusals that keep a turn
Brakeman's), `brakeman_session.go` (the request and brief routes),
`brakeman_refs.go` (resolution), `Server.Launch`'s names for a turn, the
`brakeman` event; `Adapter.ModelArgs` and `Adapter.AllowTools`; the crew
store's taken id; the guide's table gains the routes; the protocol's
Brakeman section. Go only; the Mac and Windows preflight rows run here (a
launch changes).

**PR-4** (`internal/api`, `internal/crew/ledger`, `docs/protocol.md`):
`brakeman_observe.go` with the eleven reads and the computed status, the
`make` route for drafts, `Ledger.LastEvent`; the table's entries; the
protocol's rows. Go only.

**PR-5** (`internal/cli`, `internal/agents`, `web/e2e/stub-agent.sh`): the
three MCP tools and their listing rule, the crew tools left out for
Brakeman, `conductor brakeman`, its usage and completion; `codexMCPEnv`;
the skill's section; the stub's `STUB_BRAKEMAN` mode, which PR-6's spec
drives. Go only (PR-3's tests drive a turn with the API tests' own scripted
agent, submitting the brief over HTTP with the session's token).

**PR-6** (`web/app`, `web/e2e`): `useBrakeman.ts`, `utils/brakeman.ts`,
`BrakemanSettings.vue` on the Settings page, `pages/brakeman.vue`,
`BrakemanAsk.vue`, `BrakemanBrief.vue`, `BrakemanFact.vue`, the sidebar's
Brakeman row; the e2e catalog gains a Brakeman stub agent
(`web/e2e/conductor.e2e.json`: the stub under Claude Code's adapter, so the
configuration takes it, the MCP and model arguments it is launched with
ignored by the stub, which uses the CLI path). Proposals show their
operation's name and say the controls come next. UI only; the Mac and
Windows preflight rows run here.

**PR-7** (`web/app`, `web/e2e`): `BrakemanDecision.vue`,
`BrakemanProposal.vue`, `BrakemanDraftTicket.vue`, `BrakemanDraftCrew.vue`, `BrakemanHandoff.vue`,
the palette's group, the run page's task action, `utils/brakemanCards.ts`.
The ticket page's entries wait for PR-11 if DS1's PR #501 and PR #503 have
not merged by then. UI only.

**PR-8** (docs): the features entry for the first slice, written from what
PR-1 to PR-7 verified and from the owner's first turns (Acceptance), or
saying they are pending; the README's paragraph; the repository map's row;
the architecture's section. Docs only; no check applies.

**PR-9** (`internal/api`, `internal/cli`, `docs/protocol.md`), after #488:
the guide's `providers` and `host` from C9's route. Go only.

**PR-10** (`internal/brakeman`, `internal/api`, `docs/protocol.md`), after
DS2's PR-6 to PR-8: the refs `plan:`, `launch:`, `candidate:`; the reads
`plans` and `candidates`; the item `draft_plan/1` and its `make` through
DS2's propose route under the actor `admin`; a decision card about a
`plan:` ref. Go only.

**PR-11** (`web/app`, `web/e2e`), after DS2's PR-10 and PR-11: the draft
plan's form and "Propose as plan revision", the card's Approve from DS2's
own control, the ticket page's Brakeman entries. UI only.

**PR-12** (`internal/brakeman`, `internal/api`, `docs/protocol.md`), once
DR1 and RV1 serve their operations and BK1 is Done: `Permissions.Act` and
its closed list; `POST /api/sessions/{id}/brakeman/act`; each operation's
one function shared with its admin route; the receipts. Go only.

**PR-13** (`internal/cli`, `internal/agents`, `web/app`, `web/e2e`): the MCP
tool `act` (listed only with a grant) and `conductor brakeman act`, the
grants in Settings with what each authorizes in words, and a brief's
proposal that Brakeman ran shown with its receipt. Go and UI.

**PR-14** (`internal/crew`, `internal/brakeman`, `internal/api`,
`docs/protocol.md`), once BK2 is Done: `crew/role.go` (the role loop, its
records in `brakeman/role.json`, its wake sources, window, cooldown and
budget), `Wake.Mode` `background` with its fields, the request kind
`wake`, the desk's sources wired (the activity sink, the engine's run
changes, Dispatch's `OnChange`, the briefing timer). Go only; the Mac and
Windows preflight rows run here.

**PR-15** (`web/app`, `web/e2e`, `internal/cli`): the role's controls,
log and unavailable state on the Brakeman page and in Settings;
`conductor brakeman role on|off|pause|status`. Go and UI.

**PR-16** (docs): the features entries of BK0, BK1, BK2 and BK3 with each
acceptance's by-hand record; the README. Docs only.

## Owner decisions

Dated 2026-10-31. Each is a default the owner can overturn with a dated row
in the Program's status log, its convention for a decision the owner did not
make (the Program, section 3.1); the reasoning is why it was taken.

1. **One plan file for the four items.** BK0, BK1, BK2 and BK3 share the
   desk, the records, the guide's table and the names, so they are planned
   together here; the Program's rows for BK0 and BK3 point at this file in
   place of `docs/plans/capabilities.md` and `docs/plans/brakeman-role.md`
   (a status-log row records it).
2. **O2, the role primitive: a dedicated loop over ordinary bounded turns,
   in Yardmaster's engine** (`internal/crew/role.go`), not a general "role
   session" member. A turn is already BK1's bounded run of a fresh session;
   the loop only decides when to start one, from records, so the role
   never needs a process between turns, never polls a model, rebuilds from
   Conductor's records by construction, and asks nothing of the managed
   engine's member rules (the 409 `managed_run` boundary stands until live
   plan revision is designed). A role session would keep a conversation
   alive for hours, whose state is the provider's and lost on a restart or
   a model change, which BK3 forbids relying on. The owner would overturn
   this if a long-lived session proves worth its cost in context and tokens.
3. **BK1 and BK0 start before their prerequisites are Done.** The Program
   takes an item whose prerequisites are all Done (section 2.2); the
   owner's goal of 2026-10-31, a Brakeman usable before Monday, comes first
   (the owner's brief beats the Program). PR-1 to PR-8 use only what `main`
   has; the parts that need DS2 (PR-10, PR-11) and C9 (PR-9) wait for them,
   and BK1 and BK0 are Done only after those PRs and their acceptance. A
   status-log row records the start.
4. **A turn is a one-member run Yardmaster launches**, not a managed task
   and not a bare session. A run gives Brakeman Yardmaster's launch path,
   the guarded typing of its line, Stop, the run page and the records with
   no new execution code; a managed task would need a worktree and a
   commit as its result, which a brief is not.
5. **Every turn is a fresh session.** Nothing Brakeman knows lives in a
   conversation: it reads its request, the earlier briefs and their cursors
   from the data directory, so a restart or another model changes nothing
   it can know. The cost is the agent's start-up on each request.
6. **Brakeman's records are JSON documents in the data directory**
   (`brakeman/`), the newest 500 requests kept, not a SQLite database:
   they are written a few times a turn by one server, read as a list of
   hundreds, and carried by the export as plain files.
7. **Brakeman writes nothing to Dispatch or the crews in BK1.** Its drafts
   are forms a person edits and makes work (`make`, under the actor
   `admin` with the person's name), as DS1's and DS2's plans already
   require of agents' drafts (DS2's decision 14). BK2's grants add four
   operations and nothing else.
8. **Every fact names evidence, resolved when the brief is kept.** A brief
   with a ref that names nothing, or lies outside the scope, is refused
   with the ref, so the agent fixes it in its turn; a brief is never kept
   with claims a person cannot follow.
9. **What Brakeman may see by default**: runs, Dispatch, sessions and
   touched files on; terminal screens off, since a screen can show what no
   record holds (a secret typed, a private file); scope all of it. The
   owner turns screens on in Settings.
10. **The account is a folder reference**: the agent's own login folder
    (its adapter's `CLAUDE_CONFIG_DIR` or `CODEX_HOME`), or its default
    login; Conductor stores no credential and keeps no account registry
    (AC1's later).
11. **The model through the adapter's own flag**, for Claude Code and Codex
    only, each verified against the CLI; no free arguments, so a model is
    never passed to an agent that would read it as something else.
12. **The agent CLI's permissions**: yolo off by default; under Claude Code,
    Conductor's MCP tools allowed without asking when the person ticks it
    (the CLI's per-server rule), everything else still asked; under Codex,
    what the PR finds the CLI does, said in Settings. A worktree is not a
    sandbox, and Brakeman's folder is the person's choice.
13. **One turn at a time**, 8 queued, and a budget of 15 minutes and 300
    reads a turn and 40 turns a day, each changeable in Settings within
    its bounds.
14. **A decision card's controls are Conductor's**: a question is answered
    on its session page, the one place a question is answered today, until
    DR1 and Program item MB0 give decisions a page of their own; a task's
    Retry, Accept anyway and Cancel carry the revision Brakeman read, so a
    run that moved is refused rather than acted on blind.
15. **MCP is Brakeman's boundary and the cards are Conductor's own**: a
    small versioned set (`decision/1`, `comparison/1`, `evidence/1`, the
    drafts and the handoff) rendered by the web app. A2UI, AG-UI and MCP
    Apps are not adopted now; they are worth evaluating once the cards
    have been used.
16. **Brakeman's tools only in Brakeman's sessions, and one `observe`**:
    a worker never sees Brakeman's tools, and Brakeman reads through one
    tool whose variants the guide describes, so neither carries eleven
    tools' descriptions in its context.
17. **The guide is per caller and in sections**, its providers unknown
    until C9: an agent reads a 4 KiB overview and asks for more, rather
    than a large static prompt.
18. **Brakeman forms no crew and adds no member**: the self-crew routes and
    tools refuse a Brakeman session, so the sidekick never schedules.
19. **BK2's grants are a closed list of four**: launching an approved plan,
    requesting a supported repair, preparing a follow-up, routing a
    decision; never approving, answering, accepting, closing or changing
    the configuration, whatever is granted.
20. **BK3's briefing times are local**: they fire while the server runs, in
    its local zone; a briefing that must fire while the machine sleeps is
    Program item AU1's (paid, from the cloud).
21. **The Monday cut is PR-1 to PR-8** (The PRs); the rest waits on the
    prerequisite each names.

No question is left open: each answer above lets the plan stand, and the
owner overturns any of them with one row.

## Acceptance

**BK1's boundary**, in this plan's words: a briefing produced by the
person's own agent, with linked evidence and a precise decision card, that
is still useful after a restart from Conductor's records. It is observed,
not ticked:

- Once PR-7 is merged, on the owner's `conductor serve` with Claude Code
  (and once with Codex) on the owner's own subscription, configured in
  Settings: after an evening of real managed crews (one with a check that
  fails until a retry, one with a task whose attempts are spent, one whose
  member asks a question and is left unanswered overnight), "Brief me
  since 18:00" yields a brief whose facts link to the runs, the tasks, the
  failing check's receipt (its log hash equal to the step log route's) and
  the open question; that tells the quiet session from the failed task;
  and whose decision card for the spent task offers Retry, Accept anyway
  and Cancel, one of which the owner presses with its receipt.
- "Draft tickets" from a real ticket's discussion yields drafts the owner
  edits and makes, with their blocking relation; "Propose a crew" yields a
  crew that saves and opens in the editor.
- The server is restarted and the agent's model changed; "What changed
  since your last brief?" reads the earlier brief and its cursors and
  answers from them, citing only what happened since.
- The observation, and what was missing, slow or wrong, goes into
  `docs/features.md` (PR-8, or a later docs PR of the stream); a bug the
  stream caused is fixed in the stream, a defect outside it filed.
- BK1 is Done when that observation is made again after PR-11, with a
  ticket launched from an approved plan revision among the evidence (DS2,
  its prerequisite), and the owner says the brief answered what happened,
  how Conductor knows, and what needs the owner.

**BK0's boundary**: an agent with no static prompt discovers what this
build supports and which providers are present. Observed after PR-2 for the
first part: a Codex session with nothing but "call the guide tool" lists
what it may do there and the limits it must keep, and a plain session's
guide differs from an attempt's; after PR-9 for the second: the official
app's build lists its providers, and a plain build says there are none.
BK0 is Done then.

**BK2's boundary**: a proposal executes with its receipt; a gate it would
skip stays enforced; privileged telemetry is separate from mutation
authority. Observed after PR-13: Brakeman, granted `launch_approved_plan`,
launches a ticket's approved revision and the run page and the brief show
the receipt; asked to launch an unapproved revision, it is refused and says
so; with every read granted and no grant to act, every act is refused; with
a grant and a narrowed visibility, the act on what it cannot see is
refused.

**BK3's boundary**: Brakeman wakes on an event, briefs, and sleeps within
its budget; after a restart it resumes from records, not conversation
history. Observed after PR-15: a task failing overnight wakes it once, a
second failure inside the window joins the same brief, the cooldown holds
the next, the day's budget stops it with words; the server restarted while
a batch was gathering fires that batch once; with Brakeman's agent logged
out, the role says it is unavailable while the crews run and their
questions are answered as before.

What needs the owner: the review of this plan and its decisions; cutting
the issues; the owner's own agent and subscription for every observation
above; the merges. A2UI, AG-UI and MCP Apps, and decision 2's role
session, are the owner's to reopen.
