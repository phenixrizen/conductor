# A withdrawal takes back everything the task built on (part of #334)

## Why a plan

Main's CI failed on #369's merge: an attempt whose result was refused at
checkout handed that result to the next attempt, which failed at its
worktree before its agent could act. Ten Codex rounds on the fix each found
one more path to the same end, an attempt spent before its agent can act,
or one started on the wrong commit:

1. a result refused at checkout became the next start;
2. a result the crew's setup refuses, and a continuation's candidate
   refused (its start was the candidate itself);
3. the crew's directory made a file;
4. a work task with no checks skipped the checkout (also with a setup);
   `":web"` read as pathspec magic;
5. a result with no tracked file left in the crew's directory was refused;
6. case folding (macOS): judged in git's tree, not on this disk;
7. a continuation test raced the first engine;
8. a continuation forgot the refusal and started at a checkpoint holding
   the refused result;
9. a continuation of a continuation that made no attempt forgot it too;
10. a refusal kept across a review's withdrawal of the task's input.

1 to 9 are fixed on the branch. 10 is why this plan exists: the next start
is derived in four places (the run's attempts, the checkpoint, a
continuation's `refused`, the walk back along `ContinuedFrom`) and a
withdrawal clears only one (the checkpoint). The same flaw is older than
the branch inside one run: a task whose input is withdrawn after its last
attempt failed a check still starts at that attempt's result, built on the
withdrawn input, and its submission is refused (`ErrNotFromInput`).

## Where this stands

PR #370 ships rounds 1 to 7 (a refused result hands the next attempt its
start; every work result checked out and judged on disk) and fixes main's
CI. Rounds 8 and 9 (a continuation keeps the refusal, and looks back along
`ContinuedFrom`) are on this branch: they let round 10's withdrawn input
come back, so they ship only with the rule below. Codex's review of the
first version of this plan found six more ways withdrawn work is kept or
brought back; all but the migration one are on main already.

## The rule

A withdrawal takes back every attempt of every task that rested on the
withdrawn result, transitively, whatever the task's state. Each such task
keeps `withdrawn`, the number of its last attempt taken back (0 for none),
in memory and in the ledger's task row (schema v4: `tasks.withdrawn
INTEGER NOT NULL DEFAULT 0`), written in the withdrawal's transaction.
Attempt numbers stay monotonic; `withdrawn` never renumbers or refunds.

Nothing from an attempt numbered `withdrawn` or less is used again: not its
result (as a start, or for Accept anyway), not its start after a refusal,
not its checkpoint, not as a candidate, not in the task file.

## Every path

1. **The start** (`startCommit`, `startFrom`, `taskFile`): the last attempt
   is used only above `withdrawn` (`lastKept(t)`); `t.checkpoint` only when
   it came from such an attempt.
2. **A dependent's repair.** A withdrawal clears the repair of each task it
   takes back (its findings were on the old input); the reopened subject
   keeps its own (that is what reopened it). Not compared with `withdrawn`:
   `repair.attempt` numbers the review's attempts.
3. **Candidates.** A withdrawal clears `t.candidate` of each task it takes
   back; `reverify` refuses a candidate numbered `withdrawn` or less; a
   continuation imports none at or below it.
4. **Failed, cancelled and blocked dependents.** `withdraw` walks them too:
   it sets `withdrawn`, clears repair, candidate, checkpoint and refusal,
   and recurses, but leaves a failed or cancelled task in its state with its
   `extra`, until a person retries it. A ready or succeeded one is blocked as
   today. One visit per task per withdrawal (a visited set, not the
   blocked state), so a dense graph of 128 tasks is walked once under
   `e.mu`; a test counts the visits.
5. **Imported results.** Dropping a result clears `t.imported`; a new result
   made in this run has none. A continuation finds the row's
   `ResultAttempt` in its own run or along `ContinuedFrom`, and records
   that reference; a source it cannot find is refused (the task reads
   failed, "its result's attempt is not in the ledger: retry it"), never
   made a success with an empty commit as today.
6. **Accept anyway** takes the last submitted result above `withdrawn`, and
   is refused (`409`, "no attempt since the withdrawal submitted a commit")
   when there is none.
7. **A continuation** reads `withdrawn` from the task row; the `last` attempt
   it takes (its own run's, or further back along `ContinuedFrom`) is
   ignored at or below it, and never numbered above the row's `attempts`
   (the history the continued run had). A run once continued takes no
   change of its tasks (retry, cancel, accept, pause or unpause: `409
   run_continued`): its continuation owns that history, and a retry there
   would fork it. The refusal holds from the moment a continuation is
   being made, not only once it is: `ContinueRun` takes the source run's
   `txMu` while it marks the source `resuming` under `e.mu`, so a change
   under way commits first and every later one sees the mark and refuses;
   the snapshot is loaded after that. A continuation that fails clears the
   mark. The durable link is monotonic: `PutRun` never clears
   `continued_by` (`CASE WHEN excluded.continued_by = '' THEN
   runs.continued_by ELSE excluded.continued_by END`), so a stop's or any
   other write of the source's row, snapshotted before the link, cannot
   erase it and let the run be continued twice (on main today it can).
8. **Version 3 databases: conservative** (the owner, 2026-10-25). The
   migration to 4, in Go in the migration's transaction. The reopened
   subjects of a run's chain are seeded from everything version 3 kept, not
   only its events (path 9: a failed commit could save rows without them):
   a `task.reopened` event, a succeeded review attempt whose result's
   verdict is `needs_changes` (its subject through the saved crew), and an
   attempt with stage `withdrawn` (its task's needs). Every task that needs
   a seeded subject, transitively in the saved crew, gets `withdrawn` = its
   highest attempt, inherited down the chain (a continuation's task takes
   its ancestor's value when higher, but never above its own row's
   `attempts`: version 3 let an ancestor be retried after it was
   continued, and a higher value would discard the child's fresh
   attempts), and loses its repair. One `migrated`
   event per run it changes. It may drop good carried work of attempts made
   after a reopen; it never reuses withdrawn work.

9. **A failed commit's events.** `transact` drops a change's events when
   its commit fails; with no undo the state stays ahead, and the next
   commit (the pause's, `pauseFor`) carries its rows without them, so the
   ledger can hold a reopened subject and withdrawn dependents with no
   `task.reopened`. A change with no undo keeps its events pending on the
   run (`r.pendingEvents`), and the next commit writes them first, in
   order; a change undone drops them, as its state. Only writable events
   are kept: every event is encoded when it is made, and one over
   `ledger.MaxPayload` is replaced there by a bounded note (`{"kind",
   "oversized": <bytes>, "task", "attempt"}`), so no event can fail every
   commit after it. `check.finished` bounds its path lists by bytes as well
   as count (a list cut short ends with how many more); on main today an
   oversized one fails the check's own commit.

10. **What an agent sends is bounded as the ledger stores it.** A
    result's findings pass `Result.validate` by characters, but JSON
    escaping can make the repair a review derives from them larger than
    `ledger.MaxPayload` (20 findings of 1,000 `<` are about 120 KiB): the
    ledger refuses the task row, and with no undo every later commit fails
    on it, the pause and the stop too (on main today). Every row field and
    event made from an agent's input (the result, the repair it implies, a
    checkpoint, a blocker, a message) is encoded and measured where it is
    taken (`SubmitResult`, `SaveCheckpoint`, `Blocked`, `SendMessage`), and
    refused there (`413 too_large`, nothing changed, the attempt goes on)
    when over its limit.

11. **The task file always fits.** Each part can be within its own bound
    and the whole over `maxTaskFile` (a repair of 60 KB beside 8,000
    characters of instructions): dispatch fails at "task file" before the
    agent, for every attempt. The task file is a view of the ledger, so it
    is cut to fit: it is built in order of need (the task and how to
    finish, the start, the inputs' commits, the attempt before, the
    repair's findings, the checkpoint, the inputs' summaries), and what
    does not fit is cut with a line saying how much and that
    `task_context` (32 KiB pages) has all of it. A test builds the
    largest of each part at once.
12. **A command's receipt is small.** `Receipt.Task` embeds the whole task
    state (result, issues, findings, checkpoint), which can pass every
    input's bound and still exceed what `Settle` takes, after the command
    changed the task with no undo: the command stays `accepted` with no
    receipt. A receipt keeps what idempotency needs (the command, the
    task's id, state, attempt and result commit, the run's revision); the
    rest is read from the run. A test accepts a result and checkpoint at
    their bounds and repeats the command.

## Two pull requests

Paths 9 to 12 stall a run today whatever withdrawal does, and need no
schema change: they go first, as their own PR of the stream ("the ledger
takes every change it is given"). Paths 1 to 8 and the continuation guard
(path 7) follow as #371 with schema v4 and the rounds 8 and 9 code.

## Tests

Each fails without its part, `-race -count=30`:

- In a run: docs needs build; docs' result fails a check (and, a second
  case, is refused by the setup); build's review asks for changes; build's
  repair succeeds; docs' next attempt starts at build's new result, its task
  file naming no attempt before.
- Across a continuation of that, also through a continuation that made no
  attempt.
- A dependent's own repair dropped by a withdrawal of its input; the
  subject's kept.
- An imported candidate waiting behind the attempt cap when its input is
  withdrawn: never verified.
- A dependent that failed its attempts before the withdrawal: stays failed;
  Retry starts it at the new input.
- An imported review withdrawn, re-run to success, continued: its
  dependent starts at the new reviewed commit.
- Accept anyway after a withdrawal with no submission since: refused; with
  one: that one.
- Version 3 fixtures, migrated and continued, nothing withdrawn reused: a
  withdrawn attempt with a checkpoint; ready, failed and cancelled
  dependents with no marker; a chain with a continuation that made no
  attempt; an imported review withdrawn and re-run in the middle run of
  three.
- A review's verdict whose commit fails once: the pause's commit carries
  its `task.reopened` and the withdrawals' events (the fake ledger's
  `failOn`); an undone change's events are not written.
- A check that leaves 50 untracked paths of 1,453 bytes: its
  `check.finished` commits (cut short, "and N more"), and the attempt's
  failure, a pause and the stop commit after it; an event made oversized by
  hand becomes the note.
- Version 3: a reopen whose commit failed and whose rows the pause saved
  (no `task.reopened`): migrated from the review attempt's verdict, nothing
  downstream reused.
- A retry gated to land while a continuation is being made is refused,
  and the continuation's snapshot is the run as it was.
- Version 3: an ancestor retried after its continuation (attempts 5 to the
  child's 3): the child's `withdrawn` is at most 3, and its attempt 4 is
  kept through another continuation.
- A stop of the source gated to commit while its continuation links it:
  the ledger reloaded still names the continuation, and a second
  continuation is refused.
- A continued run refuses a retry (`409 run_continued`); a ledger edited
  to give an ancestor a later attempt is not taken past the row's count.
- A review's result of 20 findings of 1,000 `<` on the real ledger:
  refused at submission with nothing changed; the review submits a smaller
  one; a pause and the stop commit after.
- The rounds 8 and 9 tests, kept.

## Decided

Version 3 kept a marker for only some withdrawals, and continuations
chain, so the exact history cannot be rebuilt. The owner chose the
conservative backfill (path 8) over exact replay and over no migration.

## Not here

A durable "next start" record replacing the derivation (more state to keep
consistent for the same answers); a per-task cap on refusals.
