import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from gateway_steps import run_gateway_steps
from service_steps import Journey


class FakeResponse:
    status = 404
    headers = {}

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return b'{"error":"fixture route unavailable"}'


class GatewayStepTests(unittest.TestCase):
    def test_fake_cli_and_http_failures_make_every_gateway_claim_red(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_JOURNEY_PROJECT_ROOT": directory,
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "OPENAI_API_KEY": "fake-secret",
        }, clear=False):
            def failed(command, **_kwargs):
                return subprocess.CompletedProcess(command, 1, "", "fixture failed")

            journey = Journey("local", "fake-gateway")
            run_gateway_steps(journey, token="fixture-token", runner=failed,
                              opener=lambda *_args, **_kwargs: FakeResponse())

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses, {
            "S-017.1": "FAIL", "S-017.2": "FAIL", "S-017.3": "FAIL", "S-017.4": "FAIL",
            "S-018.1": "FAIL", "S-018.2": "FAIL", "S-018.3": "FAIL",
            "S-189.1": "FAIL", "S-189.2": "FAIL", "S-189.3": "FAIL", "S-189.4": "FAIL",
            "S-190.1": "FAIL", "S-190.2": "FAIL",
        })


if __name__ == "__main__":
    unittest.main()
