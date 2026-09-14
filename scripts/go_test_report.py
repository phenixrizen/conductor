#!/usr/bin/env python3
"""Stream Go's readable test output and summarize completed top-level timings."""
import html
import json
import os
from pathlib import Path
import sys


def cell(value):
    return "<code>" + html.escape(value).replace("|", "&#124;").replace("\n", " ") + "</code>"


def report(source, output, mode):
    tests = []
    packages = []
    cached = set()
    for line in source:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            # Compiler/tool failures may also emit plain text. Never hide them.
            output.write(line)
            output.flush()
            continue
        if not isinstance(event, dict):
            output.write(line)
            output.flush()
            continue
        action = event.get("Action")
        package = event.get("Package", "")
        test = event.get("Test", "")
        if "Output" in event:
            # Newer Go versions also emit build-output events for compiler errors.
            text = event.get("Output", "")
            output.write(text)
            output.flush()
            if not test and text.startswith("ok ") and text.rstrip().endswith("(cached)"):
                cached.add(package)
        if action in {"pass", "fail", "skip"}:
            item = (package, test, action, event.get("Elapsed", 0))
            if not test:
                packages.append(item)
            elif "/" not in test:
                # Parent elapsed time already includes its subtests.
                tests.append(item)

    lines = [f"## Go tests: {mode}", ""]
    counts = {action: sum(item[2] == action for item in tests) for action in ("pass", "fail", "skip")}
    if tests:
        lines.append(f"Top-level results: {counts['pass']} passed, {counts['fail']} failed, {counts['skip']} skipped.")
    else:
        lines.append("No completed top-level test results were observed; inspect the command status and log.")
    if cached:
        lines.append(f"{len(cached)} package result(s) came from Go's test cache; replayed timings are excluded below.")
    lines += ["", "### Slowest top-level tests", "", "Parent durations include subtests. Package tests may overlap; these times are not summed.", "",
              "| Package | Test | Seconds | Result |", "|---|---|---:|---|"]
    timed = [item for item in tests if item[0] not in cached and item[2] != "skip"]
    for package, test, action, elapsed in sorted(timed, key=lambda item: item[3], reverse=True)[:20]:
        lines.append(f"| {cell(package)} | {cell(test)} | {elapsed:.3f} | {action} |")
    lines += ["", "### Slowest packages", "", "| Package | Seconds | Result |", "|---|---:|---|"]
    for package, _, action, elapsed in sorted(packages, key=lambda item: item[3], reverse=True)[:20]:
        suffix = " (cached)" if package in cached else ""
        lines.append(f"| {cell(package)} | {elapsed:.3f} | {action}{suffix} |")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    summary = report(sys.stdin, sys.stdout, sys.argv[1])
    destination = os.environ.get("GITHUB_STEP_SUMMARY")
    if destination:
        with Path(destination).open("a", encoding="utf-8") as stream:
            stream.write(summary)
    else:
        print(summary)
