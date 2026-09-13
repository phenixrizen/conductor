# Assistant adapter profiles

Research and package inspection performed on 2026-09-13. These versions are exact
integration profiles, not floating compatibility promises. Source text is not
execution authority; the worker retains the approved task and path boundaries.

| Component | Pin | Inspection and actual capability |
|---|---|---|
| Codex CLI | npm `@openai/codex` 0.154.0 | Installed native CLI reports `codex-cli 0.154.0`; `exec --help` confirms stdin prompts, JSONL events, ephemeral sessions, ignored user configuration/rules, custom configuration, and externally sandboxed execution. |
| Claude Code | npm `@anthropic-ai/claude-code` 2.1.270 | Installed native CLI reports `2.1.270 (Claude Code)`; help confirms print/JSON, bare/safe mode, no session persistence, strict MCP config, disabled skills, explicit tool permissions, and a USD budget. |
| Claude result types | npm `@anthropic-ai/claude-agent-sdk` 0.3.270, inspected only | `sdk.d.ts` distinguishes `result/success/is_error=false` from execution, turn, budget and structured-output errors. The SDK is not a runtime dependency. |
| Runtime image | Node 22.19.0 Bookworm slim at SHA-256 `4a4884e8a44826194dff92ba316264f392056cbe243dcc9fd3551e71cea02b90` | Runtime image builds and CLI discovery run in actual Docker; its final immutable image ID is recorded by the runner. |
| Docker acceptance | Engine 29.6.1 | Internal isolated gateway mode, separate gateway/producers, non-root/read-only/capability-free execution, actual process termination and offline verification exercised. |

The reviewed [worker package manifest](../../deploy/worker/package.json) and
[npm lockfile](../../deploy/worker/package-lock.json) pin actual package integrity
and platform dependencies. Claude's inspected postinstall copies its selected
native optional dependency into the CLI path; the build first performs
`npm ci --ignore-scripts`, then invokes that exact reviewed installer.

The [official Codex automation guide](https://learn.chatgpt.com/docs/non-interactive-mode)
documents noninteractive generation, terminal JSON events, API-key automation,
and separation of coding and publication. It explicitly cautions against exposing
the API key to repository-controlled commands. Conductor therefore keeps the real
key in a separate trusted gateway, not the coding process environment. Its
[configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)
documents custom Responses providers, base URLs, environment-backed credentials,
WebSocket capability and retry settings. Conductor disables WebSockets and provider
retries in its pinned custom provider; its own gateway also does not retry.

The [official Claude CLI reference](https://code.claude.com/docs/en/cli-reference)
and [programmatic guide](https://code.claude.com/docs/en/headless) define print mode,
the JSON result and tool/permission settings. The
[gateway guide](https://code.claude.com/docs/en/llm-gateway) documents
`ANTHROPIC_BASE_URL` and gateway credentials. Conductor exposes a task credential
to the CLI, fixed Messages endpoints through the gateway, and no saved login state.
Claude's cost estimate/budget does not establish an exact provider invoice.

[Docker's official gateway-mode documentation](https://docs.docker.com/engine/network/port-publishing/#gateway-modes)
explains that `isolated` mode requires an internal network and removes the host
bridge address. Conductor uses this mode, an explicit gateway peer and disabled
producer DNS instead of granting a coding container general external access.

Observed native version/help and controlled protocol tests are not successful
model execution. No claim is made for subscription authentication, interactive
login, resume/fork, cross-version result schemas, cloud assistant sessions, model
tier availability, or arbitrary external MCP integrations inside these profiles.
Durable task coordination belongs to Conductor/Temporal, and repository publishing
uses a separate trusted integration service.

Git dependency merging uses the inspected 2.39.5 binary and the official
[merge-tree 2.39 documentation](https://git-scm.com/docs/git-merge-tree/2.39.0).
Its write-tree mode performs a real three-way merge without modifying the working
tree; a nonzero merge exit is treated as blocked, never a usable conflict tree.
