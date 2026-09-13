# Temporal transport profile research

**Implemented, verified locally on 2026-09-13.** All trusted worker processes use
`internal/temporalconnection`. Temporal remains the execution sequencer; this adds
connection security and no domain state machine or transport-derived approval.

Official sources inspected:

- [Temporal Go client documentation](https://docs.temporal.io/develop/go/client/temporal-client)
  documents `client.Options`, TLS and mTLS. Conductor supplies these options directly
  and does not invoke ambient `contrib/envconfig` configuration loading.
- [Go SDK v1.44.1 connection options](https://github.com/temporalio/sdk-go/blob/v1.44.1/internal/client.go)
  expose `ConnectionOptions.TLS`, explicit `TLSDisabled` and bounded payload size.
  TLS and `TLSDisabled` are mutually exclusive. Optional SDK API-key and dynamic
  credential features are outside Conductor's selected transport profile.
- [Go SDK v1.44.1 gRPC dialer](https://github.com/temporalio/sdk-go/blob/v1.44.1/internal/grpc_dialer.go)
  supplies `credentials.NewTLS` for a non-nil TLS configuration and insecure
  credentials otherwise. Conductor always supplies verified TLS for `remote-tls`
  and does not expose arbitrary SDK dial options that could replace this transport.
- [gRPC Go v1.79.3 TLS credentials](https://github.com/grpc/grpc-go/blob/v1.79.3/credentials/tls.go)
  use the Go TLS handshake and configured server name for peer verification.
- [Temporal CLI v1.8.3](https://github.com/temporalio/cli/tree/v1.8.3)
  embeds server 1.31.2. Its actual `server start-dev --help` offers no server TLS
  certificate flags. Acceptance therefore uses an explicitly owned TLS gateway.

Existing module pins remain unchanged: SDK checksum
`h1:Mt2OZLZpqkzDIdg9YyQzO0Rb/HqCDnnqHlIAGAJ5gqM=` and gRPC checksum
`h1:sybAEdRIEtvcD68Gx7dmnwjZKlyfuc61Dyo9pGXXkKE=` are retained in `go.sum`.

Trust has two separate checks. TLS verifies the configured service identity before
any Temporal application RPC. Existing runtime binding then verifies actual cluster,
namespace, retention and execution identity. Tests prove that a valid TLS peer with
a replacement cluster ID still fails the retained binding check. Changing transport
does not weaken repository grants, human approval, outbox fencing or source secrecy.

The shared loader consumes protected files once at startup. Certificate rotation
requires process restart, and server-side revocation remains the operator's policy.
No OCSP/CRL client polling, API-key authentication, vendor-specific headers or
production deployment are implied. See [setup and acceptance](../operations/temporal-tls.md)
and [feature contract](../../specs/015-temporal-tls/spec.md).
