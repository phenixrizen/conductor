# Feature 019: Guided Change authoring

**Status:** Implemented and locally verified with signed browser, real terminal
and PostgreSQL acceptance. Review, merge and deployment remain separate. This is
the first usability increment, not completion of the broader assisted-workflow proposal.

## Outcome and vocabulary

An author can create a **Change**, edit its **Design**, save an immutable revision,
and request independent review in either the browser or terminal without writing
a JSON document. A Change is the existing work-package record at `/changes`;
Design names its reviewable content. These labels do not introduce new database
entities or alter existing identifiers, command fields, digests, or approvals.

The broader Objective, Plan, Step, Run, Result and Publication vocabulary and
tracker mappings remain in the separate
[vocabulary proposal](https://github.com/phenixrizen/conductor/pull/36).
Conductor's name came from the project's author seeing a conductor while riding
a train to Chicago. The interface uses ordinary engineering language rather than
requiring a railway metaphor for every operation.

## Acceptance criteria

1. Present an obvious new-Change action, including an empty shared list. Guide
   authors through title, intended outcome, scope, design, planned work and
   verification plan. Require a nonblank title and intended outcome for new guided
   drafts only; do not tighten the content contract for existing API clients.
2. Store these fields as top-level strings named `title`, `intent`, `scope`,
   `design`, `tasks` and `verification`. Edit only absent or string-valued fields.
   Display existing objects, arrays, booleans, numbers and null values read-only;
   preserve them and all unknown fields when saving. Untouched missing fields
   remain missing. Never silently convert structured content into text.
3. Show readable sections in inspection and preview, with complete JSON available
   for all retained content. Keep the exact revision and digest visible for review.
   Escape untrusted content; do not interpret stored text as HTML, scripts or
   terminal commands. Existing context and criterion evidence retain their separate
   provenance and verification semantics.
4. Preview the complete proposed content before creating or saving. Create uses
   the existing create command; edits capture the inspected expected revision.
   Requesting review is a separate action on a saved current revision. Confirmation
   performs no reads, refreshes or implicit submission. Historical views remain
   read-only and approval still requires an independent authorized human.
5. Bound the complete guided command, including its content and revision fields,
   to 1 MiB and prevent unchanged form saves. Preserve the
   existing limitation that an exact historical content revert is rejected by the
   store; this feature does not invent a new duplicate-content policy.
6. Derive controls from the current server principal and repository capabilities.
   Retain fixed terminal identity, browser session/scope fencing, cancellation and
   denied-access clearing, including editor and preview state. Leaving a browser
   workflow clears unsubmitted confirmations without silently retrying commands.
7. A stale or uncertain write requires explicit inspection. Create/revise/submit
   have no idempotency-key contract: never retry them automatically or present a
   lost acknowledgment as a saved Change. Inspect shared saved work to reconcile
   an uncertain creation before deliberately starting another draft. Retain the
   handwritten input through successful recovery in the same identity and scope.
   Reusing it requires a separate explicit choice and complete replacement preview
   bound to the newly inspected revision; it is not a merge or command replay.
   An uncertain creation may have committed even when a bounded list is empty.
8. Shared discovery includes optional `title`, `intent`, `titleTruncated` and
   `intentTruncated` projections from the current authorized revision. Retain exact
   case-sensitive string values, capped at 200 and 400 Unicode code points
   respectively; emit a truncation flag only when shortened. Omit empty or nonstring
   values. Apply existing scope and authorization before pagination in the same
   store query. Summary fields never replace full revision content or its digest.
9. Keep explicit bounded JSON-file import as an advanced terminal operation with
   complete replacement preview. The guided editor does not silently replace the
   imported document or launch an external editor.
10. Verify actual signed browser and terminal authoring against PostgreSQL, exact
    confirmation inputs, shared discovery, independent approval and invalidation.
    Cover legacy structured content, missing keys, stale/uncertain writes, access
    denial and existing review paths.

## Flow

```text
New Change
    |
    v
Title + intended outcome + Design sections
    |
    v
Preview --> Save draft revision --> Request review
                                     |
                                     v
                          Independent inspection
                                     |
                                     v
                         Approve exact revision/digest
                                     |
                       Edit Design --+
                            |
                            v
                   New draft; review required again
```

Saving, submitting and approving are distinct actions. Approval does not start a
Run, publish code, merge a branch, or prove an implementation.

## Boundaries

This increment reuses the existing API commands and adds a read-only discovery
projection. It adds no migration, provider authentication or model invocation.
Codex, Claude Code and Antigravity assistance, their API/OAuth capability matrix,
guided Plan construction, and broader vocabulary adoption remain later work.
Existing coding adapters and MCP retain their independently documented limits.

See the [delivery plan](plan.md) and
[authoring walkthrough](../../docs/operations/change-authoring.md).

[Feature 020](../020-native-design-assistance/spec.md) extends guided authoring with
shared native-assistant section suggestions, a restricted assistance MCP profile,
and explicit requester application as an ordinary unapproved revision. It also
adopts the Switch visual specification across the web and ASCII terminal header.
Provider accounts remain native; hosted inference and broad source selection are
not implemented by this increment.
