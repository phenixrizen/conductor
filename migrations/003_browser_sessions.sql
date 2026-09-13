-- Only hashed browser bearer/state material is stored. The short-lived PKCE
-- verifier is necessary to complete the one-use authorization-code exchange.
CREATE TABLE browser_logins (
  state_hash text PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
  binding_hash text NOT NULL CHECK (binding_hash ~ '^[0-9a-f]{64}$'),
  nonce text NOT NULL CHECK (nonce ~ '^[A-Za-z0-9_-]{43}$'),
  code_verifier text NOT NULL CHECK (code_verifier ~ '^[A-Za-z0-9_-]{43}$'),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes')
);
CREATE INDEX browser_logins_expiry ON browser_logins(expires_at);

-- A session references an immutable principal identity. Active/human eligibility
-- is rechecked when a session is created and whenever it is presented.
CREATE TABLE browser_sessions (
  token_hash text PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  csrf_token text NOT NULL CHECK (csrf_token ~ '^[A-Za-z0-9_-]{43}$'),
  principal_id text NOT NULL REFERENCES access_principals(id),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at AND expires_at <= created_at + interval '1 hour')
);
CREATE INDEX browser_sessions_expiry ON browser_sessions(expires_at);
CREATE INDEX browser_sessions_principal_expiry ON browser_sessions(principal_id, expires_at);
