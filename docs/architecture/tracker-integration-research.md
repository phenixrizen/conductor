# Tracker API profiles and evidence

**Reviewed 2026-09-13.** Conductor calls the documented HTTP APIs directly using the
Go standard library; no unpinned tracker SDK is installed.

Linear GraphQL profile `linear-graphql/23f11eb41ef63ba219ec582911079c19d1abbf62`
uses the official [schema source](https://github.com/linear/linear/blob/23f11eb41ef63ba219ec582911079c19d1abbf62/packages/sdk/src/schema.graphql).
Its inspected SHA-256 is
`a40587082cc4adc62d0daf7458c5a04db25ac3062cedce92c727fd08b5c4f0f5`.
The [authentication/query guide](https://linear.app/developers/graphql) documents
API keys and OAuth bearer tokens, the fixed GraphQL endpoint, stable issue IDs and
GraphQL errors inside successful HTTP responses. The adapter checks organization,
team and issue IDs before exposing fields; GraphQL partial errors fail closed.

The [attachment contract](https://linear.app/developers/attachments) documents URL
uniqueness within an issue and updates when the same URL is created again. Conductor
uses `attachmentsForURL` with a bounded page and `attachmentCreate`, publishing
only URL/title/subtitle. It never modifies issue planning fields. A truncated
attachment page cannot establish absence. No atomic conditional card update is
claimed.

The [webhook contract](https://linear.app/developers/webhooks) documents raw-body
HMAC-SHA256 and a signed millisecond timestamp. The receiver requires the matching
organization, an Issue event, a valid signature and an age within one minute
(allowing one minute of future clock skew). The body digest provides durable
replay deduplication; event contents only request an authoritative refresh.

Jira profile `jira-cloud-rest/v3-2026-09-13` pins the official
[REST v3 OpenAPI source](https://dac-static.atlassian.com/cloud/jira/platform/swagger-v3.v3.json)
reviewed with version `1001.0.0-SNAPSHOT-3d120dbfd2d826e450656947143e5b8779387242`
and SHA-256 `44a651e69946782fcb943bab316bee98f5d831fb69aa9b3b0d566dade3b66ba8`.
The [v3 overview](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/)
describes Jira Cloud and structured ADF descriptions; Conductor retains those as
untrusted JSON. This profile supports `*.atlassian.net` Cloud sites with
[email/API-token basic authentication](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/).
It does not claim Jira Data Center, Forge, Connect or OAuth tenant compatibility.

The [remote issue link API](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-remote-links/)
allows reads by `globalId` and create-or-update by that identity. Conductor owns one
card whose global ID is the immutable Conductor relationship URL. Jira Browse
Projects and Link Issues permissions, including applicable issue security, remain
necessary. Unspecified remote-link fields may be cleared by an update, so only the
dedicated Conductor card is written.

Jira's [secure admin webhooks](https://developer.atlassian.com/cloud/jira/platform/webhooks/#secure-admin-webhooks)
support `X-Hub-Signature: sha256=…`. Conductor implements that profile, not OAuth-app
JWT callbacks. It requires a signed timestamp within 24 hours and allows one minute
of future skew; retries deduplicate by body digest. Unsupported signature methods
fail closed. Webhook installation and secret rotation remain explicit operator
steps; the worker never creates provider hooks implicitly.

Controlled fixtures exercise real HTTP requests for both adapters, dropped write
responses, conflicting cards and raw signed callbacks. Live PostgreSQL and owned
Temporal restart tests verify durable receipts and absence of source/credentials
from history. No live Linear or Jira credentials were available for these checks;
fixtures establish the implemented protocol boundary, not live tenant compatibility.
