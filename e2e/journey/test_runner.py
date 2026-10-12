from __future__ import annotations

import importlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from service_steps import Journey


class RunnerInstallFailureTests(unittest.TestCase):
    def test_capture_case_failure_can_read_before_claims_split_across_journeys_run(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as temporary:
            with patch("tempfile.mkdtemp", return_value=temporary):
                runner = importlib.import_module("runner")

        with patch.dict("os.environ", {"KB_JOURNEY_SELECTED_STEPS": "S-042.1,S-042.2"}, clear=False):
            installer_journey = Journey("local", "installer-local-service-explore")
            service_journey = Journey("local", "service-local")
            installer_journey.step("candidate provenance", "journey.setup", lambda: None, role="lifecycle")
            with patch.object(runner, "CASE_JOURNEY", installer_journey), patch.object(runner, "FIRST_FAILURE", ""):
                runner.capture_case_failure()
                self.assertEqual(runner.FIRST_FAILURE, "")
            installer_journey.step("installer claim", "install", lambda: None, cases=["S-042.1"])
            service_journey.step("service claim", "gateway", lambda: None, cases=["S-042.2"])
            self.assertEqual(installer_journey.result().status, "PASS")
            self.assertEqual(service_journey.result().status, "PASS")

    def test_run_level_selection_completion_merges_journeys(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as temporary:
            with patch("tempfile.mkdtemp", return_value=temporary):
                runner = importlib.import_module("runner")

        selected = "S-042.1,S-042.2,S-042.3"
        Journey.reset_run_registry()
        try:
            with patch.dict("os.environ", {"KB_JOURNEY_SELECTED_STEPS": selected}, clear=False):
                first = Journey("local", "first-journey")
                first.step("first claim", "test", lambda: None, cases=["S-042.1"])
                second = Journey("local", "second-journey")
                second.step("second claim", "test", lambda: None, cases=["S-042.2"])
                second.step("third claim", "test", lambda: None, cases=["S-042.3"])
            results = [journey.result().__dict__ for journey in Journey.run_instances()]
            with patch.object(runner, "FIRST_FAILURE", ""):
                self.assertEqual(runner.check_selected_claim_completion(results, selected), [])
                self.assertEqual(runner.FIRST_FAILURE, "")
        finally:
            Journey.reset_run_registry()

    def test_run_level_selection_completion_fails_only_missing_claims(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as temporary:
            with patch("tempfile.mkdtemp", return_value=temporary):
                runner = importlib.import_module("runner")

        with patch.object(runner, "FIRST_FAILURE", ""):
            runner.check_selected_claim_completion(
                [{"steps": [{"status": "PASS", "cases": ["S-042.1"]}]}],
                "S-042.1,S-042.2,S-042.3",
            )
            self.assertEqual(
                runner.FIRST_FAILURE,
                "selected claim steps did not execute: S-042.2, S-042.3; "
                "the selected runner did not call their registered step functions",
            )

    def test_real_failure_remains_first_when_selection_claims_never_execute(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as temporary:
            with patch("tempfile.mkdtemp", return_value=temporary):
                runner = importlib.import_module("runner")

        with patch.dict("os.environ", {"KB_JOURNEY_SELECTED_STEPS": "S-042.1,S-042.2"}, clear=False):
            journey = Journey("local", "aborted-run")
            journey.step("install", "platform.install", lambda: "install command failed", cases=["S-042.1"])
            result = journey.result()
        with patch.object(runner, "FIRST_FAILURE", "install command failed: exit 1"):
            missing = runner.check_selected_claim_completion([result.__dict__], "S-042.1,S-042.2")
            self.assertEqual(missing, ["S-042.2"])
            self.assertEqual(runner.FIRST_FAILURE, "install command failed: exit 1")
        self.assertEqual(result.case_run["claims"], [
            {"claimId": "S-042.1", "kind": "journey", "status": "FAIL"},
        ])

    def test_json_install_refusal_branch_records_j55_envelope_as_case_fail(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as temporary:
            with patch("tempfile.mkdtemp", return_value=temporary):
                runner = importlib.import_module("runner")
        journey = Journey("local", "j55-install-refusal")
        envelope = {
            "code": "KB_INSTALL_LAYOUT_UNSUPPORTED",
            "message": "This platform root does not use the supported versioned installation layout.",
            "cause": "read installation identity: open /tmp/kb-platform/installer/layout.json: no such file or directory",
            "hint": "Choose a fresh writable path, then run `kb-create install --platform-root NEW_ROOT`.",
        }
        result = runner.subprocess.CompletedProcess(
            ["kb-create", "install", "--json"], 2,
            stdout=json.dumps({"ok": False, "error": envelope}) + "\n", stderr="",
        )
        with patch.object(runner, "CASE_JOURNEY", journey), patch.object(runner, "FIRST_FAILURE", ""):
            handled = runner.handle_json_install_failure("install", result)
            first_failure = runner.FIRST_FAILURE

        report = journey.result()
        self.assertTrue(handled)
        self.assertEqual(report.status, "FAIL")
        self.assertEqual(report.first_failure["name"], "JSON install progress")
        self.assertIn("PC-001.17", report.first_failure["cases"])
        self.assertEqual(report.first_failure["details"]["productEnvelope"], envelope)
        self.assertIn(envelope["cause"], report.first_failure["message"])
        self.assertIn(envelope["hint"], report.first_failure["message"])
        self.assertEqual(first_failure, f"kb-create install --json: {envelope['code']}: {envelope['message']}; cause={envelope['cause']}; hint={envelope['hint']}")
