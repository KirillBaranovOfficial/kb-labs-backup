from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

from installer_case_steps import run_installer_case_steps, run_uninstall_decline_step


class InstallerCaseStepsTest(unittest.TestCase):
    def test_uninstall_decline_claim_fails_for_a_failed_fake_response(self):
        with tempfile.TemporaryDirectory(prefix="journey-uninstall-decline-") as temp:
            receipt = Path(temp) / "receipt.json"
            receipt.write_text('{"id":"unchanged"}')

            def fake_pty(args, *, on_output):
                self.assertEqual(on_output("Continue? [y/N] "), "n\n")
                return subprocess.CompletedProcess(args, 2, "KB_INSTALL_CONFIRMATION_REQUIRED", ""), 0.0

            journey = run_uninstall_decline_step(mode="local", args=["kb-create", "uninstall"],
                                                 receipt_path=receipt, run_pty=fake_pty)
            self.assertEqual(journey.steps[0].cases, ["S-050.3"])
            self.assertEqual(journey.steps[0].status, "FAIL")

    def test_each_claim_fails_when_the_cli_returns_a_failed_envelope(self):
        with tempfile.TemporaryDirectory(prefix="journey-installer-cases-") as temp:
            root = Path(temp)
            stable = root / "stable.json"
            canary = root / "canary.json"
            stable.write_text('{"platforms": []}')
            canary.write_text('{"platforms": []}')
            installed = root / "installed"
            installed.mkdir()

            def fail_every_command(args):
                return subprocess.CompletedProcess(args, 2, '{"ok":false,"error":{"code":"FAKE_FAILURE"}}', "")

            journey = run_installer_case_steps(mode="local", launcher="kb-create", stable_index=stable,
                                               canary_index=canary, platform_root=installed,
                                               secret_value="journey-test-secret",
                                               run_command=fail_every_command)
            claims = {claim: status for step in journey.steps for claim in step.cases
                      for status in [step.status]}
            expected = {"PC-001.4", "PC-001.5", "PC-001.8", "S-181.1", "S-181.2",
                        "S-181.3", "S-181.4", "S-181.5", "S-025.1", "S-025.2",
                        "S-025.3", "S-025.4", "S-025.5", "S-025.6",
                        "S-053.2", "S-053.3", "S-053.4", "S-053.5", "S-053.6", "S-053.7",
                        "S-187.4", "S-187.5"}
            self.assertEqual(set(claims), expected)
            self.assertTrue(all(claims[claim] == "FAIL" for claim in expected))


if __name__ == "__main__":
    unittest.main()
