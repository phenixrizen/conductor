#!/usr/bin/env python3
"""The one draft release a tag's packages go into (release.yml, #372).

The three package jobs publish through electron-builder, which uploads to
the tag's draft release and creates one when it finds none. Two jobs that
find none at the same moment each create one, and the packages are split
over two drafts. So the release is made once, before the package jobs
start, and they only find it; the end of the workflow checks that the tag
has that one draft and that every package built is on it.

    python3 scripts/release_draft.py ensure v0.7.0-rc.18
    python3 scripts/release_draft.py check v0.7.0-rc.18 packages/

`ensure` makes the tag's draft as electron-builder would (named the
version, a draft, not a prerelease), or keeps the one a run before this
made; it refuses a tag whose release is published or which has more than
one. `check` refuses a tag with any release but one draft, or a package in
the directory (the package jobs' artifacts) that the draft does not have.

Reads and writes the repository in GITHUB_REPOSITORY through `gh api`
(GH_TOKEN); only Python's standard library otherwise.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


def version(tag: str) -> str:
    """The package version of a tag: the tag without its v (electron-builder's
    release name)."""
    return tag[1:] if tag.startswith("v") else tag


def of_tag(releases: list[dict], tag: str) -> list[dict]:
    """The releases electron-builder takes for the tag: tagged with it, or with
    its version."""
    return [r for r in releases if r.get("tag_name") in (tag, version(tag))]


def plan(releases: list[dict], tag: str) -> tuple[str, int | None]:
    """What ensure does: ("create", None), or ("keep", id) of the draft a run
    before this made. Raises ValueError when the tag's releases are not
    something to upload to."""
    mine = of_tag(releases, tag)
    published = [r for r in mine if not r.get("draft")]
    if published:
        raise ValueError(f"{tag} has a published release ({published[0]['id']}): packages are not built into it again")
    if len(mine) > 1:
        ids = ", ".join(str(r["id"]) for r in mine)
        raise ValueError(f"{tag} has {len(mine)} draft releases ({ids}): put their assets on one and delete the others")
    if mine:
        return "keep", mine[0]["id"]
    return "create", None


def problems(releases: list[dict], tag: str, packages: list[str]) -> list[str]:
    """What check finds wrong: the tag's releases other than one draft, and
    the packages that draft does not have."""
    mine = of_tag(releases, tag)
    if len(mine) != 1:
        ids = ", ".join(str(r["id"]) for r in mine) or "none"
        return [f"{tag} has {len(mine)} releases ({ids}), not one draft"]
    r = mine[0]
    out = []
    if not r.get("draft"):
        out.append(f"{tag}'s release {r['id']} is not a draft")
    have = {a["name"] for a in r.get("assets", []) if a.get("state", "uploaded") == "uploaded"}
    out += [f"{name} is not on {tag}'s release {r['id']}" for name in sorted(set(packages) - have)]
    return out


def built(folder: Path) -> list[str]:
    """The package files in the package jobs' artifacts: what electron-builder
    publishes (its own builder-*.yml logs aside)."""
    return sorted(p.name for p in folder.rglob("*") if p.is_file() and not p.name.startswith("builder-"))


def gh(*args: str) -> str:
    return subprocess.run(["gh", *args], check=True, capture_output=True, text=True).stdout


def releases(repo: str) -> list[dict]:
    # One release per line, every page; drafts are listed to a token that may push.
    out = gh("api", "--paginate", f"repos/{repo}/releases?per_page=100", "--jq", ".[]")
    return [json.loads(line) for line in out.splitlines() if line.strip()]


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="The one draft release a tag's packages go into.")
    sub = ap.add_subparsers(dest="cmd", required=True)
    e = sub.add_parser("ensure", help="make the tag's draft release, or keep the one there")
    e.add_argument("tag")
    c = sub.add_parser("check", help="the tag has one draft holding every package built")
    c.add_argument("tag")
    c.add_argument("packages", type=Path)
    a = ap.parse_args(argv)
    repo = os.environ.get("GITHUB_REPOSITORY", "phenixrizen/conductor")
    if a.cmd == "ensure":
        try:
            what, rid = plan(releases(repo), a.tag)
        except ValueError as err:
            print(f"release_draft: {err}", file=sys.stderr)
            return 1
        if what == "keep":
            print(f"{a.tag}: keeping draft release {rid}")
            return 0
        made = json.loads(gh("api", f"repos/{repo}/releases", "-f", f"tag_name={a.tag}", "-f", f"name={version(a.tag)}",
                             "-F", "draft=true", "-F", "prerelease=false"))
        print(f"{a.tag}: made draft release {made['id']}")
        return 0
    packages = built(a.packages)
    if not packages:
        print(f"release_draft: no packages under {a.packages}", file=sys.stderr)
        return 1
    found = problems(releases(repo), a.tag, packages)
    for p in found:
        print(f"release_draft: {p}", file=sys.stderr)
    if not found:
        print(f"{a.tag}: one draft release with all {len(packages)} packages")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
