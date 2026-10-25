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
   ignored at or below it.
8. **Version 3 databases: conservative** (the owner, 2026-10-25). The
   migration to 4, in Go in the migration's transaction: for every run with
   a `task.reopened` event anywhere in its chain, every task that needs a
   reopened task, transitively in the saved crew, gets `withdrawn` = its
   highest attempt, inherited down the chain (a continuation's task takes
   its ancestor's value when higher), and loses its repair. One `migrated`
   event per run it changes. It may drop good carried work of attempts made
   after a reopen; it never reuses withdrawn work.

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
- The rounds 8 and 9 tests, kept.

## Decided

Version 3 kept a marker for only some withdrawals, and continuations
chain, so the exact history cannot be rebuilt. The owner chose the
conservative backfill (path 8) over exact replay and over no migration.

## Not here

A durable "next start" record replacing the derivation (more state to keep
consistent for the same answers); a per-task cap on refusals.
