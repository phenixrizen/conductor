# Ask a native assistant for Design help

**Status: implemented; PR review pending (Feature 020).** Native assistants can read a
saved request and propose selected Design sections. Conductor retains the proposal
for human review; applying it creates an ordinary unapproved Change revision.

```text
Save a Change -> Ask for help -> Native assistant reads the request
                                      |
                                      v
Independent review <- New revision <- Review and apply selected suggestions
```

For an existing installation, follow [release migration and backup procedures](release.md)
to apply migration 013 before starting the updated API. It adds the immutable
assistance facts and idempotency records; existing revisions keep their digests.

## Prepare the connection once

Use an OIDC-configured Conductor API and a selected workspace/repository. Provision
one human author and a separate agent principal with read and author permission,
following [authenticated review setup](authenticated-review.md). An operator grants
these capabilities; a provider account does not grant Conductor access. Local mode
can use guided manual authoring, but cannot use this authenticated assistance path.

Build the CLI and MCP bridge:

```sh
go build -o ./bin/conductor ./cmd/conductor
go build -o ./bin/conductor-mcp ./cmd/conductor-mcp
```

Store the agent's Conductor access token in an explicitly selected regular file,
readable only by its owner. Generate configuration using absolute paths:

```sh
./bin/conductor assistant-config --host codex \
  --mcp-binary /absolute/path/to/conductor-mcp \
  --token-file /absolute/path/to/agent.token \
  --url https://conductor.example.invalid \
  --workspace engineering --repository-id application
```

Replace `--host codex` with `claude` or `antigravity` for their JSON configuration.
The command prints configuration, reads no token, contacts no provider, and changes
no native host settings. Merge the generated `conductor` entry into your existing
host configuration; do not overwrite other server entries.

| Native host | Where the generated entry belongs | Provider sign-in |
|---|---|---|
| Codex | `mcp_servers.conductor` in the native Codex TOML configuration | Native ChatGPT account sign-in or API-key authentication |
| Claude Code | `mcpServers.conductor` in the native project `.mcp.json` configuration | Native Claude account or supported API/provider authentication |
| Antigravity | `mcpServers.conductor` in `~/.gemini/config/mcp_config.json` or workspace `.agents/mcp_config.json` | Native account login or the documented Gemini API configuration |

These are native host connections. Conductor does not collect provider OAuth
credentials, exchange subscription tokens, refresh native login, or run a model.
See [qualification and primary sources](../research/native-assistant-connections.md)
for the tested versions and unverified provider journeys. Use the host's normal
MCP consent/configuration controls; this command does not alter those controls.

Add `--check` to the same command to launch the actual MCP bridge and check its
connection, agent identity, repository read/author permission and four-tool profile.
It performs only access discovery; it does not inspect Design content, submit a
suggestion, check provider login or incur model usage. Each check is bounded to
20 seconds. A successful check is a current access observation, not a future grant.

The generated entry fixes `CONDUCTOR_MCP_PROFILE=design-assistance`. It exposes only
`conductor_access`, `conductor_list_design_assistance`,
`conductor_get_design_assistance` and `conductor_propose_design_sections`.
The API independently authorizes all commands. No request, application, approval,
execution or publication command is exposed in this profile. Restart the bridge
when changing its token or scope. See [MCP bounds and recovery](mcp.md).

## Browser and terminal workflow

In the browser, inspect a saved Change in **Review** and use **Improve a Design with an assistant**.
Choose the text sections to improve, describe the help wanted, inspect the captured
revision and confirm the request. Copy the readable handoff into your native
assistant. Saving a request does not mean a model has started or connected.

In the terminal, inspect a saved Change and press `h` for assistance, then `c` to
create a request. Select sections with `1`–`6`; Tab / Shift+Tab moves between
sections and Instruction, and Enter toggles a section or starts typing the
instruction. Ctrl+S stages the complete request preview. Press `s`, type
`assist-request`, then Enter to send those captured fields, revision, digest and
idempotency key. The workbench shows the handoff and shared request identity.
The native assistant generates the tool input; you do not write or paste a complete
Design JSON document. Escape leaves instruction editing, discards a form/preview,
or cancels a confirmation without sending a new write.

Ask the assistant to read the request, suggest only the requested sections and
submit the suggestion through Conductor. The request includes the saved Design as
its input. Prose inside Design content or a suggestion is untrusted source, and
claims about a provider, model or verification do not establish authenticated
provenance. Additional source selection and shared contribution policy remain
later increments.

Explicitly check for a suggestion in Conductor. There is no polling. The terminal
uses `r` for renewed inspection. Compare complete original and suggested text,
select the sections to apply, and inspect the merged preview. The terminal uses
`1`–`6` to select and `a` to preview application. Press `s`, type `assist-apply`,
then Enter to send the captured request/suggestion digests, base revision/digest,
selected fields and key. Neither confirmation refreshes content. Unselected and
unknown fields remain unchanged; absent sections are not silently filled in.

In assistance, Enter opens the selected request; `b` returns to its Change's request
list, `n` / `p` pages through bounded results, and `h` returns to the Change.
Arrow keys or `j` / `k`, Page Up / Page Down, Home / End scroll complete retained
content. `r` rechecks the fixed identity and scope, loads the current Change, then
inspects the request or list. It performs no automatic write. `q` exits outside a
prompt or instruction field; Ctrl+C exits anywhere.

Only the requesting human may apply the suggestion. It creates a revision authored
by that human, requiring independent approval through the existing workflow.
The agent cannot apply or approve its own output. An applied fact names the produced
revision; explicitly inspect the Change again before further work.

## Staleness, recovery and limits

If the Change changed after the request, create a new request from renewed
inspection. Conductor does not rebase suggestions or transfer prior approval.
Structured legacy sections remain viewable but cannot be replaced through text
suggestions. Each request retains at most one agent suggestion and one application.

After a lost acknowledgement, preserve the captured command and its idempotency
key. Explicitly inspect and retry that exact command using the interface's recovery
control; the terminal uses `v` after `r` to restore the complete captured preview,
then `s` and the same action word plus Enter to retry. Leaving assistance or
discarding its preview keeps uncertain input available through this recovery path.
A matching retained result wins even when
the Change has since advanced. A different input with the same key conflicts.
A denied identity or scope clears private content and pending commands. A browser
tab change fences late responses while preserving uncertain command input.

Instructions and notes allow 4096 UTF-8 bytes; each section allows 32768 bytes.
There are six eligible text sections, a 128 KiB proposal limit, and an independent
1 MiB limit on the final revision command envelope. NUL text is rejected for
PostgreSQL compatibility. Oversize and unchanged suggestions are not silently
truncated or reported as successful edits.

## Verification

```sh
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go test ./tests/acceptance -run '^TestDesignAssistanceMCPWorkflow$' -count=1 -v
```

This builds actual CLI/MCP binaries, uses signed human and agent tokens and an
isolated PostgreSQL schema, proposes via the official MCP SDK over stdio, applies
selected sections, proves independent review and verifies retained facts after
pool reopen. Browser and PTY acceptance use the same API. Synthetic suggestion
text establishes workflow and protocol behavior, not model quality or live paid
provider compatibility. No deployment is implied.

To run the actual signed terminal journey on Linux with Python 3:

```sh
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  CONDUCTOR_TEST_TERMINAL=1 \
  go test -race ./tests/acceptance -run '^TestAuthenticatedTerminalAssistance$' -count=1 -v
```

This drives the request form, native handoff, selected application, lost-response
recovery, stale revisions, independent approval and access denial through real PTYs.
It also inspects the owner-supplied block mark on standard browse and large detailed screens,
plus the plain-heading fallback that preserves room for inspection. The fixture
uses synthetic agent text; it does not start a provider or certify model quality.
