# Native Spec Kit and ADRKit tools

Conductor can create core design artifacts, propose ADRs and execute native checks
inside the existing isolated coding workflow. Reports retain source and tool
identity. A passing artifact-prerequisite or schema check does not approve the
design; package approval still belongs to the configured human review process.

First build the ordinary worker image as described in
[coding workers](coding-workers.md), inspect its digest, and build the design image:

```bash
export CONDUCTOR_DESIGN_BASE_IMAGE=$(docker image inspect conductor-worker:local --format '{{.Id}}')
./scripts/build-design-tools-image.sh
export CONDUCTOR_TEST_DESIGN_IMAGE=$(docker image inspect conductor-design-tools:1.0.6-0.13.0 --format '{{.Id}}')
CONDUCTOR_TEST_DESIGN_EXECUTION=1 go test -race ./internal/execution -run DockerNativeDesign -count=1 -v
```

Use `sg docker -c '…'` if this shell has not inherited Docker group membership.
The build needs GitHub/npm/Python-package access, Go, uv, curl and Docker. It pipes
an isolated build context for Docker Snap, verifies the upstream archive and wheel
hashes and uses reviewed dependency locks. It creates and removes an owned local
build tag because BuildKit cannot use bare local image IDs in `FROM`; runtime
configuration still requires the final immutable image digest. The final image
also pins installed OS dependencies; package mirror rebuilds are not a claim of
historically identical image bytes.

Configure a workspace execution profile with the built image through the existing
trusted access administrator and [execution profile catalog](coordinated-execution.md).
For a synthetic ADR proposal, its `profile` field can be:

```json
{"adapter":"command/v1","command":["conductor-design-tools","adr-new","--title","Synthetic architecture proposal","--dir","docs/adr"]}
```

The coordination plan must capture the returned profile digest and exact image,
include approved package/graph/source inputs, and explicitly allow the `docs/adr`
write prefix. Add independent `checks` such as:

```json
{"id":"adr-schema","repositoryId":"application","argv":["conductor-design-tools","adr-lint","--dir","docs/adr"],"timeoutSeconds":30}
```

The same image retains the pinned coding-agent binaries. A native agent profile
can create implementation patches and run these commands as separately declared
checks; no agent/provider/publication credential reaches the verification stage.
Do not change an already inspected plan or substitute another image during human
authorization.

| Command inside the image | Meaning |
| --- | --- |
| `conductor-design-tools spec-init` | Create a fresh native generic Spec Kit scaffold, including core prompt artifacts; conflicting existing files block the entire write |
| `conductor-design-tools spec-template --feature specs/001-example --kind spec` | Create a new pinned core spec template; `plan`, `tasks` and `checklist` are also supported |
| `conductor-design-tools spec-check --feature specs/001-example` | Run native existence checks for spec, plan and tasks on a staged copy |
| `conductor-design-tools adr-new --title "Synthetic proposal" --dir docs/adr` | Create exactly one new Proposed ADR using the native CLI |
| `conductor-design-tools adr-lint --dir docs/adr` | Validate the selected ADR corpus |
| `conductor-design-tools adr-check --dir docs/adr src/example.go` | Report applicable decisions and validate any selected changed ADRs |
| `conductor-design-tools adr-explain --dir docs/adr src/example.go` | Explain which source-recorded decisions apply to one path |
| `conductor-design-tools adr-graph --dir docs/adr` | Emit the native decision graph as JSON |

Flags precede positional paths. Features are explicit `specs/<feature>` directories;
paths must be clean relative paths. The wrapper rejects an empty `adr-check`
selection because the native CLI would otherwise return a successful no-op.
A lint result covering zero ADRs is unavailable evidence, even if native exit is 0.
Templates retain placeholders for the engineering workflow to fill. No command
accepts an actor, approval grant, accepted ADR status or arbitrary executable.

Each JSON report records native identity, check scope, input/output digests, source
files and created artifacts. The enclosing execution result supplies exact Git
source/result trees, the operator profile/image, declared-check output and cleanup
facts. Existing API/client/MCP execution-artifact reads share those facts under
current repository permissions; source can later be collected into a new immutable
graph snapshot. Failed native checks stay failed, and truncation/unavailability
cannot be rendered as passing evidence.

Limits: 256 retained files, 64 KiB per file, 4 MiB text, 1,024 traversed entries,
128 selected source paths, 30 seconds per native subprocess and 1 MiB retained
output. Symbolic links and special/binary files are rejected. No live provider
credential is needed for these offline commands. See
[upstream research](../architecture/design-tool-research.md) for exact pins and
unsupported extension/semantic-analysis claims.
