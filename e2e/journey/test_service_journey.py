import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from nightly_report import aggregate, fingerprint, load_results, sync_issues
from service_runner import checks, discover_workflow
from service_steps import Journey


class FakeGitHub:
    def __init__(self):
        self.issues = [{"number": 10, "title": "Nightly status", "state": "open", "labels": []}]
        self.created = 20

    def list_open(self):
        return self.issues

    def create(self, title, body, labels):
        issue = {"number": self.created, "title": title, "body": body, "labels": labels, "state": "open"}
        self.created += 1
        self.issues.append(issue)
        return issue

    def update(self, number, **fields):
        issue = next(item for item in self.issues if item["number"] == number)
        issue.update(fields)
        return issue


class ServiceJourneyTests(unittest.TestCase):
    def test_first_failure_and_json_result(self):
        journey = Journey("local", "fixture")
        journey.step("health", "gateway", lambda: None)
        journey.step("workflow", "workflow", lambda: "workflow route returned 404")
        result = journey.result()
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.first_failure["name"], "workflow")
        self.assertEqual(len(result.steps), 2)
        with tempfile.TemporaryDirectory() as directory:
            saved = journey.write_json(Path(directory) / "journey.json")
            self.assertEqual(saved.schema, "kb.service-journey/v1")

    def test_aggregate_compares_previous_status_and_uses_stable_fingerprint(self):
        bad = {"capability": "workflow", "mode": "local", "name": "run", "status": "FAIL",
               "message": "HTTP 404 at host A", "duration_seconds": 1}
        report = aggregate([{"steps": [bad]}])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(fingerprint(bad), fingerprint({**bad, "message": "HTTP 500 at host B"}))
        good = {**bad, "status": "PASS", "message": "passed"}
        next_report = aggregate([{"steps": [good]}], [{"steps": [bad]}])
        self.assertEqual(next_report["delta"]["fixed"], ["workflow:local"])

    def test_yesterday_today_fixtures_report_fixed_and_new_failures(self):
        fixtures = Path(__file__).parent / "fixtures" / "nightly"
        report = aggregate(load_results(fixtures / "today"), load_results(fixtures / "yesterday"))
        self.assertEqual(report["delta"]["fixed"], ["workflow:local"])
        self.assertEqual(report["delta"]["new"], ["mcp:local"])
        self.assertEqual(report["status"], "FAIL")

    def test_issue_sync_is_idempotent_updates_existing_and_closes_fixed(self):
        client = FakeGitHub()
        failure = {"capability": "mcp", "mode": "secured", "name": "tools/list",
                   "status": "FAIL", "message": "HTTP 404", "duration_seconds": 2}
        report = aggregate([{"steps": [failure]}])
        first = sync_issues(client, report, 10)
        self.assertEqual(len(first["created"]), 1)
        self.assertEqual(len(client.issues), 2)
        second = sync_issues(client, report, 10)
        self.assertEqual(second["updated"], first["created"])
        self.assertEqual(len(client.issues), 2)
        green = aggregate([{"steps": [{**failure, "status": "PASS", "message": "passed"}]}])
        closed = sync_issues(client, green, 10)
        self.assertEqual(len(closed["closed"]), 1)
        self.assertEqual(client.issues[1]["state"], "closed")

    def test_issue_sync_creates_one_pinned_status_issue_when_missing(self):
        client = FakeGitHub()
        client.issues.clear()
        changes = sync_issues(client, aggregate([]), 0)
        self.assertEqual(len(client.issues), 1)
        self.assertIn("nightly-status", client.issues[0]["labels"])
        self.assertEqual(changes, {"created": [], "updated": [], "closed": []})

    def test_required_mode_steps_and_workflow_fixture_are_explicit(self):
        local = {capability for _, capability, _ in checks("local")}
        secured = {capability for _, capability, _ in checks("secured")}
        self.assertNotIn("auth.invites", local)
        self.assertIn("auth.invites", secured)
        with patch("service_runner.request", return_value=(200, {"data": {"workflows": []}}, {})):
            failure = discover_workflow("prj_fixture", "")
        self.assertIn("e2e-hello fixture", failure)


if __name__ == "__main__":
    unittest.main()
