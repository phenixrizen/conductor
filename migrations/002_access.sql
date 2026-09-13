CREATE TABLE access_principals (
  id text PRIMARY KEY,
  issuer text NOT NULL,
  subject text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('human', 'agent')),
  active boolean NOT NULL,
  UNIQUE (issuer, subject)
);
CREATE TABLE workspaces (
  id text PRIMARY KEY,
  name text NOT NULL
);
CREATE TABLE workspace_memberships (
  workspace_id text NOT NULL REFERENCES workspaces(id),
  principal_id text NOT NULL REFERENCES access_principals(id),
  active boolean NOT NULL,
  PRIMARY KEY (workspace_id, principal_id)
);
CREATE TABLE managed_repositories (
  id text PRIMARY KEY,
  workspace_id text NOT NULL REFERENCES workspaces(id),
  provider text NOT NULL CHECK (provider IN ('github', 'gitlab')),
  host text NOT NULL,
  provider_id text NOT NULL,
  name text NOT NULL,
  UNIQUE (workspace_id, provider, host, provider_id),
  UNIQUE (id, workspace_id)
);
CREATE TABLE repository_grants (
  repository_id text NOT NULL REFERENCES managed_repositories(id),
  principal_id text NOT NULL REFERENCES access_principals(id),
  can_read boolean NOT NULL,
  can_author boolean NOT NULL,
  can_approve boolean NOT NULL,
  CHECK (can_read OR NOT (can_author OR can_approve)),
  PRIMARY KEY (repository_id, principal_id)
);
CREATE TABLE access_audit_events (
  sequence bigserial PRIMARY KEY,
  operator text NOT NULL,
  event_type text NOT NULL,
  data jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE changes ADD COLUMN workspace_id text REFERENCES workspaces(id);
ALTER TABLE changes ADD COLUMN repository_id text;
ALTER TABLE changes ADD CONSTRAINT changes_scope_complete CHECK ((workspace_id IS NULL) = (repository_id IS NULL));
ALTER TABLE changes ADD CONSTRAINT changes_repository_workspace FOREIGN KEY (repository_id, workspace_id) REFERENCES managed_repositories(id, workspace_id);
CREATE INDEX changes_workspace_repository_created ON changes(workspace_id, repository_id, created_at DESC, id DESC);

-- Legacy packages remain unscoped. Neither migration nor later edits may silently
-- reinterpret an author-supplied repository label as authenticated ownership.
CREATE FUNCTION preserve_change_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.workspace_id, NEW.repository_id) IS DISTINCT FROM (OLD.workspace_id, OLD.repository_id) THEN
    RAISE EXCEPTION 'change ownership is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER changes_immutable_scope BEFORE UPDATE ON changes FOR EACH ROW EXECUTE FUNCTION preserve_change_scope();
CREATE FUNCTION preserve_principal_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.id, NEW.issuer, NEW.subject, NEW.kind) IS DISTINCT FROM (OLD.id, OLD.issuer, OLD.subject, OLD.kind) THEN
    RAISE EXCEPTION 'principal identity is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER principals_immutable_identity BEFORE UPDATE ON access_principals FOR EACH ROW EXECUTE FUNCTION preserve_principal_identity();
CREATE FUNCTION preserve_repository_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.id, NEW.workspace_id, NEW.provider, NEW.host, NEW.provider_id) IS DISTINCT FROM (OLD.id, OLD.workspace_id, OLD.provider, OLD.host, OLD.provider_id) THEN
    RAISE EXCEPTION 'repository identity is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER repositories_immutable_identity BEFORE UPDATE ON managed_repositories FOR EACH ROW EXECUTE FUNCTION preserve_repository_identity();
