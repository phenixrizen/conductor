-- Native assistants propose immutable text; the requesting human alone applies
-- selected sections through the ordinary revision authority. No execution state.
CREATE TABLE design_assistance_requests (
  id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
  workspace_id text NOT NULL,
  repository_id text NOT NULL,
  requester_id text NOT NULL REFERENCES access_principals(id),
  change_id text NOT NULL,
  revision bigint NOT NULL,
  revision_digest text NOT NULL,
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  input json NOT NULL,
  base json NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (repository_id, workspace_id) REFERENCES managed_repositories(id, workspace_id),
  FOREIGN KEY (change_id, revision, revision_digest) REFERENCES work_package_revisions(change_id, revision, digest)
);
CREATE INDEX design_assistance_scope_created ON design_assistance_requests(workspace_id, repository_id, created_at DESC, id DESC);
CREATE INDEX design_assistance_change_created ON design_assistance_requests(change_id, created_at DESC, id DESC);
CREATE TABLE design_assistance_suggestions (
  request_id text PRIMARY KEY REFERENCES design_assistance_requests(id),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  agent_id text NOT NULL REFERENCES access_principals(id),
  fact json NOT NULL
);
CREATE TABLE design_assistance_applications (
  request_id text PRIMARY KEY REFERENCES design_assistance_requests(id),
  digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
  applied_by text NOT NULL REFERENCES access_principals(id),
  change_id text NOT NULL,
  revision bigint NOT NULL,
  revision_digest text NOT NULL,
  fact json NOT NULL,
  FOREIGN KEY (change_id, revision, revision_digest) REFERENCES work_package_revisions(change_id, revision, digest)
);
CREATE TABLE design_assistance_commands (
  repository_id text NOT NULL REFERENCES managed_repositories(id),
  principal_id text NOT NULL REFERENCES access_principals(id),
  operation text NOT NULL CHECK (operation IN ('request','suggestion','application')),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  input_digest text NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
  request_id text NOT NULL REFERENCES design_assistance_requests(id),
  PRIMARY KEY (repository_id, principal_id, operation, idempotency_key)
);
CREATE FUNCTION preserve_design_assistance_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'design assistance facts are append only';
END $$;
CREATE TRIGGER design_assistance_requests_immutable BEFORE UPDATE OR DELETE ON design_assistance_requests FOR EACH ROW EXECUTE FUNCTION preserve_design_assistance_fact();
CREATE TRIGGER design_assistance_suggestions_immutable BEFORE UPDATE OR DELETE ON design_assistance_suggestions FOR EACH ROW EXECUTE FUNCTION preserve_design_assistance_fact();
CREATE TRIGGER design_assistance_applications_immutable BEFORE UPDATE OR DELETE ON design_assistance_applications FOR EACH ROW EXECUTE FUNCTION preserve_design_assistance_fact();
CREATE TRIGGER design_assistance_commands_immutable BEFORE UPDATE OR DELETE ON design_assistance_commands FOR EACH ROW EXECUTE FUNCTION preserve_design_assistance_fact();
