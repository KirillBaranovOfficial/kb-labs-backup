from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from project_identity_steps import run_project_identity_steps
from service_steps import Journey


class ProjectIdentityStepsTest(unittest.TestCase):
    # case: S-204.7
    def test_unregistered_cwd_reports_unknown_project_and_add_hint(self):
        repo_root = Path(__file__).resolve().parents[2]
        journey = Journey("local", "project-identity")
        run_project_identity_steps(journey, repo_root)

        result = journey.result()
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(claims, {"S-204.7"})


if __name__ == "__main__":
    unittest.main()
