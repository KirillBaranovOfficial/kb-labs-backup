from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from command_scope_steps import run_command_scope_steps, run_config_failure_steps
from service_steps import Journey


class CommandScopeStepsTest(unittest.TestCase):
    # case: S-124.10, S-054.17, PC-002.6
    def test_system_command_scope_and_global_flags_outside_projects(self):
        repo_root = Path(__file__).resolve().parents[2]
        journey = Journey("local", "command-scope")
        run_command_scope_steps(journey, repo_root)
        run_config_failure_steps(journey, repo_root)

        result = journey.result()
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(
            claims,
            {"S-124.10", "S-054.17", "PC-002.6", "PC-002.7", "S-203.7", "S-203.8"},
        )


if __name__ == "__main__":
    unittest.main()
