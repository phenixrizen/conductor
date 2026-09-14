# Feature 020: Native assistant section suggestions

**Status: Implemented; PR review pending.** This increment depends on Feature 019,
merged through PR #37. PR #39 now targets `main` and connects the existing native
MCP boundary to reviewable Design suggestions;
embedded inference, managed provider login, generalized shared drafts and broader
source selection remain later increments.

## Complete journey

1. An authenticated human author inspects a saved Change, selects one or more text
   sections and describes the help wanted. Confirmation captures the exact revision
   and digest and creates an immutable request with an idempotency key.
2. The user asks their natively configured assistant to handle that request. The
   assistant discovers/reads it through Conductor MCP and proposes small section
   strings, rather than reconstructing a complete package JSON document.
3. Conductor retains the agent proposal separately. It does not alter the Change,
   request approval or start execution. The browser and terminal show the original
   and suggested sections, explanatory notes and the recorded proposing agent.
4. Only the requesting human may apply selected proposed sections. The exact
   request, suggestion, current Change revision/digest and selected fields are
   confirmed. The server preserves every other field and creates one ordinary
   unapproved revision attributed to that human. Existing independent approval
   rules therefore still exclude the person who requested and applied the text.

## Fixed scope and authority

This path requires OIDC and a selected workspace/repository. Local actor headers
cannot substitute for an authenticated human/agent identity. Server-owned principal
kind and author permission govern request, suggestion and application commands in
the committing transaction. Readers may inspect authorized records; agents may
propose but cannot request human-owned assistance or apply it. Other humans cannot
apply another person's request. Existing full-document commands retain their
existing behavior; this increment does not rewrite the general attribution policy.

The assistance input is only the captured saved Design. It adds no external source
selector or authenticated citation contract. Suggestions and notes are unverified
author-supplied prose. Provider/model names or source claims inside that prose do
not establish provenance or verification. Existing source tools retain their own
access rules; cross-repository derived-source tracking is not implemented here.
The dedicated assistance MCP profile exposes only access, request discovery/read
and suggestion submission. It launches no repository commands or provider calls.

The native host owns provider credentials, consent and billing. Conductor retains
its separate fixed-scope agent token-file model. Native account sign-in is not
Conductor browser sign-in. A recorded request is an inbox item, not evidence that
an assistant is connected, running or has incurred usage. PostgreSQL stores facts;
no scheduler or second execution authority is introduced. Hosted durable inference
continues to require a separately qualified Temporal activity design.

## Domain and transport contract

All commands reject unknown, duplicate, case-aliased and null fields where not
explicitly optional. New assistance IDs are 32 lowercase hex characters; existing Change and access
identifiers retain their original contracts. Digests are 64 lowercase
hex characters over deterministic domain serialization. Use `Idempotency-Key` on
each POST; a retry preserves the identical key and input. Authorization is checked
before returning a retained command result, and a retained matching result wins
over later revision drift. A different input with the same key conflicts.

| Route | Input / result |
|---|---|
| `POST /api/v1/design-assistance` | `AssistanceInput`; creates or recovers a request |
| `GET /api/v1/design-assistance?changeId=...&before=...&limit=20` | `AssistancePage`; optional exact Change filter, bounded keyset pagination |
| `GET /api/v1/design-assistance/{id}` | Complete `DesignAssistance` with exact historical base and retained facts |
| `POST /api/v1/design-assistance/{id}/suggestion` | `SuggestionInput`; agent-only, at most one immutable suggestion per request |
| `POST /api/v1/design-assistance/{id}/application` | `ApplySuggestionInput`; requester-only, at most one application per request |

- `AssistanceInput`: `changeId`, `expectedRevision`, `expectedDigest`,
  `instruction` (1–4096 UTF-8 bytes, nonblank), `sections` (1–6 unique section names).
- Section names are exactly `title`, `intent`, `scope`, `design`, `tasks`,
  `verification`. Requested sections must be absent or string-valued in the base.
  Structured/null legacy fields cannot be replaced through this operation.
- `SuggestionInput`: `requestDigest`, `sections` (1–6 requested section names mapped
  to strings), optional `note` (up to 4096 bytes). Each proposed string is at most
  32768 UTF-8 bytes and the complete proposal is at most 128 KiB. Empty proposed
  strings explicitly set an empty string; there is no delete/coercion operation.
- `ApplySuggestionInput`: `requestDigest`, `suggestionDigest`, `expectedRevision`,
  `expectedDigest`, `sections` (nonempty unique subset of proposed section names).
  The current Change must still match the original request revision/digest.
  No rebasing occurs. Reject unchanged content and exact historical content reverts
  with a bounded conflict instead of a false successful revision.
- `DesignAssistance`: `id`, `workspaceId`, `repositoryId`, `requesterId`, `createdAt`,
  `digest`, `input`, `base` (the exact `Revision`), optional `suggestion` and
  optional `application`. Neither optional fact is an execution status.
- Suggestion fact: `digest`, `agentId`, `createdAt`, `sections`, optional `note`.
- Application fact: `digest`, `appliedBy`, `createdAt`, `revision`, `revisionDigest`,
  `sections`. It records the produced revision, not the latest revision or approval.
- `AssistancePage`: `requests` (summaries with `id`, `requesterId`, `createdAt`,
  `digest`, `input`, `hasSuggestion`, `appliedRevision` when applied), optional
  `nextBefore`. Default 20, maximum 100, authorized filtering before pagination.

Append migration 013. Request/suggestion/application facts and their concise audit
events commit atomically with authorization; application also commits the ordinary
package revision/event in that same transaction. Do not log instructions, section
text or credentials. Bound final merged content to the existing 1 MiB command
envelope. Preserve old revisions, digests, approvals and all unselected fields.

## Client requirements

Browser and terminal use the same commands. Show empty, waiting, available,
historical/stale, applied, denied and uncertain states explicitly. No polling or
automatic retries. Capture inputs, selected fields, pins and key before confirmation.
Retain uncertain commands for explicit same-key recovery. Switching identity/scope
or receiving denied access clears every private request/proposal/preview. Leaving
a browser tab fences late responses and clears unsubmitted confirmations while
retaining uncertain input. Access recovery requires renewed inspection.

MCP exposes `conductor_list_design_assistance`, `conductor_get_design_assistance`
and `conductor_propose_design_sections`. No MCP request/application/approval tool
is exposed. An optional startup `CONDUCTOR_MCP_PROFILE=design-assistance` selects
only these tools and `conductor_access`, with no resource content from other views.
The default profile retains the existing full bridge plus these three tools.

## Acceptance

Use signed human and agent identities, actual Go MCP stdio, PostgreSQL, Chromium
and real terminal PTYs. Prove creation, native-tool proposal transport, shared
inspection, selected-section application, self-approval denial, independent review,
unknown-field preservation, strict inputs, stale revisions, lost acknowledgments,
idempotency, concurrent proposals/applications, access revocation and persistence
across pool reopen. Source and account qualification claims must match evidence.
Synthetic proposed text proves the transport and workflow, not real model quality
or paid inference. Native host configuration/help inspection is distinct from a
live authenticated provider journey.

## Adopted visual direction

The owner requested inclusion of the Switch brand branch and application of its
[design specification](../../docs/design/brand.md). Browser workflows use the
shared palette, flat panels, system-font fallback, focus styling and supplied logo.
The terminal renders a compact ASCII Switch header with a plain heading fallback
on small terminals. Branding never substitutes for status text or evidence.
The original brand commits from PR #38 are retained in this branch.
