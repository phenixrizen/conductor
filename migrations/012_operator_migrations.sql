-- The migration operator bootstraps this ledger before applying 001. Keeping its
-- definition here makes full-schema installs and existing manual installs agree.
CREATE TABLE IF NOT EXISTS conductor_migrations (
 number integer PRIMARY KEY CHECK(number>0),
 name text NOT NULL UNIQUE,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 operator text NOT NULL,
 origin text NOT NULL CHECK(origin IN ('executed','operator_baseline')),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION conductor_migration_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'migration records are immutable';
END;
$$;
CREATE TRIGGER conductor_migrations_immutable BEFORE UPDATE OR DELETE ON conductor_migrations
FOR EACH ROW EXECUTE FUNCTION conductor_migration_immutable();
