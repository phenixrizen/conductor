# Workspace tracker setup

Conductor synchronizes existing Linear or Jira tickets with exact work-package and
provider-publication references. The selected tracker owns assignment, priority and
planning status. Conductor publishes a separate link card; ticket state never
proves approval, passing checks, merge, deployment or completion across repositories.

Apply ordered migrations through 009. Existing API OIDC configuration and workspace/
repository provisioning remain required. The trusted operator uses a separate
command to select one tracker and its grants:

```bash
go run ./cmd/conductor-tracker-admin apply --file tracker.json --operator synthetic-operator --check
go run ./cmd/conductor-tracker-admin apply --file tracker.json --operator synthetic-operator
```

A synthetic Linear configuration is:

```json
{
  "workspaceId": "team",
  "provider": "linear",
  "host": "linear.app",
  "organizationId": "11111111-1111-4111-8111-111111111111",
  "scopeId": "22222222-2222-4222-8222-222222222222",
  "profile": "linear-graphql/23f11eb41ef63ba219ec582911079c19d1abbf62",
  "credentialId": "tracker-api",
  "webhookCredentialId": "tracker-hook",
  "conductorOrigin": "https://conductor.example.invalid",
  "enabled": true,
  "statuses": [{"id": "44444444-4444-4444-8444-444444444444", "display": "Planning in progress", "ignore": false}],
  "grants": [{"principalId": "person-author", "canRead": true, "canSync": true, "canResolve": true}]
}
```

Use actual stable IDs and explicitly reviewed status mappings. For Jira, select
`provider: jira`, your `*.atlassian.net` host, numeric project/status IDs and profile
`jira-cloud-rest/v3-2026-09-13`; omit `organizationId`. No built-in status name grants
completion. `ignore: true` marks an intentionally unsynchronized planning state.

Omitted grants remain unchanged. Explicit false values revoke them; disabled
configuration blocks new provider work. Every operator update increments the
configuration version, so queued work from the previous binding stops. Retained
links prevent silent provider/team/project/host/origin replacement. Credential
rotation inside the same operator-owned secret file does not alter that binding.

The worker credential-reference file contains a `credentials` array with entries
`id`, `workspaceId`, `provider`, `host`, optional `organizationId`, `scopeId`, and an
absolute `secretFile`. Include entries for both API and webhook credential IDs.
Each must exactly match its workspace/provider binding. Files must be regular,
owner-only (0600 or stricter), absolute paths; symlinks and FIFOs are rejected.
Secret files contain JSON: `{"token":"REPLACE_LOCALLY"}` for a Linear API key or
webhook secret, `{"token":"REPLACE_LOCALLY","oauth":true}` for a Linear OAuth
token, or `{"token":"REPLACE_LOCALLY","email":"synthetic@example.invalid"}`
for a Jira API credential. Never put credentials in command arguments or repositories.

```bash
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor
export CONDUCTOR_TRACKER_CREDENTIALS_FILE=/absolute/operator/tracker-credentials.json
export CONDUCTOR_TRACKER_WEBHOOK_ADDRESS=127.0.0.1:8091
go run ./cmd/conductor-tracker-worker
```

The worker requires `DATABASE_URL` through the existing trusted runtime environment.
Its initial Temporal profile is explicitly local. A trusted HTTPS reverse proxy
forwards provider callbacks to `/webhooks/tracker/{workspaceId}` on the loopback
listener. Configure a Linear Issue webhook or a signed Jira admin issue webhook
with the matching secret. OAuth-app Jira JWT callbacks are unsupported. The worker
does not install webhooks or expose an unauthenticated administrative endpoint.

Use the shared API/client or MCP to inspect settings, create a relationship and
read its `latestSyncId`. Creation queues an initial refresh. The relationship
includes the selected repository and every exact package revision/digest; readers
need tracker permission plus access to every included repository. Publication
references include immutable first-publication receipts and exact provider targets;
the separate `publications` response exposes ongoing provider observations. Every
repository and package pinned by each publication run must appear on the ticket
relationship. One merged change never completes a ticket spanning other work. Inspect a fresh
observation before requesting `publish` with the link digest and observed
projection digest (omit the latter for an inspected absent card). Retain the same
input and key after an uncertain request.

A changed Conductor card is a visible conflict. A separately permitted human can
request `restore` with the inspected conflicting digest. Agents cannot resolve
these conflicts; MCP exposes refresh/publish only. Neither operation edits the
tracker's title, description, assignment, priority or status.

A lost provider response is reconciled by reading the unique card. If its intended
content cannot be confirmed, retain `unknown`; the activity never blindly resends
that write. A new command requires explicit inspection and a new key. Missing
retained Temporal history becomes `unresolved`/`unknown`, never a replacement run.
Recent observations are still point-in-time reads; after five minutes `current`
becomes false. A delayed snapshot older than already observed provider data is
retained as `conflict` with `provider_snapshot_older` and `current: false`.
External APIs provide no atomic compare-and-swap for link cards,
so edits in the read/write race window may not be detectable.

Limits: 1–16 package references, 16 publication references, 100 configured statuses,
256 configured grants, 100 pending events per linked ticket, 100 matching links per
webhook, 100 records/page, 256 KiB webhook bodies, 512 KiB provider responses,
64 KiB descriptions and 15-second provider requests. Truncated provider pagination
cannot establish missing data. Signed callbacks exceeding queue capacity receive a
retryable error without committing their inbox entry.

```bash
export CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
go test -race ./internal/tracker ./internal/trackerworker ./tests/acceptance -run Tracker -count=1
CONDUCTOR_TEST_TEMPORAL=1 go test -race ./tests/acceptance -run TrackerTemporal -count=1
```

Owned runtime acceptance requires the verified Temporal CLI 1.8.3. Tests own their
schemas and Temporal SQLite/processes; they never restart the development database.
Provider fixtures are synthetic. Live tenant credentials, webhook HTTPS routing
and hosted/TLS Temporal remain separate deployment verification prerequisites.
See [API research](../architecture/tracker-integration-research.md) for exact sources.
