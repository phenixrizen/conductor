#!/usr/bin/env python3
"""Run a Codex adversarial review of this branch (AGENTS.md "Reviews").

The Codex plugin for Claude Code (openai/codex-plugin-cc, codex@openai-codex)
keeps its slash commands from agents, so this runs the same review through
the plugin's own script: the installation that applies here, read from
Claude Code's plugin registry (one installed for this repository, else the
user's), never the newest copy in the cache.

    python3 scripts/codex_review.py "<focus: the risks this change touches>"

It reviews the commits origin/main...HEAD (the plugin's --base), not the
working tree, and waits for the result, in the directory it is run from.
Extra plugin flags go before the focus (--base <ref> replaces origin/main);
--cwd and -C are refused, since the installation is chosen for this
repository. The registry is under CLAUDE_CONFIG_DIR when that is set, as
Claude Code's own is.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys

PLUGIN = "codex@openai-codex"
SCRIPT = "scripts/codex-companion.mjs"
DIRECTORY_FLAGS = ("--cwd", "-C")


class NotInstalled(Exception):
    pass


def registry_path(env: dict[str, str]) -> Path:
    """Claude Code's plugin registry: under CLAUDE_CONFIG_DIR, else ~/.claude."""
    return Path(os.path.expanduser(env.get("CLAUDE_CONFIG_DIR") or "~/.claude")) / "plugins/installed_plugins.json"


def repository(path: Path) -> Path:
    """The repository path belongs to, the same for its main checkout and every
    linked worktree (git's common directory); path itself outside git."""
    try:
        out = subprocess.run(["git", "-C", str(path), "rev-parse", "--path-format=absolute", "--git-common-dir"],
                             check=True, capture_output=True, text=True).stdout.strip()
        return Path(out).resolve()
    except (OSError, subprocess.CalledProcessError):
        return path.resolve()


def install_root(registry: dict, project: Path, repo=repository) -> Path:
    """The installPath of the plugin's installation that applies to project:
    the one installed for project's repository (from any of its worktrees),
    else the one installed for the user; two that apply equally are refused
    rather than guessed between."""
    entries = registry.get("plugins", {}).get(PLUGIN, [])
    mine = repo(project)
    here = [e for e in entries if e.get("projectPath") and repo(Path(e["projectPath"])) == mine]
    user = [e for e in entries if e.get("scope") == "user" and not e.get("projectPath")]
    for found in (here, user):
        if len(found) > 1:
            raise NotInstalled(f"{PLUGIN} is installed {len(found)} times for the same scope; remove the extra installation")
        if found:
            return Path(found[0]["installPath"])
    raise NotInstalled(f"{PLUGIN} is not installed for this repository or the user: /plugin marketplace add openai/codex-plugin-cc, /plugin install {PLUGIN}, /codex:setup")


def main(argv: list[str]) -> int:
    if not argv or argv[0] in ("-h", "--help"):
        print(__doc__.strip() if __doc__ else "")
        return 0 if argv else 2
    if any(a in DIRECTORY_FLAGS or a.startswith("--cwd=") for a in argv):
        sys.stderr.write("codex_review: run it from the repository to review; --cwd and -C are refused\n")
        return 2
    try:
        reg = registry_path(dict(os.environ))
        registry = json.loads(reg.read_text()) if reg.exists() else {}
        root = install_root(registry, Path.cwd())
    except NotInstalled as e:
        sys.stderr.write(f"codex_review: {e}\n")
        return 1
    script = root / SCRIPT
    if not script.exists():
        sys.stderr.write(f"codex_review: {script} is missing; reinstall {PLUGIN}\n")
        return 1
    flags = [] if "--base" in argv else ["--base", "origin/main"]
    return subprocess.run(["node", str(script), "adversarial-review", "--wait", *flags, *argv]).returncode


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
