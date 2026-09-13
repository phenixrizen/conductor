# Feature 010: isolated coding and verification workers

**Status:** Implemented worker boundary, with actual Docker acceptance. Durable
authorization, activity scheduling, shared receipts, and publication belong to
their corresponding control-plane features. Live model execution is unverified
until a dedicated provider credential is configured and exercised.

## Outcome

An authorized task can change explicitly selected paths across pinned repositories
and produce recoverable patches. Separate containers run the task's declared
checks against those exact resulting trees. Assistant output cannot establish
design approval, passing verification, publication, merge, or deployment.

## Input and authority

The trusted activity supplies immutable run/task/change identity, approved package
revision and digest, graph digest, dependency receipt digests, prompt, canonical
repository IDs, full SHA-1 commit IDs, complete Git bundle bytes, literal writable
path prefixes, exact check argument vectors, and finite deadlines. Clients cannot
select host directories, Docker flags, images, credentials, or executables through
this worker request. The activity must authorize every repository before preparing
its bundle and recheck authority before committing a result.

Each repository begins at the specified commit in its bundle. Unsupported symlinks,
submodules, malformed paths, missing commits, and oversized source fail explicitly.
Original commit trees are reconstructed and compared before any generated patch is
accepted. Dirty files, user Git configuration, credential helpers, remote URLs,
hooks, and publication credentials are absent from the sandbox baseline.

Repository inputs can also include up to 32 authorized predecessor patches. Each
is verified independently against the same original bundle commit and resulting
tree. Temporary commits and Git's three-way tree merge combine them. Shared ancestor
changes merge once; conflicts block generation. The producer sees the combined
predecessor content. Its writable-path check compares against that combined
baseline, while its output patch remains cumulative from the original commit.
The activity must resolve ancestor receipt authority before supplying artifacts;
a JSON patch/digest alone cannot confer that authority.

The operator selects an immutable image digest and one versioned adapter profile:
Codex 0.154.0, Claude Code 2.1.270, or a credential-free `command/v1` program.
The command profile is an actual executable, not a simulated assistant. The result
records the image and profile digest independently of the task input digest.

## Isolation and evidence

1. A disposable, non-root container has a read-only root, default Docker seccomp
   and AppArmor, no Linux capabilities, no host mounts or Docker socket, bounded
   memory/CPU/processes, and writable memory filesystems. Its trusted PID 1 sets
   kernel no-new-privileges before decoding source or starting child processes.
2. Generation uses an isolated internal Docker network without a host bridge
   address or direct egress. Its only peer is the trusted provider gateway.
   DNS forwarding is disabled. The gateway alone has an external network, pins
   provider hosts and API paths, rejects redirects and arbitrary proxy operations,
   and limits request count, input, output, concurrency, and lifetime.
3. The real provider key enters only the trusted gateway's standard input. The
   producer receives an expiring task credential. Neither container receives
   Conductor API, repository publication, or production credentials. Native
   assistant output is withheld from persisted logs; it retains a digest and
   execution status. The ephemeral credential cannot appear literally in patches.
4. A zero CLI exit is insufficient: the pinned assistant protocol must report its
   terminal successful result. Missing, malformed, or error results are explicit
   unavailable or failed generation. No provider call is retried by the gateway.
5. After the producer exits, the trusted supervisor kills every remaining process
   in its container PID namespace. It captures regular files and reconstructs a
   fresh trusted Git index, ignoring the producer's Git metadata. Changed paths
   outside the authorized prefixes reject the complete result.
6. Each patch records base commit/tree, resulting tree, complete binary Git diff,
   changed paths, and SHA-256 digest. No patch is automatically committed or pushed
   to a managed repository.
7. Verification uses a separate, offline, credential-free container. Each check
   starts with fresh baseline repositories plus the exact patches, and checks the
   resulting Git trees before invoking the declared argument vector. It retains
   command, exit code, timestamps, bounded output/digest, truncation, and a digest
   covering all repository patches. Previous checks cannot mutate later inputs.
8. Failed production leaves checks unexecuted. Failed commands, deadlines,
   cancellation, missing executables, and output bounds never become passing
   checks. Empty check lists provide no verification evidence.
9. The runner has no durable state machine or automatic task retry. Temporal and
   the control plane own scheduling, dependencies, reconciliation, cancellation
   intent, and durable result acceptance. A lost result is not invented completion.

## Bounds

Tasks admit 1–16 repositories, at most 32 MiB of bundle input total, 10,000 regular
files and 32 MiB of expanded files per repository, 128 writable prefixes per
repository, 64 dependency digests, 256 KiB prompts, 32 checks, 128 arguments per
check, and a 1–3,600 second total deadline. Each check has a positive deadline no
longer than the task. Serialized sandbox input/output is bounded to 64 MiB, patches
to 8 MiB per repository, and command logs to 1 MiB.

Containers have two CPUs, 1 GiB memory with no extra swap, 128 processes, 512 MiB
workspace tmpfs and 256 MiB temporary tmpfs. Source expansion still remains subject
to those aggregate container limits. Exceeding them is failure, never truncation
masquerading as a complete patch.

The provider gateway admits at most two concurrent requests and 64 requests per
task. Requests are at most 4 MiB, each response at most 16 MiB, cumulative response
bytes at most 64 MiB, and each generation at most 16,384 output tokens. Provider
requests time out after two minutes. Claude additionally requires a configured
USD budget enforced by its CLI; provider billing is not an exact worker guarantee.
Codex has no claimed currency-budget contract. Task deadline and call/token bounds
apply to both.

## Acceptance

Actual Docker tests cover source-bound patches, preservation of other files,
independent successful checks, failed and unexecuted evidence, timeouts,
cancellation, absent commits, symlink rejection, output path scope, background
process cleanup, inherited-credential exclusion, read-only root, UID and kernel
no-new-privileges, native pinned CLI versions/help, and the isolated gateway
network. HTTP fixtures separately test gateway authorization, operation bounds,
token ceilings, redirects, output limits, and call budgets.

Dependent-agent acceptance verifies predecessor context, parallel children sharing
an ancestor, joined cumulative patches, blocked conflicts and forged tree rejection.

These tests execute real Git, Docker, shell/Node checks and native CLI discovery.
They do not establish successful paid model execution, arbitrary build-tool image
compatibility, a VM boundary, or hosted production deployment. See the
[worker runbook](../../docs/operations/coding-workers.md) and
[pinned adapter research](../../docs/research/assistant-profiles.md).

Native profiles require an explicit reviewed model ID. The gateway enforces that
exact requested model and a stateless request profile before upstream I/O. Hosted
tools, remote source URLs, saved provider file/response/conversation references,
background requests and unknown capability fields are rejected. Local tools and
inline content remain supported, Codex storage is false, and Codex web search is
disabled. Claude's CLI budget is not an independent currency ceiling; hard gateway
controls are requests, tokens, response bytes and time.

The trusted supervisor disables dumping and same-UID process inspection before
starting children. Repository-controlled code cannot access its memory or result
file descriptors through `/proc`, independently of host Yama policy. Trusted
receipt validation separately checks exact request/profile/image identity,
complete check IDs/argv/source digests, bounded patch metadata and confirmed cleanup.

Retained task artifact inspection is implemented independently of publication:
exact run/task/artifact pins, all-repository read locks, complete bounded typed
output, and shared API/client/MCP/CLI/TUI access. Failed checks and design-only
reports retain their evidence states. Live authorization/integrity checks and
actual PTY inspection cover this read path.
