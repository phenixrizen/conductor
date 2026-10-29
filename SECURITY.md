# Security

## Reporting a vulnerability

Report it through GitHub's private vulnerability reporting on this
repository: Security, "Report a vulnerability", at
https://github.com/rocksolidlabs/conductor/security/advisories/new. That
opens a draft security advisory that only the maintainers and you see.
Never an issue, a pull request, a discussion or a public comment: those are
public the moment they are posted, fix or no fix.

If GitHub cannot be used, write to conductor@rocksolidlabs.io with
"security" in the subject.

## What to include

- The version (`conductor version`, or the app's About card in Settings →
  The app) and the platform.
- What an attacker needs: a share link and its role (view or control), a
  hosted session, the switchyard's relay, a local account on the machine,
  or nothing at all.
- What they get.
- Steps that reproduce it.

## What happens

- An acknowledgement within seven days.
- The work happens in the draft advisory, and in the temporary private fork
  GitHub makes for it when a fix needs one.
- The fix lands as an ordinary pull request that names the change and not
  the vulnerability.
- The advisory is published when the fix has shipped, with credit if you
  want it. The maintainers decide the timing.
- There is no bounty.

## Scope

In scope: the `conductor` binary in all its modes (`serve`, `host`,
`switchyard`), the web app it serves, the desktop app, the published
packages and the image the `Dockerfile` builds, and the public switchyard
at switchyard.rslabs.net that RockSolid Labs runs.

Out of scope: the coding agents Conductor launches (report to their
vendors), and what the README's "Security model and limits" documents as by
design, a switchyard trusted with what is published to it among them.

## Supported versions

The latest release and `main`. Older release candidates get no fixes. The
desktop app updates itself on macOS, on Windows and as an AppImage
(`desktop/src/updater.ts`, `updateChannel`); a deb or rpm install only
points at the release page; the `conductor` binary, the packages and the
image are updated by whoever installed them. Install the latest release.

## Everyone working in this repository

A vulnerability found while working, by a person or an agent, goes into a
draft security advisory and never into an issue, a pull request, a commit
message or a doc, until a maintainer decides on disclosure (`AGENTS.md`).
