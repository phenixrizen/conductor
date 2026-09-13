# Context and history delivery plan

1. Define the history, context provenance, evidence-state, and freshness contracts.
2. Expose persisted history and audit records through bounded read-only service/API
   queries, backed by PostgreSQL integration coverage.
3. Collect a small set of local Git artifacts at a resolved commit with explicit
   coverage and safe output limits. Validate snapshots without dropping unknown
   package fields.
4. Add Go client and CLI operations for historical inspection, context attachment,
   and freshness checks. Keep collection separate from network publication.
5. Add browser history/comparison, context inspection, human perspective prompts,
   and stale-response protection. Verify real browser approval/conflict paths.
6. Document and exercise one repository → pinned artifacts → submitted package →
   independent review → edit → retained historical approval workflow.

Keep implementation commits focused on these boundaries. Run applicable repository
checks with live PostgreSQL, and capture browser evidence for the interface changes.


## Authentication follow-through

[Feature 003](../003-workspace-access/spec.md) applies workspace and repository
permissions to the existing discovery, history, revision, audit, and review command
paths. Source snapshot labels remain evidence; canonical ownership lives outside
immutable content. Existing local packages retain their history without acquiring
shared authority. Feature 004 adds browser sign-in and protected sessions through
these same context/history commands. Interactive terminal authentication follows.
