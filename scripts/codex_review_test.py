"""Tests for codex_review: python3 -m unittest discover -s scripts -p '*_test.py'"""
import contextlib
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import codex_review
from codex_review import BaseUnknown, NotInstalled, base_of, behind, install_root, main, refused_flag, registry_path

HERE = Path("/repo/conductor")


def same(p: Path) -> Path:
    return p


def entry(path: str, scope: str = "user", project: str | None = None) -> dict:
    e = {"installPath": path, "scope": scope, "version": path.rsplit("/", 1)[-1]}
    if project:
        e["projectPath"] = project
    return e


def registry(*entries: dict) -> dict:
    return {"version": 2, "plugins": {"codex@openai-codex": list(entries)}}


class InstallRoot(unittest.TestCase):
    def test_the_users_installation(self):
        self.assertEqual(install_root(registry(entry("/cache/codex/1.0.6")), HERE, same), Path("/cache/codex/1.0.6"))

    def test_another_projects_installation_listed_first_is_passed_over(self):
        reg = registry(entry("/cache/codex/0.9.0", "project", "/elsewhere"), entry("/cache/codex/1.0.6"))
        self.assertEqual(install_root(reg, HERE, same), Path("/cache/codex/1.0.6"))

    def test_this_projects_installation_wins_over_the_users(self):
        reg = registry(entry("/cache/codex/1.0.6"), entry("/cache/codex/1.0.7", "local", str(HERE)))
        self.assertEqual(install_root(reg, HERE, same), Path("/cache/codex/1.0.7"))

    def test_only_another_projects_installation_is_not_installed(self):
        with self.assertRaises(NotInstalled):
            install_root(registry(entry("/cache/codex/1.0.6", "project", "/elsewhere")), HERE, same)

    def test_two_for_the_same_scope_are_refused(self):
        with self.assertRaises(NotInstalled):
            install_root(registry(entry("/cache/codex/1.0.6"), entry("/cache/codex/1.0.7")), HERE, same)

    def test_nothing_installed(self):
        with self.assertRaises(NotInstalled):
            install_root({"version": 2, "plugins": {}}, HERE, same)

    def test_an_inherited_git_dir_does_not_make_another_project_match(self):
        with tempfile.TemporaryDirectory() as tmp:
            mine, other = Path(tmp, "mine"), Path(tmp, "other")
            for repo in (mine, other):
                repo.mkdir()
                subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
            reg = registry(entry("/cache/codex/0.9.0", "project", str(other)), entry("/cache/codex/1.0.6"))
            with mock.patch.dict(os.environ, {"GIT_DIR": str(other / ".git")}):
                self.assertEqual(install_root(reg, mine), Path("/cache/codex/1.0.6"))

    def test_a_linked_worktree_finds_its_main_checkouts_installation(self):
        with tempfile.TemporaryDirectory() as tmp:
            main_checkout, worktree = Path(tmp, "main"), Path(tmp, "wt")
            git = lambda *a: subprocess.run(["git", *a], cwd=main_checkout, check=True, capture_output=True)
            main_checkout.mkdir()
            git("init", "-q")
            git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x")
            git("worktree", "add", "-q", str(worktree))
            reg = registry(entry("/cache/codex/1.0.6"), entry("/cache/codex/1.0.7", "project", str(main_checkout)))
            self.assertEqual(install_root(reg, worktree), Path("/cache/codex/1.0.7"))
            self.assertEqual(install_root(reg, worktree / "."), Path("/cache/codex/1.0.7"))


class Registry(unittest.TestCase):
    def test_under_claude_config_dir_when_set(self):
        self.assertEqual(registry_path({"CLAUDE_CONFIG_DIR": "/profiles/work"}), Path("/profiles/work/plugins/installed_plugins.json"))

    def test_under_home_otherwise(self):
        self.assertEqual(registry_path({}), Path(os.path.expanduser("~/.claude/plugins/installed_plugins.json")))


class Flags(unittest.TestCase):
    def test_base_and_scope_pass(self):
        for argv in (["focus"], ["--base", "main", "focus"], ["--base=main", "--scope", "branch", "focus"], ["--scope=auto", "a", "b"]):
            self.assertEqual(refused_flag(argv), "", argv)

    def test_every_other_flag_is_refused_the_directory_options_first(self):
        for argv, bad in ((["--cwd", "/other", "focus"], "--cwd"), (["--cwd=/other"], "--cwd=/other"), (["-C", "../other"], "-C"),
                          (["--C", "/other"], "--C"), (["--C=/other"], "--C=/other"), (["-cwd", "/other"], "-cwd"),
                          (["--base", "main", "--background", "focus"], "--background")):
            self.assertEqual(refused_flag(argv), bad, argv)

    def test_main_refuses_before_reading_anything(self):
        for argv in (["--cwd", "/other", "focus"], ["-cwd", "/other", "focus"], ["--C=/other", "focus"]):
            with contextlib.redirect_stderr(io.StringIO()) as err:
                self.assertEqual(main(argv), 2, argv)
            self.assertIn("refused", err.getvalue())


class FakeGit:
    """Answers git as a repository would, keeping every argv: remotes, a
    fetch that works or not, and how far HEAD is behind each base."""

    def __init__(self, behind_by: dict[str, int], remotes=("origin",), fetch_ok=True):
        self.calls: list[list[str]] = []
        self.behind_by, self.remotes, self.fetch_ok = behind_by, remotes, fetch_ok

    def __call__(self, argv, capture_output=True, text=True, env=None):
        self.calls.append(argv)
        if argv[:2] == ["git", "remote"]:
            return subprocess.CompletedProcess(argv, 0, "\n".join(self.remotes) + "\n", "")
        if argv[:2] == ["git", "fetch"]:
            return subprocess.CompletedProcess(argv, 0 if self.fetch_ok else 1, "", "" if self.fetch_ok else "network down")
        base = argv[-1].split("..", 1)[1]
        if base not in self.behind_by:
            return subprocess.CompletedProcess(argv, 128, "", f"fatal: bad revision '{argv[-1]}'")
        return subprocess.CompletedProcess(argv, 0, f"{self.behind_by[base]}\n", "")


class Behind(unittest.TestCase):
    def test_the_base_is_base_s_value_else_origin_main(self):
        self.assertEqual(base_of(["focus"]), "origin/main")
        self.assertEqual(base_of(["--base", "origin/parent", "focus"]), "origin/parent")
        self.assertEqual(base_of(["--base=origin/parent", "focus"]), "origin/parent")

    def test_the_base_s_remote_is_fetched_before_counting(self):
        git = FakeGit({"origin/main": 3})
        self.assertEqual(behind("origin/main", git), (3, ""))
        self.assertIn(["git", "fetch", "-q", "origin"], git.calls)
        self.assertLess(git.calls.index(["git", "fetch", "-q", "origin"]), git.calls.index(["git", "rev-list", "--count", "HEAD..origin/main"]))

    def test_a_local_base_fetches_nothing(self):
        git = FakeGit({"parent-branch": 0})
        self.assertEqual(behind("parent-branch", git), (0, ""))
        self.assertFalse([c for c in git.calls if c[:2] == ["git", "fetch"]])

    def test_a_failed_fetch_compares_with_what_was_fetched_and_says_so(self):
        count, note = behind("origin/main", FakeGit({"origin/main": 0}, fetch_ok=False))
        self.assertEqual(count, 0)
        self.assertIn("could not fetch origin", note)

    def test_an_unknown_base_is_refused(self):
        with self.assertRaises(BaseUnknown):
            behind("origin/nope", FakeGit({}))


class MainChecksTheBase(unittest.TestCase):
    def test_a_branch_behind_its_base_is_refused_before_the_plugin_is_looked_for(self):
        with mock.patch.object(codex_review, "behind", return_value=(2, "")), \
                mock.patch.object(codex_review, "registry_path", side_effect=AssertionError("looked for the plugin")), \
                contextlib.redirect_stderr(io.StringIO()) as err:
            self.assertEqual(main(["focus"]), 2)
        self.assertIn("2 commits behind origin/main", err.getvalue())
        self.assertIn("rebase onto it first", err.getvalue())

    def test_a_stacked_branch_is_compared_with_the_base_it_names(self):
        seen = []
        with mock.patch.object(codex_review, "behind", side_effect=lambda base, env=None: seen.append(base) or (0, "")), \
                mock.patch.object(codex_review, "install_root", side_effect=NotInstalled("not installed")), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(main(["--base", "origin/parent", "focus"]), 1)
        self.assertEqual(seen, ["origin/parent"])

    def test_a_branch_on_its_base_goes_on_to_the_review(self):
        with mock.patch.object(codex_review, "behind", return_value=(0, "")), \
                mock.patch.object(codex_review, "install_root", side_effect=NotInstalled("not installed")), \
                contextlib.redirect_stderr(io.StringIO()) as err:
            self.assertEqual(main(["focus"]), 1)
        self.assertIn("not installed", err.getvalue())

    def test_an_unknown_base_is_refused(self):
        with mock.patch.object(codex_review, "behind", side_effect=BaseUnknown("cannot compare this branch with origin/nope")), \
                contextlib.redirect_stderr(io.StringIO()) as err:
            self.assertEqual(main(["--base", "origin/nope", "focus"]), 2)
        self.assertIn("cannot compare", err.getvalue())


if __name__ == "__main__":
    unittest.main()
