# Shared Temporal transport

**Status: Implemented.** Trusted context, execution, publication, tracker and
runtime-evidence workers share one explicit local or remote TLS connection profile.
TLS protocol and retained workflow recovery are verified locally; deployment to a
hosted Temporal tenant is not verified. ADR 0003 remains Proposed.

The operator selects `CONDUCTOR_TEMPORAL_MODE=local` or `remote-tls`. Local mode
retains literal loopback plaintext and rejects TLS settings. Remote mode requires
an explicit host/port, namespace and TLS server identity, system or selected CA
trust, TLS 1.2 or later, and optionally one matching client certificate/key pair.
There is no insecure remote mode, verification bypass, automatic transport fallback,
ambient SDK profile, arbitrary gRPC metadata, or API-key authentication profile.

All credential files are absolute, clean, bounded regular files. No path component
may be a symlink; special files, oversized inputs, malformed/expired certificates,
incomplete or mismatched key pairs and broadly readable private keys fail startup.
Errors contain fixed operator guidance without paths, certificate subjects, keys or
SDK error causes. Files are loaded once at startup and are not workflow inputs.

TLS authenticates the transport peer. Every existing actual cluster/namespace ID,
namespace state, history retention, queue and execution binding check remains in
place. A replacement cluster cannot resume a retained run merely because it has a
valid server certificate. Authorization, receipts, outbox leases and human approval
semantics are unchanged. No data migration or HTTP contract change is required.

Acceptance requires real TLS/gRPC denial checks, an actual owned Temporal workflow
and process restart over mTLS, and all worker configuration and Go regression checks.
The TLS gateway used in acceptance is a test fixture, not a shipped deployment.
See [operator setup](../../docs/operations/temporal-tls.md).
