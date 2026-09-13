# Coding checks and approved criteria

**Current:** explicit criterion catalogs are optional immutable package content.
An independent human approval still covers the entire exact revision. Plans refer
to selected catalog entries through package/revision/digest/criterion tuples.
The selected command is inspected as part of the immutable human-authorized plan;
Conductor does not invent an executable interpretation of a prose requirement.

The admission transaction resolves every reference against its already-pinned
package. The original task scopes limit cross-repository references and loaded
source. Existing all-source grant locks and current exact approval checks remain
in place before execution and receipt commit. Worker requests and check evidence
retain the same optional references, and the trusted publisher compares them to
the exact task before permitting artifact publication.

Inspection derives support from the artifact's checks and the original catalog.
The derived view belongs beside the stored artifact, outside its immutable digest.
It evaluates only this task's declared checks: all linked checks for a criterion
must pass completely on the same artifact. Other tasks, unlinked checks, provider
statuses and runtime results cannot implicitly satisfy it. Missing catalog data
and unsupported schema versions remain visible gaps.

No overall business-requirement verdict is inferred. A command's observed pass
supports the declared criterion; the author and independent reviewer remain
responsible for selecting a suitable check. Historical support remains historical
when the design changes. Criteria do not accept ADRs or grant any authority.

See [feature 018](../../specs/018-verification-criteria/spec.md),
[setup and review](../operations/verification-criteria.md) and
[coordinated execution](coordinated-execution.md).
