# Product vocabulary and tracker mapping

**Status: Proposed vocabulary for user review, 2026-09-14.** This document records
an opinionated recommendation. It does not rename API fields, change lifecycle or
approval rules, implement tracker hierarchy support, or establish acceptance of an
ADR. Existing implementation terms remain documented below.

## Name and naming principles

The user supplied the origin of **Conductor**: they were on a train to Chicago
when a conductor walked past them. This is the recorded origin of the name.

Use familiar words for the work. The train story belongs to the brand; railway
metaphors are not required for every product object. Choose Conductor's vocabulary
from its responsibilities, then map external tools into it. Jira and Linear do not
define Conductor's domain or require identical hierarchies.

Prefer five primary nouns: **Objective, Change, Plan, Step, Run**. Use **Design,
Result, Publication, Requirement, Check, Evidence** where a more specific object
or view needs a name. A user can start a Change directly; creating an Objective,
tracker item or execution Plan is not a prerequisite to drafting.

## Recommended terms

| Term | One meaning | Existing implementation or proposed boundary |
|---|---|---|
| **Objective** | An optional goal that related Changes help achieve: "Make sign-in resistant to abuse." | New proposed grouping/view, initially informed by selected tracker grouping. No current first-class Objective record or approval. Never use Objective as proof of an achieved production outcome. |
| **Change** | A reviewable unit of engineering intent: what should change, why, its scope, design and requirements. | Canonical product name for existing change/work package/package. Retain stable `CHG-...` identity, immutable revisions and current repository ownership. |
| **Design** | The Change's complete reviewable proposal, shown as readable sections. | A view of versioned Change content, including goal, scope, decisions, context and requirements; not another independently versioned or approved object. |
| **Plan** | The exact proposed Steps, dependencies, source and tooling for implementing selected approved Changes. | Existing coordination/execution plan. Use "Design" for architectural explanation and "implementation roadmap" for a delivery document, avoiding several product objects all called plans. |
| **Step** | One bounded unit of work executed through a selected profile, with dependencies, allowed paths and declared checks. | Product label for existing execution `Task`. Steps may run in parallel and can concern several included repositories. A Step is not an agent message or an automatic tracker ticket. |
| **Run** | The record of carrying out a Plan, including authorization, observed progress and retained Step results. | Existing coordination run; it exists at proposal time. Show "Not started" until execution is observed, rather than implying that creating a Run starts work. |
| **Result** | What a Step produced and what happened: patch/report, check outcomes and any gaps. | Readable presentation over retained artifact and receipt facts, including failed, blocked or unexecuted work. Preserve their separate identities internally. |
| **Publication** | A proposal and authorized action to publish an exact retained implementation as a draft PR or MR. | Product name for the existing repository delivery record. One publication targets one repository; multiple publications do not become an atomic cross-repository release. |
| **Requirement** | A stated condition the Change should satisfy. | Readable label for approved criterion descriptions. Optional verification catalogs and stable criterion IDs retain their existing schemas and meanings. |
| **Check** | An actual declared command used to test the resulting implementation. | Existing verification command and its observed result. Proposed or unexecuted checks do not pass. |
| **Evidence** | Retained information supporting a specific conclusion, with its source and limitations. | Existing scoped source, execution and runtime evidence. A passing check supports only its declared requirement linkage. |

Keep **Workspace**, **Repository**, **Revision**, **Source**, **Approval**, **ADR**,
**PR/MR**, **Deployment** and **Runtime evidence** with their established meanings.
Keep digest, receipt, artifact, attempt, workflow and activity available in technical
details. They need not dominate normal authoring controls.

Use **Linked work** for the section showing external tracker records. Preserve
their native type, key, title and URL: for example, "Jira Story AUTH-42" or
"Linear issue AUTH-42." Do not require the user to learn an additional universal
Issue or Ticket entity to create a Conductor Change.

## Why these choices

**Change** covers features, bugs, refactors, documentation and operational fixes.
It already matches the stable ID and API resource, and avoids a second meaning of
"package." **Objective** expresses why several Changes belong together without
borrowing a provider's project or epic model. It is optional, so small work stays
small. **Step** removes the collision between a Jira Task and a bounded execution
unit. **Plan** and **Run** distinguish what is proposed from what happens.

Avoid adopting Epic, Story, Task, Sprint, or Project as mandatory Conductor
hierarchy levels. Also avoid renaming core objects to journey, carriage, station,
ticket or dispatch solely to fit the product name. Domain boundaries and useful
actions should explain the vocabulary.

## Mapping Jira and Linear

The following is a recommended association by purpose, not a vendor schema
equivalence or a new synchronization capability. No hierarchy or record is created
automatically by the mapping.

| Planning purpose | Jira example | Linear example | Conductor association |
|---|---|---|---|
| Broader strategic goal | Configured hierarchy above Epic, if present | Initiative | Upstream context for an Objective; preserve native ancestry without requiring another Conductor level |
| Group work toward a deliverable | Epic | Project | Default source for an optional Objective and its related Changes |
| Track a piece of work | Story, Task, Bug or another configured standard type | Issue | Link to one or more scoped Changes; a small item usually needs one Change |
| Track a smaller piece | Subtask | Sub-issue | Link to a Change where separately reviewed work is needed; optionally associate a Plan Step later when appropriate |
| Schedule work | Sprint | Cycle | Scheduling context; not a Plan or Run |
| Organize people and settings | Jira project/space | Workspace/team | Tracker connection scope; not a Change, repository or deliverable Objective |

Jira's default hierarchy places Epic above standard work and Subtask below it;
administrators can configure names and supported levels. Resolve actual provider
IDs and capabilities rather than inferring meaning from a displayed label. See
[Jira hierarchy](https://support.atlassian.com/jira-cloud-administration/docs/configure-the-issue-type-hierarchy/)
and [work types](https://support.atlassian.com/jira-cloud-administration/docs/what-are-issue-types/).

Linear organizes Issues with Projects and broader Initiatives, but not every Issue
belongs to a Project and its grouping is not a compulsory universal tree. A Linear
Project is a deliverable grouping; a Jira project/space is an organizational scope.
The matching word does not imply matching semantics. See
[Linear concepts](https://linear.app/docs/conceptual-model),
[projects](https://linear.app/docs/projects), and
[parent/sub-issues](https://linear.app/docs/parent-and-sub-issues).

An Objective is an explicit interpretation of selected planning scope. For example,
a broad Jira Epic may motivate several Objectives, or several small related tracker
items may motivate one Objective. Do not treat the table as automatic one-to-one
conversion. Keep native hierarchy and grouping relationships visible when available;
denied, missing or truncated discovery remains unknown, not an empty hierarchy.

## Relationships and current limits

- A tracker item may link to several exact Change revisions. A Change may have
  links to several tracker items. Neither side is created or completed merely
  because the other exists.
- A current authenticated Change belongs to exactly one canonical repository.
  An Objective can describe a goal spanning repositories, represented by related
  Changes and a coordinating Plan. Renaming a package must not silently change its
  ownership boundary.
- A current Plan pins exactly one approved Change revision per included repository,
  with 1–16 repositories and 1–32 Steps. It cannot currently combine two separate
  Changes in the same repository into one Plan. Broader composition would require
  its own domain change.
- A Plan contains Steps. A Step may touch several of the Plan's repositories and
  must obey all its source, path and permission boundaries. Dependency relationships
  permit parallel work; numbered Steps need not imply strict serial execution.
- Current Plan and Run data share one immutable proposal record. The vocabulary
  distinguishes the recipe from execution facts without requiring a new entity
  split. A new run proposal or substantive plan edit never inherits authorization.
- A retained Result may include several repository patches. A Publication selects
  exact eligible output for one target repository. GitHub/GitLab merge and deployment
  happen externally and Conductor retains observations.
- Direct Step-to-tracker associations, Objective/group discovery, parent/type
  metadata, ticket creation and tracker planning-field writes are proposed or absent.
  The implemented tracker link binds existing issues to exact Change revisions and
  optional publication receipts.

These limits come from [tracker behavior](../../specs/012-work-tracking/spec.md),
[plan admission](../../internal/domain/coordination.go), and
[tracker adapter reads](../../internal/tracker/provider.go).

## Worked example

The names below are synthetic examples. Objective and Step labels describe the
proposed product presentation, not new implemented API records.

```text
TRACKER CONTEXT                         CONDUCTOR

Jira Epic / Linear Project ----------> Objective: Safer sign-in
  "Safer sign-in"                         |
                                          +-- Change: Limit login requests
Story / Task / Bug / Issue ---------------+     Repository: auth-service
  "Throttle repeated login requests"     |     Design + requirements + revisions
                                          |
                                          +-- Change: Explain throttling
                                                Repository: web-app
                                                Design + requirements + revisions

                      Approved Change revisions
                                  |
                                  v
                       Plan: Implement throttling
                                  |
                       +----------+----------+
                       |                     |
                       v                     v
                 Step: API logic       Step: UI message
                       |                     |
                       +----------+----------+
                                  |
                                  v
                       Step: Integration checks

                  Human authorizes exact Plan
                                  |
                                  v
                                 Run
                                  |
                                  v
                        Results + check evidence
                                  |
                     Human reviews and authorizes
                                  |
                                  v
                     Publications: API PR + UI PR
                                  |
                       External merge/deployment
                                  |
                                  v
                      Observed delivery + runtime evidence
```

The tracker item can cover both Changes. A tracker subtask for the UI may associate
with the UI Change; no ticket is required for every agent action or check. When
one Step fails, its Result remains inspectable and dependent work stays blocked.
Neither one successful Step nor one published PR completes the whole Objective.

## Ownership, actions and status language

The tracker owns planning title/description, assignment, priority, scheduling and
status. Conductor owns its Change design and immutable revisions, approval, Plan
authorization, execution evidence and Publication decisions. Initial drafting can
use a tracker description as source; later tracker edits do not silently rewrite
an approved Change. A Change title may describe a narrower engineering scope than
its linked work item's title.

Use explicit actions: **New Change**, **Edit Design**, **Request Design Review**,
**Approve Design**, **Prepare Plan**, **Authorize Run**, **Inspect Results** and
**Publish Draft PR/MR**. The publication action includes the current separate
artifact review and authorization; it does not bypass those steps.

Show facts separately, for example:

```text
Design:       Approved, revision 3
Execution:    Not started
Checks:       Not executed
Publication:  None
Jira:         In progress
```

Do not invent a single shared "Done" status. Tracker completion, approved design,
observed terminal execution, successful declared checks, publication, merge,
deployment and supported runtime criteria have different meanings. A native
tracker status never establishes a Conductor approval or a passing check. Automatic
status writes would need explicit field ownership, mappings and separate behavior;
they are not enabled by this vocabulary proposal.

## Adoption boundary

If adopted, use this glossary for new UI copy, mockups and the
[assisted-workflow plan](assisted-workflows.md). Retire work package/package from
primary product copy in favor of Change; retire execution Task in primary copy
in favor of Step; qualify publication instead of using Delivery for the individual
publication record. Delivery can remain the name of the broader phase containing
publication and subsequent provider observations.

Keep existing JSON keys, endpoints, IDs, database names and immutable digests until
a separately reviewed compatible migration is needed. Document legacy-to-product
mapping in developer references. Existing records and approval semantics remain
unchanged by a label choice.

Adoption checks: one name per visible concept; explicit tracker association; no
required Objective for a small Change; no manufactured tracker hierarchy; no task
or project ambiguity; no assumed cross-repository ownership change; and no status
that presents missing evidence as success. The proposed Objective view and richer
tracker discovery require working implementation and separate qualification before
being described as available.
