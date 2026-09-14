# Guided Change authoring delivery plan

This increment branches directly from `main` as requested on 2026-09-14. It does
not depend on merging the broader proposed vocabulary document in PR #36. No ADR
is accepted by this implementation.

1. Add bounded title and intended-outcome summaries to the existing authorized
   discovery query, domain transport and OpenAPI contract. Verify Unicode limits,
   legacy values, current-revision projection, paging and access revocation against
   PostgreSQL; preserve immutable content and digest serialization.
2. Build readable browser creation, revision preview and separate review-request
   controls on the existing command API. Preserve structured/unknown fields,
   current/historical distinctions, exact input capture and interruption recovery.
3. Add an in-process terminal editor with the same fields and content-preservation
   rules. Retain an explicit advanced JSON import, readable preview, complete JSON
   inspection, fixed identity and separate save/submission controls.
4. Exercise each complete review loop with real Chromium and PTYs against the
   shared API and isolated PostgreSQL. Keep signed access, stale approval,
   independent review, lost acknowledgment and revocation regressions.
5. Run Go tests, race tests and vet with Go 1.26.8, frontend typecheck/build, OpenAPI
   validation and documentation checks. Inspect a synthetic browser screenshot.
   Record verified behavior separately from merge and deployment.

## Implementation status

Implementation is produced and locally verified for the backend summary and both
authoring interfaces. Go tests, race tests against PostgreSQL, vet, frontend
typecheck/build, OpenAPI validation and documentation checks passed. The signed
browser suite passed; focused authoring additionally covers retained-content reuse,
net-zero edits and a delayed discovery response that predates uncertain creation.
Real local/authenticated PTYs cover guided authoring, stale and lost-save recovery,
advanced import, independent approval, access revocation and collection regression.
Synthetic desktop/mobile form and saved-Change screenshots were inspected.

The browser's native Keycloak qualification and the separate full platform
Temporal/Docker/provider workflow gates were not rerun for this UI increment;
their existing qualification limits remain. No provider-assisted authoring, new
tracker hierarchy, merge, deployment or new execution authority is claimed here.

[Feature 020](../020-native-design-assistance/spec.md) extends guided authoring with
shared native-assistant section suggestions, a restricted assistance MCP profile,
and explicit requester application as an ordinary unapproved revision. It also
adopts the Switch visual specification across the web and ASCII terminal header.
Provider accounts remain native; hosted inference and broad source selection are
not implemented by this increment.
