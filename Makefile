.DEFAULT_GOAL := help

# Export settings instead of interpolating them into shell commands. In
# particular, database credentials and token-file paths must not be echoed.
CONDUCTOR_LOCAL_PROJECT ?= conductor-local
CONDUCTOR_POSTGRES_PORT ?= 5432
DATABASE_URL ?= postgres://conductor:conductor@127.0.0.1:$(CONDUCTOR_POSTGRES_PORT)/conductor?sslmode=disable
CONDUCTOR_ADDR ?= 127.0.0.1:8080
CONDUCTOR_AUTH_MODE ?= local
CONDUCTOR_URL ?= http://$(CONDUCTOR_ADDR)
CONDUCTOR_API_URL ?= $(CONDUCTOR_URL)
ACTOR ?= developer
VIEW ?=
FILE ?=
CHANGE ?=
BASELINE ?= 0
OPERATOR ?= local-development
export DATABASE_URL CONDUCTOR_ADDR CONDUCTOR_AUTH_MODE CONDUCTOR_URL CONDUCTOR_API_URL
export CONDUCTOR_LOCAL_PROJECT CONDUCTOR_POSTGRES_PORT ACTOR VIEW FILE CHANGE BASELINE OPERATOR

.PHONY: help run dev serve tui db-up db-migrate db-stop db-logs web-install web-dev temporal test check web

help:
	@printf '%s\n' \
	  'Local review (run each foreground target in a separate terminal):' \
	  '  make run          Start persistent PostgreSQL, then the local API (Ctrl+C stops API)' \
	  '  make dev          Alias for run' \
	  '  make tui          Connect the TUI to the running API (q exits)' \
	  '  make web-dev      Install locked web dependencies and start the browser workbench' \
	  '' \
	  'Services:' \
	  '  make serve        Run only the API using the configured database/authentication' \
	  '  make db-up        Start local PostgreSQL and apply checked migrations' \
	  '  make db-migrate   Migrate DATABASE_URL; BASELINE requires inspected legacy history' \
	  '  make db-stop      Stop local PostgreSQL; retain its data volume' \
	  '  make db-logs      Follow local PostgreSQL logs' \
	  '  make temporal     Run the pinned local Temporal server (configured shared workflows)' \
	  '' \
	  'Checks: make test | make check | make web-install | make web' \
	  '' \
	  'TUI: ACTOR=reviewer, CHANGE=CHG-..., FILE=/path/package.json, VIEW=graphs|runs|deliveries|tracker|runtime' \
	  'Connection: CONDUCTOR_URL=http://127.0.0.1:8080 (defaults to http://CONDUCTOR_ADDR)' \
	  'Shared TUI: set CONDUCTOR_TOKEN_FILE, CONDUCTOR_WORKSPACE and CONDUCTOR_REPOSITORY_ID.' \
	  'Local actor mode supports unscoped package review; shared views require configured OIDC.' \
	  'Setup and migration limits: docs/operations/local-development.md'

# Keep the API in the foreground, so its lifetime and failures stay visible.
# serve has no database dependency: it can use an already running installation.
run: db-up
	@$(MAKE) --no-print-directory serve

dev: run

serve:
	@exec go run ./cmd/conductord

# Merely connecting must never start services, change data, or refresh identity.
# The CLI reads a configured credential once and rejects invalid/empty tokens;
# their presence must never fall back to a local actor.
tui:
	@set --; \
	  if [ -z "$${CONDUCTOR_TOKEN_FILE+x}$${CONDUCTOR_TOKEN+x}" ]; then \
	    set -- "$$@" --actor "$$ACTOR"; \
	  fi; \
	  if [ -n "$$VIEW" ]; then set -- "$$@" --view "$$VIEW"; fi; \
	  if [ -n "$$FILE" ]; then set -- "$$@" --file "$$FILE"; fi; \
	  if [ -n "$$CHANGE" ]; then set -- "$$@" -- "$$CHANGE"; fi; \
	  exec go run ./cmd/conductor tui "$$@"

db-up:
	@./scripts/start-local-db.sh

# Only this explicit operator action accepts a legacy baseline. Startup never
# guesses one from existing tables or skips recorded migration checksums.
db-migrate:
	@exec go run ./cmd/conductor-db migrate --directory migrations --operator "$$OPERATOR" --baseline "$$BASELINE"

# Piping Compose configuration supports Docker Snap and /mnt checkouts. Stop
# only the database service; never delete its volume as part of routine shutdown.
db-stop:
	@docker compose --project-name "$$CONDUCTOR_LOCAL_PROJECT" --file - stop postgres < deploy/local/compose.yaml

db-logs:
	@docker compose --project-name "$$CONDUCTOR_LOCAL_PROJECT" --file - logs --follow --tail 100 postgres < deploy/local/compose.yaml

web-install:
	npm --prefix apps/web ci

web-dev: web-install
	@exec npm --prefix apps/web run dev -- --host 127.0.0.1

temporal:
	@exec ./scripts/start-local-temporal.sh

test:
	go test ./...
check:
	go test -race ./...
	go vet ./...
web:
	npm --prefix apps/web run typecheck
	npm --prefix apps/web run build
