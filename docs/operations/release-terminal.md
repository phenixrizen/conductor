# Graphs, agent work, publication and tickets from the terminal

The CLI and interactive terminal use the same authenticated API as the browser and
MCP bridge. They let a developer inspect repository relationships, propose related
agent tasks, review implementation artifacts and synchronize linked tickets.
PostgreSQL keeps those facts shared with other authorized people and agents.

Configure the API and worker integrations using [execution setup](coordinated-execution.md),
[repository delivery](repository-delivery.md) and [work tracking](work-tracking.md).
Use the supported token-file setup in [terminal review](terminal-review.md). A human's
execution and publication grants are separate from package design approval. The
server checks current permissions on every included repository for each command.

## Request files and CLI

Consequential file commands use one regular UTF-8 JSON file, at most 1 MiB:

```json
{
  "idempotencyKey": "synthetic-graph-review-1",
  "input": {
    "sources": [{
      "repositoryId": "application",
      "collectionId": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    }]
  }
}
```

The example describes a graph request; replace its synthetic IDs and digest with
inspected server receipts. Other request kinds use the same envelope with the
corresponding [OpenAPI](../../api/openapi.yaml) input: `CoordinationPlan`,
`DeliveryInput`, `TrackerLinkInput` or `TrackerSyncInput`. A whole-source graph and
execution plan also pin each inspected `fullSourceDigest`. Native task profiles
require exact operator configuration and image digests.

Preview validates and normalizes the input without contacting the API or reading
credentials. Duplicate, case-aliased, unknown and null fields are rejected before
preview. Pipes, devices and stdin cannot supply these request files.

```bash
conductor graph-preview --file /private/graph-request.json
conductor graph-create --file /private/graph-request.json --digest PREVIEW_DIGEST
conductor run-preview --file /private/run-request.json
conductor run-propose --file /private/run-request.json --digest PREVIEW_DIGEST
```

Set `CONDUCTOR_URL`, `CONDUCTOR_TOKEN_FILE`, `CONDUCTOR_WORKSPACE` and
`CONDUCTOR_REPOSITORY_ID`, or pass the documented workspace/repository flags.
The create/propose command requires the complete request digest printed by its
preview. The preview digest binds its request kind, key and normalized input. The
command rereads the explicitly selected file and refuses any change to those facts.
Keep the original file and key after a lost response; an explicit retry sends the
same input. An edit needs a new preview and key. Recording a proposal does not
start execution or publication.

| Purpose | CLI commands |
|---|---|
| Repository relationships | `graphs`, `graph ID`, `graph-query --search TEXT ID`, `graph-query --node NODE_ID --depth 1 ID` |
| Read graph source | `graph-artifact --digest GRAPH_DIGEST --source-repository SOURCE_ID --collection-id COLLECTION_ID --receipt-digest RECEIPT_DIGEST --full-source-digest BUNDLE_DIGEST --path FILE GRAPH_ID` |
| Create a graph | `graph-preview --file FILE`, then `graph-create --file FILE --digest PREVIEW_DIGEST` |
| Shared task plans | `runs`, `run ID`, `execution-profiles`, `execution-capabilities` |
| Propose tasks | `run-preview --file FILE`, then `run-propose --file FILE --digest PREVIEW_DIGEST` |
| Complete task evidence | `run-artifact --digest RUN_DIGEST --task-id TASK_ID --artifact-digest ARTIFACT_DIGEST RUN_ID` reads the exact retained report or check output, including failures and reports with no patch. |
| Human execution decisions | `run-authorize --digest PLAN_DIGEST ID`, `run-cancel --digest PLAN_DIGEST ID` |
| Publication proposals | `delivery-preview --file FILE`, then `delivery-propose --file FILE --digest PREVIEW_DIGEST` |
| Inspect publication evidence | `deliveries`, `delivery ID`, `delivery-artifact ID` |
| Human publication decision | `delivery-authorize --digest DELIVERY_DIGEST ID` |
| Refresh provider observations | `delivery-reconcile --digest DELIVERY_DIGEST --idempotency-key KEY ID` |
| Workspace tracker and links | `tracker`, `tracker-links`, `tracker-link ID` |
| Link inspected work to a ticket | `tracker-link-preview --file FILE`, then `tracker-link-create --file FILE --digest PREVIEW_DIGEST` |
| Request ticket synchronization | `tracker-sync-preview --file FILE`, then `tracker-sync --file FILE --digest PREVIEW_DIGEST LINK_ID` |
| Inspect a synchronization request | `tracker-sync-show SYNC_ID` |

Commands print bounded JSON. List commands accept `--limit 1-100` and the opaque
`--page` cursor returned by the previous page. Graph queries support depth 0–5 and
up to 100 returned nodes, with explicit source gaps and truncation. Graph artifact
reads require the complete inspected source tuple; omit `--full-source-digest` only
when the graph source has no whole-source pin. Unretained paths remain unavailable.
Flags precede the positional record ID. Record authorization uses the exact displayed record
digest; it does not fetch a newer record inside the command.

## Interactive controls

```bash
conductor tui --view graphs
conductor tui --view runs
conductor tui --view deliveries
conductor tui --view tracker
```

An optional record ID opens it after access discovery. The default `conductor tui`
keeps the existing package/collection review controls. Release views require a
selected authenticated workspace and repository; local actors cannot open them.

| Key | Action |
|---|---|
| `1` / `2` / `3` / `4` | Switch to graphs / runs / deliveries / tracker; clear inspection and recheck access |
| Enter or `o` | Inspect the selected row or an exact record ID |
| `n` / `p` | Browse bounded pages; at most 1,000 visited cursors |
| `c` | Select and preview a request file for the current view |
| `s` | Confirm the complete preview and its retained idempotency key |
| `a` | Confirm human execution authorization, or publication authorization after artifact inspection |
| `x` in runs | Confirm cancellation against the inspected plan digest |
| `e` in runs / tracker | Inspect public operator profiles / tracker settings |
| `/` / `t` in graphs | Search text / traverse an inspected node at depth 1 |
| `f` in graphs | Select an exact inspected source repository and relative path; read the retained artifact without changing the authenticated anchor |
| `v` in deliveries | Load the exact retained patch and check artifact for inspection |
| `u` in deliveries | After a provider observation is available, preview a reconciliation file whose `input` is `{"digest":"INSPECTED_DELIVERY_DIGEST"}` |
| `u` in tracker | Preview a `TrackerSyncInput` file bound to the inspected link digest |
| `i` in tracker | Inspect the link's latest recorded synchronization request |
| `r` | Clear private inspection and recheck the same identity, scope and capabilities |
| `b` | Return to shared browsing |
| Arrows, Page Up/Down, Home/End | Select rows or scroll complete escaped records |
| Escape | Cancel a prompt/read or discard a preview; a cancelled write can have committed |
| `q` / Ctrl+C | Exit and cancel pending I/O |

The confirmation displays the exact record or input digest. A viewport too small
to show that identity cannot confirm a command. Repository text, patches, logs and
errors are escaped so they cannot control the terminal. Full artifact JSON remains
available along with readable copies of its patches. The artifact is bounded to
16 MiB, and its exact digest and selected delivery patch are checked before enabling
publication authorization.

An unavailable or missing profile cannot enable execution. A zero-check task has
no implementation verification evidence. Authorization acknowledgment is separate
from observed runtime completion; cancellation is separate from confirmed stopping
and cleanup. Delivery observations distinguish draft publication, provider checks,
merge and deployment. Tracker status cannot grant approval or prove those events.

Conflicts and uncertain authorization/cancellation responses block another command
until explicit refresh and inspection. An uncertain create or synchronization
retains its exact preview and key for explicit retry. Authentication or permission
failure clears every private record, artifact, draft, profile and confirmation.
Recovery reads the same session credential; changing the token file cannot change
a running terminal's identity. Display aging does not poll the API.

## Acceptance

```bash
go test -race ./internal/tui ./internal/reviewinput ./cmd/conductor
go vet ./internal/tui ./internal/reviewinput ./cmd/conductor
CONDUCTOR_TEST_TERMINAL=1 \
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go test -race ./tests/acceptance -run TestAuthenticatedTerminal -count=1
```

The release acceptance drives the actual compiled CLI and Linux PTYs through a
signed synthetic OIDC API and isolated PostgreSQL schema. It checks all four
workflows, exact plan retries after a real lost response, stale design rejection,
human decisions, reader/agent limits, artifact-gated publication, tracker refresh,
revocation and fixed-token recovery. Its immutable implementation receipts are
explicit review fixtures; real Docker/Temporal execution and provider compatibility
retain their separate acceptance evidence. Missing opt-in is a skip; missing tools
or database after opting in fail the test. Existing development data is preserved.

Task output is shared independently of publication. In the **runs** view, press
`v` and enter a task key or opaque task ID from the inspected receipt list. The
request captures the displayed run digest and that receipt's artifact digest;
it never refreshes the run inside the read. Failed checks remain failed, and a
report without a patch does not establish verified implementation. All repository
read grants remain required, while execution and publication grants are not
needed to inspect historical output. The complete JSON response is bounded to
17 MiB, containing an artifact of at most 16 MiB. Missing artifacts and invalid
digests produce explicit errors. MCP's `conductor_get_task_artifact` keeps its
2 MiB response bound and directs larger complete inspections to the API or CLI.
