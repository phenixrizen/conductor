# Review pinned repository context and history

This local-development workflow captures committed artifacts for design review.
It does not execute tests, interpret an ADR as approval, or authenticate a user's
organizational role. Use synthetic/local development repositories and identities.

## Start the services

Start PostgreSQL as described in [local development](local-development.md), then:

```bash
export DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable'
CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

In another terminal, start the browser workbench:

```bash
npm --prefix apps/web ci
npm --prefix apps/web run dev -- --host 127.0.0.1
```

Vite proxies `/api` to `http://localhost:8080`. Set `CONDUCTOR_API_URL` when the API
uses a different address. Open the local URL Vite prints.

## Capture committed files

From the Conductor repository, collect a specification and an ADR as native text
artifacts. The collector resolves `HEAD` once and reads that commit's objects,
ignoring uncommitted files:

```bash
go run ./cmd/conductor context \
  --repo . \
  --repository phenixrizen/conductor \
  --ref HEAD \
  --title 'Review the package approval design' \
  --path specs/001-work-package-review/spec.md \
  --path docs/adr/0001-immutable-work-package-revisions.md \
  > /tmp/conductor-review-content.json
```

To attach context to existing structured content, replace `--title` with
`--file /path/to/content.json`. Other fields are retained; `repositoryContext` is
replaced with the new snapshot. Use a separate output file rather than redirecting
over the input. Review the resulting content before submitting it.

The CLI prints a package content object and does not send it to the server. Each
artifact includes its path and a collection state. `collected` means complete text
was captured, not that verification passed. Missing, unsupported, binary, or
oversized files remain visible as gaps. Collection inspects up to 32 explicit paths,
64 KiB per file and 256 KiB total text. It does not follow symlinks or execute
repository scripts. Spec Kit and ADRKit files are retained without parsing their
native schemas or claiming compatibility with their commands.

## Submit and inspect

```bash
go run ./cmd/conductor create --actor developer \
  --file /tmp/conductor-review-content.json
go run ./cmd/conductor submit --actor developer --revision 1 CHG-...
go run ./cmd/conductor show --actor reviewer CHG-...
```

Use **Browse shared work** to find packages across clients, filter by the exact
repository identity, or enter the returned change ID in the workbench. Inspect the revision, digest, selected
repository commit, artifact text, and gaps. Perspective choices tailor review
questions; they grant no authorization. Approval still targets the exact displayed
revision and digest. A stale response requires a fresh explicit inspection.

The snapshot is author-supplied evidence. The API checks its structure and content
digests but does not independently establish repository identity or provenance.

## Check freshness without changing the package

```bash
go run ./cmd/conductor context-check --repo . --ref HEAD \
  --file /tmp/conductor-review-content.json
```

The result is `current`, `stale`, or `unavailable`; the command exits nonzero for
the latter two. It compares commit IDs in the selected local repository. It does
not fetch remote refs, prove the repository's identity, modify content, or carry
approval forward. To review new source, collect it again into a new package revision
using an explicit expected revision.

## Find shared work

```bash
go run ./cmd/conductor list --actor reviewer
go run ./cmd/conductor list --actor reviewer --repository phenixrizen/conductor
```

All clients using the same API read the same saved dataset. The list shows current
package revisions, authors, and approval state; it does not report live activity.
Pass `--page <nextBefore>` to retrieve another shared-work page. Repository labels
are author-supplied grouping data, not authorization. See the
[collaboration model](../architecture/collaboration.md).

## Inspect retained history

```bash
go run ./cmd/conductor history --actor reviewer --limit 20 CHG-...
go run ./cmd/conductor show --actor reviewer --revision 1 CHG-...
go run ./cmd/conductor events --actor reviewer --limit 20 CHG-...
```

Pass `--before <nextBeforeRevision>` to retrieve older revision metadata, or
`--after <nextAfterSequence>` for the next audit page. Limits range from 1 to 100.
Historical inspection returns the stored revision and up to 100 approval records;
`approvalsTruncated` explicitly signals any omitted approvals. Audit pagination
can recover further approval events.

The browser supports historical selection and content comparison. Historical
approvals are records about that revision, not permission to approve it again or
evidence that a later revision is approved. Selecting a historical view disables
approval; return to an explicit latest inspection to review the current package.

## Browser acceptance check

With the API and workbench running against a test database, run:

```bash
CONDUCTOR_BROWSER_API_URL=http://127.0.0.1:8080 \
CONDUCTOR_BROWSER_WEB_URL=http://127.0.0.1:5173 \
  uv run tests/browser/review.py
```

This uses the pinned Playwright version declared in the script and local Google
Chrome (`CONDUCTOR_CHROME` can override its executable path). It creates synthetic
packages, exercises shared discovery and stale approval/history behavior, and
writes desktop/mobile screenshots to `/tmp` by default. Use a test database: the
application deliberately has no operation to erase review history.
