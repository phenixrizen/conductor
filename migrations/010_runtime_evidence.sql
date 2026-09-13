-- Runtime reads have their own authority, retained facts and Temporal dispatch.
-- They do not rewrite publication receipts or authorize deployment.
CREATE TABLE runtime_integrations (
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 environment text NOT NULL,
 configuration jsonb NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 PRIMARY KEY(repository_id,environment)
);
CREATE TABLE runtime_evidence (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 binding text NOT NULL UNIQUE CHECK(binding ~ '^[0-9a-f]{32}$'),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 requester_id text NOT NULL REFERENCES access_principals(id),
 delivery_id text NOT NULL REFERENCES repository_deliveries(id),
 idempotency_key text NOT NULL,
 input jsonb NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 target jsonb NOT NULL,
 deployment jsonb NOT NULL,
 criteria jsonb NOT NULL,
 credential_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(repository_id,requester_id,idempotency_key)
);
CREATE INDEX runtime_evidence_discovery ON runtime_evidence(repository_id,created_at DESC,id DESC);
CREATE TABLE runtime_receipts (
 evidence_id text PRIMARY KEY REFERENCES runtime_evidence(id),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 receipt jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE runtime_audit_events (
 sequence bigserial PRIMARY KEY,
 evidence_id text REFERENCES runtime_evidence(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 actor text NOT NULL,
 event_type text NOT NULL,
 data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE runtime_outbox (
 id bigserial PRIMARY KEY,
 evidence_id text NOT NULL UNIQUE REFERENCES runtime_evidence(id),
 lease_token text,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 delivered_at timestamptz,
 first_attempt_at timestamptz,
 unresolved_at timestamptz,
 failure_code text,
 attempts integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX runtime_outbox_pending ON runtime_outbox(next_attempt_at,id) WHERE delivered_at IS NULL;
CREATE TABLE runtime_dispatch_bindings (
 outbox_id bigint PRIMARY KEY REFERENCES runtime_outbox(id),
 target jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE runtime_execution_observations (
 outbox_id bigint PRIMARY KEY REFERENCES runtime_outbox(id),
 namespace text NOT NULL,
 workflow_id text NOT NULL,
 temporal_run_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('running','completed','cancelled','failed','timed_out','terminated','unavailable','unresolved')),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER runtime_request_immutable BEFORE UPDATE OR DELETE ON runtime_evidence FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER runtime_receipt_immutable BEFORE UPDATE OR DELETE ON runtime_receipts FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER runtime_audit_immutable BEFORE UPDATE OR DELETE ON runtime_audit_events FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER runtime_binding_immutable BEFORE UPDATE OR DELETE ON runtime_dispatch_bindings FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
