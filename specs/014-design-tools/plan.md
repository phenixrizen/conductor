# Native design tools delivery

1. Inspect official source/release artifacts and pin tested Spec Kit and ADRKit
   versions, including their actual command and compatibility limits.
2. Build a dedicated image atop an inspected immutable Conductor worker image,
   retaining existing isolation, credentials and execution authority boundaries.
3. Stage bounded artifacts for native read commands; create only new template or
   Proposed ADR files. Emit explicit native result/digest reports.
4. Verify actual upstream commands, failing inputs, file limits, symlinks and
   exclusive creation. Run real Docker producers and independent checks against
   exact Git trees, preserving native failures and cleanup evidence.
5. Publish operator profile/check examples and expose their existing execution
   artifacts through the authenticated shared interfaces.

Steps 1–4 have working code and actual upstream/Docker acceptance. The runbook
provides step 5 configuration through the existing profile catalog; deployment
requires an operator-selected built image digest and enabled workspace profile.
No model choice, new database execution state or ADR acceptance is introduced.
