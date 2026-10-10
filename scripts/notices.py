#!/usr/bin/env python3
"""Generate/check THIRD_PARTY_NOTICES: the licences of what Conductor ships.

Three parts, each from what its build carries rather than what builds it:
the Go modules compiled into the conductor binary (`go list -deps` for the
platforms released), the npm packages in the web bundle the binary embeds
(web/.nuxt/bundled-packages.json, written by web/build/bundledPackages.ts
during `make web-build`) and the desktop app's own: Electron and the
production packages electron-builder puts in the app. Texts a package does
not carry (the Inter font @nuxt/fonts fetches, the Lucide icons Nuxt Icon
inlines) are kept in scripts/notices/.

    python3 scripts/notices.py           # write THIRD_PARTY_NOTICES
    python3 scripts/notices.py --check   # exit 1 when it is not current

Needs Go, a web build and `npm ci` in desktop/; only Python's standard
library otherwise.
"""
from __future__ import annotations

import argparse
import difflib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "THIRD_PARTY_NOTICES"
EXTRA = ROOT / "scripts/notices"
WEB = ROOT / "web"
DESKTOP = ROOT / "desktop"
BUNDLED = WEB / ".nuxt/bundled-packages.json"
# The platforms the conductor binary is released for (the desktop packages and the image): their module sets are united.
PLATFORMS = [("linux", "amd64"), ("linux", "arm64"), ("darwin", "amd64"), ("darwin", "arm64")]
LICENCE_FILE = re.compile(r"^(licen[cs]e|copying|notice|thirdpartynotices|unlicense)([.-][a-z0-9.-]*)?$", re.I)
NOT_TEXT = re.compile(r"\.(js|mjs|cjs|ts|json|html?)$", re.I)
WIDTH = 78
# Packages that ship without their licence text: the upstream text, kept in scripts/notices.
TEXT_KEPT = {
    "embla-carousel": "embla-carousel.LICENSE",
    "embla-carousel-auto-height": "embla-carousel.LICENSE",
    "embla-carousel-auto-scroll": "embla-carousel.LICENSE",
    "embla-carousel-autoplay": "embla-carousel.LICENSE",
    "embla-carousel-class-names": "embla-carousel.LICENSE",
    "embla-carousel-fade": "embla-carousel.LICENSE",
    "embla-carousel-reactive-utils": "embla-carousel.LICENSE",
    "embla-carousel-vue": "embla-carousel.LICENSE",
    "embla-carousel-wheel-gestures": "embla-carousel-wheel-gestures.LICENSE",
    "vaul-vue": "vaul-vue.LICENSE",
    "lazy-val": "lazy-val.LICENSE",
    "@iconify-json/lucide": "lucide.LICENSE",
}


class Entry:
    def __init__(self, name: str, version: str, licence: str, texts: list[tuple[str, str]], note: str = ""):
        self.name, self.version, self.licence, self.texts, self.note = name, version, licence, texts, note

    @property
    def title(self) -> str:
        return f"{self.name} {self.version}".strip()


def run(argv: list[str], cwd: Path = ROOT, env: dict[str, str] | None = None) -> str:
    return subprocess.run(argv, cwd=cwd, env=env, check=True, capture_output=True, text=True).stdout


def licence_files(directory: Path) -> list[tuple[str, str]]:
    """The licence and notice files at a package's root, as (file name, text), sorted by name."""
    found = []
    for p in sorted(directory.iterdir(), key=lambda p: p.name.lower()):
        if p.is_file() and LICENCE_FILE.match(p.name) and not NOT_TEXT.search(p.name):
            found.append((p.name, p.read_text(encoding="utf-8", errors="replace")))
    return found


def classify(text: str) -> str:
    """The SPDX id a licence text reads as, or '' when it is none this script knows."""
    t = " ".join(text.split())
    if "Apache License" in t and "Version 2.0" in t:
        return "Apache-2.0"
    if "Mozilla Public License" in t and ("Version 2.0" in t or "version 2.0" in t):
        return "MPL-2.0"
    if "SIL OPEN FONT LICENSE" in t.upper():
        return "OFL-1.1"
    if "Permission is hereby granted, free of charge" in t:
        return "MIT"
    if "Permission to use, copy, modify, and/or distribute" in t or "Permission to use, copy, modify, and distribute" in t:
        return "ISC"
    if "Redistribution and use in source and binary forms" in t:
        return "BSD-3-Clause" if ("Neither the name" in t or "names of its contributors" in t) else "BSD-2-Clause"
    if "free and unencumbered software released into the public domain" in t:
        return "Unlicense"
    return ""


def go_entries() -> list[Entry]:
    mods: dict[tuple[str, str], Path] = {}
    fmt = "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{with .Replace}}{{.Dir}}{{else}}{{.Dir}}{{end}}{{end}}{{end}}"
    for goos, goarch in PLATFORMS:
        env = {**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0", "GOFLAGS": "-mod=readonly"}
        for line in run(["go", "list", "-deps", "-f", fmt, "./cmd/conductor"], env=env).splitlines():
            if line.strip():
                path, version, directory = line.split("\t")
                mods[(path, version)] = Path(directory)
    entries = []
    for (path, version), directory in sorted(mods.items()):
        texts = licence_files(directory)
        kinds = sorted({classify(t) for n, t in texts if not n.lower().startswith("notice")} - {""})
        if not kinds:
            raise SystemExit(f"notices: no licence this script knows for the Go module {path} {version} in {directory}")
        # More than one licence text: each applies to the files it names (a project's COPYING says which); MPL-2.0 files' source is named.
        note = f"the source of its MPL-2.0 files: https://{path} at {version}" if "MPL-2.0" in kinds else ""
        entries.append(Entry(path, version, ", ".join(kinds), texts, note))
    toolchain = re.search(r"^toolchain (go\S+)", (ROOT / "go.mod").read_text(), re.M)
    goroot = Path(run(["go", "env", "GOROOT"]).strip())
    entries.insert(0, Entry("Go (the standard library and runtime)", toolchain.group(1) if toolchain else "", "BSD-3-Clause",
                            [("LICENSE", (goroot / "LICENSE").read_text())]))
    return entries


def npm_entry(directory: Path, note: str = "") -> Entry:
    pkg = json.loads((directory / "package.json").read_text())
    licence = pkg.get("license") or ""
    if isinstance(licence, dict):
        licence = licence.get("type", "")
    texts = licence_files(directory)
    if not texts and pkg["name"] in TEXT_KEPT:
        texts = [("LICENSE", (EXTRA / TEXT_KEPT[pkg["name"]]).read_text())]
    if not texts:
        raise SystemExit(f"notices: the npm package {pkg['name']} {pkg.get('version', '')} carries no licence file; keep one in scripts/notices")
    return Entry(pkg["name"], pkg.get("version", ""), licence or classify(texts[0][1]), texts, note)


def unique(entries: list[Entry]) -> list[Entry]:
    seen: dict[str, Entry] = {}
    for e in entries:
        seen.setdefault(e.title, e)
    return sorted(seen.values(), key=lambda e: (e.name.lower(), e.version))


def web_entries() -> list[Entry]:
    if not BUNDLED.exists():
        raise SystemExit(f"notices: no {BUNDLED.relative_to(ROOT)}: run make web-build first")
    dirs = json.loads(BUNDLED.read_text())
    entries = [npm_entry(WEB / d) for d in dirs]
    entries.append(npm_entry(WEB / "node_modules/@iconify-json/lucide", note="the icons, inlined by Nuxt Icon"))
    entries = unique(entries)
    entries.append(Entry("Inter (font)", "", "OFL-1.1", [("LICENSE", (EXTRA / "inter.LICENSE").read_text())], note="fetched from Google Fonts by @nuxt/fonts at build time"))
    return entries


def desktop_entries() -> list[Entry]:
    if not (DESKTOP / "node_modules/electron").exists():
        raise SystemExit("notices: no desktop/node_modules: run npm ci in desktop/ first")
    lines = run(["npm", "ls", "--omit=dev", "--all", "--parseable"], cwd=DESKTOP).splitlines()
    entries = [npm_entry(Path(line)) for line in lines if line.strip() and Path(line) != DESKTOP]
    entries.append(npm_entry(DESKTOP / "node_modules/electron", note="Chromium's and Node.js's own notices ship beside the app as LICENSES.chromium.html"))
    return unique(entries)


def render(parts: list[tuple[str, list[Entry]]]) -> str:
    out = [
        "THIRD-PARTY NOTICES",
        "",
        "Conductor is licensed under the Apache License, Version 2.0 (LICENSE, with",
        "NOTICE). It includes the software below, each under its own licence, whose",
        "texts follow the lists. Written by scripts/notices.py from what each build",
        "ships; do not edit it by hand.",
        "",
    ]
    for heading, entries in parts:
        out += [heading, "-" * len(heading)]
        for e in entries:
            line = f"  {e.title:<56} {e.licence}"
            out.append(line.rstrip())
            if e.note:
                out.append(f"    ({e.note})")
        out.append("")
    printed: dict[str, str] = {}
    for _, entries in parts:
        for e in entries:
            out += ["=" * WIDTH, e.title, "=" * WIDTH]
            for name, text in e.texts:
                body = text.replace("\r\n", "\n").strip("\n")
                key = " ".join(body.split())
                out += ["", f"--- {name}", ""]
                if key in printed:
                    out.append(f"The same text as {printed[key]}'s above.")
                else:
                    printed[key] = e.title
                    out += [l.rstrip() for l in body.split("\n")]
            out.append("")
    return "\n".join(out).rstrip("\n") + "\n"


def main() -> int:
    ap = argparse.ArgumentParser(description=(__doc__ or "").split("\n\n")[0])
    ap.add_argument("--check", action="store_true", help="exit 1 when THIRD_PARTY_NOTICES is not current")
    args = ap.parse_args()
    text = render([
        ("The conductor binary: Go modules", go_entries()),
        ("The workbench, embedded in the binary: npm packages in the web bundle", web_entries()),
        ("The desktop app: Electron and its packages", desktop_entries()),
    ])
    if args.check:
        have = OUT.read_text() if OUT.exists() else ""
        if have == text:
            return 0
        diff = difflib.unified_diff(have.splitlines(), text.splitlines(), "THIRD_PARTY_NOTICES", "generated", n=1, lineterm="")
        sys.stderr.write("\n".join(list(diff)[:60]) + "\nTHIRD_PARTY_NOTICES is not current: run make notices and commit it\n")
        return 1
    OUT.write_text(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
