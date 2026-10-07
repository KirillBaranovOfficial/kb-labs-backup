from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
import invocations


class PtyInvocationTest(unittest.TestCase):
    def test_pty_eof_waits_for_child_exit_instead_of_recording_timeout(self):
        class ExitingProcess:
            pid = 12345
            returncode = None

            def poll(self):
                return self.returncode

            def wait(self, timeout=None):
                self.returncode = 1
                return self.returncode

            def kill(self):
                raise AssertionError("an exited PTY child must not be killed as a timeout")

        process = ExitingProcess()
        records = []
        with tempfile.TemporaryDirectory(prefix="journey-pty-evidence-") as temp:
            with (invocations.invocation_context(report_root=temp),
                  patch.object(invocations.subprocess, "Popen", return_value=process),
                  patch.object(invocations.select, "select", side_effect=lambda readers, *_args: (readers, [], [])),
                  patch.object(invocations.os, "read", side_effect=OSError(5, "PTY reached EOF")),
                  patch.object(invocations, "record_invocation",
                               side_effect=lambda **record: records.append(record))):
                result, _ = invocations.run_pty_command(
                    [sys.executable, "-c", "raise SystemExit(1)"], timeout=120, raise_on_timeout=False,
                )

        self.assertEqual(result.returncode, 1)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["exit_code"], 1)
        self.assertIsNone(records[0]["error"])


if __name__ == "__main__":
    unittest.main()
