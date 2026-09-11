# Conductor

**Engineering intent, orchestrated.**

Conductor is an architect-governed work-package review system. This repository is
currently implementing Milestone 1: immutable package revisions, independent
exact-revision approval, and automatic approval invalidation after an edit.

## Documentation

- [Documentation index](docs/README.md)
- [System architecture and roadmap](docs/architecture/system.md)
- [Milestone 1 domain and sequence diagrams](docs/architecture/milestone-1.md)
- [Local development and troubleshooting](docs/operations/local-development.md)
- [Current OpenAPI contract](api/openapi.yaml)

## Local development

```sh
docker compose -f deploy/local/compose.yaml up -d
export DATABASE_URL=postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable
go run ./cmd/conductord
```

In another terminal:

```sh
go run ./cmd/conductor create --title "Replay handling" --actor developer
go run ./cmd/conductor revise --actor developer --revision 1 --file package.json CHG-...
go run ./cmd/conductor submit --actor developer --revision 1 CHG-...
go run ./cmd/conductor show --actor reviewer CHG-...
go run ./cmd/conductor approve --actor reviewer --revision 1 --digest <digest> CHG-...
```

Development identities are supplied explicitly through `X-Conductor-Actor`; this
mechanism is intentionally local-only and must not be used in production.

Package content can be supplied as a JSON object with `--file package.json` (or
`--file -` for standard input). Mutating commands require the revision the caller
inspected; a conflict never silently refreshes the command.

The [feature specification](specs/001-work-package-review/spec.md) defines normative
behavior; the [implementation plan](specs/001-work-package-review/plan.md) tracks
the refined delivery sequence. Proposed architectural decisions live under
[`docs/adr`](docs/adr/).
