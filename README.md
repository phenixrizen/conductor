# Conductor

**Engineering intent, orchestrated.**

Conductor is an architect-governed work-package review system. This repository is
currently implementing Milestone 1: immutable package revisions, independent
exact-revision approval, and automatic approval invalidation after an edit.

## Local development

```sh
docker compose -f deploy/local/compose.yaml up -d
export DATABASE_URL=postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable
go run ./cmd/conductord
```

In another terminal:

```sh
go run ./cmd/conductor create --title "Replay handling" --author developer
go run ./cmd/conductor submit --actor developer --revision 1 CHG-...
go run ./cmd/conductor show --actor reviewer CHG-...
go run ./cmd/conductor approve --actor reviewer --revision 1 --digest <digest> CHG-...
```

Development identities are supplied explicitly through `X-Conductor-Actor`; this
mechanism is intentionally local-only and must not be used in production.

See [`specs/001-work-package-review/spec.md`](specs/001-work-package-review/spec.md)
and [`specs/001-work-package-review/plan.md`](specs/001-work-package-review/plan.md).
