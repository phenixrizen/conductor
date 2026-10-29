# Checks by affected surface

`make preflight` runs every check CI runs, here, from the committed head, and
writes a receipt with a log per step under the git common dir's `preflight/`;
`python3 scripts/preflight.py --machines mac,windows` runs the Mac's (over
`ssh mac`, the revision pushed to its checkout) and the Windows host's (WSL
interop, a checkout under the Windows profile) rows too, never through GitHub.
It is how to see what CI will see before a pull request; CI stays the gate.

Run the narrow tests while iterating, then the complete checks for every affected surface. A change to the Python scripts runs their tests; it does not require unrelated Go, UI, desktop, network or hardware tests. A change spanning server and UI requires tests at both ends: Go race tests, vitest and Playwright. Runtime changes run the relevant runtime suites below. A skipped or unavailable check is a limitation, never a pass.


```bash
git fetch origin && python3 scripts/dco_check.py --base origin/main --head HEAD  # every commit of the branch signed off by its author (CI's dco job); the fetch so that origin/main is GitHub's
make lint                      # gofmt -l, go vet
go test -race -count=1 ./...   # includes PTY, WebSocket and pion loopback tests
npm --prefix web run typecheck
npm --prefix web test          # vitest (link detection)
make web-build                 # nuxt generate + copy into internal/web/dist
make build-go                  # embeds whatever is in internal/web/dist
make check-version             # the binary reports the version and commit make injected (a missed -X site would say dev (unknown))
make test-e2e                  # Playwright (web/e2e): builds both, then a server with stub agents in Chromium 1117
make test-pebble               # certificates end to end against Let's Encrypt's Pebble (go install'ed when missing)
make test-live                 # the real Claude Code and Codex (CONDUCTOR_E2E_LIVE_REPO, keys or logins); weekly in CI
make desktop-test              # the Electron shell: tsc --noEmit and vitest (desktop/)
make lint-static               # staticcheck, pinned
make vuln                      # govulncheck (network); the standard library's findings need the latest Go patch
make test-network              # the built-in agents' sites on the network (nightly)
make test-recipes              # the recipe flags against the CLIs on this machine (nightly)
python3 scripts/brand_assets.py --check
python3 scripts/notices.py --check  # THIRD_PARTY_NOTICES current (after make web-build, npm ci in desktop/)
python3 -m unittest discover -s scripts -p '*_test.py'  # the scripts' own tests (dco_check.py, notices.py, preflight.py, release_draft.py)
```

The e2e stub (`web/e2e/stub-agent.sh`) answers as Claude Code, Codex or an
impostor by `STUB_IDENTITY`, reports as its agent's hooks do by
`STUB_REPORT`, keeps a transcript per agent session, draws the trust
question with `STUB_TRUST_DIALOG=1` and Codex's "Hooks need review" when
`$HOME/.codex/stub-hooks-review` exists, and on the line `stub tools codex`
(or `copilot`, `agy`) replays that agent's captured hook payloads through
`conductor notify`; `web/e2e/conductor.e2e.json` wires it
with the real recipes. Run the narrowest package tests while iterating, then
the full set for affected surfaces before finishing. Every harness that launches sessions sets
`CONDUCTOR_RENDEZVOUS=0` (the cli tests' `clearConductorEnv`, `web/e2e/server.ts`):
a server publishes its sessions to the public switchyard by default, and a
test's must stay on the machine. A check that cannot run (no Docker, no network, no browser) is a
reported limitation, not a pass.
