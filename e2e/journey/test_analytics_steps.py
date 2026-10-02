import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from analytics_steps import run_analytics_steps
from service_steps import Journey


class FakeResponse:
    status = 503
    headers = {}

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return b'{"error":"fixture unavailable"}'


class AnalyticsStepTests(unittest.TestCase):
    def test_api_and_command_failures_record_exact_case_claims_red(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_PLATFORM_ROOT": directory,
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
        }, clear=False):
            failed = lambda command, **_kwargs: subprocess.CompletedProcess(command, 1, "", "fixture failed")
            journey = Journey("local", "fake-analytics")
            run_analytics_steps(journey, token="fixture-token", runner=failed,
                                opener=lambda *_args, **_kwargs: FakeResponse())

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses, {
            "S-191.1": "FAIL", "S-191.2": "FAIL", "S-191.3": "FAIL", "S-201.3": "FAIL",
        })


if __name__ == "__main__":
    unittest.main()
