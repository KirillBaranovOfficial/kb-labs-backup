from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from io import StringIO
from unittest.mock import patch
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import select_from_cases
from select_from_cases import read_coverage_result, selection_from_coverage, selectors_from_coverage
from service_steps import Journey
from select_from_cases import _case_declarations


def coverage(mode: str, *, source: str, kind: str = "journey", status: str = "active", release: bool = True):
    root = Path(__file__).resolve().parents[2]
    line = next(iter(_case_declarations(source, root)))
    return {
        "mode": mode,
        "cases": [{
            "id": "S-042",
            "status": status,
            "release": release,
            "steps": [{"stepId": "S-042.1", "claims": [{"source": source, "kind": kind, "line": line}]}],
        }],
    }


class CaseJourneySelectionTests(unittest.TestCase):
    # case: S-203.1
    def test_coverage_cli_reader_requires_success_envelope(self):
        self.assertEqual(read_coverage_result(
            subprocess.CompletedProcess(["kb", "qa", "coverage", "show"], 0, '{"ok":true,"data":{"mode":"local","cases":[]}}', ""),
        ), {"mode": "local", "cases": []})
        for stdout, returncode in (
            ('{"mode":"local","cases":[]}', 0),
            ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', 1),
            ('{"ok":true,"data":{}}', 1),
        ):
            with self.subTest(stdout=stdout, returncode=returncode), self.assertRaises(ValueError):
                read_coverage_result(subprocess.CompletedProcess(["kb", "qa", "coverage", "show"], returncode, stdout, ""))

    def test_selector_runs_coverage_without_caller_home_or_platform_overrides(self):
        with tempfile.TemporaryDirectory() as caller_home:
            captured = []

            def invoke(command, **options):
                captured.append(options["env"])
                mode = command[command.index("--mode") + 1]
                return subprocess.CompletedProcess(command, 0, json.dumps({
                    "ok": True, "data": {"mode": mode, "cases": [{
                        "id": "S-042", "status": "active", "release": True,
                        "steps": [{"stepId": "S-042.1", "claims": [{
                            "kind": "journey", "source": "e2e/journey/service_runner.py",
                            "line": next(iter(_case_declarations(
                                "e2e/journey/service_runner.py", Path(__file__).resolve().parents[2]))),
                        }]}],
                    }]},
                }), "")

            output = StringIO()
            with patch.dict(os.environ, {
                "HOME": caller_home,
                "KB_HOME": "/caller/kb-home",
                "KB_PLATFORM_ROOT": "/caller/platform",
                "KB_NET_OFFSET": "731",
                "KB_JOURNEY_CASES": "S-042",
            }, clear=False), patch("hermetic_cli.run_command", side_effect=invoke), redirect_stdout(output):
                self.assertEqual(select_from_cases.main(), 0)

            self.assertEqual(len(captured), 2)
            for child_env in captured:
                self.assertNotEqual(child_env["HOME"], caller_home)
                self.assertFalse(Path(child_env["HOME"]).exists())
                self.assertNotIn("KB_HOME", child_env)
                self.assertEqual(Path(child_env["KB_PLATFORM_ROOT"]), Path(child_env["HOME"]) / "platform")
                self.assertNotIn("KB_NET_OFFSET", child_env)
            self.assertEqual(json.loads(output.getvalue()), {
                "selection": ["S-042"], "steps": ["S-042.1"],
                "owners": {"service-local:explore": ["S-042.1"],
                           "service-secured:explore": ["S-042.1"]},
                "runners": ["service-local:explore", "service-secured:explore"],
                "prerequisiteSteps": [], "prerequisiteFlows": [],
                "stepSources": ["e2e/journey/service_runner.py"], "capabilities": ["gateway"],
                "selectedOwningSteps": {"platform readiness (gateway + project workflow)": ["S-042.1"]},
            })

    def test_selector_failure_includes_decoded_cli_error_envelope(self):
        failure = {"ok": False, "error": {
            "code": "KB_CONFIG_INVALID",
            "message": "Saved authentication file .kb/credentials.json is invalid.",
            "hint": "Run \"kb auth login\" to replace the saved authentication data, then retry the command.",
        }}

        def invoke(command, **_options):
            return subprocess.CompletedProcess(command, 1, json.dumps(failure), "")

        with patch("hermetic_cli.run_command", side_effect=invoke), patch.dict(
            os.environ, {"KB_JOURNEY_CASES": "S-042"}, clear=False,
        ):
            with self.assertRaisesRegex(ValueError, "KB_CONFIG_INVALID; Saved authentication file") as raised:
                select_from_cases.main()
        self.assertIn("hint: Run \"kb auth login\"", str(raised.exception))

    def test_selects_only_entry_points_with_active_release_journey_claims(self):
        results = [
            coverage("local", source="e2e/journey/service_runner.py"),
            coverage("secured", source="e2e/journey/service_runner.py"),
            coverage("local", source="e2e/journey/runner.py", kind="unit"),
            coverage("local", source="e2e/journey/runner.py", status="historical"),
            coverage("local", source="e2e/journey/runner.py", release=False),
        ]
        self.assertEqual(selectors_from_coverage(results), ["service-local:explore", "service-secured:explore"])

    def test_selects_the_installer_entry_point_for_a_journey_claim(self):
        self.assertEqual(
            selectors_from_coverage([coverage("local", source="e2e/journey/runner.py")]),
            ["agent:commit"],
        )

    def test_rejects_unknown_coverage_modes(self):
        with self.assertRaisesRegex(ValueError, "unsupported QA coverage mode"):
            selectors_from_coverage([coverage("nightly", source="e2e/journey/service_runner.py")])

    def test_resolves_cases_and_steps_from_the_qa_claim_registry(self):
        result = selection_from_coverage([
            coverage("local", source="e2e/journey/service_runner.py"),
            coverage("secured", source="e2e/journey/service_runner.py"),
        ], "S-042,S-042.1")
        self.assertEqual(result, {
            "selection": ["S-042", "S-042.1"], "steps": ["S-042.1"],
            "owners": {"service-local:explore": ["S-042.1"],
                       "service-secured:explore": ["S-042.1"]},
            "runners": ["service-local:explore", "service-secured:explore"],
            "prerequisiteSteps": [], "prerequisiteFlows": [],
            "stepSources": ["e2e/journey/service_runner.py"], "capabilities": ["gateway"],
            "selectedOwningSteps": {"platform readiness (gateway + project workflow)": ["S-042.1"]},
        })

    def test_rejects_unknown_case_or_step_id_by_name(self):
        with self.assertRaisesRegex(ValueError, "S-999"):
            selection_from_coverage([coverage("local", source="e2e/journey/service_runner.py")], "S-999")

    def test_case_selection_refuses_unclaimed_steps_before_runner_start(self):
        result = coverage("local", source="e2e/journey/service_runner.py")
        result["cases"][0]["id"] = "PC-002"
        result["cases"][0]["steps"].append({"stepId": "PC-002.4", "claims": []})
        with self.assertRaisesRegex(ValueError, r"PC-002.*unclaimed.*PC-002.4"):
            selection_from_coverage([result], "PC-002")

    def test_only_filters_resolved_runners_and_reports_omitted_owners(self):
        results = [
            coverage("local", source="e2e/journey/runner.py"),
            coverage("secured", source="e2e/journey/runner.py"),
        ]
        with patch.dict(os.environ, {"KB_JOURNEY_ONLY": "agent:commit,agent-secured:commit"}):
            resolved = selection_from_coverage(results, "S-042.1")
        self.assertEqual(resolved["runners"], ["agent-secured:commit", "agent:commit"])
        with patch.dict(os.environ, {"KB_JOURNEY_ONLY": "agent:commit"}):
            local = selection_from_coverage(results, "S-042.1")
        self.assertEqual(local["runners"], ["agent:commit"])
        with patch.dict(os.environ, {"KB_JOURNEY_ONLY": "service-local:explore"}):
            with self.assertRaisesRegex(ValueError, r"agent:commit owns S-042.1: KB_JOURNEY_CASES=S-042.1 KB_JOURNEY_ONLY=agent:commit"):
                selection_from_coverage(results, "S-042.1")

    def test_full_run_runner_plan_still_uses_all_active_claim_owners(self):
        results = [
            coverage("local", source="e2e/journey/runner.py"),
            coverage("secured", source="e2e/journey/runner.py"),
        ]
        self.assertEqual(selectors_from_coverage(results), ["agent-secured:commit", "agent:commit"])

    def test_resolve_only_does_not_write_journey_reports(self):
        root = Path(__file__).resolve().parents[2]
        reports = root / ".journey" / "reports"
        before = {path.relative_to(root).as_posix(): (path.stat().st_size, path.stat().st_mtime_ns)
                  for path in reports.rglob("*") if path.is_file()} if reports.exists() else None
        with tempfile.TemporaryDirectory(prefix="journey-resolve-home-") as home:
            env = os.environ.copy()
            env.update({"KB_JOURNEY_ONLY": "agent:commit", "HOME": home})
            for key in ("KB_JOURNEY_CASES", "KB_PLATFORM_ROOT", "KB_HOME", "KB_NET_OFFSET"):
                env.pop(key, None)
            result = subprocess.run(["bash", "e2e/journey/run.sh", "--resolve-only"], cwd=root, env=env,
                                    capture_output=True, text=True, check=False, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('"front_door": "agent"', result.stdout)
        after = {path.relative_to(root).as_posix(): (path.stat().st_size, path.stat().st_mtime_ns)
                 for path in reports.rglob("*") if path.is_file()} if reports.exists() else None
        self.assertEqual(after, before)

    def test_runner_variant_registration_is_enforced_when_claim_executes(self):
        with patch.dict(os.environ, {
                "KB_JOURNEY_SELECTED_STEPS": "S-040.3",
                "KB_JOURNEY_RUNNER_SELECTOR": "agent:commit",
        }, clear=False):
            journey = Journey("local", "wrong-variant")
            result = journey.step("post-install running status", "platform.install", lambda: None,
                                  cases=["S-040.3"], runners=("human:default",))
        self.assertEqual(result.status, "FAIL")
        self.assertIn("registered for runner(s) human:default", result.message)

    def test_unselected_runner_variant_registration_is_not_emitted(self):
        with patch.dict(os.environ, {"KB_JOURNEY_RUNNER_SELECTOR": "agent:commit"}, clear=False):
            journey = Journey("local", "wrong-unselected-variant")
            result = journey.step("human-only install", "platform.install", lambda: None,
                                  cases=["S-040.1"], runners=("human:default",))
        self.assertEqual(result.status, "SKIPPED")

    def test_every_registry_claim_is_reachable_by_step_selection(self):
        result = coverage("local", source="e2e/journey/service_runner.py")
        case = result["cases"][0]
        case["steps"] = [
            {"stepId": f"S-042.{number}", "claims": [{
                "kind": "journey", "source": "e2e/journey/service_runner.py",
                "line": next(iter(_case_declarations("e2e/journey/service_runner.py",
                                                       Path(__file__).resolve().parents[2])))}]}
            for number in (1, 2, 3)
        ]
        for step in case["steps"]:
            step_id = step["stepId"]
            self.assertEqual(selection_from_coverage([result], step_id)["steps"], [step_id])

    def test_unselected_claimed_step_is_reported_skipped_and_not_claimed(self):
        with patch.dict("os.environ", {"KB_JOURNEY_CASES": "S-042", "KB_JOURNEY_SELECTED_STEPS": "S-042.1"}):
            journey = Journey("local")
            skipped = journey.step("other step", "auth", lambda: self.fail("check must not run"),
                                   cases=["S-042.2"])
            self.assertEqual(skipped.status, "SKIPPED")
            self.assertEqual(skipped.message, "not selected")
            unclaimed = journey.step("unclaimed helper", "auth", lambda: self.fail("helper must not run"))
            self.assertEqual(unclaimed.status, "SKIPPED")
            self.assertEqual(journey.result().case_run["claims"], [])
            self.assertTrue(journey.result().case_run["partial"])

if __name__ == "__main__":
    unittest.main()
