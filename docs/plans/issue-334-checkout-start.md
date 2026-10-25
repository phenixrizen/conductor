# Where a task's next attempt starts (PR #370, part of #334)

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

## The rule

A withdrawal takes back every attempt of the task made so far. The task
keeps the number of the last one it took back, `withdrawn` (0 for none),
in memory and in the ledger's task row (schema v4: `tasks.withdrawn
INTEGER NOT NULL DEFAULT 0`), written in the withdrawal's transaction.

An attempt numbered `withdrawn` or less leaves nothing to the next attempt:

- no start: not its result (failed a check), not its start (refused before
  its checks), not its checkpoint;
- no candidate for a continuation to verify again;
- nothing in the task file's "The attempt before" or "Where the last
  attempt got to".

What remains of the start rule is unchanged: a repair's commit, else the
last attempt's (if above `withdrawn`) result after a failed check, its
start after a refusal, its checkpoint after an end without a result, else
the first need's result, else the run's base.

## What changes

- `withdraw` sets `t.withdrawn` to the task's highest attempt number (the
  run's attempts and `t.prior`), with its other changes; `taskRow` writes
  it; the ledger migration 3 to 4 adds the column and `Load` reads it.
- `startCommit`, `startFrom` and `taskFile` skip a last attempt numbered
  `withdrawn` or less (one helper, `lastKept(t)`), and `t.checkpoint` is
  not used when it came from one.
- A continuation reads `withdrawn` from the task row; the `last` attempt it
  takes (from its own run or further back along `ContinuedFrom`) is
  ignored when numbered `withdrawn` or less: no checkpoint, refusal or
  candidate from it.
- The withdrawal already cancels an attempt in flight and drops a
  succeeded result; those keep their behaviour.

## Tests

- In one run: build, then review and docs needing build; docs' result
  fails a check; the review asks for changes (docs withdrawn); build's
  repair succeeds; docs' next attempt starts at build's new result and its
  task file names no attempt before. The same with docs' result refused by
  the setup. Both fail on main's behaviour (the check variant on main as
  it is).
- Across a continuation: the same, stopped after the withdrawal and
  continued, also through a continuation that made no attempt.
- The ledger: migration 3 to 4 on a version 3 database keeps its rows and
  reads `withdrawn` 0; a newer database still opens read-only.
- Every new test fails without its part of the change; `-race -count=30`.

## Not here

A durable "next start" record replacing the derivation (more state to keep
consistent for the same answers); a per-task cap on refusals.
