-- Execution/publication authority is separate from design-review capability.
CREATE TABLE execution_grants (
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 principal_id text NOT NULL REFERENCES access_principals(id),
 can_execute boolean NOT NULL DEFAULT false,
 can_publish boolean NOT NULL DEFAULT false,
 PRIMARY KEY(repository_id,principal_id),
 FOREIGN KEY(repository_id,principal_id) REFERENCES repository_grants(repository_id,principal_id)
);

CREATE TABLE coordination_runs (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 binding text NOT NULL CHECK(binding ~ '^[0-9a-f]{32}$'),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 proposer_id text NOT NULL REFERENCES access_principals(id),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(repository_id,proposer_id,idempotency_key),
 UNIQUE(id,digest)
);
CREATE TABLE coordination_repositories (
 run_id text NOT NULL REFERENCES coordination_runs(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 PRIMARY KEY(run_id,repository_id)
);
CREATE TABLE coordination_authorizations (
 run_id text PRIMARY KEY,
 digest text NOT NULL,
 actor text NOT NULL REFERENCES access_principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(run_id,digest) REFERENCES coordination_runs(id,digest)
);
CREATE TABLE coordination_cancellations (
 run_id text PRIMARY KEY REFERENCES coordination_runs(id),
 actor text NOT NULL REFERENCES access_principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE coordination_tasks (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 run_id text NOT NULL REFERENCES coordination_runs(id),
 task_key text NOT NULL,
 UNIQUE(run_id,task_key),
 UNIQUE(id,run_id)
);
CREATE TABLE coordination_task_receipts (
 task_id text PRIMARY KEY,
 run_id text NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 outcome text NOT NULL CHECK(outcome IN ('succeeded','failed','blocked','cancelled','unresolved')),
 artifact_digest text CHECK(artifact_digest ~ '^[0-9a-f]{64}$'),
 artifact jsonb NOT NULL CHECK(octet_length(artifact::text)<=16777216),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(task_id,run_id) REFERENCES coordination_tasks(id,run_id)
);
-- Claims coordinate write admission; they do not schedule execution. Temporal
-- owns task ordering and retries. Only reconciliation of terminal execution may
-- release a claim, so an uncertain worker never looks like an available writer.
CREATE TABLE coordination_claims (
 run_id text NOT NULL REFERENCES coordination_runs(id),
 repository_id text NOT NULL REFERENCES managed_repositories(id),
 path text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 released_at timestamptz,
 PRIMARY KEY(run_id,repository_id,path)
);
CREATE INDEX coordination_claims_active ON coordination_claims(repository_id) WHERE released_at IS NULL;
CREATE TABLE coordination_outbox (
 id bigserial PRIMARY KEY,
 run_id text NOT NULL REFERENCES coordination_runs(id),
 operation text NOT NULL CHECK(operation IN ('start','cancel')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token text,
 lease_until timestamptz,
 delivered_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 UNIQUE(run_id,operation)
);
CREATE INDEX coordination_outbox_pending ON coordination_outbox(next_attempt_at,id) WHERE delivered_at IS NULL;
CREATE TABLE coordination_execution_observations (
 run_id text PRIMARY KEY REFERENCES coordination_runs(id),
 namespace text NOT NULL,
 workflow_id text NOT NULL,
 temporal_run_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('running','completed','cancelled','failed','timed_out','terminated','unavailable','unresolved')),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE coordination_dispatch_bindings (
 run_id text PRIMARY KEY REFERENCES coordination_runs(id),
 target jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE coordination_audit_events (
 sequence bigserial PRIMARY KEY,
 run_id text NOT NULL REFERENCES coordination_runs(id),
 event_type text NOT NULL,
 actor text NOT NULL,
 data jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX coordination_runs_discovery ON coordination_runs(workspace_id,created_at DESC,id DESC);

CREATE FUNCTION coordination_immutable_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'coordination facts are immutable'; END;
$$;
CREATE TRIGGER coordination_runs_immutable BEFORE UPDATE OR DELETE ON coordination_runs FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_repositories_immutable BEFORE UPDATE OR DELETE ON coordination_repositories FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_authorizations_immutable BEFORE UPDATE OR DELETE ON coordination_authorizations FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_cancellations_immutable BEFORE UPDATE OR DELETE ON coordination_cancellations FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_tasks_immutable BEFORE UPDATE OR DELETE ON coordination_tasks FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_receipts_immutable BEFORE UPDATE OR DELETE ON coordination_task_receipts FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_bindings_immutable BEFORE UPDATE OR DELETE ON coordination_dispatch_bindings FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
CREATE TRIGGER coordination_audit_immutable BEFORE UPDATE OR DELETE ON coordination_audit_events FOR EACH ROW EXECUTE FUNCTION coordination_immutable_fact();
