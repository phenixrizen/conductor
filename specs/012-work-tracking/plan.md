# Work tracking delivery plan

1. Pin official Linear schema and Jira Cloud REST profile; document actual read,
   idempotent-card and webhook capabilities without inventing ticket authority.
2. Add migration 009 for versioned operator configuration, permissions, immutable
   links/sync requests/results, signed inbox, fenced outbox and audit facts.
3. Implement authenticated shared API/client and MCP commands. Every relationship
   includes exact package/publication identity and all-repository authorization.
4. Run provider I/O in Temporal activities with credential binding, one retained
   write attempt, authoritative reconciliation and conservative missing history.
5. Verify controlled HTTP providers, signed identities, real PostgreSQL rollback/
   revocation, actual MCP transport and owned Temporal process recovery. Record
   live tenant credentials as an external verification prerequisite.
6. Integrate browser/terminal controls and full-release delivery acceptance on the
   root stack, preserving the same captured-input contracts.

Items 1–5 have implementation and passing controlled-provider, real PostgreSQL,
compiled MCP and owned Temporal acceptance. Full Go normal/race tests, vet, OpenAPI,
local documentation validation, web typecheck and production build also pass.
Final release verification includes the parent stack's interfaces and live service
evidence. Ticket creation and assignment/priority/status mutations remain explicit
unsupported capabilities under the accepted existing-ticket field-ownership model.
