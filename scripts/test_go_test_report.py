#!/usr/bin/env python3
"""The reporting pipeline must preserve failed tests and their diagnostics."""
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from go_test_report import report


class ReportTests(unittest.TestCase):
    def test_output_timings_and_cached_results(self):
        events = [
            {"Action": "build-output", "ImportPath": "example/a", "Output": "build diagnostic\n"},
            {"Action": "output", "Package": "example/a", "Output": "diagnostic\n"},
            {"Action": "pass", "Package": "example/a", "Test": "TestParent/child", "Elapsed": 2},
            {"Action": "fail", "Package": "example/a", "Test": "TestParent", "Elapsed": 3},
            {"Action": "skip", "Package": "example/a", "Test": "TestOptional"},
            {"Action": "fail", "Package": "example/a", "Elapsed": 4},
            {"Action": "pass", "Package": "example/cached", "Test": "TestReplayed", "Elapsed": 100},
            {"Action": "output", "Package": "example/cached", "Output": "ok  \texample/cached\t(cached)\n"},
            {"Action": "pass", "Package": "example/cached", "Elapsed": 0},
        ]
        source = "compiler diagnostic\n" + "\n".join(json.dumps(event) for event in events)
        output = io.StringIO()
        summary = report(io.StringIO(source), output, "normal")
        self.assertEqual(output.getvalue(), "compiler diagnostic\nbuild diagnostic\ndiagnostic\nok  \texample/cached\t(cached)\n")
        self.assertIn("1 passed, 1 failed, 1 skipped", summary)
        self.assertIn("TestParent</code> | 3.000 | fail", summary)
        self.assertNotIn("TestParent/child", summary)
        self.assertNotIn("TestReplayed", summary)
        self.assertIn("replayed timings are excluded", summary)

    def test_actual_go_test_failure_and_success_reach_the_caller(self):
        script = Path(__file__).resolve().with_name("test-with-timings.sh")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "go.mod").write_text("module example.invalid/ci-report\n\ngo 1.25.0\n")
            (root / "report_test.go").write_text('''package report_test
import ("os"; "testing")
func TestOutcome(t *testing.T) {
    if os.Getenv("CONDUCTOR_REPORT_FIXTURE_FAIL") == "1" {
        t.Fatal("retained failure diagnostic")
    }
}
func TestOptional(t *testing.T) { t.Skip("explicitly unexecuted") }
''')
            for mode in ("normal", "race"):
                for fail in (False, True):
                    with self.subTest(mode=mode, fail=fail):
                        summary_file = root / "summary.md"
                        summary_file.unlink(missing_ok=True)
                        env = dict(os.environ, GITHUB_STEP_SUMMARY=str(summary_file),
                                   CONDUCTOR_REPORT_FIXTURE_FAIL="1" if fail else "0")
                        result = subprocess.run(["bash", str(script), mode, "-count=1", "./..."], cwd=root,
                                                env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60)
                        self.assertEqual(result.returncode, 1 if fail else 0, result.stdout)
                        summary = summary_file.read_text()
                        self.assertIn("0 passed, 1 failed, 1 skipped" if fail else "1 passed, 0 failed, 1 skipped", summary)
                        if fail:
                            self.assertIn("retained failure diagnostic", result.stdout)
                            self.assertIn("--- FAIL: TestOutcome", result.stdout)

            (root / "report_test.go").write_text("package report_test\nvar broken = missingBuildSymbol\n")
            result = subprocess.run(["bash", str(script), "normal", "./..."], cwd=root,
                                    env=dict(os.environ, GITHUB_STEP_SUMMARY=str(root / "compile-summary.md")),
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60)
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("undefined: missingBuildSymbol", result.stdout)


if __name__ == "__main__":
    unittest.main()
