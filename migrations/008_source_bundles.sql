-- Exact Git history/tree bundles live outside version 2 source snapshot JSON.
-- Their bytes/digest and derived index commit with the original context receipt.
CREATE TABLE context_source_bundles (
 collection_id text PRIMARY KEY REFERENCES context_receipts(collection_id),
 workspace_id text NOT NULL,
 repository_id text NOT NULL,
 receipt_digest text NOT NULL CHECK(receipt_digest ~ '^[0-9a-f]{64}$'),
 commit_oid text NOT NULL CHECK(commit_oid ~ '^[0-9a-f]{40}$'),
 tree_oid text NOT NULL CHECK(tree_oid ~ '^[0-9a-f]{40}$'),
 bundle_digest text NOT NULL CHECK(bundle_digest ~ '^[0-9a-f]{64}$'),
 bundle bytea NOT NULL CHECK(octet_length(bundle) BETWEEN 1 AND 33554432),
 artifacts jsonb NOT NULL,
 file_count integer NOT NULL CHECK(file_count >= 0),
 truncated boolean NOT NULL,
 index_data jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(repository_id,workspace_id) REFERENCES managed_repositories(id,workspace_id)
);
CREATE TRIGGER context_source_bundles_immutable BEFORE UPDATE OR DELETE ON context_source_bundles FOR EACH ROW EXECUTE FUNCTION preserve_context_fact();
