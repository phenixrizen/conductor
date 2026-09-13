# Browser sign-in delivery plan

1. Research and pin OIDC/OAuth libraries compatible with Go 1.24. Share bounded
   issuer discovery and key rotation with the API verifier, while keeping ID-token
   and API access-token validation distinct.
2. Add migration 003 for bounded, expiring login attempts and opaque sessions.
   Prove one-time consumption, concurrent admission, revocation, and pool reopen
   with PostgreSQL. Never persist provider tokens or infer principal permissions.
3. Add fixed-origin login/callback/session/logout routes and integrate cookie
   identity with the existing authenticated command service. Require browser
   binding, CSRF protection, explicit credentials, and safe failure messages.
4. Add the browser identity and scope controls around the existing workbench.
   Remount review state on access changes and preserve exact-inspection approval.
5. Exercise a real signed TLS authorization-code flow, PostgreSQL, and Chromium;
   retain local review regression and actual API/PostgreSQL restart checks.
6. Expose the implemented collection API in the repository workbench: bounded
   shared discovery, exact-input creation with explicit same-key recovery, receipt
   inspection, requester cancellation, and revision-checked attachment. Derive
   author controls from repository discovery, serialize collection and package
   actions, and clear both on identity or scope changes.
7. Extend source inspection for version 2 snapshots without upgrading arbitrary
   JSON references into trusted receipt linkage. Keep complete JSON and evidence
   gaps visible. Confirm attachment from captured package and receipt facts, and
   require renewed inspection after stale or uncertain mutation results.
8. Exercise browser collection paths with signed sessions and real PostgreSQL,
   including two readers of shared receipts, cancellation intent, exact attachment
   tuples, unknown request recovery, stale revisions, permission denial, and
   responses superseded by a scope change. Keep these UI checks distinct from
   owned Temporal recovery and controlled provider fixtures.

## Next increment

[Feature 005](../005-authenticated-terminal/spec.md) now implements authenticated
terminal review using the existing Go client, server-provided identity, and
repository capabilities. It preserves file preview, cancellation, exact revision
confirmation, and ambiguous-outcome recovery. [Feature 006](../006-durable-context/spec.md)
supplies a durable context workflow, bounded GitHub/GitLab read adapters, and the
collection commands now exposed in the browser. Its local Temporal mode and live
provider compatibility limits still apply. General browser package editing and
submission, assistant execution, publication, and tracker synchronization remain
separate increments.

Publish each increment on a focused branch with a ready PR targeting current
`main`. The implementing agent integrates prerequisites and resolves dependencies
before requesting review; reviewers must not need to infer a merge order across
stacked PRs. Merge remains a human action; successful tests do not grant approval.

## Current release delivery instruction

The user authorized stacked PRs on 2026-09-13 for the full release. This supersedes
the earlier main-only sequencing guidance above. Follow the required gates and
explicit dependency order in the [full release contract](../../docs/full-release.md).
