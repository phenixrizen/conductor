# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 the Conductor Authors and RockSolid Labs, Inc.
"""The DCO check: the sign-off lines, the judgement, the report, and a branch read from a repository as git logs it."""

import contextlib
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import dco_check as d

ADA = ("Ada Example", "ada@example.org")
BOB = ("Bob Builder", "bob@example.org")
SHA = "0123456789abcdef0123456789abcdef01234567"
OTHER = "fedcba9876543210fedcba9876543210fedcba98"


def commit(message, author=ADA, parents=(SHA,), sha=OTHER):
    return d.Commit(sha, parents, author[0], author[1], message.split("\n", 1)[0], message)


def signed(message, who=ADA):
    return f"{message}\n\nSigned-off-by: {who[0]} <{who[1]}>"


def run_main(*args):
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        code = d.main(list(args))
    return code, out.getvalue(), err.getvalue()


class Signoffs(unittest.TestCase):
    def test_a_signoff_line_is_parsed_with_its_name_and_address(self):
        message = (
            "subject\n\nbody\n"
            "   signed-off-by: Ada Example <ada@example.org>\n"
            "more body\n"
            "SIGNED-OFF-BY:   Bob Builder   <Bob@Example.org>  \n"
        )
        self.assertEqual(d.signoffs(message), [ADA, ("Bob Builder", "Bob@Example.org")])
        self.assertEqual(d.signoffs("subject\n\nSigned-off-by: Ada Example <ada@example.org>"), [ADA])

    def test_a_line_without_an_address_is_no_signoff(self):
        for line in (
            "Signed-off-by: Ada Example",
            "Signed-off-by:",
            "Signed-off-by: ada@example.org",
            "Signed-off-by: Ada Example <ada@example.org",
            # Not the line's key either: a mention, or no colon.
            "see the Signed-off-by: Ada Example <ada@example.org> line",
            "Signed-off-by Ada Example <ada@example.org>",
        ):
            self.assertEqual(d.signoffs("subject\n\n" + line), [], line)


class Problems(unittest.TestCase):
    def test_a_commit_signed_off_by_its_author_passes(self):
        self.assertEqual(d.problems([commit(signed("one"))]), [])
        self.assertEqual(d.problems([commit(signed("one", BOB), author=BOB), commit(signed("two"))]), [])

    def test_a_commit_with_no_signoff_is_named_with_its_author_and_subject(self):
        [line] = d.problems([commit("two: the change\n\nits body")])
        self.assertEqual(line, f"{OTHER[:12]} two: the change by Ada Example <ada@example.org>: no Signed-off-by")

    def test_another_persons_signoff_does_not_count(self):
        message = (
            "one\n\n"
            "Co-authored-by: Bob Builder <bob@example.org>\n"
            "Signed-off-by: Bob Builder <bob@example.org>\n"
            "Reviewed-by: Cy Reviewer <cy@example.org>\n"
            "Signed-off-by: Cy Reviewer <cy@example.org>\n"
        )
        [line] = d.problems([commit(message)])
        self.assertTrue(line.startswith(f"{OTHER[:12]} one by Ada Example <ada@example.org>: "), line)
        self.assertTrue(
            line.endswith("Signed-off-by: Bob Builder <bob@example.org>, Cy Reviewer <cy@example.org> (not the author)"), line
        )

    def test_the_address_is_compared_without_regard_to_case_and_the_name_exactly(self):
        self.assertEqual(d.problems([commit("one\n\nSigned-off-by: Ada Example <ADA@Example.ORG>")]), [])
        padded = ("  Ada Example ", "ada@example.org")
        self.assertEqual(d.problems([commit("one\n\nSigned-off-by:   Ada Example   <ada@example.org>", author=padded)]), [])
        self.assertEqual(len(d.problems([commit("one\n\nSigned-off-by: ada example <ada@example.org>")])), 1)
        self.assertEqual(len(d.problems([commit("one\n\nSigned-off-by: Ada Example <ada@example.com>")])), 1)

    def test_a_merge_commit_is_judged_like_any_other(self):
        [line] = d.problems([commit("merge main into the branch", parents=(SHA, OTHER))])
        self.assertEqual(
            line, f"{OTHER[:12]} merge main into the branch (merge) by Ada Example <ada@example.org>: no Signed-off-by"
        )
        self.assertEqual(d.problems([commit(signed("merge main into the branch"), parents=(SHA, OTHER))]), [])


class Report(unittest.TestCase):
    def test_an_empty_range_passes(self):
        with mock.patch.object(d, "read", return_value=[]) as read:
            code, out, err = run_main("--base", "origin/main", "--head", "HEAD")
        self.assertEqual((code, out, err), (0, "no commits between origin/main and HEAD\n", ""))
        self.assertEqual(read.call_args.args, (".", "origin/main", "HEAD"))

    def test_the_report_counts_the_commits_and_names_the_fix(self):
        with mock.patch.object(d, "read", return_value=[commit(signed("one")), commit(signed("two"))]):
            self.assertEqual(run_main("--base", "origin/main", "--head", "HEAD")[:2], (0, "2 commits signed off by their authors\n"))
        with mock.patch.object(d, "read", return_value=[commit(signed("one"))]):
            self.assertEqual(run_main("--base", "origin/main", "--head", "HEAD")[:2], (0, "1 commit signed off by its author\n"))
        # A range without a merge: the rebase that signs a linear branch, with the base named.
        linear = [commit(signed("one")), commit("two"), commit("three")]
        with mock.patch.object(d, "read", return_value=linear):
            code, out, _ = run_main("--base", "origin/main", "--head", "HEAD")
        self.assertEqual(code, 1)
        lines = out.splitlines()
        self.assertEqual(len(lines), 3, out)
        self.assertIn(" two by ", lines[0])
        self.assertIn(" three by ", lines[1])
        self.assertEqual(
            lines[2],
            "2 of 3 commits are not signed off by their authors. Fix: git rebase --signoff origin/main && "
            "git push --force-with-lease, run by each commit's author (CONTRIBUTING.md, Sign-off).",
        )
        # A range with a merge, signed or not: a plain rebase would drop it with what it carried.
        for merge in (commit("merge", parents=(SHA, OTHER)), commit(signed("merge"), parents=(SHA, OTHER))):
            with mock.patch.object(d, "read", return_value=[commit(signed("one")), merge, commit("three")]):
                code, out, _ = run_main("--base", "origin/main", "--head", "HEAD")
            self.assertEqual(code, 1)
            self.assertEqual(
                out.splitlines()[-1].split(". ", 1)[1],
                "The branch has a merge commit: CONTRIBUTING.md, Sign-off, says how to sign it without losing what the merge carried.",
            )
        self.assertTrue(out.startswith(f"{OTHER[:12]} three by "), out)
        self.assertIn("1 of 3 commits is not signed off by its author. ", out)


# A developer's global configuration (a signing key, a commit template, a
# hooks path) and the GIT_* variables of a shell cannot make the repository
# tests fail or pass for their own reasons: the global and system files are
# hidden, and the identity, signing and the default branch are set per call.
def git_env():
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    return env


class Repo:
    """A repository in a directory, with git run as a named person."""

    def __init__(self, directory):
        self.dir = directory
        self.env = git_env()

    def git(self, *args, who=ADA, text=True):
        argv = [
            "git", "-c", f"user.name={who[0]}", "-c", f"user.email={who[1]}",
            "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-C", self.dir, *args,
        ]
        p = subprocess.run(argv, capture_output=True, env=self.env)
        if p.returncode:
            raise AssertionError(f"git {' '.join(args)} failed ({p.returncode}): {p.stderr.decode('utf-8', 'replace')}")
        return p.stdout.decode("utf-8").strip() if text else p.stdout

    def write(self, name, content):
        Path(self.dir, name).write_text(content, encoding="utf-8")
        self.git("add", name)

    def commit(self, name, message, who=ADA, signoff=False):
        self.write(name, message + "\n")
        self.git("commit", "-q", *(["-s"] if signoff else []), "-m", message, who=who)
        return self.head()

    def head(self):
        return self.git("rev-parse", "HEAD")

    def tree(self):
        return self.git("rev-parse", "HEAD^{tree}")


class Repository(unittest.TestCase):
    def repo(self):
        """A repository with a base commit, and the base's sha."""
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        r = Repo(tmp.name)
        r.git("init", "-q")
        return r, r.commit("base.txt", "base", signoff=True)

    def check(self, r, base):
        return run_main("--repo", r.dir, "--base", base, "--head", "HEAD")

    def test_a_range_is_read_from_a_repository_as_git_logs_it(self):
        r, base = self.repo()
        one = r.commit("one.txt", "one, signed", signoff=True)
        two = r.commit("two.txt", "two, unsigned")
        r.git("checkout", "-q", "-b", "side", base)
        three = r.commit("three.txt", "three, signed on the side", signoff=True)
        r.git("checkout", "-q", "main")
        r.git("merge", "-q", "--no-ff", "-m", "merge side", "side")
        merge = r.head()
        commits = d.read(r.dir, base, "HEAD")
        self.assertEqual({c.sha for c in commits}, {one, two, three, merge})
        self.assertEqual(commits[-1].sha, merge)
        self.assertEqual(len(commits[-1].parents), 2)
        self.assertEqual(d.signoffs(commits[0].message), [ADA])

        code, out, err = self.check(r, base)
        self.assertEqual(code, 1, out + err)
        lines = out.splitlines()
        self.assertEqual(len(lines), 3, out)
        self.assertEqual(lines[0], f"{two[:12]} two, unsigned by Ada Example <ada@example.org>: no Signed-off-by")
        self.assertEqual(lines[1], f"{merge[:12]} merge side (merge) by Ada Example <ada@example.org>: no Signed-off-by")
        self.assertTrue(lines[2].startswith("2 of 4 commits are not signed off by their authors. The branch has a merge commit"), lines[2])
        # The fix the script prints for a linear branch, run here: the rebase
        # replays the three non-merge commits, signs the one that lacked it and
        # drops the merge, which carried nothing (the tree is the same).
        tree = r.tree()
        r.git("rebase", "--signoff", base)
        code, out, err = self.check(r, base)
        self.assertEqual((code, out), (0, "3 commits signed off by their authors\n"), err)
        self.assertEqual(r.tree(), tree)

    def test_a_merge_that_carried_its_own_edit_is_repaired_without_losing_it(self):
        r, base = self.repo()
        r.commit("one.txt", "one, signed", signoff=True)
        r.commit("two.txt", "two, signed", signoff=True)
        r.git("checkout", "-q", "-b", "side", base)
        r.commit("three.txt", "three, signed on the side", signoff=True)
        r.git("checkout", "-q", "main")
        r.git("merge", "-q", "--no-ff", "--no-commit", "side")
        r.write("four.txt", "added in the merge itself\n")
        r.git("commit", "-q", "-m", "merge side, with an edit of its own")
        merge = r.head()
        code, out, _ = self.check(r, base)
        self.assertEqual(code, 1)
        lines = out.splitlines()
        self.assertEqual(len(lines), 2, out)
        self.assertTrue(lines[0].startswith(f"{merge[:12]} merge side, with an edit of its own (merge) by "), lines[0])
        self.assertTrue(lines[1].startswith("1 of 4 commits is not signed off by its author. The branch has a merge commit"), lines[1])
        # The plain rebase passes the check and loses the file: what a count of
        # commits and sign-offs cannot see.
        before, tree = r.head(), r.tree()
        r.git("rebase", "--signoff", base)
        code, out, _ = self.check(r, base)
        self.assertEqual((code, out), (0, "3 commits signed off by their authors\n"))
        self.assertNotEqual(r.tree(), tree)
        self.assertIn("four.txt", r.git("diff", "--stat", before, "HEAD"))
        # The recipe of CONTRIBUTING.md, Sign-off: the tip's tree put back as a
        # signed commit of its own.
        r.git("restore", "--source=" + before, "--staged", "--worktree", ":/")
        r.git("commit", "-q", "-s", "-m", "put back what the merge carried")
        self.assertEqual(r.tree(), tree)
        self.assertEqual(r.git("diff", "--stat", before, "HEAD"), "")
        code, out, _ = self.check(r, base)
        self.assertEqual((code, out), (0, "4 commits signed off by their authors\n"))

    def test_the_fix_signs_as_the_person_running_it_so_each_author_signs_their_own(self):
        r, base = self.repo()
        r.commit("alice.txt", "alice, signed", who=ADA, signoff=True)
        bob = r.commit("bob.txt", "bob, unsigned", who=BOB)
        code, out, _ = self.check(r, base)
        self.assertEqual(code, 1)
        lines = out.splitlines()
        self.assertEqual(lines[0], f"{bob[:12]} bob, unsigned by Bob Builder <bob@example.org>: no Signed-off-by")
        self.assertEqual(
            lines[1],
            f"1 of 2 commits is not signed off by its author. Fix: git rebase --signoff {base} && "
            "git push --force-with-lease, run by each commit's author (CONTRIBUTING.md, Sign-off).",
        )
        # Run by Alice, the rebase signs Bob's commit as Alice: git signs as the
        # committer and keeps the author, so the check still fails.
        r.git("rebase", "--signoff", base, who=ADA)
        code, out, _ = self.check(r, base)
        self.assertEqual(code, 1)
        self.assertTrue(
            out.splitlines()[0].endswith(
                "bob, unsigned by Bob Builder <bob@example.org>: Signed-off-by: Ada Example <ada@example.org> (not the author)"
            ),
            out,
        )
        # Run by Bob, it signs his own; Alice's commit keeps her line.
        r.git("rebase", "--signoff", base, who=BOB)
        code, out, _ = self.check(r, base)
        self.assertEqual((code, out), (0, "2 commits signed off by their authors\n"))
        by_subject = {c.subject: c for c in d.read(r.dir, base, "HEAD")}
        self.assertEqual(d.signoffs(by_subject["bob, unsigned"].message), [ADA, BOB])
        self.assertIn(ADA, d.signoffs(by_subject["alice, signed"].message))

    def test_a_commit_with_non_ascii_text_is_read_as_utf8_whatever_git_is_told_to_write(self):
        r, base = self.repo()
        lovelace = ("Ada Lövelace", "ada@example.org")
        subject = "the notes – a first draft"
        r.commit("notes.txt", subject, who=lovelace, signoff=True)
        # The repository's own configuration, which the hidden global one does
        # not cover: the mechanism, proved here, so the test cannot pass for
        # nothing on a git that ignored the setting.
        r.git("config", "i18n.logOutputEncoding", "cp1252")
        raw = r.git("log", "-1", "--format=%an%x1f%s%x1f%B", text=False)
        self.assertIn(b"Ada L\xf6velace", raw)
        with self.assertRaises(UnicodeDecodeError):
            raw.decode("utf-8")
        code, out, err = self.check(r, base)
        self.assertEqual((code, out), (0, "1 commit signed off by its author\n"), err)
        [c] = d.read(r.dir, base, "HEAD")
        self.assertEqual((c.author_name, c.subject), (lovelace[0], subject))
        self.assertEqual(d.signoffs(c.message), [lovelace])

    def test_a_record_that_does_not_parse_fails_the_read(self):
        r, base = self.repo()
        message = Path(r.dir, "message.txt")
        for name, byte in (("record", b"\x1e"), ("unit", b"\x1f")):
            start = r.head()
            message.write_bytes(b"a separator in the body\n\nbody" + byte + b"split\n")
            r.write(name + ".txt", name + "\n")
            r.git("commit", "-q", "--cleanup=verbatim", "-F", str(message))
            with self.assertRaises(d.Error):
                d.read(r.dir, start, "HEAD")
            code, out, err = self.check(r, start)
            self.assertEqual((code, out), (2, ""), err)
            self.assertIn("did not parse", err)
        # git failing is the same exit, with git's message.
        code, out, err = run_main("--repo", r.dir, "--base", "nope", "--head", "HEAD")
        self.assertEqual((code, out), (2, ""), err)
        self.assertIn("unknown revision", err)


if __name__ == "__main__":
    unittest.main()
