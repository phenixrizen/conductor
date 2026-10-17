# Plan: the test suites on macOS

Branch `mac-test-fixes`. On an Apple Silicon Mac (macOS 27.0.1) `make test`
fails 21 Go tests (in session, api, cli, config and hostagent) and
`make test-e2e` 39 Playwright tests that pass in CI on Linux. All but one
come from a trait of the machine (its filesystem, its sockets, what is
installed), not from the feature under test; the one that does not is
left to the owner (see Pending). This change makes the rest pass there
without loosening what they check: Linux runs the same checks as before,
and each change is in a test or a harness, never in the product.

A plan is written although no state machine, protocol, concurrency or
security behaviour changes (AGENTS.md, Reviews): it is the record of which
trait each change answers, so that CI on Linux can be read against it.

## The traits

1. **`/var` and `/tmp` are symbolic links** to `/private/var` and
   `/private/tmp`. Go's `t.TempDir()` and Node's `os.tmpdir()` give paths
   through `/var/folders/…`; the product resolves paths it is given
   (`resolveCwd`, `ResolvePath`, `session.Leads`, `config.withTargets`)
   and reports `/private/var/…`. A test that compares a path it made with
   one the product reported sees two spellings of one directory; a deny
   list built from such a path gains a second entry, the resolved one,
   that Linux never has. The deny list protects the file either way:
   `denySet` matches an entry that exists by identity (`os.SameFile`),
   so a `/var/…` entry refuses the `/private/var/…` file.
2. **APFS is case-insensitive** (and case-preserving) by default:
   `Lstat(".Git")` finds `.git`.
3. **The configuration directory is `~/Library/Application Support`**
   (`os.UserConfigDir`), not `~/.config`, and `XDG_CONFIG_HOME` is not
   read on Darwin. The product's desktop-app entries follow
   `os.UserConfigDir`; tests that spelt `~/.config` or set
   `XDG_CONFIG_HOME` looked elsewhere.
4. **Loopback sockets keep more in flight than they are asked to.** A
   receive buffer set before `connect` (SO_RCVBUF through
   `net.Dialer.Control`, the test's `smallReceiver`) is replaced at
   connect by TCP's autotuned size (measured: 8 KiB asked, 326 KiB
   after), and loopback holds a few hundred KiB between a blocked writer
   and a reader that reads nothing (340–545 KiB measured with the test's
   sizes), where Linux holds what the two buffers allow.
5. **`/tmp`, the directory of root's the completion test writes in, is a
   link**: the product reports "leads to a file of root's" rather than
   "belongs to".
6. **Neovim is not installed** on this machine, where CI installs it. The
   session package caches `nvim.Available()` for a minute, so a fake
   `nvim` put on PATH for a test is not consulted when an earlier check
   found none.

## The changes, each a test's or a harness's

Trait 1, symbolic links:

- `internal/session`: `newLocal` resolves the working directory it is
  given and `newLocalWith` its own, as `gitDirTree` and the deny tests
  already do; `gitRepo` returns its repository resolved;
  `TestCopiesBesideADanglingGitFileTargetAreHeld` resolves its root, and
  the touched-index tests the working directory they compare with (they
  passed before only because both sides were unresolved).
  Fixes `TestFileWriteSavesAndRefuses` (the Activity path is relative to
  `Info.Cwd`), `TestGitSeenRecordsWhatNoHookNamed` (the same for what git
  saw), the three fake-Neovim tests (the open resolves its target under
  the folder; see trait 6 too) and the dangling-gitfile test (`Leads`
  gives the resolved directory).
- `internal/api`: `newTestEnvAgents` resolves the root it makes, so
  `e.root` is what the server reports as a session's or a run's `cwd`
  (the `realRoot` helper already existed for single tests). Fixes
  `TestSessionLifecycle`, `TestCrewRunLifecycle`,
  `TestAgentTokenFormsACrewAndIsItsFirstMember`,
  `TestAgentCrewInheritsCwdAndYoloAndNeverEscalates`.
- `internal/config`: the deny tests resolve every temporary directory
  their homes, config files and data directories live in (`realTempDir`),
  so the list holds one entry per path on every system.
- `internal/cli`: `clearConductorEnv` resolves the home it gives a test,
  and the host test the directory of its config file, so a deny list
  built from them has one entry per path.
- `internal/hostagent`: `homeWithConductor` resolves the home it makes.
- `web/e2e/global-setup.ts`: the scratch root is `fs.realpathSync` of
  the directory made, so `state.root`, `home`, `data` and `repo` are the
  paths the server reports in `data-file-node`, `data-touched` and the
  pickers. This alone clears the files, saving, touched, editor and
  terminal-height specs.

Trait 2, case-insensitive filesystem:

- `TestFileWriteRefusesARepositorysGitDir` notes which of the paths it
  expects "not made" are found before any save (`.Git` beside `.git` on
  such a filesystem) and leaves those out of the "was made" check; the
  refusal of the save and the unchanged `.git` files are checked as
  before.

Trait 3, the configuration directory:

- The `internal/config` deny tests, the `internal/cli` host test and
  `internal/hostagent` `TestHostRefusesTheServersFilesOnItsMachine` build
  the desktop app's entries, and the home's layout, from
  `os.UserConfigDir()` after setting the home, as the product does,
  instead of `~/.config` or `XDG_CONFIG_HOME` by name. On Linux both give
  the same paths.

Trait 4, loopback buffering:

- `TestLinkGoneReachesHostAheadOfQueuedFrames` keeps its bound of 4
  relayed frames on Linux and allows 32 on Darwin: what the sockets hold
  there is a few hundred KiB (up to about 13 relayed messages of 32 KiB
  input each), still well under the viewers' lane's 7 MiB, some 160 such
  messages, which is what the test pins (link_gone is read within what
  the sockets held, not behind the queue). Measured on this Mac: 5 and
  10 frames, run to run. It is a threshold, not slower delivery: the
  count stays within what the sockets hold, and link_gone arrives well
  inside the 10-second read deadline each time.

Trait 5, `/tmp` a link:

- `rootDir` resolves each candidate directory before using it, so the
  file is made in `/private/tmp` and the product's answer names that
  directory as belonging to root.

Trait 6, Neovim absent:

- `fakeNvim` resets the session package's one-minute availability cache
  after putting the fake on PATH, and again when the test ends, so the
  fake is found as a real Neovim would be and the next test checks
  afresh. The three tests then run on a machine without Neovim; on CI
  nothing changes.

## Pending: the owner's call

- `internal/hostagent` `TestICEOnOneUDPPortAdvertisesTheGivenAddress`
  fails on this Mac because it has IPv6 ULA addresses: with `ICE.PublicIP`
  set, pion's address rewrite replaces the IPv4 host candidates only, so
  the IPv6 host candidates are advertised unrewritten beside the public
  address, against the flag's description. Linux CI runners have no
  routable IPv6 and never see it. This is the feature under test, not a
  trait of the test: either the product gathers UDP4 alone when the public
  address is IPv4 (`hostagent/peer.go`, a change of its own under
  AGENTS.md) or the test accepts the IPv6 candidates, which weakens its
  claim. Decided (2026-10-17): the test is not weakened; it stays as it is
  and fails on a dual-stack Mac until the owner says whether the product
  gathers UDP4 alone with an IPv4 public address, which would be a
  hostagent pull request of its own. This pull request says so.
- `TestFirstReadWaitsForTheFirstPong/start` (`internal/hostagent`) failed
  once in the full run with the race detector (the first read refused
  after the 3-second bound) and passed three of three alone. Watched, not
  changed: a timing margin under load, if it recurs.

## Out of scope

- A `conductor host` started in a directory reached through a symbolic
  link keeps that spelling as `Info.Cwd` (`hostagent/agent.go`,
  `filepath.Abs` alone) while its file paths are resolved: a saved file's
  Activity path is then absolute rather than relative. The server resolves
  its own sessions' working directories (`resolveCwd`). This is
  independent of the tests and on every system; it is reported as an
  issue for the owner's call.
- Installing Neovim on the Mac, which would also run the Neovim bridge's
  own tests there.

## Review

The Codex adversarial review cannot run on this machine: the plugin is not
installed (`codex_review.py` refuses with the install steps). A limitation,
not a pass; the pull request says so.
