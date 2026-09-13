# Feature 011 delivery plan

**Status: Implementation verified within the documented fixture and protocol bounds.** Depends on Features 007–010, including retained
full-source bundles and cumulative coding patches. Migrations retain numeric order;
007 is publication, while its runtime additionally requires source migration 008.

| Work | Current result |
|---|---|
| Official integration research | GitHub REST 2026-03-10 and GitLab v4/19.3 source inspected; see research notes |
| Exact patch reconstruction | Real Git binary/delete/executable tests pass; no checkout, hooks, scripts or credentials |
| GitHub and GitLab adapters | Controlled real HTTP fixtures pass lost acknowledgment, draft creation, exact-tree and no-replay cases |
| Shared proposal and human authorization | Live PostgreSQL tests pass isolation, revocation, audit rollback and stale approval |
| Durable dispatch and workflow | Fenced leases, target binding, missing-history handling and immutable receipt recovery implemented |
| Process recovery | Owned API and PostgreSQL process restarts retained exact content, approvals and audit history |
| Actual Temporal protocol | Owned CLI 1.8.3 / server 1.31.2 publication workflow and binding tests pass |
| Signed API and shared client | Live PostgreSQL/signed-issuer acceptance passes agent denial and forged identity controls |
| Webhook inbox | Signature/token verification, payload deduplication and transactional reconciliation implemented |
| Operator and worker processes | Configuration, publisher and separate loopback webhook listener implemented |
| Browser review | Signed-login/PostgreSQL/Chromium acceptance passes complete 4.5 MiB patch inspection, tampered digest denial, exact retries/authorization, revocation, reader controls and mobile layout |
| Live provider publication | Not performed; fixture repositories do not authorize writes to a managed external repository |
| Deployment reads | Both provider APIs retain exact commit/environment provenance; partial/unavailable histories remain explicit |
| Production outcome | Not observed; never inferred from publication, merge or deployment |

See [the specification](spec.md), [operations](../../docs/operations/repository-delivery.md)
and [integration research](../../docs/architecture/publication-integration-research.md).
