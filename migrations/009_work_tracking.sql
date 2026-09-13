-- One selected provider per workspace. Operator changes are versioned and audited;
-- incoming ticket state has no foreign key or write path to package approvals.
CREATE TABLE workspace_trackers (
    workspace_id text PRIMARY KEY REFERENCES workspaces(id),
    version bigint NOT NULL CHECK(version>0),
    config jsonb NOT NULL
);
CREATE TABLE tracker_grants (
    workspace_id text NOT NULL REFERENCES workspace_trackers(workspace_id),
    principal_id text NOT NULL REFERENCES access_principals(id),
    can_read boolean NOT NULL,
    can_sync boolean NOT NULL,
    can_resolve boolean NOT NULL,
    PRIMARY KEY(workspace_id,principal_id),
    CHECK(can_read OR (NOT can_sync AND NOT can_resolve)),
    CHECK(can_sync OR NOT can_resolve)
);
CREATE TABLE tracker_config_audit (
    sequence bigserial PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id),
    version bigint NOT NULL,
    operator text NOT NULL,
    config jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE tracker_links (
    id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
    workspace_id text NOT NULL REFERENCES workspace_trackers(workspace_id),
    creator_id text NOT NULL REFERENCES access_principals(id),
    config_version bigint NOT NULL CHECK(config_version>0),
    input jsonb NOT NULL,
    input_digest text NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
    digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
    projection jsonb NOT NULL,
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(workspace_id,creator_id,idempotency_key),
    UNIQUE(id,workspace_id)
);
CREATE TABLE tracker_link_repositories (
    link_id text NOT NULL REFERENCES tracker_links(id),
    workspace_id text NOT NULL,
    repository_id text NOT NULL,
    PRIMARY KEY(link_id,repository_id),
    FOREIGN KEY(link_id,workspace_id) REFERENCES tracker_links(id,workspace_id),
    FOREIGN KEY(repository_id,workspace_id) REFERENCES managed_repositories(id,workspace_id)
);
CREATE TABLE tracker_syncs (
    id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
    link_id text NOT NULL REFERENCES tracker_links(id),
    actor_id text NOT NULL REFERENCES access_principals(id),
    config_version bigint NOT NULL CHECK(config_version>0),
    input jsonb NOT NULL,
    input_digest text NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
    binding text NOT NULL CHECK(binding ~ '^[0-9a-f]{32}$'),
    idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(link_id,actor_id,idempotency_key),
    UNIQUE(id,link_id)
);
CREATE TABLE tracker_sync_outbox (
    sync_id text PRIMARY KEY REFERENCES tracker_syncs(id),
    target text,
    namespace text,
    run_id text,
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_token text,
    lease_until timestamptz,
    first_attempt_at timestamptz,
    delivered_at timestamptz,
    unresolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE tracker_write_attempts (
    sync_id text PRIMARY KEY REFERENCES tracker_syncs(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE tracker_observations (
    sync_id text PRIMARY KEY REFERENCES tracker_syncs(id),
    link_id text NOT NULL REFERENCES tracker_links(id),
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE tracker_observations ADD FOREIGN KEY(sync_id,link_id) REFERENCES tracker_syncs(id,link_id);
CREATE INDEX tracker_observations_latest ON tracker_observations(link_id,created_at DESC,sync_id DESC);
CREATE TABLE tracker_inbox (
    workspace_id text NOT NULL REFERENCES workspace_trackers(workspace_id),
    body_digest text NOT NULL CHECK(body_digest ~ '^[0-9a-f]{64}$'),
    issue_id text NOT NULL,
    config_version bigint NOT NULL CHECK(config_version>0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(workspace_id,body_digest)
);
CREATE TABLE tracker_audit (
    sequence bigserial PRIMARY KEY,
    link_id text NOT NULL REFERENCES tracker_links(id),
    actor_id text NOT NULL REFERENCES access_principals(id),
    event_type text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER tracker_links_immutable BEFORE UPDATE OR DELETE ON tracker_links FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_syncs_immutable BEFORE UPDATE OR DELETE ON tracker_syncs FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_observations_immutable BEFORE UPDATE OR DELETE ON tracker_observations FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_writes_immutable BEFORE UPDATE OR DELETE ON tracker_write_attempts FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_inbox_immutable BEFORE UPDATE OR DELETE ON tracker_inbox FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_audit_immutable BEFORE UPDATE OR DELETE ON tracker_audit FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER tracker_config_audit_immutable BEFORE UPDATE OR DELETE ON tracker_config_audit FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();

CREATE TRIGGER tracker_link_repositories_immutable BEFORE UPDATE OR DELETE ON tracker_link_repositories FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
