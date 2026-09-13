-- Credentials remain in operator-controlled files. This catalog stores only
-- references and the exact repository/profile binding authorized for collection.
CREATE TABLE context_integrations (
  repository_id text PRIMARY KEY,
  workspace_id text NOT NULL,
  version bigint NOT NULL CHECK (version > 0),
  configuration jsonb NOT NULL,
  enabled boolean NOT NULL,
  FOREIGN KEY (repository_id, workspace_id) REFERENCES managed_repositories(id, workspace_id)
);
CREATE TABLE context_collections (
  id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
  binding text NOT NULL CHECK (binding ~ '^[0-9a-f]{32}$'),
  workspace_id text NOT NULL,
  repository_id text NOT NULL,
  requester_id text NOT NULL REFERENCES access_principals(id),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  input jsonb NOT NULL,
  input_digest text NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
  integration jsonb NOT NULL,
  binding_digest text NOT NULL CHECK (binding_digest ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  cancel_requested_at timestamptz,
  UNIQUE (repository_id, requester_id, idempotency_key),
  FOREIGN KEY (repository_id, workspace_id) REFERENCES managed_repositories(id, workspace_id)
);
CREATE INDEX context_collections_scope_created ON context_collections(workspace_id,repository_id,created_at DESC,id DESC);
CREATE TABLE context_receipts (
  collection_id text PRIMARY KEY REFERENCES context_collections(id),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  snapshot jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE context_audit_events (
  sequence bigserial PRIMARY KEY,
  collection_id text NOT NULL REFERENCES context_collections(id),
  event_type text NOT NULL,
  actor text NOT NULL,
  data jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- Outbox delivery is separate from Temporal execution. Leases fence database
-- acknowledgements; they do not imply an external call happened exactly once.
CREATE TABLE context_outbox (
  collection_id text NOT NULL REFERENCES context_collections(id),
  operation text NOT NULL CHECK (operation IN ('start','cancel')),
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_token text,
  lease_until timestamptz,
  first_attempt_at timestamptz,
  delivered_at timestamptz,
  unresolved_at timestamptz,
  error_code text NOT NULL DEFAULT '',
  PRIMARY KEY (collection_id,operation)
);
CREATE INDEX context_outbox_pending ON context_outbox(available_at,collection_id,operation DESC)
  WHERE delivered_at IS NULL AND unresolved_at IS NULL;
-- Persist the actual cluster/namespace identity before the first external call.
-- Changing worker configuration cannot redirect a request whose start outcome
-- is still uncertain, including a crash before the first acknowledgment.
CREATE TABLE context_runtime_bindings (
  collection_id text PRIMARY KEY REFERENCES context_collections(id),
  target text NOT NULL CHECK (target ~ '^[0-9a-f]{64}$'),
  namespace text NOT NULL CHECK (namespace ~ '^[A-Za-z0-9._-]{1,128}$'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (collection_id,namespace)
);
CREATE TABLE context_execution_observations (
  collection_id text PRIMARY KEY REFERENCES context_collections(id),
  namespace text NOT NULL,
  workflow_id text NOT NULL,
  run_id text NOT NULL,
  state text NOT NULL CHECK (state IN ('running','completed','cancelled','failed','timed_out','terminated','unavailable','unresolved')),
  observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (collection_id,namespace) REFERENCES context_runtime_bindings(collection_id,namespace)
);
CREATE INDEX context_observations_refresh ON context_execution_observations(observed_at)
  WHERE state IN ('running','unavailable') AND run_id <> '';
CREATE FUNCTION preserve_collection_request() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - 'cancel_requested_at') IS DISTINCT FROM (to_jsonb(OLD) - 'cancel_requested_at')
     OR (OLD.cancel_requested_at IS NOT NULL AND NEW.cancel_requested_at IS DISTINCT FROM OLD.cancel_requested_at) THEN
    RAISE EXCEPTION 'collection request and cancellation facts are immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER context_collections_immutable BEFORE UPDATE ON context_collections FOR EACH ROW EXECUTE FUNCTION preserve_collection_request();
CREATE FUNCTION preserve_context_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'context receipts and audit events are append only';
END $$;
CREATE TRIGGER context_receipts_immutable BEFORE UPDATE OR DELETE ON context_receipts FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER context_audit_immutable BEFORE UPDATE OR DELETE ON context_audit_events FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER context_runtime_binding_immutable BEFORE UPDATE OR DELETE ON context_runtime_bindings FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
