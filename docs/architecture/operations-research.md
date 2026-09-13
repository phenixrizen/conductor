# Operations profiles and inspected upstream behavior

Inspected on 2026-09-13. These versions describe actual tooling used for this lane;
service examples do not certify an operator's network, certificate or account.

- PostgreSQL 17.11, pinned image
  `postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73`.
  Official [logical backup documentation](https://www.postgresql.org/docs/17/backup-dump.html)
  and [pg_restore reference](https://www.postgresql.org/docs/17/app-pgrestore.html)
  establish consistent logical snapshots, custom archives and atomic restoration.
  Role ownership and grants are deliberately not restored by this operator profile.
  [libpq service files](https://www.postgresql.org/docs/17/libpq-pgservice.html)
  avoid credentials in subprocess arguments; the generated file is private and removed.
- Go 1.26.8 and Node.js 22.14.0 are the tested deterministic build toolchains.
  Existing Go/npm dependency locks remain unchanged. This is a Linux x86_64 archive;
  native worker images retain their independent pinned build profiles.
- [systemd v255 execution source documentation](https://github.com/systemd/systemd/blob/v255/man/systemd.exec.xml)
  was inspected for environment files, separate identities, filesystem restrictions
  and `NoNewPrivileges`. Service templates require systemd and pre-provisioned users;
  syntax checking does not create users, install services or prove isolation from a
  privileged Docker daemon.
- nginx 1.30.4 uses the inspected official
  [HTTPS configuration](https://nginx.org/en/docs/http/configuring_https_servers.html)
  and [TLS module](https://nginx.org/en/docs/http/ngx_http_ssl_module.html) directives.
  The example is checked with synthetic certificates in
  `nginx:1.30.4-alpine@sha256:dc5069ad14f19660b141b21236140b91656bf89bbc3e2417c70ae650cd66104c`.
  The check owns its temporary container, has no network access and starts no host
  service. It does not qualify public certificates, DNS or a production proxy.
- GitHub Actions source was inspected at
  [checkout v6.0.2](https://github.com/actions/checkout/tree/v6.0.2),
  [setup-go v6.3.0](https://github.com/actions/setup-go/tree/v6.3.0),
  [setup-node v6.0.0](https://github.com/actions/setup-node/tree/v6.0.0) and
  [upload-artifact v6.0.0](https://github.com/actions/upload-artifact/tree/v6.0.0).
  The workflow pins their full commit IDs, uses read-only repository permission,
  disables persisted checkout credentials and never publishes a release or deploys.
  YAML parsing/local checks are distinct from a hosted runner result.
- CI installs the existing verified Temporal CLI 1.8.3 archive using release asset
  digest `6f0afac1e9ddea71f480c43a49f5db5167a244c21db923707f069a79bcabdfea`,
  [official release](https://github.com/temporalio/cli/releases/tag/v1.8.3).
  It pins Playwright 1.62.0, OpenAPI validator 0.9.0 and uv 0.11.25 for the existing
  acceptance and native design-tool builds. Provider/model credentials remain external.

See the [operator contract](../../specs/016-operations/spec.md) and
[runbook](../operations/release.md) for recovery boundaries and evidence limits.
