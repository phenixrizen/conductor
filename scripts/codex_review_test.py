"""Tests for codex_review.install_root: python3 -m unittest discover -s scripts -p '*_test.py'"""
from pathlib import Path
import unittest

from codex_review import NotInstalled, install_root

HERE = Path("/repo/conductor")


def entry(path: str, scope: str = "user", project: str | None = None) -> dict:
    e = {"installPath": path, "scope": scope, "version": path.rsplit("/", 1)[-1]}
    if project:
        e["projectPath"] = project
    return e


def registry(*entries: dict) -> dict:
    return {"version": 2, "plugins": {"codex@openai-codex": list(entries)}}


class InstallRoot(unittest.TestCase):
    def test_the_users_installation(self):
        self.assertEqual(install_root(registry(entry("/cache/codex/1.0.6")), HERE), Path("/cache/codex/1.0.6"))

    def test_another_projects_installation_listed_first_is_passed_over(self):
        reg = registry(entry("/cache/codex/0.9.0", "project", "/elsewhere"), entry("/cache/codex/1.0.6"))
        self.assertEqual(install_root(reg, HERE), Path("/cache/codex/1.0.6"))

    def test_this_projects_installation_wins_over_the_users(self):
        reg = registry(entry("/cache/codex/1.0.6"), entry("/cache/codex/1.0.7", "local", str(HERE)))
        self.assertEqual(install_root(reg, HERE), Path("/cache/codex/1.0.7"))

    def test_only_another_projects_installation_is_not_installed(self):
        with self.assertRaises(NotInstalled):
            install_root(registry(entry("/cache/codex/1.0.6", "project", "/elsewhere")), HERE)

    def test_two_for_the_same_scope_are_refused(self):
        with self.assertRaises(NotInstalled):
            install_root(registry(entry("/cache/codex/1.0.6"), entry("/cache/codex/1.0.7")), HERE)

    def test_nothing_installed(self):
        with self.assertRaises(NotInstalled):
            install_root({"version": 2, "plugins": {}}, HERE)


if __name__ == "__main__":
    unittest.main()
