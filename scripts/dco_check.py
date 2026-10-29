# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 the Conductor Authors and RockSolid Labs, Inc.
"""Every commit of a branch signed off by its author: the check CI's `dco` job runs.

    python3 scripts/dco_check.py --base <rev> --head <rev> [--repo <dir>]

The commits of base..head are read as git logs them and judged one by one,
merge commits included. A commit passes when one of its Signed-off-by lines
carries the author's name (trimmed, exact) and the author's address (compared
without regard to case): the line `git commit -s` adds, by which the author
certifies the Developer Certificate of Origin (`DCO` at the root;
CONTRIBUTING.md, Sign-off). The committer is not consulted, so a
maintainer's rebase keeps the author's sign-off; no author is exempt, bots
included. A merge on the branch is a commit of the pull request and may carry
a conflict's resolution, so it is judged like any other (the DCO GitHub App
skips merges; this check does not).

Exit 0 when every commit passes, with the count. Exit 1 with one line per
commit that does not and a last line that counts them and names the fix.
Exit 2 when git fails or a record does not parse, with git's message. There
is no path to exit 0 without every commit read and judged.
"""

import argparse
import os
import subprocess
import sys

# One record per commit, oldest first: the record separator, then the six
# fields split by the unit separator; %B is the whole message. Both ends of
# the pipe are pinned. git is told to write UTF-8 whatever
# i18n.logOutputEncoding or i18n.commitEncoding says, and the bytes are
# decoded as UTF-8 whatever the locale, so a non-ASCII name or subject is
# judged the same on a laptop and on the runner; and it is told to write the
# records and nothing else (--no-show-signature): log.showSignature in a
# developer's configuration would put a signed commit's verification lines
# ("gpg: Signature made ...", "No signature") on stdout before its record,
# and a branch signed off in full would fail the read.
RECORD = "\x1e"
UNIT = "\x1f"
FORMAT = "%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%s%x1f%B"
FIELDS = 6
KEY = "signed-off-by"

# The repository is the one --repo names and no other. These are the
# variables by which git selects a repository over -C (a hook exports
# GIT_DIR; a shell may hold any of them): they are dropped from the
# environment git log runs in, or the check would read another repository
# in silence.
REPOSITORY_VARIABLES = (
    "GIT_DIR",
    "GIT_WORK_TREE",
    "GIT_COMMON_DIR",
    "GIT_OBJECT_DIRECTORY",
    "GIT_ALTERNATE_OBJECT_DIRECTORIES",
    "GIT_INDEX_FILE",
    "GIT_NAMESPACE",
    "GIT_CEILING_DIRECTORIES",
    "GIT_DISCOVERY_ACROSS_FILESYSTEM",
)


class Error(Exception):
    pass


class Commit:
    def __init__(self, sha, parents, author_name, author_email, subject, message):
        self.sha = sha
        self.parents = list(parents)
        self.author_name = author_name
        self.author_email = author_email
        self.subject = subject
        self.message = message

    @property
    def merge(self):
        return len(self.parents) > 1


def signoffs(message):
    """Every Signed-off-by line of message as (name, address): the key compared
    without regard to case, leading whitespace allowed, the rest parsed as
    `Name <address>`. A line without angle brackets is no sign-off."""
    found = []
    for line in message.splitlines():
        key, colon, rest = line.lstrip().partition(":")
        if not colon or key.strip().lower() != KEY:
            continue
        rest = rest.strip()
        start, end = rest.rfind("<"), rest.rfind(">")
        if start < 0 or end < start:
            continue
        found.append((rest[:start].strip(), rest[start + 1:end].strip()))
    return found


def problems(commits):
    """One line per commit not signed off by its author: the first twelve
    characters of the sha, the subject (a merge marked), the author, and
    either `no Signed-off-by` or the sign-offs found, which are not the
    author's."""
    out = []
    for c in commits:
        lines = signoffs(c.message)
        name, address = c.author_name.strip(), c.author_email.strip().lower()
        if any(n == name and a.lower() == address for n, a in lines):
            continue
        what = c.subject + (" (merge)" if c.merge else "")
        if lines:
            why = "Signed-off-by: " + ", ".join(f"{n} <{a}>" for n, a in lines) + " (not the author)"
        else:
            why = "no Signed-off-by"
        out.append(f"{c.sha[:12]} {what} by {c.author_name} <{c.author_email}>: {why}")
    return out


def read(repo, base, head):
    """The commits of base..head in repo, oldest first, as git logs them: the
    records alone, in UTF-8 (the comment at FORMAT), read from repo whatever
    repository the environment would select (REPOSITORY_VARIABLES)."""
    argv = [
        "git", "-C", repo, "log", "--reverse", "--encoding=UTF-8", "--no-show-signature",
        "--format=" + FORMAT, f"{base}..{head}",
    ]
    env = {k: v for k, v in os.environ.items() if k not in REPOSITORY_VARIABLES}
    try:
        p = subprocess.run(argv, capture_output=True, env=env)
    except OSError as e:
        raise Error(f"git could not be run: {e}") from None
    if p.returncode:
        raise Error(p.stderr.decode("utf-8", "replace").strip() or "git log failed")
    try:
        text = p.stdout.decode("utf-8")
    except UnicodeDecodeError as e:
        raise Error(f"git log wrote bytes that are not UTF-8: {e}") from None
    if text and not text.startswith(RECORD):
        raise Error("git log wrote something before the first record: " + repr(text[:80]))
    commits = []
    for record in text.split(RECORD)[1:]:
        parts = record.split(UNIT)
        if len(parts) != FIELDS:
            raise Error(f"a log record did not parse into {FIELDS} fields ({len(parts)} found): {record[:80]!r}")
        sha, parents, name, address, subject, message = parts
        commits.append(Commit(sha, parents.split(), name, address, subject, message.rstrip("\n")))
    return commits


def count(n, noun="commit"):
    return f"{n} {noun}" if n == 1 else f"{n} {noun}s"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--base", required=True, help="the revision the branch starts from (a pull request's base)")
    ap.add_argument("--head", required=True, help="the branch's tip")
    ap.add_argument("--repo", default=".", help="the repository (default: the current directory)")
    a = ap.parse_args(argv)
    try:
        commits = read(a.repo, a.base, a.head)
    except Error as e:
        print(f"dco: {e}", file=sys.stderr)
        return 2
    if not commits:
        print(f"no commits between {a.base} and {a.head}")
        return 0
    bad = problems(commits)
    if not bad:
        print(f"{count(len(commits))} signed off by {'its author' if len(commits) == 1 else 'their authors'}")
        return 0
    for line in bad:
        print(line)
    k, n = len(bad), len(commits)
    verdict = f"{k} of {n} commits {'is not signed off by its author' if k == 1 else 'are not signed off by their authors'}."
    if any(c.merge for c in commits):
        print(f"{verdict} The branch has a merge commit: CONTRIBUTING.md, Sign-off, says how to sign it without losing what the merge carried.")
    else:
        print(f"{verdict} Fix: git rebase --signoff {a.base} && git push --force-with-lease, run by each commit's author (CONTRIBUTING.md, Sign-off).")
    return 1


if __name__ == "__main__":
    sys.exit(main())
