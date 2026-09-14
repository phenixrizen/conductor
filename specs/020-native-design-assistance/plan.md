# Native Design assistance delivery plan

1. Pin the native host connection contract against official documentation and
   available binaries. Keep provider account login in each host; generate usable
   connection configuration without reading provider credentials.
2. Implement immutable request, suggestion and application facts with exact pins,
   strict section operations, idempotency and current authorization in PostgreSQL.
   Reuse the saved Change instead of introducing a second mutable draft authority.
3. Expose the common HTTP and Go client contract, then narrow MCP discovery and
   suggestion tools. An optional assistance-only profile omits unrelated commands.
4. Add browser and terminal request/review/apply journeys, readable native handoff,
   selected-section previews and explicit uncertain-result recovery.
5. Exercise actual signed API/MCP/Chromium/PTy workflows and persistence, preserving
   all existing authoring, source, execution and approval regressions. Run the
   applicable Go, frontend, schema, documentation and migration checks.

This branch is stacked on `codex/change-authoring-20260914` / PR #37 at `5633cbb`.
Review that prerequisite first. The separate broad proposal in PR #36 stays Proposed.
The requester's exclusive apply permission keeps this restricted native path under
the existing revision-author independence rule. General shared contribution policy,
embedded API inference and managed provider OAuth are later work.

The owner also requested inclusion and closure of the standalone brand PR #38.
Its exact branch head `a94a89537abe3d92f1795feb25787c7aa707675d` is merged into this
branch with its original commits preserved. The combined implementation applies
the Switch specification to browser workflow styling and responsive ASCII terminal
branding. This expands the original asset-only branch scope at the owner's request.

## Verification status

Implemented in the combined branch. Normal and race-enabled Go suites pass with
real PostgreSQL, along with vet, frontend typecheck/build, OpenAPI and documentation
checks. The actual compiled MCP/CLI acceptance verifies signed agent proposal,
selected human application, independent approval, revoked access, exact retries
and retained facts after pool reopen. The backup/restore gate additionally compares
the new assistance tables, idempotency records, produced revision and audit across
actual PostgreSQL dump/restore. All 15 signed browser regression tests pass under
race detection, including lost acknowledgements and interrupted requests. Real PTY
acceptance covers the no-file assistance flow, uncertain recovery and both ASCII
header sizes. Screenshots cover desktop/mobile workbench and brand specimen.

Native provider inference and login were not executed. Codex CLI 0.154.0 parsed a
synthetic configuration; Claude Code and Antigravity configurations are checked
against current official documentation with their unverified native-host limits
recorded in the research guide. No merge, deployment or production outcome is
claimed. Hosted CI is recorded on the combined PR.
