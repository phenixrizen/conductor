# Repository publication integration research

**Research date: 2026-09-13. Implementation profiles are pinned below.**

| Provider | Tested contract | Inspected primary sources |
|---|---|---|
| GitHub public API | REST `2026-03-10`; `github-delivery/2026-03-10` | [Git references](https://docs.github.com/en/rest/git/refs?apiVersion=2026-03-10), [Git trees](https://docs.github.com/en/rest/git/trees?apiVersion=2026-03-10), [pull requests](https://docs.github.com/en/rest/pulls/pulls?apiVersion=2026-03-10) |
| GitLab public API | REST v4, 19.3; `gitlab-delivery/v4-19.3` | [commits](https://docs.gitlab.com/api/commits/), [merge requests](https://docs.gitlab.com/api/merge_requests/), [v19.3.0-ee commit service source](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/commits/create_service.rb), [multi-file service](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/files/multi_service.rb) |

The adapters use bounded standard-library HTTP, not an unpinned provider SDK. Each
call rechecks publication authority. HTTPS is fixed to the public provider host;
redirects, cookies, proxies, keep-alive command replay and caller-supplied URLs are
excluded. Controlled HTTP fixtures establish these profiles' implemented behavior;
no managed external repository was written during verification.

## Publication and retry constraints

GitHub Git objects are content-addressed. The publisher creates blobs, a tree and
an exact-parent commit before attempting **create-ref**, never update-ref. It checks
the returned tree, commit and repository identity before requesting a draft PR.
The fine-grained token needs Contents write and Pull requests write for this path;
review/check/deployment reads require the applicable read capabilities too.

GitLab's commit service treats `start_sha` as a new-branch operation. Its checked
19.3 implementation rejects an existing branch unless forced. The adapter sends
`force: false` and never updates an existing branch on retry. Base64 file actions
retain binary bytes; separate chmod actions retain executable mode. A draft MR is
created using GitLab's documented `Draft:` title prefix.

The GitLab multi-file service can transform source according to LFS attributes.
Therefore the returned commit's full root tree is recomputed from bounded root
entries before any MR creation. A transformation or truncated tree cannot silently
change the verified artifact. The publisher may leave a partial branch when that
check fails; it reports no successful publication receipt.

A deterministic branch and unique proposal marker allow reconciliation after a
lost PR/MR response. This is not a claim of globally exactly-once external writes.
Branch protection, permissions, quotas and provider-side validation can still
reject publication. Changing the target branch after inspection blocks creation;
a fresh proposal does not inherit an earlier human authorization.

## Checks, deployments and incoming events

[GitHub deployment records](https://docs.github.com/en/rest/deployments/deployments?apiVersion=2026-03-10)
name an exact commit and environment; [deployment statuses](https://docs.github.com/en/rest/deployments/statuses?apiVersion=2026-03-10)
are reports made by external deployment systems. Conductor performs GET requests
only. It reads at most 20 deployments per published/merged commit and up to 100
retained statuses per deployment. It chooses the newest observed status by creation
time and ID only when that page is complete. GitHub's documented history retention
means this is an observation of retained provider evidence, not a complete lifetime
history or independent runtime verification.

[GitLab deployments](https://docs.gitlab.com/api/deployments/) lack a documented
commit filter. Conductor scans the newest 100 deployment records, selects exact
published-head or independently observed merge commits, and re-reads matching
records by ID. Further pages remain explicitly truncated. Environment names and IDs,
provider status, revision relation, source profile and timestamps are retained.
GitLab deployment approval events are distinct from successful deployment state.
Production outcomes remain unobserved until a separate observability integration
provides that evidence; environment names do not establish production status.

[GitHub webhook verification](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries)
uses HMAC-SHA256 over the raw body. The configured GitLab profile uses the documented
[`X-Gitlab-Token` and `Idempotency-Key`](https://docs.gitlab.com/user/project/integrations/webhooks/)
headers over operator-provided HTTPS transport. This implementation does not claim
the newer GitLab `webhook-signature` profile. GitLab deployment payloads carry a
[commit URL rather than a full `sha` field](https://docs.gitlab.com/user/project/integrations/webhook_events/#deployment-events);
Conductor parses its fixed-host commit identifier and never follows that URL.

Verified events retain only identity, type and payload digest in the inbox; they
queue provider reads and cannot directly grant approval or establish lifecycle
facts. Publication observations retain that trigger provenance. Event authentication
and fixture coverage do not establish a live webhook configuration or deployment.
