#!/usr/bin/env python3
"""The one draft release a tag's packages go into (release.yml, #372).

electron-builder, publishing, uploads to the tag's draft release and makes
one when it finds none, and it looks at the first page of releases only. Two
package jobs that found none at the same moment each made a draft, and the
packages were split over two. So electron-builder only builds; the release is
made once, before the package jobs start, each job uploads its packages to it
by its id, and the end of the workflow checks that the tag has that one draft
with every package on it.

    python3 scripts/release_draft.py ensure v0.7.0-rc.18
    python3 scripts/release_draft.py upload v0.7.0-rc.18 desktop/release
    python3 scripts/release_draft.py check v0.7.0-rc.18 packages/

`ensure` makes the tag's draft as electron-builder would (named the version,
a draft, not a prerelease), or keeps the one a run before this made; it
refuses a tag whose release is published or which has more than one.
`upload` puts a build's packages (installers, their blockmaps, the
latest*.yml the updater reads) on that draft, replacing one a failed run
left; it never makes a release. `check` refuses a tag with any release but
one draft, a package in the directory (the package jobs' artifacts) the
draft does not have, or an installer without its blockmap.

A package's file name is its name on the release: the Windows installer is
named without spaces (electron-builder.yml), as electron-builder would have
published it.

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
import urllib.parse

# What electron-builder publishes for the targets in electron-builder.yml.
PACKAGE_SUFFIXES = (".dmg", ".zip", ".deb", ".rpm", ".AppImage", ".exe", ".blockmap")
# The installers whose differential updates read a blockmap beside them.
BLOCKMAPPED = (".dmg", ".zip", ".exe")


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


def draft(releases: list[dict], tag: str) -> dict:
    """The tag's one draft, which upload puts packages on. Raises ValueError
    when there is not exactly one: upload never makes a release."""
    what, _ = plan(releases, tag)
    if what == "create":
        raise ValueError(f"{tag} has no draft release: the release job makes it")
    return of_tag(releases, tag)[0]


def is_package(name: str) -> bool:
    return name.endswith(PACKAGE_SUFFIXES) or (name.startswith("latest") and name.endswith(".yml"))


def built(folder: Path) -> list[str]:
    """The packages at the top of a folder (a build's output, or the package
    jobs' artifacts), by name: what electron-builder publishes, without its
    logs, its effective config or the unpacked apps beneath."""
    return sorted(p.name for p in folder.iterdir() if p.is_file() and is_package(p.name))


def problems(releases: list[dict], tag: str, packages: list[str]) -> list[str]:
    """What check finds wrong: the tag's releases other than one draft, the
    packages that draft does not have, and installers on it without their
    blockmap."""
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
    out += [f"{name} has no {name}.blockmap on {tag}'s release {r['id']}"
            for name in sorted(have | set(packages)) if name.endswith(BLOCKMAPPED) and f"{name}.blockmap" not in have]
    return out


def gh(*args: str) -> str:
    return subprocess.run(["gh", *args], check=True, capture_output=True, text=True).stdout


def releases(repo: str) -> list[dict]:
    # One release per line, every page; drafts are listed to a token that may push.
    out = gh("api", "--paginate", f"repos/{repo}/releases?per_page=100", "--jq", ".[]")
    return [json.loads(line) for line in out.splitlines() if line.strip()]


def assets(repo: str, rid: int) -> dict[str, int]:
    out = gh("api", "--paginate", f"repos/{repo}/releases/{rid}/assets?per_page=100", "--jq", ".[]")
    return {a["name"]: a["id"] for a in (json.loads(line) for line in out.splitlines() if line.strip())}


def upload(repo: str, rid: int, path: Path, tries: int = 3) -> None:
    """Puts path on release rid under its own name, in place of an asset of
    that name (a failed run's, whole or cut short)."""
    for attempt in range(1, tries + 1):
        old = assets(repo, rid).get(path.name)
        if old is not None:
            gh("api", "-X", "DELETE", f"repos/{repo}/releases/assets/{old}")
        url = f"https://uploads.github.com/repos/{repo}/releases/{rid}/assets?name={urllib.parse.quote(path.name)}"
        try:
            gh("api", "-X", "POST", url, "-H", "Content-Type: application/octet-stream", "--input", str(path))
            return
        except subprocess.CalledProcessError as err:
            print(f"release_draft: {path.name}, try {attempt} of {tries}: {err.stderr.strip()}", file=sys.stderr)
            if attempt == tries:
                raise


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="The one draft release a tag's packages go into.")
    sub = ap.add_subparsers(dest="cmd", required=True)
    e = sub.add_parser("ensure", help="make the tag's draft release, or keep the one there")
    e.add_argument("tag")
    u = sub.add_parser("upload", help="put a build's packages on the tag's draft release")
    u.add_argument("tag")
    u.add_argument("packages", type=Path)
    c = sub.add_parser("check", help="the tag has one draft holding every package built")
    c.add_argument("tag")
    c.add_argument("packages", type=Path)
    a = ap.parse_args(argv)
    repo = os.environ.get("GITHUB_REPOSITORY", "phenixrizen/conductor")
    try:
        if a.cmd == "ensure":
            what, rid = plan(releases(repo), a.tag)
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
        if a.cmd == "upload":
            rid = draft(releases(repo), a.tag)["id"]
            for name in packages:
                upload(repo, rid, a.packages / name)
                print(f"{a.tag}: {name} on draft release {rid}")
            return 0
        found = problems(releases(repo), a.tag, packages)
    except ValueError as err:
        print(f"release_draft: {err}", file=sys.stderr)
        return 1
    for p in found:
        print(f"release_draft: {p}", file=sys.stderr)
    if not found:
        print(f"{a.tag}: one draft release with all {len(packages)} packages")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main())
