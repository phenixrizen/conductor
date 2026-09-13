-- Provider publication is an independently authorized exact-artifact operation.
-- Request/auth/audit/outbox commits are transactional; Temporal sequences I/O.
CREATE TABLE delivery_integrations (
 repository_id text PRIMARY KEY REFERENCES managed_repositories(id),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 profile text NOT NULL CHECK(profile IN ('github-delivery/2026-03-10','gitlab-delivery/v4-19.3')),
 locator text NOT NULL,
 credential_id text NOT NULL,
 base_branches jsonb NOT NULL CHECK(jsonb_typeof(base_branches)='array'),
 enabled boolean NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0)
);
CREATE TABLE repository_deliveries (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 binding text NOT NULL CHECK(binding ~ '^[0-9a-f]{32}$'),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 proposer_id text NOT NULL REFERENCES access_principals(id),
 run_id text NOT NULL REFERENCES coordination_runs(id),
 task_id text NOT NULL REFERENCES coordination_tasks(id),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 input jsonb NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 target jsonb NOT NULL,
 credential_id text NOT NULL,
 base_commit text NOT NULL CHECK(base_commit ~ '^[0-9a-f]{40}$'),
 base_tree text NOT NULL CHECK(base_tree ~ '^[0-9a-f]{40}$'),
 result_tree text NOT NULL CHECK(result_tree ~ '^[0-9a-f]{40}$'),
 patch_digest text NOT NULL CHECK(patch_digest ~ '^[0-9a-f]{64}$'),
 branch text NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(repository_id,proposer_id,idempotency_key),
 UNIQUE(id,digest)
);
CREATE TABLE delivery_authorizations (
 delivery_id text PRIMARY KEY,
 digest text NOT NULL,
 actor text NOT NULL REFERENCES access_principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(delivery_id,digest) REFERENCES repository_deliveries(id,digest)
);
CREATE TABLE delivery_receipts (
 delivery_id text PRIMARY KEY REFERENCES repository_deliveries(id),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 observation jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE delivery_observations (
 sequence bigserial PRIMARY KEY,
 delivery_id text NOT NULL REFERENCES repository_deliveries(id),
 observation jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX delivery_observations_latest ON delivery_observations(delivery_id,sequence DESC);
CREATE TABLE delivery_audit_events (
 sequence bigserial PRIMARY KEY,
 delivery_id text REFERENCES repository_deliveries(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 event_type text NOT NULL,
 actor text NOT NULL,
 data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE delivery_outbox (
 id bigserial PRIMARY KEY,
 delivery_id text NOT NULL REFERENCES repository_deliveries(id),
 operation text NOT NULL CHECK(operation IN ('publish','reconcile')),
 deduplication_key text NOT NULL,
 binding text NOT NULL CHECK(binding ~ '^[0-9a-f]{32}$'),
 lease_token text,
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 delivered_at timestamptz,
 first_attempt_at timestamptz,
 unresolved_at timestamptz,
 failure_code text,
 attempts integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(delivery_id,operation,deduplication_key)
);
CREATE INDEX delivery_outbox_pending ON delivery_outbox(next_attempt_at,id) WHERE delivered_at IS NULL;
CREATE TABLE delivery_dispatch_bindings (
 outbox_id bigint PRIMARY KEY REFERENCES delivery_outbox(id),
 target jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE delivery_execution_observations (
 outbox_id bigint PRIMARY KEY REFERENCES delivery_outbox(id),
 namespace text NOT NULL,
 workflow_id text NOT NULL,
 temporal_run_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('running','completed','cancelled','failed','timed_out','terminated','unavailable','unresolved')),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE delivery_event_inbox (
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 delivery_key text NOT NULL,
 payload_digest text NOT NULL CHECK(payload_digest ~ '^[0-9a-f]{64}$'),
 event_type text NOT NULL,
 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(repository_id,delivery_key)
);
CREATE INDEX repository_deliveries_discovery ON repository_deliveries(repository_id,created_at DESC,id DESC);
CREATE FUNCTION delivery_immutable_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'delivery facts are immutable'; END;
$$;
CREATE TRIGGER repository_deliveries_immutable BEFORE UPDATE OR DELETE ON repository_deliveries FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_authorizations_immutable BEFORE UPDATE OR DELETE ON delivery_authorizations FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_receipts_immutable BEFORE UPDATE OR DELETE ON delivery_receipts FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_observations_immutable BEFORE UPDATE OR DELETE ON delivery_observations FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_audit_immutable BEFORE UPDATE OR DELETE ON delivery_audit_events FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_binding_immutable BEFORE UPDATE OR DELETE ON delivery_dispatch_bindings FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE TRIGGER delivery_inbox_immutable BEFORE UPDATE OR DELETE ON delivery_event_inbox FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
CREATE UNIQUE INDEX delivery_outbox_binding ON delivery_outbox(binding);
CREATE TABLE delivery_operation_receipts (
 outbox_id bigint PRIMARY KEY REFERENCES delivery_outbox(id),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER delivery_operation_receipts_immutable BEFORE UPDATE OR DELETE ON delivery_operation_receipts FOR EACH ROW EXECUTE FUNCTION delivery_immutable_fact();
