# Coding workers

Conductor's coding worker produces patches, then runs declared checks against the
exact changed code in a separate container. It cannot approve a design or publish
code. A trusted activity supplies authorized task inputs; a trusted publisher later
handles repository writes with separate credentials.

## Build and verify the image

The supported acceptance environment is Linux with Docker Engine 29.6.1, Git, Go,
and a working Docker daemon socket. The build uses the pinned Node Debian base,
Git package, and reviewed npm lockfile under `deploy/worker/`. It installs Codex
0.154.0 and Claude Code 2.1.270. No host login/session directory is copied.

```bash
./scripts/build-worker-image.sh
export CONDUCTOR_TEST_WORKER_IMAGE=$(docker image inspect conductor-worker:local --format '{{.Id}}')
CONDUCTOR_TEST_EXECUTION=1 go test -race ./internal/execution -count=1 -v
go vet ./internal/execution ./cmd/conductor-sandbox
```

On the Snap installation, run the build and tests inside `sg docker -c '…'` when
the current shell has not inherited Docker group membership. The build script pipes
a dedicated context to Docker, so the checkout can remain outside your home.
Runtime configuration accepts the immutable image digest, never a mutable tag.
The final built digest identifies the installed OS dependencies too; rebuilding
against package mirrors is not a claim of byte-identical historical images.

Docker's early no-new-privileges option conflicts with Snap's AppArmor profile
transition in the verified environment. Conductor preserves the same irreversible
kernel control in its trusted PID 1 before reading input or spawning commands.
That goroutine stays on its OS thread so every child inherits the flag. Acceptance
checks `NoNewPrivs: 1` in an actual repository command. The default AppArmor and
seccomp profiles remain enabled; there is no unconfined or privileged fallback.

## Trusted configuration and inputs

`internal/execution.Runner` accepts an operator-owned image digest and profile.
`command/v1` runs its exact configured argument vector offline with no credential;
use it for synthetic acceptance or a deliberately configured automation program.
Codex and Claude profiles use their exact version strings. A model identifier can
be pinned by the operator; the worker does not select or change it automatically.
Claude requires an explicit `MaxBudgetUSD`. Codex exposes no currency budget here.

Native profiles require `AllowProviderNetwork` and an absolute credential-file
path. That file must be regular, not a symlink, private to its owner (0600 or
stricter), and at most 8 KiB. Use a dedicated provider API credential, never a
Conductor token, GitHub/GitLab publication token, production credential, or a user's
existing CLI authentication directory. No credentials go in command-line arguments.

The worker creates a per-task Docker network with `--internal` and
`com.docker.network.bridge.gateway_mode_ipv4=isolated`. It verifies those options,
then starts a trusted gateway sidecar. Only the gateway connects to an external
network. IP forwarding is disabled in the gateway; the producer has no host bridge
address, direct egress or DNS forwarding. Provider endpoints are fixed to OpenAI
Responses/compaction or Anthropic Messages/token counting. There is no general
HTTP proxy, CONNECT, arbitrary host, redirect, or file-upload API.

The real provider key enters the gateway over standard input. The producer receives
a random task credential that expires with that gateway. Native assistant output
is not persisted as log text because it can contain sensitive source or that
credential. Check output is retained only from the independent credential-free
verification stage. Source and patches are still protected repository data and must
be persisted only under the activity's current canonical authorization.

Task inputs contain complete, bounded Git bundles and exact commit IDs, never
caller-controlled host paths or mutable refs. Source acquisition is the trusted
activity's job. The activity must validate access for every related repository and
the approved package/graph/dependency receipts before execution, then again before
accepting the result. A worker request by itself is not proof of authorization.

For dependent tasks, supply verified maximal ancestor patches for each repository
in `Repository.Dependencies`. Their base commits must match the original bundle.
The worker verifies each result tree and merges the cumulative changes through
Git's three-way merge, so shared ancestors are not applied twice. Conflicts return
a blocked producer with no patch or passing checks. The resulting artifact still
spans the original commit through all predecessor and current-task edits, allowing
the publisher to preserve the complete coordinated result.

## Results and recovery

Each result includes the input/profile/image identities, producer outcome, full
binary patches with original and resulting trees, and independently executed check
evidence. Treat output-limit, unavailable, timed-out, cancelled, failed and
unexecuted states explicitly. A successful assistant response alone is not passing
verification. An empty check list supplies no verification evidence.

The runner never retries an entire task or publishes its patches. Temporal and
the control plane resolve uncertain activity results, dependencies, approval and
revocation. A retry must use the original immutable inputs and preserve previously
committed receipts. Changing a task, source baseline or profile is a new reviewed
execution decision.

Cancellation kills the named owned sandbox and gateway, and removes their owned
network. The helper also enforces its own finite deadline if the Docker client
disconnects. Docker failures can leave stopped resources needing operator cleanup;
inspect only names beginning `conductor-task-`, `conductor-proxy-`, and
`conductor-isolated-` associated with the task. Do not prune unrelated containers,
volumes or development databases. The worker does not create persistent volumes.

The bundled image includes Git, Node, and both assistant CLIs. Other language
toolchains and offline dependencies require an operator-built immutable image
derived from this image. Verification has no package-registry network access.
Unsupported symlinks, submodules, SHA-256 Git repositories and oversized bundles
fail explicitly. Docker is a container boundary, not a VM or proof of hostile
multi-tenant production hardening.

No dedicated OpenAI or Anthropic API key was configured during initial acceptance.
The verified claims are real sandbox execution, native CLI discovery, protocol
parsing and controlled gateway behavior. Paid model execution and real-provider
compatibility remain unverified until separately exercised with synthetic source.

Each admitted task attempt owns deterministic Docker resource names derived from its
complete input digest. The trusted coordinator persists that digest before starting
a producer. A recovered attempt can remove its exact containers and network and
confirm their absence without starting another producer. The runner reports
`cleanupConfirmed`; missing cleanup confirmation retains write reservations.
Do not invoke the same request concurrently outside this durable attempt boundary.
Canonical repository IDs remain unchanged in receipts; a SHA-256 directory mapping
in the prompt prevents IDs containing slashes or Unicode from becoming host paths.
