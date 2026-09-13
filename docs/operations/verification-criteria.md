# Link checks to inspected acceptance criteria

Add an explicit catalog to package content before submitting the revision for
independent human review. These examples are synthetic requirements, not defaults
or inferred policy:

```json
{
  "intent": "Inspect a synthetic related contract",
  "verificationCriteria": {
    "schemaVersion": 1,
    "criteria": [
      {"id": "related-contract", "description": "The synthetic application reads the related repository contract."},
      {"id": "another-criterion", "description": "A separate requirement needs its own evidence."}
    ]
  }
}
```

After inspecting the stored revision/digest, copy that exact package tuple into the
plan's package pin and the selected check's `requirements` array:

```json
{
  "id": "contract-test",
  "repositoryId": "synthetic-application",
  "argv": ["go", "test", "./contract"],
  "timeoutSeconds": 60,
  "requirements": [{
    "changeId": "synthetic-design",
    "revision": 1,
    "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "criterionId": "related-contract"
  }]
}
```

Replace the synthetic digest with the inspected server value. Include the referenced
package's repository in the task scope, using empty `writablePaths` when it is read
only. A check can inspect related repositories and explicitly reference their exact
criteria. Conductor rejects unknown IDs or mismatched package/revision/digest pins;
it cannot decide whether a prose description and command are semantically adequate.

The browser plan preview, CLI `run-preview`, terminal plan import and MCP
`conductor_propose_run` retain these links. Human execution authorization still
requires current independent approval of every pinned package and separate execute
permission on every repository. An edit requires a new plan and renewed inspection.

Inspect the run's task artifact using its exact run/task/artifact pins. The browser
and terminal show criterion support beside complete retained check output; CLI
`run-artifact`, the shared API client and MCP `conductor_get_task_artifact` return
the same `verification` envelope. `supported` means all checks declared for that
criterion in this task passed on this artifact. `not_verified` covers unlinked,
failed, unexecuted, truncated or incomplete evidence. Catalog gaps are explicit.
Another criterion's passing result supplies no missing evidence, and support does
not establish an overall requirement, approval or production outcome.

Rebuild the worker image with `scripts/build-worker-image.sh` and native design
image with `CONDUCTOR_DESIGN_BASE_IMAGE` selecting that exact worker ID before
reviewing new operator profiles. Images built before requirement support may
reject the optional input field; Conductor does not silently downgrade linked
inputs or replace an authorized image. See [worker setup](coding-workers.md) and
[native design tools](design-tools.md). Image builds preserve the existing pinned
Codex, Claude Code, Spec Kit and ADRKit versions.

Relevant acceptance uses an explicitly selected PostgreSQL database and the
existing owned Docker/Temporal/browser opt-ins:

```bash
go test -race ./internal/domain ./internal/store ./internal/execution ./internal/mcpserver
CONDUCTOR_TEST_BROWSER=1 go test -race ./tests/acceptance \
  -run 'TestBrowser(CoordinatedRuns|FailedTaskArtifact|VerificationCriterionSupport)' -count=1
CONDUCTOR_TEST_EXECUTION=1 CONDUCTOR_TEST_CODEGRAPH=1 CONDUCTOR_TEST_TEMPORAL=1 \
  go test -race ./tests/acceptance -run 'TestCoordinated(DockerTasks|TemporalDockerExecution)' -count=1
```

Set the documented immutable worker/CodeGraph image variables, browser Python and
Temporal CLI paths first. The command examples inherit
`CONDUCTOR_TEST_DATABASE_URL`; an unset value skips database qualification.
Missing dependencies after explicit opt-in fail. Transport fixtures test UI/API
inspection separately from actual isolated command execution; neither claims live
model inference, SaaS publication or production deployment.

Verified on 2026-09-13 with these rebuilt immutable local images:

- Worker: `sha256:e444df1257e8acb4273b40d7122a9db54b74a0909e0242fecd3e74e5903566f3`.
- Native design tools on that worker:
  `sha256:d7881235925c2f4296105682b84bdedce16f80f951cc8c44ea35afb841b002cd`.

Go normal/race tests against isolated PostgreSQL schemas, `go vet`, web clean
install/typecheck/build, OpenAPI and local documentation checks passed. Actual
linked Docker execution and cross-repository Temporal receipt/restart recovery
passed, as did native Spec Kit/ADRKit production and separate linked checks, the
failed ADR check, and authenticated terminal PTYs. Signed-browser acceptance
verified linked support, unlinked `not_verified`, escaped descriptions, retained
output and rejection of a substituted revision, invented check or contradictory
passing summary. Desktop/mobile screenshots were captured and inspected. These
images include the credential gateway short-response and delayed-launch fixes;
prior image IDs are not silently substituted in an authorized profile.

The complete release gate also passed for both Linear and Jira workspaces
(`TestFullReleaseCrossRepositoryWorkflow`, race enabled, 82.311 seconds). It carries
explicit catalogs and check links through actual source acquisition, native graph
indexing, stdio MCP proposal and artifact reads, Docker task dependencies, retained
Temporal restart, GitHub/GitLab publication protocol fixtures and correlated runtime
evidence. It verifies two supported criteria and two separate unlinked criteria;
tracker and runtime observations do not supply missing coding verification.
