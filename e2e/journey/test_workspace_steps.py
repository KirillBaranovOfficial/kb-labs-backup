from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from service_steps import Journey
from workspace_steps import run_workspace_cli_steps


class WorkspaceCliStepsTest(unittest.TestCase):
    def test_workspace_cli_loads_plugins_and_validates_cases(self):
        repo_root = Path(__file__).resolve().parents[2]
        journey = Journey("local", "workspace-cli")
        run_workspace_cli_steps(journey, repo_root)

        result = journey.result()
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(claims, {
            "S-179.2", "S-179.3", "S-179.4", "S-179.5", "S-179.13", "S-142.5", "S-203.1", "S-203.19",
        })


if __name__ == "__main__":
    unittest.main()
