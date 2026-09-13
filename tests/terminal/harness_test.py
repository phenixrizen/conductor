"""Deterministic regressions for PTY observation timing, without an API fixture."""
import unittest

from review import Terminal


class ExitingProcess:
    """Expose exit only after the predicate's first process observation."""

    def __init__(self):
        self.observations = 0

    def poll(self):
        self.observations += 1
        return None if self.observations == 1 else 0


class TerminalWaitTest(unittest.TestCase):
    def terminal(self):
        terminal = Terminal.__new__(Terminal)
        terminal.process = ExitingProcess()
        terminal.output = bytearray()
        terminal.pump = lambda timeout=0.05: None
        return terminal

    def test_exit_observed_after_predicate_is_not_a_failed_wait(self):
        terminal = self.terminal()
        terminal.wait(lambda: terminal.process.poll() is not None, "terminal did not exit")
        self.assertEqual(terminal.process.poll(), 0)

    def test_exit_does_not_satisfy_a_missing_content_predicate(self):
        terminal = self.terminal()
        with self.assertRaisesRegex(AssertionError, "terminal did not show"):
            terminal.wait_text("missing required content")


if __name__ == "__main__":
    unittest.main()
