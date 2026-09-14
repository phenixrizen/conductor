# Native assistant connections: qualification record

Inspected 2026-09-14. This feature uses a common fixed-scope MCP stdio bridge. It
ships no embedded provider SDK, model scheduler, OAuth broker or native-host
installer. No provider credential files were read and no paid model call was made.

| Surface | Evidence | Limit |
|---|---|---|
| Conductor MCP | Pinned Go SDK 1.7.0, Go toolchain 1.26.8; compiled stdio bridge and signed API/PostgreSQL acceptance | Synthetic proposed prose proves transport and authority, not model quality |
| Codex CLI | Locally installed `codex-cli 0.154.0`; native `mcp add --help`, configuration parser and documented TOML schema | Provider login and live inference were not exercised |
| Claude Code | Existing qualified worker research pins CLI 2.1.270 and Agent SDK 0.3.270; current native MCP documentation inspected | Native host was not installed on PATH for this feature; generated config follows documentation, not a new end-to-end host qualification |
| Antigravity | Current official MCP, CLI authentication and SDK documentation inspected | No local native binary or live provider journey qualified; CLI 1.2.0 / SDK 0.1.16 are research candidates, not tested runtime dependencies |

Codex supports stdio server `command` and `env` entries under `mcp_servers` in its
native TOML configuration. Its native account sign-in and API authentication remain
host-owned. `codex mcp login` authenticates a remote MCP server where supported;
it is not an embedded model-account login for Conductor. The generated profile uses
stdio with a separate Conductor token file. Sources:
[Codex MCP](https://learn.chatgpt.com/docs/extend/mcp),
[Codex authentication](https://learn.chatgpt.com/docs/auth).

Claude Code documents stdio entries under `mcpServers`, including project `.mcp.json`
configuration and native CLI registration. It manages native account/API authentication
and MCP consent. The Conductor feature does not collect Claude account tokens or
provide a subscription OAuth broker. Hosted SDK use has separate authentication,
branding and usage conditions and is not implemented here. Sources:
[Claude Code MCP](https://code.claude.com/docs/en/mcp),
[Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview),
[legal and compliance](https://code.claude.com/docs/en/legal-and-compliance),
[personal SDK plan guidance](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan).

Antigravity documents `mcpServers` entries with `command`, `args` and `env` in
`~/.gemini/config/mcp_config.json` or workspace `.agents/mcp_config.json`. Native
headless operation can use cached account login. API use requires the documented
Gemini model-provider setting and `GEMINI_API_KEY`, not simply adding an arbitrary
key to Conductor. A host process exit code alone cannot establish that its tool
actually submitted a proposal; the Conductor receipt is the shared fact. Sources:
[Antigravity MCP](https://antigravity.google/docs/mcp),
[headless CLI](https://antigravity.google/docs/cli/headless/),
[installation and authentication](https://antigravity.google/docs/cli/install/),
[SDK overview](https://antigravity.google/docs/sdk/overview).

The CLI configuration helper never installs or launches a native provider host.
Generation is offline and emits only paths, fixed scope and the assistance profile.
Its explicit `--check` launches Conductor's MCP binary to verify current agent access
and the four-tool surface. This establishes neither provider-account availability
nor a model's ability to complete the request. Operators must qualify any native
host upgrade in their own environment before making broader compatibility claims.
