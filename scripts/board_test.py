"""Tests for board: python3 -m unittest discover -s scripts -p '*_test.py'"""
import contextlib
import io
import json
import subprocess
import unittest
from unittest import mock

import board

OPTIONS = [{"id": "o1", "name": "Backlog"}, {"id": "o2", "name": "Ready"}, {"id": "o3", "name": "In progress"},
           {"id": "o4", "name": "In review"}, {"id": "o5", "name": "Done"}]


class FakeGh:
    """Answers gh as GitHub would, and keeps every argv it was given."""

    def __init__(self, fail_on: str | None = None):
        self.calls: list[list[str]] = []
        self.fail_on = fail_on

    def __call__(self, argv, capture_output=True, text=True):
        self.calls.append(argv)
        joined = " ".join(argv)
        if self.fail_on and self.fail_on in joined:
            return subprocess.CompletedProcess(argv, 1, "", "HTTP 403: missing scope project")
        if "projectV2(number" in joined:
            out = {"data": {"user": {"projectV2": {"id": "P1", "field": {"id": "F1", "options": OPTIONS}}}}}
        elif argv[:2] == ["gh", "api"] and argv[2].startswith("repos/"):
            out = {"node_id": "I_138"}
        elif "addProjectV2ItemById" in joined:
            out = {"data": {"addProjectV2ItemById": {"item": {"id": "IT1"}}}}
        else:
            out = {"data": {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "IT1"}}}}
        return subprocess.CompletedProcess(argv, 0, json.dumps(out), "")


class PickOption(unittest.TestCase):
    def test_a_status_whatever_its_case(self):
        self.assertEqual(board.pick_option(OPTIONS, "in PROGRESS")["id"], "o3")

    def test_an_unknown_status_names_the_boards(self):
        with self.assertRaises(board.BoardError) as e:
            board.pick_option(OPTIONS, "Doing")
        self.assertIn("Backlog, Ready, In progress, In review, Done", str(e.exception))


@mock.patch.object(board, "BOARD", 7)
class Move(unittest.TestCase):
    def test_puts_the_issue_on_the_board_then_sets_its_status(self):
        fake = FakeGh()
        self.assertEqual(board.move(138, "in review", fake), "In review")
        self.assertEqual(fake.calls[1], ["gh", "api", "repos/phenixrizen/conductor/issues/138"])
        add = " ".join(fake.calls[2])
        self.assertIn("project=P1", add)
        self.assertIn("content=I_138", add)
        last = fake.calls[3]
        for want in ("project=P1", "item=IT1", "field=F1", "option=o4"):
            self.assertIn(want, last)

    def test_the_boards_number_goes_as_an_int(self):
        fake = FakeGh()
        board.move(1, "Done", fake)
        first = fake.calls[0]
        self.assertEqual(first[first.index("number=7") - 1], "-F")

    def test_an_unknown_status_changes_nothing(self):
        fake = FakeGh()
        with self.assertRaises(board.BoardError):
            board.move(138, "Doing", fake)
        self.assertEqual(len(fake.calls), 1)

    def test_a_gh_failure_says_why(self):
        with self.assertRaises(board.BoardError) as e:
            board.move(138, "Done", FakeGh(fail_on="graphql"))
        self.assertIn("missing scope project", str(e.exception))


class Main(unittest.TestCase):
    def test_usage_without_two_arguments(self):
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(board.main(["138"], FakeGh()), 2)
        self.assertIn("board.py", err.getvalue())

    @mock.patch.object(board, "BOARD", 7)
    def test_a_hash_before_the_number_is_taken(self):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            self.assertEqual(board.main(["#138", "Done"], FakeGh()), 0)
        self.assertEqual(out.getvalue().strip(), "#138: Done")

    def test_an_unset_board_is_refused(self):
        err = io.StringIO()
        with mock.patch.object(board, "BOARD", 0), contextlib.redirect_stderr(err):
            self.assertEqual(board.main(["138", "Done"], FakeGh()), 1)
        self.assertIn("BOARD is not set", err.getvalue())


if __name__ == "__main__":
    unittest.main()
