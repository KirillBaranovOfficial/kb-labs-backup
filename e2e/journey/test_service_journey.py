import ast
import hashlib
import io
import shutil
import json
import os
import re
import shlex
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from nightly_report import aggregate, fingerprint, load_results, render_summary, sync_issues
from entrypoint import run_runner
from runner import login_and_configure_secured_admin
from service_runner import (SERVICE_GROUPS, STATE_PRODUCERS, checks, configure_installed_cli_path, declared_capabilities,
                            discover_workflow, expect_json, mcp_diagnostics, mcp_initialize,
                            expect_html, install_project_agents_plugin,
                            request as request_result,
                            PROJECT_ID,
                            _prepare_and_export_mcp_projects,
                            main as service_main, mcp_probe, run_grouped_service_steps, run_service_steps)
from service_steps import FlowGraph, Journey, record_runner_exception, record_runner_failure, replay_steps
from invocations import run_pty_command, store_report_secret
from steps.auth import run_secured_auth_steps


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
    def test_runner_exception_keeps_completed_steps_and_adds_crash_row_to_report(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-crash-") as temp:
            root = Path(temp)
            journey = Journey("local", "runner-crash")
            for number in range(3):
                journey.step(f"completed step {number + 1}", "journey.test", lambda: None,
                             cases=[f"S-900.{number + 1}"])
            try:
                raise RuntimeError("runner body failed after recorded steps")
            except RuntimeError as error:
                record_runner_exception(journey, error, root / "journey-diagnostics.log")
            report = root / "journey.json"
            result = journey.write_json(report)
            saved = json.loads(report.read_text())
            diagnostic = (root / "journey-diagnostics.log").read_text()

        self.assertEqual(len(saved["steps"]), 4)
        self.assertEqual([step["status"] for step in saved["steps"]], ["PASS", "PASS", "PASS", "FAIL"])
        self.assertEqual(saved["steps"][-1]["name"], "outer journey orchestration")
        self.assertEqual(len(saved["case_run"]["claims"]), 3)
        self.assertIn("Traceback", diagnostic)
        self.assertEqual(result.status, "FAIL")

    def test_fixture_journey_rows_are_replayed_into_the_reporting_journey(self):
        fixture = Journey("local", "fixture-rows")
        fixture.step("fixture passes", "journey.test", lambda: None, cases=["S-900.1"])
        fixture.step("fixture fails", "journey.test", lambda: "fixture left a host running", cases=["S-900.2"])
        report = Journey("local", "reporting")
        report.step("earlier row", "journey.test", lambda: None, cases=["S-900.3"])

        replay_steps(fixture, report)

        self.assertEqual([(step.name, step.status, step.cases) for step in report.steps],
                         [("earlier row", "PASS", ["S-900.3"]),
                          ("fixture passes", "PASS", ["S-900.1"]),
                          ("fixture fails", "FAIL", ["S-900.2"])])
        self.assertEqual(report.steps[-1].message, "fixture left a host running")

    def test_journey_registry_is_shared_with_npm_resolution(self):
        compose = (Path(__file__).parent / "docker-compose.yml").read_text()
        registry = re.search(r"^\s+KB_JOURNEY_REGISTRY:\s*(.+)$", compose, re.MULTILINE)
        npm_registry = re.search(r"^\s+NPM_CONFIG_REGISTRY:\s*(.+)$", compose, re.MULTILINE)

        self.assertIsNotNone(registry)
        self.assertIsNotNone(npm_registry)
        anchor = re.fullmatch(r"&(?P<name>[A-Za-z0-9_-]+)\s+(?P<value>\S+)", registry.group(1))
        alias = re.fullmatch(r"\*(?P<name>[A-Za-z0-9_-]+)", npm_registry.group(1))
        self.assertIsNotNone(anchor)
        self.assertIsNotNone(alias)
        self.assertEqual(alias.group("name"), anchor.group("name"))

    def test_failed_setup_producer_reports_typed_reason_and_blocks_dependents(self):
        journey = Journey("secured", "setup-producer-failure")
        calls = []
        graph = FlowGraph(
            journey,
            {"install": (), "project": ("project-registered",), "adapter": ("project-registered",)},
            state_producers={"project-registered": "install"},
        )

        def failed_setup():
            raise RuntimeError("`kb project add /work/app` exited 1: KB_PROJECT_UNKNOWN; project root is missing; hint: create the project directory")

        graph.flow("journey.setup project registration", "install", "platform.install", failed_setup)
        graph.flow("project commands", "project", "projects", lambda: calls.append("project"))
        graph.flow("adapter commands", "adapter", "adapter", lambda: calls.append("adapter"))

        self.assertEqual(calls, [])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "BLOCKED", "BLOCKED"])
        self.assertIn("KB_PROJECT_UNKNOWN", journey.steps[0].message)
        self.assertIn("hint: create the project directory", journey.steps[0].message)
        self.assertIn("KB_PROJECT_UNKNOWN", journey.steps[1].message)

    def test_service_runner_main_consumes_full_list_and_selection_closure(self):
        declared = [
            ("selected analytics check", "analytics", lambda: None, ["S-201.3"]),
            ("unselected mcp check", "mcp", lambda: None, ["S-140.1"]),
        ]
        for mode in ("local", "secured"):
            for selected in (False, True):
                with self.subTest(mode=mode, selected=selected), tempfile.TemporaryDirectory() as directory:
                    output = Path(directory) / f"service-{mode}.json"
                    inherited = json.dumps({
                        "host-running": {"passed": True, "evidence": "stubbed"},
                        "project-registered": {"passed": True, "evidence": "stubbed"},
                    })
                    env = {
                        "KB_JOURNEY_PLATFORM_ROOT": str(Path(directory) / "platform"),
                        "KB_JOURNEY_STATES": inherited,
                    }
                    if selected:
                        env.update({"KB_JOURNEY_CASES": "S-201.3",
                                    "KB_JOURNEY_SELECTED_STEPS": "S-201.3"})
                    executed: list[str] = []

                    def execute_declared_steps(journey, steps, step_mode, *, graph):
                        for state, producer in STATE_PRODUCERS.items():
                            graph.probe_state(state, producer, f"producer {state}", lambda: None)
                        for name, capability, check, cases in steps:
                            group = next(group for group, spec in SERVICE_GROUPS.items()
                                         if capability in spec["capabilities"])
                            result = graph.step(name, group, capability, check, cases=cases)
                            if result.status == "PASS":
                                executed.append(name)

                    with patch("service_runner.checks", return_value=iter(declared)), \
                            patch("service_runner.run_service_steps", side_effect=execute_declared_steps), \
                            patch("service_runner.configure_installed_cli_path"), \
                            patch("service_runner.restore_secured_cli_login", return_value=None), \
                            patch("service_runner.run_analytics_steps"), \
                            patch("service_runner.run_gateway_steps"), \
                            patch("service_runner.archive_service_logs"), \
                            patch("service_runner.PROJECT_ID", ""), \
                            patch.dict(os.environ, env, clear=True), \
                            patch("sys.argv", ["service_runner.py", "--mode", mode, "--output", str(output)]), \
                            redirect_stdout(io.StringIO()):
                        service_main()

                    result = json.loads(output.read_text())
                    expected = {"selected analytics check"} if selected else {
                        "selected analytics check", "unselected mcp check"}
                    self.assertEqual(set(executed), expected)
                    self.assertEqual(len(executed), len(expected))
                    status_by_name = {step["name"]: step["status"] for step in result["steps"]}
                    if selected:
                        self.assertEqual(status_by_name["unselected mcp check"], "SKIPPED")
                        self.assertEqual(status_by_name["selected analytics check"], "PASS")
                        self.assertEqual(status_by_name["state probe: fixtures-installed"], "PASS")
                        self.assertEqual(status_by_name["state probe: marketplace-available"], "PASS")
                        allowed_passes = {
                            "selected analytics check",
                            "state probe: fixtures-installed",
                            "state probe: marketplace-available",
                            "state probe: session",
                            "state probe: agent-plugin-installed",
                            "archive runtime service logs",
                        }
                        self.assertTrue(all(
                            status == ("PASS" if name in allowed_passes else "SKIPPED")
                            for name, status in status_by_name.items()
                        ), status_by_name)

    def test_checks_returns_a_reusable_list_for_both_modes(self):
        for mode in ("local", "secured"):
            with self.subTest(mode=mode):
                declared = checks(mode)
                self.assertIsInstance(declared, list)
                first_pass = list(declared)
                second_pass = list(declared)
                self.assertEqual([step[0] for step in first_pass], [step[0] for step in second_pass])

    def test_agents_project_fixture_installs_through_marketplace(self):
        with tempfile.TemporaryDirectory() as temp:
            project = Path(temp) / "project"
            project.mkdir()
            calls = []

            def fake_run(command, **kwargs):
                calls.append((command, kwargs))
                return subprocess.CompletedProcess(command, 0, '{"ok":true,"data":{}}', "")

            with patch("service_runner._registry_version", return_value="2.120.1"), \
                    patch("service_runner.run_with_runner", side_effect=lambda runner, command, **kwargs: fake_run(command, **kwargs)):
                install_project_agents_plugin(str(project), "http://registry.test", {"HOME": temp})

            self.assertEqual(calls[0][0], [
                "kb", "marketplace", "install", "@kb-labs/agent-entry@2.120.1", "--json",
            ])
            self.assertEqual(calls[0][1]["cwd"], str(project.resolve()))

    def test_studio_shell_is_html_while_studio_api_uses_json_envelope(self):
        with patch("service_runner.request_raw", return_value=(200, b"<!doctype html><html></html>",
                                                               {"content-type": "text/html; charset=utf-8"})):
            self.assertIsNone(expect_html("/", "Studio shell"))
        with patch("service_runner.request_raw", return_value=(200, b"{\"ok\":true}",
                                                               {"content-type": "application/json"})):
            self.assertIn("HTML", expect_html("/", "Studio shell"))

    def test_studio_project_api_requires_the_shared_json_envelope(self):
        headers = {"content-type": "application/json"}
        with patch("service_runner.open_http", return_value=(200, b'{"ok":true,"data":{"projects":[]}}', headers)):
            status, body, _ = request_result("/api/v1/projects")
        self.assertEqual(status, 200)
        self.assertEqual(body, {"projects": []})

        with patch("service_runner.open_http", return_value=(200, b'{"projects":[]}', headers)):
            with self.assertRaisesRegex(RuntimeError, "UX JSON envelope"):
                request_result("/api/v1/projects")

    def test_first_failure_and_json_result(self):
        journey = Journey("local", "fixture")
        journey.step("health", "gateway", lambda: None)
        journey.step("workflow", "workflow", lambda: "workflow route returned 404")
        result = journey.result()
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.first_failure["name"], "workflow")
        self.assertEqual(len(result.steps), 2)
        self.assertEqual(result.steps[0]["cases"], [])
        self.assertEqual(result.case_run["claims"], [])
        with tempfile.TemporaryDirectory() as directory:
            saved = journey.write_json(Path(directory) / "journey.json")
            self.assertEqual(saved.schema, "kb.service-journey/v1")

    def test_selected_step_runs_declared_prerequisite_chain_and_labels_it(self):
        def selection_environment(*, cases):
            return {
                "KB_JOURNEY_SELECTED_STEPS": cases[0],
                "KB_JOURNEY_PREREQUISITE_STEPS": "candidate provenance,launcher version,start host",
            }

        env = selection_environment(cases=["S-023.9"])
        with patch.dict(os.environ, env, clear=False):
            journey = Journey("local", "selected-update-chain")
            journey.step("candidate provenance", "journey.orchestration", lambda: None)
            journey.step("launcher version", "platform.install", lambda: None, cases=["PC-001.1"])
            journey.step("start host", "platform.host", lambda: None, cases=["PC-002.1"])
            journey.step("update stopped host", "platform.update", lambda: None,
                         cases=["S-023.9"], requires=("step:candidate provenance", "step:launcher version",
                                                       "step:start host"))

        self.assertEqual([step.status for step in journey.steps], ["PASS"] * 4)
        self.assertEqual(journey.steps[0].details["selectionRole"], "prerequisite")
        self.assertEqual(journey.steps[1].details["selectionRole"], "prerequisite")
        self.assertEqual(journey.steps[2].details["selectionRole"], "prerequisite")
        self.assertEqual(journey.result().case_run["claims"][-1]["claimId"], journey.steps[-1].cases[0])

    def test_selected_claim_that_never_runs_fails_with_its_step_id(self):
        def selection_environment(*, cases):
            return {"KB_JOURNEY_SELECTED_STEPS": cases[0]}

        with patch.dict(os.environ, selection_environment(cases=["S-042.1"]), clear=False):
            journey = Journey("local", "missing-selected-claim")
            journey.step("unrelated check", "auth", lambda: None, cases=["S-042.2"])

        result = journey.result()
        self.assertEqual(result.status, "FAIL")
        self.assertIn("S-042.1", result.first_failure["message"])
        self.assertIn("registered step functions", result.first_failure["message"])

    def test_lifecycle_step_runs_outside_the_selected_claim_set(self):
        with patch.dict(os.environ, {"KB_JOURNEY_SELECTED_STEPS": "S-042.1"}, clear=False):
            journey = Journey("local", "lifecycle-step")
            result = journey.step("state probe: host", "journey.state", lambda: None, role="lifecycle")
        self.assertEqual(result.status, "PASS")
        self.assertEqual(result.details["selectionRole"], "lifecycle")

    def test_failed_prerequisite_blocks_the_selected_step_with_reason(self):
        def selection_environment(*, cases):
            return {
                "KB_JOURNEY_SELECTED_STEPS": cases[0],
                "KB_JOURNEY_PREREQUISITE_STEPS": "project registration",
            }

        with patch.dict(os.environ, selection_environment(cases=["S-042.1"]), clear=False):
            journey = Journey("local", "failed-selected-prerequisite")
            journey.step("project registration", "platform.project", lambda: "registration failed")
            target = journey.step("update", "platform.update", lambda: None, cases=["S-042.1"],
                                  requires=("step:project registration",))

        self.assertEqual(target.status, "BLOCKED")
        self.assertIn("project registration ended FAIL", target.message)
        self.assertEqual(target.details["prerequisiteFailures"],
                         ["step:project registration ended FAIL: registration failed"])
        self.assertEqual(journey.result().case_run["claims"], [])
        self.assertEqual(journey.result().status, "FAIL")

    def test_missing_prerequisite_blocks_selected_step_with_journey_status_blocked(self):
        def selection_environment(*, cases):
            return {
                "KB_JOURNEY_SELECTED_STEPS": cases[0],
            }

        with patch.dict(os.environ, selection_environment(cases=["S-042.1"]), clear=False):
            journey = Journey("local", "missing-selected-prerequisite")
            target = journey.step("update", "platform.update", lambda: None, cases=["S-042.1"],
                                  requires=("step:unrun-prerequisite",))

        self.assertEqual(target.status, "BLOCKED")
        self.assertIn("unrun-prerequisite did not run before update", target.message)
        self.assertEqual(journey.result().case_run["claims"], [])
        self.assertEqual(journey.result().status, "BLOCKED")

    def test_reflected_recorder_still_blocks_unseen_runtime_dependency(self):
        with patch.dict(os.environ, {"KB_JOURNEY_SELECTED_STEPS": "S-042.1"}, clear=False):
            journey = Journey("local", "reflected-recorder-prerequisite")
            recorder = getattr(journey, "step")
            target = recorder("update", "platform.update", lambda: None,
                              cases=["S-042.1"], requires=("step:unrun-prerequisite",))

        self.assertEqual(target.status, "BLOCKED")
        self.assertIn("step:unrun-prerequisite did not run before update", target.message)
        self.assertEqual(journey.result().case_run["claims"], [])
        self.assertEqual(journey.result().status, "BLOCKED")

    def test_failed_host_start_blocks_nested_update_in_full_run(self):
        with patch.dict(os.environ, {
            "KB_JOURNEY_SELECTED_STEPS": "",
            "KB_JOURNEY_PREREQUISITE_STEPS": "",
            "KB_JOURNEY_PREREQUISITE_FLOWS": "",
        }, clear=False):
            parent = Journey("local", "full-update-parent")
            graph = FlowGraph(parent, {})
            self.assertEqual(graph.flow(
                "start platform host", "host", "platform.host",
                lambda: parent.step("start host", "platform.host", lambda: "host did not start"),
            ), "FAIL")
            child = Journey("local", "nested-update", parent=parent)
            blocked = child.step(
                "update platform", "platform.update", lambda: None,
                requires=("flow:start platform host", "step:start host"),
            )

        self.assertEqual(blocked.status, "BLOCKED")
        self.assertIn("flow:start platform host ended FAIL", blocked.message)
        self.assertIn("step:start host ended FAIL", blocked.message)

    def test_nested_journey_resolves_parent_step_and_flow_outcomes_live(self):
        parent = Journey("local", "nested-parent")
        graph = FlowGraph(parent, {})
        graph.flow("project registration", "project", "projects",
                   lambda: parent.step("project registered", "projects", lambda: None))
        child = Journey("local", "nested-child", parent=parent)
        result = child.step("dependent", "project", lambda: None,
                            requires=("flow:project registration", "step:project registered"))
        self.assertEqual(result.status, "PASS")

    def test_journey_result_case_run_loads_through_qa_cases_coverage_consumer(self):
        root = Path(__file__).resolve().parents[2]
        node = shutil.which("node")
        self.assertIsNotNone(node)
        build = subprocess.run(
            [str(root / "tools/kb-devkit/kb-devkit"), "run", "build", "--packages",
             "@kb-labs/cli-bin,@kb-labs/qa-entry"],
            cwd=root, text=True, capture_output=True, check=False, timeout=900,
        )
        self.assertEqual(build.returncode, 0, f"case coverage CLI build failed: {build.stderr or build.stdout}")
        cli_bin = root / "cli/bin/dist/bin.js"
        self.assertTrue(cli_bin.is_file(), f"CLI binary not found at {cli_bin}")

        with tempfile.TemporaryDirectory(prefix="qa-coverage-consumer-test-") as directory:
            run_file = Path(directory) / "test-run.json"
            journey = Journey("local", "coverage-consumer-test-run")
            # Step with PASS claim: PC-001.1 is claimed by e2e/journey/runner.py
            journey.step("provenance check", "platform.install", lambda: None, cases=["PC-001.1"])
            # Step that FAILS: PC-001.2 is claimed by e2e/journey/runner.py
            journey.step("install check", "platform.install", lambda: "installation failed", cases=["PC-001.2"])
            # Step that is BLOCKED: PC-001.3 is claimed by e2e/journey/runner.py, requiring PC-001.2
            journey.step("dependent check", "platform.install", lambda: None, cases=["PC-001.3"],
                         requires=("step:install check",))

            journey_result = journey.result()
            self.assertEqual(journey_result.status, "FAIL")
            steps_by_name = {step.name: step for step in journey.steps}
            self.assertEqual(steps_by_name["provenance check"].status, "PASS")
            self.assertEqual(steps_by_name["install check"].status, "FAIL")
            self.assertEqual(steps_by_name["dependent check"].status, "BLOCKED")

            # Crucial producer invariant: claims array contains ONLY PASS and FAIL, NO BLOCKED
            claims_map = {claim["claimId"]: claim["status"] for claim in journey_result.case_run["claims"]}
            self.assertEqual(claims_map, {"PC-001.1": "PASS", "PC-001.2": "FAIL"})
            self.assertNotIn("PC-001.3", claims_map)

            # Write out result
            journey.write_json(run_file)

            # Load through the real consumer CLI
            command = [node, str(cli_bin), "qa", "cases", "coverage", "--mode", "local",
                       "--run", str(run_file), "--json"]
            env = dict(os.environ)
            env["HOME"] = directory
            env.pop("KB_PLATFORM_ROOT", None)
            env.pop("KB_HOME", None)
            env.pop("KB_NET_OFFSET", None)
            from hermetic_cli import run_hermetic_cli
            proc = run_hermetic_cli(command, cwd=root, env=env, timeout=120)
            self.assertEqual(proc.returncode, 0, f"qa cases coverage failed: {proc.stderr or proc.stdout}")
            payload = json.loads(proc.stdout)
            self.assertTrue(payload.get("ok"), payload)
            coverage_data = payload.get("data", {})
            self.assertEqual(coverage_data.get("schema"), "kb.qa-case-coverage/v1")
            pc001 = next((c for c in coverage_data.get("cases", []) if c.get("id") == "PC-001"), None)
            self.assertIsNotNone(pc001)
            steps_cov = {s.get("stepId"): s for s in pc001.get("steps", [])}
            self.assertEqual(steps_cov["PC-001.1"].get("status"), "PASS")
            self.assertEqual(steps_cov["PC-001.2"].get("status"), "FAIL")
            self.assertEqual(steps_cov["PC-001.3"].get("status"), "NOT RUN")
            self.assertIn("Journey/e2e claim exists but has no passing run result", steps_cov["PC-001.3"].get("reason", ""))

            # Also verify consumer strictly rejects if a run file contains status: BLOCKED in claims
            corrupted_run_file = Path(directory) / "corrupted-run.json"
            corrupted_data = json.loads(run_file.read_text())
            corrupted_data["case_run"]["claims"].append({"claimId": "PC-001.3", "kind": "journey", "status": "BLOCKED"})
            corrupted_run_file.write_text(json.dumps(corrupted_data))

            corrupted_proc = run_hermetic_cli([node, str(cli_bin), "qa", "cases", "coverage", "--mode", "local",
                                              "--run", str(corrupted_run_file), "--json"], cwd=root, env=env, timeout=120)
            self.assertIn("claims[2].status must be PASS or FAIL", corrupted_proc.stdout + corrupted_proc.stderr)

    def test_service_step_exception_is_saved_and_later_steps_continue(self):
        journey = Journey("local", "raised-step")

        def broken_check():
            raise RuntimeError("marketplace CLI is unavailable")

        run_service_steps(journey, [
            ("marketplace registry", "marketplace", broken_check, []),
            ("gateway readiness", "gateway", lambda: None, []),
        ], states={
            "host-running": {"passed": True, "evidence": "healthy", "group": "install", "step": "start", "claim": "PC-002.1"},
            "project-registered": {"passed": True, "evidence": "registered", "group": "project", "step": "add", "claim": "no case claim"},
        })
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "service-local.json"
            result = journey.write_json(path)
            saved = json.loads(path.read_text())

        self.assertEqual(result.status, "FAIL")
        self.assertEqual(saved["steps"][0]["status"], "FAIL")
        self.assertEqual(saved["steps"][0]["message"], "marketplace CLI is unavailable")
        self.assertEqual(saved["steps"][1]["status"], "PASS")

    def test_service_step_preserves_blocked_result_without_case_claim(self):
        journey = Journey("local", "blocked-step")
        run_service_steps(journey, [
            ("gateway readiness", "gateway", lambda: "BLOCKED: gateway fixture is missing", []),
        ])
        self.assertEqual(journey.steps[0].status, "BLOCKED")
        self.assertEqual(journey.result().case_run["claims"], [])

    def test_product_failure_with_healthy_state_probes_does_not_block_later_groups(self):
        journey = Journey("local", "product-failure-healthy-state")
        calls = []
        graph = FlowGraph(journey, {"update": (), "auth": ("host-running", "platform-version-known")},
                          states={
                              "host-running": {"passed": True, "evidence": "healthy",
                                               "group": "update", "step": "rollback", "claim": "S-024.1"},
                              "platform-version-known": {"passed": True, "evidence": "version N restored",
                                                          "group": "update", "step": "rollback", "claim": "S-024.1"},
                          })
        graph.flow("update product assertion", "update", "platform.update",
                   lambda: journey.step("saved session assertion", "platform.update",
                                         lambda: "saved session was rejected", cases=["S-023.5"]),
                   cases=["S-023.5"])
        graph.flow("auth-dependent group", "auth", "auth", lambda: calls.append("auth"),
                   cases=["S-041.1"])

        self.assertEqual(calls, ["auth"])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "PASS"])
        self.assertEqual(journey.steps[0].cases, ["S-023.5"])

    def test_failed_host_probe_blocks_dependents_with_probe_evidence_and_claim(self):
        journey = Journey("local", "host-state-block")
        calls = []
        graph = FlowGraph(
            journey, {"update": ("host-running",), "auth": ("host-running",)},
            states={"host-running": {"passed": True, "evidence": "healthy before update",
                                     "group": "install", "step": "start/status", "claim": "PC-002.2"}},
        )
        graph.flow("update journey", "update", "platform.update",
                   lambda: journey.step("update product assertion", "platform.update",
                                        lambda: "saved session rejected", cases=["S-023.5"]),
                   cases=["S-023.5"])
        graph.probe_state("host-running", "update", "rollback/status",
                          lambda: "kb-create status reports running=False; gateway /ready returned HTTP 503")
        graph.flow("auth group", "auth", "auth", lambda: calls.append("auth"), cases=["S-041.1"])

        self.assertEqual(calls, [])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "FAIL", "BLOCKED"])
        self.assertIn("blocked by state host-running:", journey.steps[2].message)
        self.assertIn("running=False", journey.steps[2].message)
        self.assertIn("update/rollback/status (no case claim)", journey.steps[2].message)

    def test_saved_session_assertion_does_not_block_after_fresh_login_probe(self):
        journey = Journey("secured", "saved-session-failure-fresh-login")
        calls = []
        graph = FlowGraph(journey, {"auth": (), "plugin-author": ("session",)})
        graph.flow("saved session product assertion", "auth", "auth",
                   lambda: journey.step("saved session rejected", "auth", lambda: "saved token rejected",
                                        cases=["S-023.5"]), cases=["S-023.5"])
        graph.probe_state("session", "auth", "fresh admin login", lambda: None)
        graph.flow("plugin author group", "plugin-author", "plugin-author",
                   lambda: calls.append("plugin-author"), cases=["S-014.1"])

        self.assertEqual(calls, ["plugin-author"])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "PASS", "PASS"])

    def test_platform_prerequisite_failure_blocks_each_dependent_group_with_claim(self):
        journey = Journey("local", "platform-stop")
        calls = []
        groups = {
            "platform": {"capabilities": {"gateway"}, "depends_on": ()},
            "auth": {"capabilities": {"auth.local"}, "depends_on": ("host-running",)},
            "projects": {"capabilities": {"projects"}, "depends_on": ("host-running",)},
        }
        graph = FlowGraph(journey, {name: spec["depends_on"] for name, spec in groups.items()})
        graph.probe_state("host-running", "install", "gateway readiness", lambda: "host is down")
        run_grouped_service_steps(journey, [
            ("local identity", "auth.local", lambda: calls.append("auth"), []),
            ("project registration", "projects", lambda: calls.append("projects"), []),
        ], groups=groups, graph=graph)

        self.assertEqual(calls, [])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "BLOCKED", "BLOCKED"])
        self.assertIn("blocked by state host-running", journey.steps[1].message)
        self.assertIn("no case claim", journey.steps[1].message)
        self.assertIn("gateway readiness", journey.steps[2].message)

    def test_fixture_probe_blocks_only_groups_that_need_the_fixture(self):
        journey = Journey("local", "fixture-state")
        calls = []
        graph = FlowGraph(journey, {"workflow": (), "mcp": ("fixtures-installed",), "studio": ()})
        graph.flow("marketplace install assertion", "marketplace", "marketplace",
                   lambda: journey.step("marketplace product assertion", "marketplace",
                                        lambda: "route returned 500", cases=["S-010.1"]), cases=["S-010.1"])
        graph.probe_state("fixtures-installed", "marketplace-fixtures", "fixture handoff",
                          lambda: "project B fixture ID is missing")
        graph.flow("workflow health", "workflow", "workflow", lambda: calls.append("workflow"))
        graph.flow("project-scoped MCP initialize", "mcp", "mcp", lambda: calls.append("mcp"),
                   cases=["S-140.2"])
        graph.flow("Studio shell", "studio", "studio", lambda: calls.append("studio"))

        self.assertEqual(calls, ["workflow", "studio"])
        self.assertEqual([step.status for step in journey.steps], ["FAIL", "FAIL", "PASS", "BLOCKED", "PASS"])
        self.assertIn("blocked by state fixtures-installed:", journey.steps[3].message)

    def test_flow_graph_isolates_failures_blocks_dependents_and_always_runs_cleanup(self):
        journey = Journey("local", "outer-flow-graph")
        calls = []
        with tempfile.TemporaryDirectory() as directory:
            graph = FlowGraph(
                journey,
                {"install": (), "host": ("platform-installed",), "adapter": (),
                 "analytics": ("host-running",), "adapter-dependent": ("adapter-defaults",),
                 "uninstall": ("install",)},
                states={"platform-installed": {"passed": True, "evidence": "receipt present", "group": "install", "step": "apply", "claim": "PC-001.1"},
                        "host-running": {"passed": True, "evidence": "healthy", "group": "install", "step": "start", "claim": "PC-002.1"}},
                diagnostics_path=Path(directory) / "journey-diagnostics.log",
            )

            graph.flow("install platform", "install", "platform.install",
                       lambda: calls.append("install"), cases=["PC-001.1"])
            graph.flow("adapter defaults", "adapter", "adapter",
                       lambda: (_ for _ in ()).throw(RuntimeError("adapter fixture broke")),
                       cases=["S-054.1"])
            graph.probe_state("adapter-defaults", "adapter", "adapter default verification",
                              lambda: "configured adapter default is unavailable")
            graph.flow("analytics extras", "analytics", "analytics",
                       lambda: calls.append("analytics"), cases=["S-191.1"])
            graph.flow("workflow depending on adapter", "adapter-dependent", "workflow",
                       lambda: calls.append("dependent"), cases=["S-054.3"])
            graph.flow("uninstall platform", "uninstall", "platform.uninstall",
                       lambda: calls.append("uninstall"), cases=["S-050.6"], role="lifecycle")

            diagnostic = (Path(directory) / "journey-diagnostics.log").read_text()

        self.assertEqual(calls, ["install", "analytics", "uninstall"])
        self.assertEqual([step.status for step in journey.steps], ["PASS", "FAIL", "FAIL", "PASS", "BLOCKED", "PASS"])
        self.assertIn("RuntimeError: adapter fixture broke", diagnostic)
        self.assertIn("blocked by state adapter-defaults:", journey.steps[4].message)
        self.assertIn("adapter/adapter default verification (no case claim)", journey.steps[4].message)

    def test_flow_exception_is_orchestration_evidence_without_case_claims(self):
        journey = Journey("local", "flow-exception-no-claim")
        graph = FlowGraph(journey, {"project": ()})

        graph.flow("project orchestration", "project", "projects",
                   lambda: (_ for _ in ()).throw(RuntimeError("project setup failed")),
                   cases=["PC-002.8"])

        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertEqual(journey.steps[0].cases, [])
        self.assertEqual(journey.result().case_run["claims"], [])

    def test_state_probe_rejects_case_claims(self):
        graph = FlowGraph(Journey("local", "probe-no-claim"), {})

        with self.assertRaises(TypeError):
            graph.probe_state("project-registered", "project", "project add", lambda: None,
                              cases=["PC-002.8"])

    def test_installed_cli_bin_is_added_to_service_path(self):
        with tempfile.TemporaryDirectory() as directory, patch("pathlib.Path.home", return_value=Path(directory)), \
                patch.dict("os.environ", {"PATH": "/usr/bin"}):
            configure_installed_cli_path()
            self.assertEqual(os.environ["PATH"].split(os.pathsep)[0], str(Path(directory) / ".local" / "bin"))

    def test_runner_writes_result_json_when_orchestration_raises(self):
        for mode in ("local", "secured"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / f"service-{mode}.json"
                diagnostics = output.with_name("journey-diagnostics.log")
                stdout = io.StringIO()
                with patch("service_runner.archive_service_logs") as archive, \
                        patch("service_runner.checks", side_effect=RuntimeError("simulated orchestration error")), \
                        patch.dict(os.environ, {"KB_JOURNEY_PLATFORM_ROOT": str(Path(directory) / "platform")}), \
                        patch("sys.argv", ["service_runner.py", "--mode", mode, "--output", str(output)]), \
                        redirect_stdout(stdout):
                    status = service_main()
                result = json.loads(output.read_text())

                self.assertEqual(status, 1)
                self.assertIn("states", result)
                runner_step = next(step for step in result["steps"] if step["name"] == "service runner")
                self.assertEqual(runner_step["status"], "FAIL")
                self.assertEqual(runner_step["message"], "simulated orchestration error")
                self.assertIn("RuntimeError: simulated orchestration error", diagnostics.read_text())
                self.assertEqual(result["steps"][-1]["name"], "archive runtime service logs")
                self.assertEqual(result["steps"][-1]["status"], "PASS")
                archive.assert_called_once()
                self.assertEqual(archive.call_args.kwargs["mode"], mode)
                self.assertEqual(archive.call_args.kwargs["platform_root"], str(Path(directory) / "platform"))
                self.assertNotIn("Traceback", stdout.getvalue())

    def test_pty_timeout_redacts_secret_from_invocation_step_report_and_stdout(self):
        with tempfile.TemporaryDirectory(prefix="journey-redaction-") as directory:
            root = Path(directory)
            output = root / "service-local.json"
            env = {
                "HOME": str(root / "home"),
                "KB_HOME": str(root / "home" / ".kb"),
                "KB_JOURNEY_PLATFORM_ROOT": str(root / "platform"),
                "KB_JOURNEY_INVOCATIONS": str(root),
            }
            password = store_report_secret(
                env, "KB_JOURNEY_ADMIN_PASSWORD", "synthetic-journey-password-never-print",
                "synthetic PTY regression secret")
            env["KB_JOURNEY_STATES"] = json.dumps({"candidateState": password})
            stdout = io.StringIO()

            def add_timeout_step(journey, *_args, **_kwargs):
                journey.step(
                    "synthetic PTY timeout", "journey.auth",
                    lambda: run_pty_command(
                        [sys.executable, "-c",
                         "import os,time; print(os.environ['KB_JOURNEY_ADMIN_PASSWORD'], flush=True); time.sleep(5)"],
                        env=os.environ.copy(), timeout=0.3,
                    ),
                )

            with patch.dict(os.environ, env, clear=False), \
                    patch("service_runner.checks", return_value=[]), \
                    patch("service_runner.configure_installed_cli_path"), \
                    patch("service_runner.run_service_steps", side_effect=add_timeout_step), \
                    patch("service_runner.FlowGraph.flow", return_value=None), \
                    patch("service_runner.archive_service_logs"), \
                    patch("sys.argv", ["service_runner.py", "--mode", "local", "--output", str(output)]), \
                    redirect_stdout(stdout):
                status = service_main()

            report_text = output.read_text()
            report = json.loads(report_text)
            invocation_text = (root / "invocations" / "calls.jsonl").read_text()
            step_message = next(step["message"] for step in report["steps"]
                                if step["name"] == "synthetic PTY timeout")
            for rendered in (invocation_text, step_message, report_text, stdout.getvalue()):
                if password in rendered:
                    self.fail("synthetic secret leaked through a journey output boundary")
            self.assertEqual(status, 1)
            self.assertIn("[REDACTED]", step_message)
            self.assertIn("[REDACTED]", invocation_text)

    def test_pre_service_failure_is_saved_with_step_and_message(self):
        with tempfile.TemporaryDirectory() as directory:
            result = record_runner_failure(Path(directory) / "service-local.json", "local",
                                           "install.sh: download returned HTTP 404")
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.steps[0]["capability"], "journey.install")
        self.assertEqual(result.steps[0]["name"], "install.sh")
        self.assertEqual(result.steps[0]["message"], "download returned HTTP 404")

    def test_runner_startup_exception_writes_failure_result_without_traceback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            broken_runner = root / "broken_runner.py"
            broken_runner.write_text('raise RuntimeError("simulated import failure")\n')
            summary = root / "journey-summary.md"
            stderr = io.StringIO()
            with redirect_stderr(stderr):
                status = run_runner(broken_runner, summary, "local")
            result = load_results(root)[0]
        self.assertEqual(status, 1)
        self.assertEqual(result["steps"][0]["capability"], "journey.runner")
        self.assertEqual(result["steps"][0]["status"], "FAIL")
        self.assertEqual(result["steps"][0]["name"], "runner startup")
        self.assertIn("simulated import failure", result["steps"][0]["message"])
        self.assertNotIn("Traceback", stderr.getvalue())

    def test_runner_crash_is_labeled_separately_from_startup_and_logs_traceback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            crashing_runner = root / "crashing_runner.py"
            crashing_runner.write_text('def main():\n    raise RuntimeError("simulated run crash")\n')
            stderr = io.StringIO()
            with patch.dict(os.environ, {"HOME": str(root)}), redirect_stderr(stderr):
                status = run_runner(crashing_runner, root / "journey-summary.md", "local")
            result = load_results(root)[0]
            self.assertEqual(status, 1)
            self.assertEqual(result["steps"][0]["name"], "runner crash")
            self.assertIn("simulated run crash", result["steps"][0]["message"])
            diagnostics = (root / "journey-diagnostics.log").read_text()
            self.assertIn("Traceback (most recent call last)", diagnostics)
            self.assertIn("RuntimeError: simulated run crash", diagnostics)
            self.assertIn("Journey runner crashed: RuntimeError: simulated run crash", stderr.getvalue())

    def test_secured_release_admin_signs_in_without_unneeded_platform_config(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            home = root / "home"
            home.mkdir()
            project = home / "fresh-project"
            project.mkdir()
            commands = []

            def fake_run(args, *, env, cwd, timeout):
                commands.append((args, cwd))
                return subprocess.CompletedProcess(args, 0, "{}", ""), 0.0

            result = login_and_configure_secured_admin(
                env={"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project),
                     "KB_JOURNEY_PLATFORM_ROOT": str(root / "platform"),
                     "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                     "KB_JOURNEY_ADMIN_PASSWORD": "password"},
                launcher="kb-create", run_command=fake_run)

        self.assertIsNone(result)
        self.assertEqual(len(commands), 1)
        self.assertIn("kb auth login", commands[0][0][2])
        self.assertEqual(commands[0][1], project)

    def test_secured_admin_signs_in_before_authenticated_provider_config(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            home = root / "home"
            home.mkdir()
            project = home / "fresh-project"
            project.mkdir()
            commands = []

            def fake_run(args, *, env, cwd, timeout):
                commands.append((args, cwd))
                return subprocess.CompletedProcess(args, 0, "{}", ""), 0.0

            result = login_and_configure_secured_admin(
                env={"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project),
                     "KB_JOURNEY_PLATFORM_ROOT": str(root / "platform"),
                     "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                     "KB_JOURNEY_ADMIN_PASSWORD": "password"},
                launcher="kb-create", configure_oidc=True, run_command=fake_run)

        self.assertIsNone(result)
        self.assertEqual(len(commands), 3)
        self.assertIn("kb auth login", commands[0][0][2])
        self.assertIn("gateway.auth.providers", commands[1][0])
        self.assertEqual(commands[1][1], home)
        self.assertEqual(commands[2][0], ["kb-create", "restart", "--platform-root", str(root / "platform")])

    def test_secured_platform_config_is_not_attempted_when_admin_login_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            project = root / "project"
            project.mkdir()
            commands = []

            def fake_run(args, *, env, cwd, timeout):
                commands.append(args)
                return subprocess.CompletedProcess(args, 1, "login denied", ""), 0.0

            result = login_and_configure_secured_admin(
                env={"HOME": str(root), "KB_JOURNEY_PROJECT_ROOT": str(project),
                     "KB_JOURNEY_PLATFORM_ROOT": str(root / "platform"),
                     "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                     "KB_JOURNEY_ADMIN_PASSWORD": "password"},
                launcher="kb-create", run_command=fake_run)

        self.assertEqual(result[0], "kb auth login")
        self.assertEqual(len(commands), 1)
        self.assertIn("kb auth login", commands[0][2])

    def test_mcp_probe_sends_streamable_http_accept_and_parses_sse(self):
        import json
        tools = [{"name": name, "inputSchema": {"type": "object"}}
                 for name in ("kb-labs-commit__commit", "kb-labs-workflow__workflow_run", "kb-labs__mcp_diagnostics")]
        event = 'event: message\ndata: ' + json.dumps({"jsonrpc": "2.0", "id": 2, "result": {"tools": tools}}) + '\n\n'
        with patch("service_runner.request", return_value=(200, event, {})) as request:
            failure = mcp_probe("prj_test", "token")
        self.assertIsNone(failure)
        self.assertEqual(request.call_args.kwargs["accept"], "application/json, text/event-stream")

    def test_mcp_probe_uses_sanitized_platform_member_manifest_id(self):
        tools = [{"name": name, "inputSchema": {"type": "object"}}
                 for name in ("kb-labs-commit__commit", "workflow__workflow_run", "kb-labs__mcp_diagnostics")]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})):
            failure = mcp_probe("prj_test", "token")
        self.assertIn("kb-labs-workflow__workflow_run", failure)

    def test_prepare_mcp_projects_exports_id_and_canonical_root_from_same_registration(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            home = root / "home"
            home.mkdir()
            project_a = root / "project-a"
            project_a.mkdir()
            subprocess.run(["git", "init", "-q", str(project_a)], check=True)
            (project_a / "note.txt").write_text("journey note\n")
            subprocess.run(["git", "-C", str(project_a), "add", "note.txt"], check=True)
            fixture_file = home / "project-fixtures.json"
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_FIXTURES_FILE": str(fixture_file)}
            calls = []
            registered_project_exists = []
            registered_project_roots = []

            def fake_runner(command, **kwargs):
                calls.append(command)
                if command[:3] == ["kb", "project", "add"]:
                    registry_project_root = str(Path(kwargs["cwd"]).resolve())
                    registered_project_roots.append(registry_project_root)
                    registered_project_exists.append(
                        Path(registry_project_root).is_dir() and (Path(registry_project_root) / ".git").is_dir())
                    return subprocess.CompletedProcess(
                        command, 0,
                        json.dumps({"ok": True, "data": {"project": {"id": "prj_b", "path": registry_project_root}}}), "")
                return subprocess.CompletedProcess(command, 0, "{}", "")

            with patch("service_runner._registry_version", return_value="2.120.1"):
                with patch.dict(os.environ, {}, clear=False):
                    project_b_id, project_b_root = _prepare_and_export_mcp_projects(
                        str(project_a), "http://registry.test", env, fake_runner)
                    self.assertEqual(os.environ["KB_JOURNEY_PROJECT_B_ID"], project_b_id)
                    self.assertEqual(os.environ["KB_JOURNEY_PROJECT_B_ROOT"], project_b_root)
            project_id = "prj_" + hashlib.sha256(
                str(project_a.resolve()).encode("utf-8")).hexdigest()[:16]
            plan_path = home / ".kb" / "state" / project_id / "commit" / "plans" / "root" / "current" / "plan.json"
            plan = json.loads(plan_path.read_text())
            handoff = json.loads(fixture_file.read_text())

        self.assertEqual(project_b_id, "prj_b")
        self.assertEqual(registered_project_roots, [project_b_root])
        self.assertEqual(env["KB_JOURNEY_PROJECT_B_ID"], project_b_id)
        self.assertEqual(env["KB_JOURNEY_PROJECT_B_ROOT"], project_b_root)
        self.assertEqual(handoff, {
            "projectAId": PROJECT_ID,
            "projectARoot": str(project_a.resolve()),
            "projectBId": project_b_id,
            "projectBRoot": project_b_root,
        })
        self.assertEqual(calls[0], ["kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1", "--json"])
        registration_calls = [call for call in calls if call[:3] == ["kb", "project", "add"]]
        self.assertEqual(len(registration_calls), 1)
        self.assertEqual(calls[2], ["kb", "marketplace", "install", "@kb-labs/review-entry@2.120.1", "--json"])
        self.assertEqual(registration_calls[0][0:3], ["kb", "project", "add"])
        self.assertEqual(registered_project_exists, [True])
        self.assertEqual(plan["gitStatus"]["staged"], ["note.txt"])
        self.assertEqual(plan["commits"][0]["files"], ["note.txt"])

    def test_mcp_initialize_checks_protocol_response(self):
        response = {"result": {"protocolVersion": "2025-03-26"}}
        with patch("service_runner.request", return_value=(200, response, {})) as request:
            self.assertIsNone(mcp_initialize("prj_test", "token"))
        self.assertEqual(request.call_args.kwargs["payload"]["method"], "initialize")
        with patch("service_runner.request", return_value=(200, {"result": {}}, {})):
            self.assertIn("no result", mcp_initialize("prj_test", "token"))

    def test_mcp_diagnostics_checks_clean_project_result(self):
        result = {"result": {"isError": False, "content": [{"text": "Project loaded cleanly"}]}}
        with patch("service_runner.request", return_value=(200, result, {})) as request:
            self.assertIsNone(mcp_diagnostics("prj_test", "token"))
        self.assertEqual(request.call_args.kwargs["payload"]["params"]["name"], "kb-labs__mcp_diagnostics")
        with patch("service_runner.request", return_value=(200, {"result": {"isError": True, "content": []}}, {})):
            self.assertIn("did not report", mcp_diagnostics("prj_test", "token"))

    def test_health_claim_requires_json_object(self):
        with patch("service_runner.request", return_value=(200, {"ok": True, "data": {"ready": True}}, {})):
            self.assertIsNone(expect_json("/ready", "gateway readiness"))
        with patch("service_runner.request", side_effect=ValueError("response is not a valid UX success envelope")):
            self.assertIn("invalid UX result envelope", expect_json("/ready", "gateway readiness"))

    # case: S-041.4
    def test_secured_auth_journey_preserves_session_until_logout_after_restart(self):
        calls = []
        state = {"admin_revoked": False}

        def fake_request(_base, path, *, method="GET", token=None, body=None, expected=200):
            calls.append((method, path, token, body, expected))
            if path == "/auth/register":
                self.assertEqual(expected, 401)
                return {
                    "code": "KB_AUTH_REQUIRED",
                    "message": "Authentication is required.",
                    "hint": 'Run "kb auth login --gateway-url http://gateway.test --email <email>".',
                }
            if path == "/auth/permissions":
                return {"permissions": ["users:read"]}
            if path == "/auth/users" and expected == 200:
                return {"users": [{"email": "member@example.test", "userId": "member-1"}]}
            if path == "/auth/invites":
                return {"activationUrl": "http://localhost/activate/invite-token"}
            if path.endswith("password-reset-link"):
                return {"resetUrl": "http://localhost/reset-password/reset-token-for-journey"}
            if path == "/auth/me" and token == "admin-access":
                self.assertEqual(expected, 401 if state["admin_revoked"] else 200)
                return {} if state["admin_revoked"] else {"email": "admin@example.test"}
            if path == "/auth/me" and token == "fresh-admin-token":
                self.assertEqual(expected, 200)
                return {"email": "admin@example.test"}
            if path == "/auth/logout/cli" and body == {"refreshToken": "admin-refresh"}:
                state["admin_revoked"] = True
                return {"ok": True}
            if path == "/auth/refresh/cli" and body == {"refreshToken": "admin-refresh"}:
                return {"error": "invalid_session"}
            return {}

        logins = iter([
            ("admin-access", "admin-refresh"),
            ("member-access", "member-refresh"),
            ("member-replacement-access", "member-replacement-refresh"),
            ("fresh-admin-token", "fresh-admin-refresh"),
        ])

        def fake_login(_base, email, password):
            calls.append(("LOGIN", email, password, None, None))
            return next(logins)

        def restart_host():
            calls.append(("RESTART", "", None, None, None))
            return "http://gateway.test/restarted"

        with patch("steps.auth._request", side_effect=fake_request), \
             patch("steps.auth._login", side_effect=fake_login):
            run_secured_auth_steps("http://gateway.test", "admin@example.test", "synthetic-admin-password",
                                   "member@example.test", restart_host)

        restart = calls.index(("RESTART", "", None, None, None))
        restart_check = calls.index(("GET", "/auth/me", "admin-access", None, 200), restart)
        logout = calls.index(("POST", "/auth/logout/cli", None, {"refreshToken": "admin-refresh"}, 200))
        revoked_check = calls.index(("GET", "/auth/me", "admin-access", None, 401))
        fresh_login = calls.index(("LOGIN", "admin@example.test", "synthetic-admin-password", None, None), restart)
        fresh_check = calls.index(("GET", "/auth/me", "fresh-admin-token", None, 200))
        self.assertLess(restart_check, logout)
        self.assertLess(logout, revoked_check)
        self.assertLess(revoked_check, fresh_login)
        self.assertLess(fresh_login, fresh_check)

    def test_dockerfile_copies_modules_imported_by_journey_scripts_together(self):
        root = Path(__file__).resolve().parents[2]
        journey_dir = Path(__file__).resolve().parent
        copied_into_package = set()
        wrapper_copied = False
        for line in (journey_dir / "Dockerfile.runner").read_text().splitlines():
            if not line.lstrip().startswith("COPY "):
                continue
            tokens = shlex.split(line)
            destination = tokens[-1]
            for source in tokens[1:-1]:
                for path in root.glob(source):
                    target = Path(destination)
                    if path.is_dir() and str(target) == "/opt/kb-journey/e2e/journey":
                        copied_into_package.update(child.name for child in path.glob("*.py"))
                    if destination.endswith("/"):
                        target /= path.name
                    if str(target) == "/usr/local/bin/kb-journey-runner" and path.name == "entrypoint.py":
                        wrapper_copied = True
                    if str(target.parent) == "/opt/kb-journey/e2e/journey":
                        copied_into_package.add(path.name)

        local_modules = {path.stem for path in journey_dir.glob("*.py")}
        imported_modules = set()
        for filename in ("runner.py", "service_runner.py", "studio_steps.py"):
            tree = ast.parse((journey_dir / filename).read_text(), filename=filename)
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    imported_modules.update(alias.name.split(".", 1)[0] for alias in node.names)
                elif isinstance(node, ast.ImportFrom) and node.module:
                    imported_modules.add(node.module.split(".", 1)[0])
        required_modules = {f"{name}.py" for name in imported_modules & local_modules}
        required_modules.update({"runner.py", "service_runner.py", "studio_steps.py"})
        self.assertTrue(wrapper_copied, "Dockerfile.runner must install entrypoint.py as kb-journey-runner")
        self.assertTrue(required_modules <= copied_into_package,
                        f"Dockerfile.runner misses journey modules: {sorted(required_modules - copied_into_package)}")
        self.assertIn("COPY e2e/journey /opt/kb-journey/e2e/journey",
                      (journey_dir / "Dockerfile.runner").read_text(),
                      "Dockerfile.runner must mirror the journey package under the repository resource root")

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

    def test_empty_results_cannot_pass(self):
        report = aggregate([])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["capabilities"][0]["first_failure"]["name"], "result files")

    def test_release_case_coverage_is_included_in_nightly_status(self):
        capability_result = {"steps": [{"capability": "gateway", "mode": "local", "name": "ready",
                                        "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        coverage = {"schema": "kb.qa-case-coverage/v1", "mode": "local", "releasePassed": False,
                    "cases": [{"id": "S-045", "release": True, "status": "active", "statusResult": "NOT COVERED"}]}
        report = aggregate([capability_result], case_coverage=[coverage])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["case_coverage"], [coverage])
        self.assertIn("Case coverage (local): **FAIL**", render_summary(report))

    def test_result_with_no_capability_steps_cannot_pass(self):
        report = aggregate([{"steps": []}])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["capabilities"][0]["first_failure"]["name"], "capability results")

    def test_profile_missing_declared_capabilities_is_blocked(self):
        result = {"steps": [{"capability": "gateway", "mode": "local", "name": "ready",
                            "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        report = aggregate([result], profiles={"local": {"gateway", "workflow"}})
        workflow = next(item for item in report["capabilities"] if item["capability"] == "workflow")
        self.assertEqual(workflow["status"], "BLOCKED")
        self.assertIn("profile local declares workflow", workflow["first_failure"]["message"])
        self.assertEqual(report["status"], "BLOCKED")

    def test_profile_passes_only_when_each_declared_capability_passes(self):
        steps = [{"capability": capability, "mode": "local", "name": capability,
                  "status": "PASS", "duration_seconds": 0, "message": "passed"}
                 for capability in ("gateway", "workflow")]
        report = aggregate([{"steps": steps}], profiles={"local": {"gateway", "workflow"}})
        self.assertEqual(report["status"], "PASS")
        self.assertEqual({item["capability"] for item in report["capabilities"]}, {"gateway", "workflow"})

    def test_nonzero_journey_exit_fails_even_when_result_steps_pass(self):
        result = {"steps": [{"capability": "gateway", "mode": "secured", "name": "ready",
                            "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        report = aggregate([result], journey_exits={"secured": 7})
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(failure["first_failure"]["name"], "journey process exit")
        self.assertIn("status 7", failure["first_failure"]["message"])

    def test_unparseable_result_file_is_reported_as_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text("{broken")
            report = aggregate(load_results(directory))
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(failure["first_failure"]["name"], "result file parse")
        self.assertIn("service-local.json", failure["first_failure"]["message"])

    def test_result_loader_skips_case_coverage_documents(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text('{"steps":[]}')
            Path(directory, "case-coverage-local.json").write_text('{"schema":"kb.qa-case-coverage/v1"}')
            Path(directory, "report.json").write_text('{"schema":"kb.nightly-report/v1"}')
            results = load_results(directory)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0], {"steps": []})

    def test_invalid_step_status_cannot_be_treated_as_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text(
                '{"steps":[{"capability":"workflow","mode":"local","name":"run",'
                '"status":"SKIP","message":"missing"}]}'
            )
            report = aggregate(load_results(directory))
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertIn("status must be", failure["first_failure"]["message"])

    def test_declared_profile_capabilities_match_service_checks(self):
        for mode, auth_capability in (("local", "auth.local"), ("secured", "auth.invite-reset")):
            self.assertEqual(declared_capabilities(mode),
                             {capability for _, capability, _, _ in checks(mode)}
                             | {auth_capability, "plugin-author"})

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
        passed = {"capability": "gateway", "mode": "local", "name": "ready",
                  "status": "PASS", "message": "passed", "duration_seconds": 0}
        changes = sync_issues(client, aggregate([{"steps": [passed]}]), 0)
        self.assertEqual(len(client.issues), 1)
        self.assertIn("nightly-status", client.issues[0]["labels"])
        self.assertEqual(changes, {"created": [], "updated": [], "closed": []})

    def test_required_mode_steps_and_workflow_fixture_are_explicit(self):
        local = {capability for _, capability, _, _ in checks("local")}
        secured = {capability for _, capability, _, _ in checks("secured")}
        self.assertNotIn("auth.invites", local | secured)
        self.assertNotIn("scaffold", local | secured)
        with patch("service_runner.request", return_value=(200, {"data": {"workflows": []}}, {})):
            failure = discover_workflow("prj_fixture", "")
        self.assertIn("e2e-hello fixture", failure)


if __name__ == "__main__":
    unittest.main()
