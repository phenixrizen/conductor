# Connect workers to Temporal securely

All five trusted workers use the same connection settings: `conductor-worker`,
`conductor-executor`, `conductor-publisher`, `conductor-tracker-worker` and
`conductor-runtime-worker`. The API does not connect to Temporal directly. Worker
provider credentials, execution profiles and database settings remain separate.

Choose the explicit local profile for a trusted loopback development service:

```bash
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor
```

Local mode accepts only a literal loopback IP and numeric port. The address defaults
to `127.0.0.1:7233` in this mode only. Clear any TLS settings when selecting local
mode; startup rejects them so a configuration mistake cannot silently disable TLS.

For a remote service, select TLS and the exact expected certificate identity:

```bash
export CONDUCTOR_TEMPORAL_MODE=remote-tls
export CONDUCTOR_TEMPORAL_ADDRESS=temporal.example.invalid:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor
export CONDUCTOR_TEMPORAL_SERVER_NAME=temporal.example.invalid
# Optional private CA bundle; omit to use the operating system trust store.
export CONDUCTOR_TEMPORAL_CA_FILE=/etc/conductor/temporal/ca.pem
# Optional mTLS identity; set both files or neither.
export CONDUCTOR_TEMPORAL_CLIENT_CERT_FILE=/etc/conductor/temporal/client.pem
export CONDUCTOR_TEMPORAL_CLIENT_KEY_FILE=/etc/conductor/temporal/client.key
```

These are synthetic names and paths. Install certificates through the deployment's
secret provisioning process. Public CA/certificate files must not be group/other
writable. The private key must be owned by the process user or root, have no group
or other permissions (for example `0600` or `0400`), and be readable by the worker.
Each file is at most 128 KiB and must be a regular file at an absolute clean path.
Every path component must be a real directory/file. Projected secret symlinks are
unsupported: provision a protected regular-file copy before worker startup.

CA bundles and client chains accept up to 16 currently valid PEM certificates;
a CA bundle contains only CA certificates. Keys contain exactly one unencrypted
PKCS#8, RSA or EC PEM private key. The matching leaf must permit client authentication.
System clocks must be correct. Server certificates undergo the Go TLS chain, expiry
and DNS/IP name verification; TLS 1.2 is the minimum. There is no skip-verification
flag, wildcard server-name setting or plaintext remote fallback.

TLS identity is distinct from execution identity. The worker still obtains the
actual cluster ID, registered namespace ID and at least 24 hours of workflow history
retention. Namespace names remain explicit and bounded to 128 letters, numbers,
dots, underscores or hyphens. A valid certificate alone cannot replace these facts.
The service must permit the worker's existing cluster-info, namespace-description,
workflow and task-queue operations. Unsupported identity/retention responses fail
closed rather than creating a weaker binding.

Certificates are read once at startup. Restart each worker to adopt rotation while
preserving its endpoint, namespace, database and task queue. Changing an endpoint
changes the persisted runtime target even when it reaches the same cluster; existing
work must be reconciled under its original binding. Do not recreate a namespace or
reset outbox rows to make old executions appear recoverable. Runtime settings and
PEM material stay outside repository commands, workflow payloads, observations and
logs. SDK diagnostics and connection failures use fixed safe messages.

The profile supports server TLS and optional mTLS. It does not load `TEMPORAL_*`
SDK configuration files/environment profiles, send arbitrary headers, support API
keys, rotate credentials without a restart, or claim compatibility with an untested
hosted vendor. Configure server-side client authorization and certificate revocation
in the actual Temporal deployment. Client TLS does not provision those policies.

## Verification

The pinned SDK is `go.temporal.io/sdk v1.44.1`, gRPC `v1.79.3`. Inspected source and
capabilities are recorded in [transport research](../architecture/temporal-tls.md).

```bash
go test -race ./internal/temporalconnection ./cmd/conductor-worker ./cmd/conductor-executor ./cmd/conductor-publisher ./cmd/conductor-tracker-worker ./cmd/conductor-runtime-worker
CONDUCTOR_TEST_TEMPORAL_TLS=1 \
  CONDUCTOR_TEMPORAL_CLI=/absolute/path/to/temporal \
  go test -race ./internal/temporalconnection -count=1
```

The opt-in requires the verified CLI 1.8.3 embedding server 1.31.2 and fails if it is
unavailable. It owns temporary processes, certificates, a TLS gateway and persistent
SQLite state. A real workflow completes over mTLS, the owned server restarts, and
the same run/result is recovered without replacement. CLI `start-dev` exposes no
server TLS flags, so the fixture terminates TLS before forwarding to its owned
loopback server. This proves the SDK transport and Temporal protocol boundary;
it does not establish a production TLS deployment or hosted account compatibility.
Unit/protocol tests also exercise wrong CA/name, missing or untrusted client identity,
plaintext downgrade rejection and valid-TLS cluster replacement denial.
