import json
import tempfile
import unittest
from pathlib import Path

from scoreboard import build_scoreboard, load_cases, release_claims_for_case, render_delta, render_markdown, write_scoreboards


class CaseScoreboardTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.catalog = self.root / "catalog"
        self.reports = self.root / "reports"
        self.catalog.mkdir()
        self.reports.mkdir()
        (self.catalog / "S-001-demo.md").write_text(
            "---\nid: S-001\ntitle: Demo\nstatus: active\nrelease: true\nmodes: [local, secured]\n---\n"
            "| S-001.1 | First | observable result |\n| S-001.2 | Second | observable result |\n",
            encoding="utf-8")
        (self.catalog / "S-002-secured.md").write_text(
            "---\nid: S-002\ntitle: Secured only\nstatus: active\nrelease: false\nmodes: [secured]\n---\n"
            "| S-002.1 | First | observable result |\n", encoding="utf-8")
        (self.catalog / "S-003-planned.md").write_text(
            "---\nid: S-003\ntitle: Planned\nstatus: planned\nrelease: true\nmodes: [local, secured]\n---\n"
            "| S-003.1 | First | observable result |\n", encoding="utf-8")
        (self.catalog / "S-004-historical.md").write_text(
            "---\nid: S-004\ntitle: Historical\nstatus: historical\nrelease: false\nmodes: [local, secured]\n---\n",
            encoding="utf-8")

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
        self.assertNotIn("S-002", local_cases)
        self.assertEqual(secured_cases["S-001"]["status"], "Partial")
        self.assertEqual(secured_cases["S-002"]["status"], "Red")
        self.assertEqual(combined_cases["S-001"]["status"], "Partial")
        self.assertEqual(combined_cases["S-001"]["claimCount"], 2)
        self.assertEqual(local["releaseCases"], {"fullyGreen": 1, "total": 1})
        self.assertEqual(local["activeCases"], {"fullyGreen": 1, "total": 1})
        self.assertEqual(secured["activeCases"], {"fullyGreen": 0, "total": 2})
        self.assertEqual(combined["activeCases"], {"fullyGreen": 0, "total": 2})
        self.assertEqual(local["plannedCount"], 1)
        self.assertEqual(local["historicalCount"], 1)
        self.assertNotIn("S-004", {case["id"] for case in local["cases"]})

    def test_reads_status_and_release_fields_from_real_scenarios(self):
        real_catalog = Path(__file__).resolve().parents[2] / "docs" / "qa" / "scenarios"
        cases = {case["id"]: case for case in load_cases(real_catalog)}
        self.assertEqual(cases["PC-001"]["status"], "active")
        self.assertIs(cases["PC-001"]["release"], True)
        self.assertEqual(cases["PC-005"]["status"], "historical")
        self.assertIs(cases["PC-005"]["release"], False)
        self.assertEqual(cases["S-034"]["status"], "planned")
        self.assertIs(cases["S-034"]["release"], False)

    def test_release_claims_follow_the_case_release_flag(self):
        catalog = Path(__file__).resolve().parents[2] / "docs" / "qa" / "scenarios"
        self.assertEqual(release_claims_for_case("S-109", catalog), [])
        self.assertEqual(release_claims_for_case("S-100", catalog),
                         ["S-100.1", "S-100.2", "S-100.3", "S-100.4"])

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
        markdown = (self.reports / "case-scoreboard.md").read_text(encoding="utf-8")
        self.assertLess(markdown.index("Release cases:"), markdown.index("All active cases:"))
        self.assertLess(markdown.index("All active cases:"), markdown.index("Planned cases:"))
        self.assertLess(markdown.index("Planned cases:"), markdown.index("Historical cases excluded:"))

    def test_writer_always_emits_both_mode_views_for_a_single_mode_run(self):
        self.write_json("service-local.json", {"case_run": {"mode": "local", "claims": []}, "steps": []})
        write_scoreboards(self.reports, self.catalog)
        secured = json.loads((self.reports / "case-scoreboard-secured.json").read_text(encoding="utf-8"))
        combined = json.loads((self.reports / "case-scoreboard.json").read_text(encoding="utf-8"))
        self.assertEqual(secured["observedModes"], ["local"])
        self.assertEqual(combined["modes"], ["local", "secured"])
        self.assertEqual(combined["observedModes"], ["local"])

    def test_partial_run_is_marked_and_never_contributes_case_evidence(self):
        self.write_json("service-secured.json", {
            "case_run": {"mode": "secured", "claims": [
                {"claimId": "S-001.1", "status": "PASS"},
                {"claimId": "S-001.2", "status": "PASS"}]},
            "steps": [],
        })
        self.write_json("service-local.json", {
            "case_run": {"mode": "local", "partial": True, "selection": ["S-001.1"],
                         "claims": [{"claimId": "S-001.1", "status": "PASS"},
                                    {"claimId": "S-001.2", "status": "PASS"}]},
            "steps": [],
        })
        scoreboard = build_scoreboard(self.reports, self.catalog, ["local"])
        case = next(item for item in scoreboard["cases"] if item["id"] == "S-001")
        self.assertTrue(scoreboard["partial"])
        self.assertEqual(scoreboard["selection"], ["S-001.1"])
        self.assertEqual(case["status"], "Unclaimed")
        self.assertIn("Partial run", render_markdown(scoreboard))

    def test_split_partial_journey_records_keep_selection_without_claiming_missing_rows(self):
        selection = ["S-001.1", "S-001.2"]
        self.write_json("installer-case-local-agent-explore-default.json", {
            "case_run": {"mode": "local", "partial": True, "selection": selection,
                         "claims": [{"claimId": "S-001.1", "status": "PASS"}]},
            "steps": [],
        })
        self.write_json("service-local.json", {
            "case_run": {"mode": "local", "partial": True, "selection": selection,
                         "claims": [{"claimId": "S-001.2", "status": "PASS"}]},
            "steps": [],
        })

        scoreboard = build_scoreboard(self.reports, self.catalog, ["local"])
        case = next(item for item in scoreboard["cases"] if item["id"] == "S-001")
        self.assertTrue(scoreboard["partial"])
        self.assertEqual(scoreboard["selection"], selection)
        self.assertEqual(case["status"], "Unclaimed")
        self.assertEqual(case["claimedCount"], 0)

    def test_delta_reports_new_and_regressed_fully_green_cases(self):
        before, after = self.root / "before", self.root / "after"
        before.mkdir()
        after.mkdir()
        for directory, green in ((before, "S-001"), (after, "S-002")):
            data = {"observedModes": ["local"], "releaseCases": {"fullyGreen": int(green == "S-001"), "total": 1},
                    "activeCases": {"fullyGreen": 1, "total": 2}, "plannedCount": 1, "historicalCount": 1,
                    "cases": [{"id": claim_id, "status": "Fully green" if claim_id == green else "Red",
                               "caseStatus": "active", "release": claim_id == "S-001", "modes": ["local"],
                               "claims": [f"{claim_id}.1", f"{claim_id}.2"],
                               "passingClaims": [f"{claim_id}.1", f"{claim_id}.2"] if claim_id == green else []}
                              for claim_id in ("S-001", "S-002")]}
            (directory / "case-scoreboard-local.json").write_text(json.dumps(data), encoding="utf-8")
        delta = render_delta(before, after)
        self.assertIn("local release cases: fully green 1/1 → 0/1", delta)
        self.assertIn("Claims passing: 2/4 → 2/4", delta)
        self.assertIn("Regressed: S-001", delta)
        self.assertIn("All active: fully green 1/2 → 1/2", delta)
        self.assertLess(delta.index("local release cases:"), delta.index("All active:"))

    def test_delta_omits_partial_runs_instead_of_reporting_regressions(self):
        before, after = self.root / "before-partial", self.root / "after-full"
        before.mkdir()
        after.mkdir()
        for directory, partial in ((before, True), (after, False)):
            data = {"observedModes": ["local"], "partial": partial, "selection": ["S-001"],
                    "releaseCases": {"fullyGreen": 0, "total": 1},
                    "activeCases": {"fullyGreen": 0, "total": 2}, "plannedCount": 1,
                    "historicalCount": 1, "cases": []}
            (directory / "case-scoreboard-local.json").write_text(json.dumps(data), encoding="utf-8")
        delta = render_delta(before, after)
        self.assertIn("delta omitted because a scoreboard is partial", delta)
        self.assertNotIn("Regressed:", delta)


if __name__ == "__main__":
    unittest.main()
