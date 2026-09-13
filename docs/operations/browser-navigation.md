# Browser workflow navigation

**Implemented.** Choose a workspace and managed repository once, then move between
six workflow tabs. Your identity and repository scope remain visible above the
workbench. Expand **Repository permissions and identity** to inspect the canonical
repository IDs and effective review permissions.

| Workflow | Use it to |
|---|---|
| Review | Find shared packages, inspect exact revisions, compare changes and approve a design. |
| Source & graph | Request repository context, inspect retained source and explore cross-repository relationships. |
| Agent work | Inspect proposed task plans, authorize or cancel execution, and read complete task reports and patches. |
| Delivery | Inspect implementation evidence and authorize a draft GitHub PR or GitLab MR. |
| Tracker | Link exact work to the workspace's Linear or Jira issue and inspect synchronization. |
| Runtime | Inspect deployment-specific metrics, logs, traces and comparisons with approved criteria. |

Only one workflow is visible at a time. Use Tab to reach the selected workflow
button, Left or Right to move between workflows, and Home or End to reach the
first or last one. **Go to active workflow** moves keyboard focus into its panel.
On a phone, all six buttons fit in two rows. Tabs remain available while scrolling.

The **Review perspective** selector offers Architect, QC / QA, Developer, Product
and Operations guidance. Operations focuses on deployment scope, recovery,
observability and evidence freshness. Perspectives do not select an identity,
change repository permissions, refresh evidence or grant approval. They stay local
to the current workbench and reset when its session or scope changes.

To attach source, inspect a package in **Review**, then choose **Source & graph**.
The attachment panel displays that package's exact inspected revision and digest.
An **Inspect linked collection** action opens Source & graph for that captured
collection. Return through **Review package** to inspect a newer package revision
or resolve an attachment conflict. Historical revisions cannot be attachment targets.

Changing workflows cancels browser requests and clears unsubmitted confirmations.
It does not cancel an already recorded server operation. An interrupted mutation
remains uncertain: request inputs and idempotency keys stay available for an
explicit identical retry, while interrupted approvals, authorizations and
attachments require renewed inspection. Returning to a workflow does not refresh
or replay a command. A tracker synchronization whose response was lost is shown
as an uncertain request with an explicit retry, rather than an open confirmation.

Changing workspace, repository or session clears every workflow, including hidden
inspection and retry state. A denied session clears the entire workbench before
explicit session recovery. A source-specific denial clears that workflow's private
source and captured decisions; switching away and back cannot restore them.

The signed Chromium acceptance uses isolated PostgreSQL schemas and a synthetic
OIDC issuer. It covers every workflow, exact decision payloads, uncertain retries,
keyboard and phone layouts, permission-free perspectives, interrupted responses
and access recovery. Run the browser command in the
[browser sign-in runbook](browser-sign-in.md#reproduce-acceptance).
