from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from studio_steps import create_studio_member, record_studio_browser_step, run_studio_browser_step


class FakeResponse:
    def __init__(self, body: dict, status: int = 200):
        self.status = status
        self.body = json.dumps(body).encode()

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return self.body


class StudioJourneyTests(unittest.TestCase):
    def test_secured_member_is_created_through_invite_and_activation(self):
        responses = [
            FakeResponse({"accessToken": "admin-token"}),
            FakeResponse({"activationUrl": "http://localhost/activate/one-time-token"}, 201),
            FakeResponse({}, 200),
        ]
        with patch("studio_steps.urllib.request.urlopen", side_effect=responses) as open_url:
            email, password = create_studio_member("http://127.0.0.1:4000", "admin@example.test", "secret")

        self.assertEqual(email, "journey-member@example.test")
        self.assertEqual(password, "JourneyMember123!")
        invite_request = open_url.call_args_list[1].args[0]
        self.assertEqual(invite_request.get_header("Authorization"), "Bearer admin-token")
        self.assertEqual(json.loads(invite_request.data), {"email": email, "role": "member"})
        activation_request = open_url.call_args_list[2].args[0]
        self.assertEqual(json.loads(activation_request.data), {"token": "one-time-token", "password": password})

    def test_browser_result_is_added_to_capability_report(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))

            record_studio_browser_step(report, "local", True, 1.25, "Studio browser journey passed")

            saved = json.loads(report.read_text())
        self.assertEqual(saved["steps"][0]["capability"], "studio")
        self.assertEqual(saved["steps"][0]["status"], "PASS")
        self.assertEqual(saved["steps"][0]["duration_seconds"], 1.25)

    def test_playwright_timeout_is_recorded_as_fail_with_last_output_and_returns(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))
            timeout = subprocess.TimeoutExpired(["playwright"], 180, output="last browser line")
            with patch("studio_steps.subprocess.run", side_effect=timeout) as run:
                passed, _elapsed, message = run_studio_browser_step(
                    ["playwright"], {}, report, "local", True)
            saved = json.loads(report.read_text())

        run.assert_called_once()
        self.assertFalse(passed)
        self.assertIn("timed out after 180s", message)
        self.assertIn("last browser line", message)
        self.assertEqual(saved["steps"][0]["capability"], "studio")
        self.assertEqual(saved["steps"][0]["status"], "FAIL")

    def test_failed_studio_shell_blocks_playwright(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "FAIL", "steps": []}))
            with patch("studio_steps.subprocess.run") as run:
                passed, elapsed, message = run_studio_browser_step(
                    ["playwright"], {}, report, "local", False)
            saved = json.loads(report.read_text())

        run.assert_not_called()
        self.assertFalse(passed)
        self.assertEqual(elapsed, 0.0)
        self.assertIn("shell check failed", message)
        self.assertEqual(saved["steps"][0]["status"], "BLOCKED")


if __name__ == "__main__":
    unittest.main()
