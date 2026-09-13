# Native design tool research

Inspected on 2026-09-13. Conductor's adapters use released commands and retained
artifacts, with separately scoped execution evidence.

| Upstream | Tested identity | Actual supported native surface |
| --- | --- | --- |
| [GitHub Spec Kit](https://github.com/github/spec-kit/tree/96c9bd657bfd5de0d651a6165084932b7304ac99) | 1.0.6, commit `96c9bd657bfd5de0d651a6165084932b7304ac99` | Bundled generic-integration project initialization, core templates, `check-prerequisites.sh --json --require-spec --require-tasks --include-tasks` |
| [ADRKit](https://github.com/mbeacom/adrkit/tree/3e40675ed6f513d9712b1dccaa68034d649d1eb9) | `@adrkit/cli` 0.13.0, commit `3e40675ed6f513d9712b1dccaa68034d649d1eb9` | `adr lint/check/explain --json`, `adr graph --format json`, `adr new --status proposed --json` |

Spec Kit's [core CLI](https://github.com/github/spec-kit/blob/96c9bd657bfd5de0d651a6165084932b7304ac99/docs/reference/core.md)
initializes from bundled assets; it requires Python 3.11 or newer. Its
[prerequisite helper](https://github.com/github/spec-kit/blob/96c9bd657bfd5de0d651a6165084932b7304ac99/scripts/bash/check-prerequisites.sh)
checks artifact existence. Setting a feature directory normally updates
`.specify/feature.json`, so Conductor runs it on a staged copy to preserve source.
The wrapper always executes trusted image helpers and ignores repository-provided
helper scripts, extensions and runtime configuration.

ADRKit's [published CLI reference](https://github.com/mbeacom/adrkit/blob/3e40675ed6f513d9712b1dccaa68034d649d1eb9/packages/cli/README.md)
requires Node 22 or newer. Its deterministic commands distinguish schema errors,
usage errors and decision relationships. `check` with no files is a successful
native no-op, so Conductor rejects an empty path selection. Native `new` supports
several unaccepted states; Conductor always selects Proposed. Native source
frontmatter does not supply verified Conductor reviewer identity or approval.

The source archive SHA-256 is
`edb4638974b539550849cf83672746d08d6071601605a46b6b8086563789a18e`;
the built Spec Kit wheel SHA-256 is
`7d80f856bda6022556037a35b8c8e1c162e8419b1ef4e380487665ecad18f37a`.
The build script verifies both. Python dependency versions and actual release
hashes are retained in `deploy/design-tools/requirements.lock`; npm package
versions and integrity hashes are retained in its reviewed `package-lock.json`.
The source wheel was reproducibly built with uv 0.11.25 in this acceptance session.
Runtime acceptance used Python 3.11.2 and Node 22.19.0 in the isolated image;
additional direct CLI tests used Python 3.12.13 and Node 22.14.0.

The separately published [ADRKit Spec Kit extension 0.1.3](https://github.com/mbeacom/adrkit/blob/3e40675ed6f513d9712b1dccaa68034d649d1eb9/packages/adapters/spec-kit/extension.yml)
requires Spec Kit `>=0.13.0,<0.16.0`. Upstream main widened that range in a later
commit, but that change is not this released extension. Conductor integrates the
native tools independently and does not install the incompatible extension.
Spec Kit slash-command workflows still require an agent; ADRKit's later evaluator
passes are not fabricated as implemented semantic verification.

The final tested design image is
`sha256:ff67ac638d4d7c5d825e69de35d4474d1188dd531215ffc5819d1abbf6fe0c20`,
built atop worker image
`sha256:a60993075fc4f7a78139916ca362a9123fdc7adaac992abc1122a3fb5d2be3da`.
Rebuilding on a later reviewed worker changes the final image identity and requires
renewed operator profile provisioning and inspected plan authorization.
