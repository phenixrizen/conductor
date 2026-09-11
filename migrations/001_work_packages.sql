CREATE TABLE changes (
  id text PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE work_package_revisions (
  change_id text NOT NULL REFERENCES changes(id),
  revision bigint NOT NULL CHECK (revision > 0),
  schema_version integer NOT NULL CHECK (schema_version > 0),
  digest text NOT NULL CHECK (length(digest) = 64),
  content jsonb NOT NULL,
  author text NOT NULL,
  created_at timestamptz NOT NULL,
  submitted_at timestamptz,
  PRIMARY KEY (change_id, revision),
  UNIQUE (change_id, revision, digest),
  UNIQUE (change_id, digest)
);
CREATE TABLE approvals (
  change_id text NOT NULL,
  revision bigint NOT NULL,
  digest text NOT NULL,
  reviewer text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (change_id, revision, reviewer),
  FOREIGN KEY (change_id, revision, digest) REFERENCES work_package_revisions(change_id, revision, digest)
);
CREATE TABLE audit_events (
  sequence bigserial PRIMARY KEY,
  change_id text NOT NULL REFERENCES changes(id),
  event_type text NOT NULL,
  actor text NOT NULL,
  revision bigint NOT NULL,
  data jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL
);
CREATE INDEX audit_events_change_sequence ON audit_events(change_id, sequence);
