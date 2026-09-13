# Shared Temporal transport implementation

**Status: Implemented and verified locally.**

1. Inspect official Temporal SDK v1.44.1 transport source and current Go client docs;
   retain the pinned SDK, gRPC v1.79.3 and CLI 1.8.3 / server 1.31.2.
2. Add one strict operator configuration loader with bounded PEM reads, explicit
   peer verification and matching optional mTLS identity.
3. Adopt it in every trusted worker process, retaining safe loggers, the 1 MiB
   payload ceiling and existing cluster/namespace and durable dispatch bindings.
4. Exercise invalid modes, paths, permissions, PEM, expiry and key pairing. Use
   actual TLS/gRPC handshakes to reject wrong identities and plaintext downgrade.
5. Start an owned Temporal process behind an owned mTLS gateway, complete a real
   workflow, restart its persistent process, and recover the same retained run.
6. Run normal/race Go checks with real PostgreSQL, vet, documentation checks and the
   existing owned Temporal acceptance. Record deployment limits explicitly.

No remote infrastructure is provisioned or restarted by this change. Operators
still configure Temporal server authorization, namespace retention, endpoint trust,
certificate issuance/rotation and their deployment's access policy.
