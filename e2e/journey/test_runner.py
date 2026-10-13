from __future__ import annotations

import importlib
import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

from service_steps import FlowGraph, Journey


class RunnerInstallFailureTests(unittest.TestCase):
    @staticmethod
    def _project_command(command, **_kwargs):
        if command[0] == "bash":
            payload = {"ok": True, "data": {"project": {"id": "prj_selected", "name": "selected"}}}
        else:
            payload = {"ok": True, "data": {"projects": [{"id": "prj_selected"}]}}
        return importlib.import_module("subprocess").CompletedProcess(command, 0, json.dumps(payload), ""), 0.0

    def _project_graph(self, *, selected, capabilities):
        runner = importlib.import_module("runner")
        service_runner = importlib.import_module("service_runner")
        env = {"KB_JOURNEY_SELECTED_STEPS": selected,
               "KB_JOURNEY_SELECTED_CAPABILITIES": capabilities}
        with patch.dict(os.environ, env, clear=True):
            journey = Journey("local", "project-registration-state")
            groups = runner.outer_selection_groups()
            states = {"host-running": {"passed": True, "evidence": "host running"},
                      "platform-version-known": {"passed": True, "evidence": "version known"}}
            graph = FlowGraph(journey, runner.OUTER_GROUP_DEPENDENCIES, groups=groups, states=states)
        return runner, service_runner, journey, graph

    def test_selected_project_claims_run_the_registered_project_state_producer(self):
        runner, _service_runner, _journey, graph = self._project_graph(
            selected="PC-002.3,PC-002.8", capabilities="gateway,projects")
        with patch("runner.run", side_effect=self._project_command):
            runner.establish_project_registration(graph, Path("/tmp/project"), {})

        self.assertTrue(graph.states["project-registered"]["passed"])
        self.assertEqual(graph.state_value("project-registered"), {
            "id": "prj_selected", "root": str(Path("/tmp/project").resolve()), "name": "selected",
        })
        child_env = {}
        self.assertEqual(runner.project_environment_from_state(graph, child_env), graph.state_value("project-registered"))
        self.assertEqual(child_env["KB_JOURNEY_PROJECT_ID"], "prj_selected")
        self.assertEqual(child_env["KB_JOURNEY_PROJECT_ROOT"], str(Path("/tmp/project").resolve()))
        self.assertEqual(graph.journey.flows["verify installed project registration"][0], "PASS")

    def test_failed_project_registration_blocks_both_selected_service_claims(self):
        runner, service_runner, journey, graph = self._project_graph(
            selected="PC-002.3,PC-002.8", capabilities="gateway,projects")

        def failed_show(command, **_kwargs):
            return importlib.import_module("subprocess").CompletedProcess(
                command, 7, "project show refused", ""), 0.0

        with patch("runner.run", side_effect=failed_show):
            runner.establish_project_registration(graph, Path("/tmp/project"), {})
        service_journey = Journey("local", "project-registration-consumers")
        service_graph = FlowGraph(
            service_journey,
            {name: spec["depends_on"] for name, spec in service_runner.SERVICE_GROUPS.items()},
            groups=service_runner.SERVICE_GROUPS,
            states={**graph.states,
                    "service-api-session": {"passed": True, "evidence": "local mode", "value": None}},
        )
        for claim, group, capability in (("PC-002.3", "platform", "gateway"),
                                         ("PC-002.8", "projects", "projects")):
            service_graph.step(claim, group, capability, lambda: None, cases=[claim])

        self.assertEqual([step.status for step in service_journey.steps], ["BLOCKED", "BLOCKED"])
        self.assertTrue(all("project-registered" in step.message and "kb project show exited 7" in step.message
                            for step in service_journey.steps))
        self.assertEqual(journey.result().status, "FAIL")
        self.assertEqual(journey.result().first_failure["name"], "verify installed project registration")

    def test_project_registration_is_not_needed_for_install_only_selection_and_skip_is_visible(self):
        runner, _service_runner, _journey, graph = self._project_graph(
            selected="PC-001.2", capabilities="platform.install")
        output = io.StringIO()
        with redirect_stdout(output), patch("runner.run", side_effect=self._project_command):
            runner.establish_project_registration(graph, Path("/tmp/project"), {})

        self.assertNotIn("project-registered", graph.states)
        self.assertIn("SKIPPED verify installed project registration: not selected", output.getvalue())
        self.assertEqual(graph.journey.flows["verify installed project registration"][0], "SKIPPED")

    def test_blocked_summary_names_its_row_without_changing_first_failure_contract(self):
        runner = importlib.import_module("runner")
        journey = Journey("local", "blocked-verdict-format")
        journey.blocked("resource preflight", "platform.update", "not enough memory", cases=["S-052.1"])
        result = journey.result()
        self.assertEqual(result.status, "BLOCKED")
        self.assertIsNone(result.first_failure)
        self.assertEqual(runner.journey_verdict_detail(result.__dict__), "resource preflight: not enough memory")

    def _run_node_guard_terminal_path(self, *, execute_selected: bool) -> tuple[int, str, str, bool]:
        with tempfile.TemporaryDirectory(prefix="journey-final-outcome-") as temporary:
            root = Path(temporary)
            report = root / "summary" / "journey-summary.md"
            with tempfile.TemporaryDirectory(prefix="journey-runner-import-") as import_home:
                with patch("tempfile.mkdtemp", return_value=import_home):
                    runner = importlib.import_module("runner")

            selection = "S-042.1,S-042.2"
            completed = runner.subprocess.CompletedProcess(
                ["install.sh", "--version"], 0,
                stdout="KB_INSTALL_NODE_VERSION Node.js 24", stderr="",
            )

            def run_node_guard(*_args, **_kwargs):
                if execute_selected:
                    for claim_id in selection.split(","):
                        runner.CASE_JOURNEY.step(
                            f"claim {claim_id}", "test", lambda: None, cases=[claim_id])
                return completed, 0.0

            with patch.dict("os.environ", {
                    "KB_JOURNEY_SELECTED_STEPS": selection,
                    "KB_JOURNEY_SUMMARY": str(report),
                    "HOME": str(root),
                }, clear=False), \
                 patch.object(runner, "HOME", root), \
                 patch.object(runner, "BIN", root / "missing-bin"), \
                 patch.object(runner, "PLATFORM", root / "platform"), \
                 patch.object(runner, "VARIANT", "wrong-node"), \
                 patch.object(runner, "BASE_ENV", {
                     **runner.BASE_ENV,
                     "KB_CREATE_DOWNLOAD_BASE_URL": "http://journey.invalid/candidate",
                 }), \
                 patch.object(runner, "FIRST_FAILURE", ""), \
                 patch.object(runner, "run", side_effect=run_node_guard), \
                 patch.object(runner, "write_scoreboards"), \
                 patch.object(runner, "dump_failure_state") as dump_failure_state:
                exit_code = runner.main()
                summary = report.read_text(encoding="utf-8")
                return exit_code, summary, runner.FIRST_FAILURE, dump_failure_state.called

    def test_main_terminal_outcome_fails_exit_and_summary_for_missing_selection(self):
        exit_code, summary, first_failure, dumped_state = self._run_node_guard_terminal_path(
            execute_selected=False)

        self.assertEqual(exit_code, 1)
        self.assertTrue(dumped_state)
        self.assertIn("| FAIL |", summary)
        self.assertIn(
            "selected claim steps did not execute: S-042.1, S-042.2; "
            "the selected runner did not call their registered step functions",
            summary,
        )
        self.assertEqual(
            first_failure,
            "selected claim steps did not execute: S-042.1, S-042.2; "
            "the selected runner did not call their registered step functions",
        )

    def test_main_terminal_outcome_passes_when_all_selected_claims_executed(self):
        exit_code, summary, _first_failure, dumped_state = self._run_node_guard_terminal_path(
            execute_selected=True)

        self.assertEqual(exit_code, 0)
        self.assertFalse(dumped_state)
        self.assertIn("| PASS |", summary)

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
