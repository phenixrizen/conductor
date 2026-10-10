#!/usr/bin/env python3
"""Run a Codex adversarial review of this branch (AGENTS.md "Reviews").

The Codex plugin for Claude Code (openai/codex-plugin-cc, codex@openai-codex)
keeps its slash commands from agents, so this runs the same review through
the plugin's own script: the installation that applies here, read from
Claude Code's plugin registry (one installed for this repository, else the
user's), never the newest copy in the cache.

    python3 scripts/codex_review.py "<focus: the risks this change touches>"

It reviews the commits origin/main...HEAD (the plugin's --base), not the
working tree, and waits for the result. Extra plugin flags go before the
focus (--base <ref> replaces origin/main).
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys

PLUGIN = "codex@openai-codex"
REGISTRY = Path(os.path.expanduser("~/.claude/plugins/installed_plugins.json"))
SCRIPT = "scripts/codex-companion.mjs"


class NotInstalled(Exception):
    pass


def install_root(registry: dict, project: Path) -> Path:
    """The installPath of the plugin's installation that applies to project:
    the one installed for project itself, else the one installed for the user;
    two that apply equally are refused rather than guessed between."""
    entries = registry.get("plugins", {}).get(PLUGIN, [])
    here = [e for e in entries if e.get("projectPath") and Path(e["projectPath"]).resolve() == project.resolve()]
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
    project = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True, capture_output=True, text=True).stdout.strip())
    try:
        registry = json.loads(REGISTRY.read_text()) if REGISTRY.exists() else {}
        root = install_root(registry, project)
    except NotInstalled as e:
        sys.stderr.write(f"codex_review: {e}\n")
        return 1
    script = root / SCRIPT
    if not script.exists():
        sys.stderr.write(f"codex_review: {script} is missing; reinstall {PLUGIN}\n")
        return 1
    flags = [] if "--base" in argv else ["--base", "origin/main"]
    return subprocess.run(["node", str(script), "adversarial-review", "--wait", *flags, *argv], cwd=project).returncode


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
