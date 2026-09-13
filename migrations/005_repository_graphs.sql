-- Graphs are immutable projections of explicit, trusted context receipts. Source
-- rows make all-repository authorization possible before pagination or counts.
CREATE TABLE repository_graphs (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'),
 workspace_id text NOT NULL REFERENCES workspaces(id),
 creator_id text NOT NULL REFERENCES access_principals(id),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 input_digest text NOT NULL CHECK(input_digest ~ '^[0-9a-f]{64}$'),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(workspace_id,creator_id,idempotency_key),
 UNIQUE(id,workspace_id)
);
CREATE TABLE repository_graph_sources (
 graph_id text NOT NULL,
 workspace_id text NOT NULL,
 repository_id text NOT NULL,
 collection_id text NOT NULL REFERENCES context_receipts(collection_id),
 receipt_digest text NOT NULL CHECK(receipt_digest ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(graph_id,repository_id),
 FOREIGN KEY(graph_id,workspace_id) REFERENCES repository_graphs(id,workspace_id),
 FOREIGN KEY(repository_id,workspace_id) REFERENCES managed_repositories(id,workspace_id)
);
CREATE INDEX repository_graphs_page ON repository_graphs(workspace_id,created_at DESC,id DESC);
CREATE INDEX repository_graphs_repository ON repository_graph_sources(repository_id,graph_id);
CREATE TABLE repository_graph_audit_events (
 sequence bigserial PRIMARY KEY,
 graph_id text NOT NULL REFERENCES repository_graphs(id),
 actor text NOT NULL REFERENCES access_principals(id),
 event_type text NOT NULL CHECK(event_type='graph.created'),
 data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER repository_graphs_immutable BEFORE UPDATE OR DELETE ON repository_graphs FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER repository_graph_sources_immutable BEFORE UPDATE OR DELETE ON repository_graph_sources FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
CREATE TRIGGER repository_graph_audit_immutable BEFORE UPDATE OR DELETE ON repository_graph_audit_events FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
-- An optional trusted activity records CodeGraph output in the same transaction
-- as its receipt. Existing receipt bytes/digests and activity replay stay intact.
CREATE TABLE context_codegraph_indexes (
 collection_id text PRIMARY KEY REFERENCES context_receipts(collection_id),
 receipt_digest text NOT NULL CHECK(receipt_digest ~ '^[0-9a-f]{64}$'),
 index_digest text NOT NULL CHECK(index_digest ~ '^[0-9a-f]{64}$'),
 index_data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER context_codegraph_immutable BEFORE UPDATE OR DELETE ON context_codegraph_indexes FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
