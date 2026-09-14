# Create and review a Change

**Implemented; locally verified.** A Change is a reviewable
unit of engineering work. Its Design records the intended outcome, scope,
approach, planned work and verification plan. Both interfaces save to the same
API and PostgreSQL dataset; a saved revision is available to authorized colleagues.

## Browser

Run `make run`, then `make web-dev` for local development, or use the configured
[authenticated browser](browser-sign-in.md). In **Review**, choose **New change**.
Enter a **Change title** and **Intended outcome**, then fill the other sections as
needed. Preview the proposed Design and confirm creation. Inspect the saved
revision before requesting review. An independent reviewer inspects that exact
revision and digest before approving it.

Use the edit action on the current Change to revise its Design. Preview and save
the changes, then request review of the new revision separately. A historical
approval remains in history but no longer approves the edited Design.

The shared list shows a title and intended-outcome excerpt when the saved content
has those string fields. Truncation is labeled; open the Change for full content.
Legacy Changes without those fields remain discoverable by ID.

## Terminal

Run `make tui` against the same API. Authenticated use requires the fixed token
file, workspace and repository described in the [terminal guide](terminal-review.md).

1. Press `c` for a new Change. Use Tab / Shift+Tab to select a field and Enter to
   edit its text. Enter title and intended outcome, plus the Design sections needed.
2. Press Ctrl+S to stage a readable preview. Press `s` and confirm `create` to save.
3. Inspect the saved revision and digest. Press `u` and confirm `submit` to request
   review; saving alone leaves a draft.
4. An independent reviewer opens the Change and presses `a` to confirm approval of
   the displayed revision and digest.
5. Press `e` on the current Change to edit its Design. Preview with Ctrl+S, then
   press `s` and confirm `revise`. Request review of the new revision with `u`.

Press `J` to switch between readable sections and the complete JSON. Advanced
file import is available with `i`: from the shared list it creates a Change; from
an inspected Change it previews a complete replacement revision. Files remain
bounded to 1 MiB and are never executed. See the terminal guide for all controls.

## Existing content and recovery

Guided fields are text. If an older Change stores a section as an object, array,
number, boolean or null, that section remains visible and read-only in the form.
Saving preserves it and every unknown field. Advanced complete JSON inspection
keeps source context, verification criteria and other extensions available.
Untouched missing fields remain absent. The new form requires title and outcome;
existing API content remains valid under its original rules.

If another author saved first, inspect the latest revision before editing again.
If a response is lost, the operation may already have committed. Inspect saved
shared work to reconcile it; the clients do not replay a create, revision,
submission or approval automatically. Retained authoring input can be deliberately
reused after inspection, with a fresh preview and confirmation. This is a complete
replacement of the inspected Design, not an automatic merge with another author's
edits. In the terminal, `r` inspects saved work and `v` opens the retained-input
comparison; `e` edits that replacement and `s` confirms its separate save. In the
browser, inspect the saved Change before choosing **Use retained content as replacement
draft** and previewing it again. An empty or partial shared list cannot prove that an earlier creation failed;
starting a separate creation may duplicate it. Changing identity or repository, or losing
access, clears private draft and inspection state.

This first usability increment provides manual guided authoring. Provider-assisted
drafting with Codex, Claude Code and Antigravity is still future work. Creating a
Change does not run a coding agent. See [Feature 019](../../specs/019-guided-change-authoring/spec.md)
for the exact scope and acceptance contract.

## Reproduce acceptance

Use a disposable PostgreSQL database. The Go fixtures create and clean up isolated
schemas; they do not restart the configured database. Install the locked frontend
dependencies and build before starting browser acceptance:

```bash
npm --prefix apps/web ci
npm --prefix apps/web run build
export CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
CONDUCTOR_TEST_BROWSER=1 CONDUCTOR_BROWSER_PYTHON=/path/to/playwright-venv/bin/python \
  go test -race ./tests/acceptance -run '^TestBrowserChangeAuthoring$' -count=1 -v
CONDUCTOR_TEST_TERMINAL=1 \
  go test -race ./tests/acceptance -run '^(TestAuthenticatedTerminalWorkbench|TestLocalTerminalReleaseRegression)$' -count=1 -v
```

Browser acceptance uses Playwright 1.62.0 and Chromium/Chrome, with the synthetic
signed issuer and an explicit local-mode API. See the
[browser setup](browser-sign-in.md#reproduce-acceptance) for dependency installation.
The terminal tests use real Linux PTYs and Python 3. These checks exercise shared
creation, review, independent approval, editing, retained fields and recovery;
they do not call paid coding providers or grant deployment approval.
