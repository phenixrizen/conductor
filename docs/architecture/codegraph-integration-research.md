# Selected CodeGraph integration profile

Inspected 2026-09-13. The user selected
[colbymchenry/codegraph](https://github.com/colbymchenry/codegraph); similarly named
projects are not substituted. Upstream combines TypeScript orchestration and a
native Rust extraction kernel. Conductor keeps its own deterministic Go/manifest
index explicitly distinct from that integration.

| Item | Pinned evidence |
|---|---|
| Upstream release | [v1.6.0](https://github.com/colbymchenry/codegraph/releases/tag/v1.6.0) |
| Release source | `dfccdf62547fcd76d343344d823a0e1998d3a89f` |
| Linux x64 archive | `codegraph-linux-x64.tar.gz` |
| Published SHA-256 | `de3391f79ed42622d937e6cd5b7642a7ea8bb7d1473607e80b879ba73ef216b0` |
| Package | `@colbymchenry/codegraph` 1.6.0 |
| Observed bundled runtime | Node 24.16.0 |
| Observed native contract | Kernel 0.1.0, ABI 2 |
| Base container | Debian bookworm-slim digest `sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171` |

The [official library documentation](https://github.com/colbymchenry/codegraph/blob/v1.6.0/README.md)
describes CodeGraph.init, indexAll and close. The adapter reads the release's
SQLite nodes/edges schema after indexing and probes native extraction for each
input file. Those internal loader/schema paths are version-specific, not a claim
of stable upstream extension APIs. Upgrade requires source review and repeatable
acceptance, not changing an image tag in place.

The archive checksum was verified against the published release checksums. This
establishes downloaded-byte consistency; artifact attestation verification is not
claimed. The upstream repository's own license is not Conductor's project license.
No upstream installer or agent registration command is run.

The current container acceptance checks actual native Go extraction and a resolved
Caller→Called edge. Signed identity/PostgreSQL activity acceptance stores that index
with its receipt and returns it through graph commands. These tests do not certify
all upstream languages, repositories, native fallback behavior or cross-language
heuristics. Unknown provenance and unresolved references remain explicit.

## Whole-source transport evidence

The full-source adapter was researched against official
[Git smart HTTP](https://git-scm.com/docs/gitprotocol-http.html),
[GitHub HTTPS authentication](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens), and
[GitLab HTTPS token access](https://docs.gitlab.com/api/oauth2/) documentation.
It runs tested Git 2.43.0 with the existing pinned REST provider profiles and a
trusted, permission-checked proxy; provider tokens never enter the Git subprocess.

On 2026-09-13, opt-in live GitHub source acceptance verified Conductor's own
repository at commit `345f7e1074988b937a76d6e4986f05ce91cac4e3`: the complete
bundle contained 583,841 bytes and the selected commit tree had 178 files, with no
index source bounds hit. This is one actual read/bundle path, not a blanket provider
compatibility or production claim. GitLab smart-HTTP behavior is covered by actual
Git plus controlled provider metadata; a real GitLab credential/profile remains
unverified.
