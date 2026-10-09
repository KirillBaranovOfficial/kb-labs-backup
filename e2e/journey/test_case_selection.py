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


def coverage(mode: str, *, source: str, kind: str = "journey", status: str = "active", release: bool = True):
    return {
        "mode": mode,
        "cases": [{
            "id": "S-042",
            "status": status,
            "release": release,
            "steps": [{"stepId": "S-042.1", "claims": [{"source": source, "kind": kind, "line": 393}]}],
        }],
    }


class CaseJourneySelectionTests(unittest.TestCase):
    # case: S-203.1
    def test_coverage_cli_reader_requires_success_envelope(self):
        self.assertEqual(read_coverage_result(
            '{"ok":true,"data":{"mode":"local","cases":[]}}', 0,
        ), {"mode": "local", "cases": []})
        for stdout, returncode in (
            ('{"mode":"local","cases":[]}', 0),
            ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', 1),
            ('{"ok":true,"data":{}}', 1),
        ):
            with self.subTest(stdout=stdout, returncode=returncode), self.assertRaises(ValueError):
                read_coverage_result(stdout, returncode)

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
                            "kind": "journey", "source": "e2e/journey/service_runner.py", "line": 393,
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
                self.assertNotIn("KB_PLATFORM_ROOT", child_env)
                self.assertNotIn("KB_NET_OFFSET", child_env)
            self.assertEqual(json.loads(output.getvalue()), {
                "selection": ["S-042"], "steps": ["S-042.1"],
                "runners": ["service-local:explore", "service-secured:explore"],
                "prerequisiteSteps": [], "stepSources": ["e2e/journey/service_runner.py"],
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
            "runners": ["service-local:explore", "service-secured:explore"],
            "prerequisiteSteps": [], "stepSources": ["e2e/journey/service_runner.py"],
        })

    def test_rejects_unknown_case_or_step_id_by_name(self):
        with self.assertRaisesRegex(ValueError, "S-999"):
            selection_from_coverage([coverage("local", source="e2e/journey/service_runner.py")], "S-999")

    def test_every_registry_claim_is_reachable_by_step_selection(self):
        result = coverage("local", source="e2e/journey/service_runner.py")
        case = result["cases"][0]
        case["steps"] = [
            {"stepId": f"S-042.{number}", "claims": [{
                "kind": "journey", "source": "e2e/journey/service_runner.py", "line": 393}]} for number in (1, 2, 3)
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
