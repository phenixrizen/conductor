# ADR 0002: One version-checked domain command path

- Status: Proposed
- Date: 2026-09-11

## Decision

HTTP, CLI, TUI, and workers invoke the same domain service. Every mutation is
optimistically version checked and persistence locks the affected change.

## Consequences

Interfaces cannot assign lifecycle state directly. Conflicts are explicit and
additional interfaces remain behaviorally consistent.
