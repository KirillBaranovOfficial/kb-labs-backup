import json
import tempfile
import unittest
from pathlib import Path

from scoreboard import build_scoreboard, render_delta, write_scoreboards


class CaseScoreboardTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.catalog = self.root / "catalog"
        self.reports = self.root / "reports"
        self.catalog.mkdir()
        self.reports.mkdir()
        (self.catalog / "S-001-demo.md").write_text(
            "---\nid: S-001\ntitle: Demo\nstatus: active\nmodes: [local, secured]\n---\n"
            "| S-001.1 | First | observable result |\n| S-001.2 | Second | observable result |\n",
            encoding="utf-8")
        (self.catalog / "S-002-secured.md").write_text(
            "---\nid: S-002\ntitle: Secured only\nstatus: active\nmodes: [secured]\n---\n"
            "| S-002.1 | First | observable result |\n", encoding="utf-8")

    def tearDown(self):
        self.temporary.cleanup()

    def write_json(self, name, data):
        (self.reports / name).write_text(json.dumps(data), encoding="utf-8")

    def test_scores_case_defined_denominator_and_mode_specific_results(self):
        self.write_json("installer-case-local-agent-explore-default.json", {
            "case_run": {"mode": "local", "claims": [
                {"claimId": "S-001.1", "status": "PASS"},
                {"claimId": "S-001.2", "status": "PASS"}]}, "steps": []})
        self.write_json("service-secured.json", {
            "case_run": {"mode": "secured", "claims": [
                {"claimId": "S-001.1", "status": "PASS"}]},
            "steps": [{"cases": ["S-001.2"], "status": "BLOCKED", "message": "KB_JOURNEY_PROJECT_B_ID is missing"},
                      {"cases": ["S-002.1"], "status": "FAIL", "message": "failed"}]})

        local = build_scoreboard(self.reports, self.catalog, ["local"])
        secured = build_scoreboard(self.reports, self.catalog, ["secured"])
        combined = build_scoreboard(self.reports, self.catalog, ["local", "secured"])
        local_cases = {case["id"]: case for case in local["cases"]}
        secured_cases = {case["id"]: case for case in secured["cases"]}
        combined_cases = {case["id"]: case for case in combined["cases"]}

        self.assertEqual(local_cases["S-001"]["status"], "Fully green")
        self.assertEqual(local_cases["S-002"]["status"], "Not applicable")
        self.assertEqual(secured_cases["S-001"]["status"], "Partial")
        self.assertEqual(secured_cases["S-002"]["status"], "Red")
        self.assertEqual(combined_cases["S-001"]["status"], "Partial")
        self.assertEqual(combined_cases["S-001"]["claimCount"], 2)

    def test_playwright_tags_and_skip_are_claimed(self):
        self.write_json("studio-playwright-secured.json", {"suites": [{"specs": [{
            "title": "admin flow @S-001.1 @S-001.2", "ok": False,
            "tests": [{"annotations": [{"description": "missing admin fixture"}], "results": [
                {"status": "skipped"}]}]}]}]})
        scoreboard = build_scoreboard(self.reports, self.catalog, ["secured"])
        case = next(item for item in scoreboard["cases"] if item["id"] == "S-001")
        self.assertEqual(case["status"], "Blocked")

    def test_writer_emits_per_mode_and_combined_json_and_markdown(self):
        self.write_json("service-local.json", {"case_run": {"mode": "local", "claims": []}, "steps": []})
        self.write_json("service-auth-secured.json", {"case_run": {"mode": "secured", "claims": []}, "steps": []})
        write_scoreboards(self.reports, self.catalog)
        for stem in ("case-scoreboard-local", "case-scoreboard-secured", "case-scoreboard"):
            self.assertTrue((self.reports / f"{stem}.json").is_file())
            self.assertTrue((self.reports / f"{stem}.md").is_file())
        combined = json.loads((self.reports / "case-scoreboard.json").read_text(encoding="utf-8"))
        self.assertEqual(combined["modes"], ["local", "secured"])

    def test_writer_always_emits_both_mode_views_for_a_single_mode_run(self):
        self.write_json("service-local.json", {"case_run": {"mode": "local", "claims": []}, "steps": []})
        write_scoreboards(self.reports, self.catalog)
        secured = json.loads((self.reports / "case-scoreboard-secured.json").read_text(encoding="utf-8"))
        combined = json.loads((self.reports / "case-scoreboard.json").read_text(encoding="utf-8"))
        self.assertEqual(secured["observedModes"], ["local"])
        self.assertEqual(combined["modes"], ["local", "secured"])
        self.assertEqual(combined["observedModes"], ["local"])

    def test_delta_reports_new_and_regressed_fully_green_cases(self):
        before, after = self.root / "before", self.root / "after"
        before.mkdir()
        after.mkdir()
        for directory, green in ((before, "S-001"), (after, "S-002")):
            data = {"observedModes": ["local"], "cases": [{"id": claim_id, "status": "Fully green" if claim_id == green else "Red",
                               "modes": ["local"]} for claim_id in ("S-001", "S-002")]}
            (directory / "case-scoreboard-local.json").write_text(json.dumps(data), encoding="utf-8")
        delta = render_delta(before, after)
        self.assertIn("Became fully green: S-002", delta)
        self.assertIn("Regressed: S-001", delta)


if __name__ == "__main__":
    unittest.main()
