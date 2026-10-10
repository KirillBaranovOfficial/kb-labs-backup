import os
import json
import subprocess
import sys
import tempfile
import unittest
from urllib.parse import urlparse
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
        return b'{"ok":false,"error":{"code":"KB_RUNTIME_UNEXPECTED","message":"fixture unavailable"}}'


class JsonResponse:
    status = 200
    headers = {}

    def __init__(self, payload):
        self.payload = payload

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return json.dumps(self.payload).encode()


class AnalyticsStepTests(unittest.TestCase):
    def test_fixture_plugin_emission_query_and_restart_are_green_with_real_outputs(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_PLATFORM_ROOT": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_STUDIO_FIXTURE_FILE": str(Path(directory) / "studio-fixtures.json"),
            "HOME": directory,
        }, clear=False):
            events = []
            paths = []

            def runner(command, **_kwargs):
                if command[:3] == ["kb", "journey", "analytics"]:
                    marker = next(arg.split("=", 1)[1] for arg in command if arg.startswith("--marker="))
                    payload = {"ok": True, "data": {"result": {"type": "case.analytics.persist", "marker": marker}}}
                    events.append({"id": f"event-{marker}", "type": "case.analytics.persist",
                                   "payload": {"source": "case-fixture", "marker": marker},
                                   "projectId": "project-a" if marker.endswith("project-a") else "project-b"})
                    return subprocess.CompletedProcess(command, 0, json.dumps(payload), "")
                return subprocess.CompletedProcess(command, 0, '{"ok":true,"data":{}}', "")

            def opener(request, **_kwargs):
                path = urlparse(request.full_url).path
                paths.append(path)
                if path == "/api/v1/projects/project-a/rest/analytics/events":
                    payload = {"ok": True, "data": {"events": [event for event in events if event["projectId"] == "project-a"]}}
                elif path == "/api/v1/projects/project-b/rest/analytics/events":
                    payload = {"ok": True, "data": {"events": [event for event in events if event["projectId"] == "project-b"]}}
                elif path == "/api/v1/projects/project-a/workflow/runs" or path == "/api/v1/projects/project-b/workflow/runs":
                    payload = {"ok": True, "data": {"runs": []}}
                else:
                    return JsonResponse({"ok": False, "error": {"code": "KB_NOT_FOUND", "message": "route not found"}})
                return JsonResponse(payload)

            journey = Journey("local", "analytics-green")
            run_analytics_steps(journey, token="fixture-token", runner=runner, opener=opener)
            fixture_body = (Path(directory) / "studio-fixtures.json").read_text()
        self.assertIn("/api/v1/projects/project-a/rest/analytics/events", paths)
        self.assertIn("/api/v1/projects/project-b/rest/analytics/events", paths)
        self.assertNotIn("/api/v1/projects/project-a/rest/api/v1/analytics/events", paths)
        self.assertNotIn("/api/v1/analytics/events", paths)

        claims = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(claims, {
            "S-191.1": "PASS", "S-191.2": "PASS", "S-191.3": "PASS", "S-201.3": "PASS",
        })
        fixture = json.loads(fixture_body)
        self.assertEqual(fixture["eventId"], "event-analytics-green:project-a")

    def test_api_and_command_failures_record_exact_case_claims_red(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_PLATFORM_ROOT": directory,
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
        }, clear=False):
            failed = lambda command, **_kwargs: subprocess.CompletedProcess(command, 1, json.dumps({
                "ok": False, "error": {"code": "KB_RUNTIME_UNEXPECTED", "message": "fixture failed"},
            }), "")
            journey = Journey("local", "fake-analytics")
            run_analytics_steps(journey, token="fixture-token", runner=failed,
                                opener=lambda *_args, **_kwargs: FakeResponse())

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses, {
            "S-191.1": "FAIL", "S-191.2": "FAIL", "S-191.3": "FAIL", "S-201.3": "FAIL",
        })


if __name__ == "__main__":
    unittest.main()
