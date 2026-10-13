from __future__ import annotations

import importlib
import io
import json
import os
import ast
import sys
import tempfile
import unittest
from contextlib import ExitStack, redirect_stdout
from pathlib import Path
from unittest.mock import patch

from service_steps import FlowGraph, Journey


def state_environment_reader_violations(sources: dict[str, str]) -> list[str]:
    """Find internal reads of keys emitted by the child-state exporter."""
    exported_keys = set()
    for filename, source_text in sources.items():
        tree = ast.parse(source_text, filename=filename)
        for exporter in ast.walk(tree):
            if (not isinstance(exporter, ast.FunctionDef)
                    or "export" not in exporter.name or "child_environment" not in exporter.name):
                continue
            for node in ast.walk(exporter):
                if isinstance(node, (ast.Assign, ast.AnnAssign)):
                    targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                    for target in targets:
                        if (isinstance(target, ast.Subscript) and isinstance(target.slice, ast.Constant)
                                and isinstance(target.slice.value, str)
                                and target.slice.value.startswith("KB_JOURNEY_")):
                            exported_keys.add(target.slice.value)
    violations = []
    for filename, source_text in sources.items():
        tree = ast.parse(source_text, filename=filename)
        parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
        for node in ast.walk(tree):
            key = None
            if isinstance(node, ast.Subscript) and isinstance(node.ctx, ast.Load):
                key = node.slice.value if isinstance(node.slice, ast.Constant) else None
            elif (isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                  and node.func.id == "journey_value" and node.args
                  and isinstance(node.args[0], ast.Constant)):
                key = node.args[0].value
            elif (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                  and node.func.attr == "get" and node.args
                  and isinstance(node.args[0], ast.Constant)):
                key = node.args[0].value
            if key not in exported_keys:
                continue
            current = node
            child_import = False
            while current in parents:
                current = parents[current]
                if isinstance(current, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    child_import = (filename == "service_runner.py" and current.name == "main"
                                    and key == "KB_JOURNEY_STATES")
                    break
            if not child_import:
                violations.append(f"{filename}:{node.lineno}: reads exported state key {key}")
    return violations


class RunnerInstallFailureTests(unittest.TestCase):
    def test_ordered_prompt_responder_consumes_new_pty_output_once(self):
        runner = importlib.import_module("runner")
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        transcript = "Project directory:"
        self.assertEqual(responder(transcript), "/tmp/project\n")
        self.assertIsNone(responder(transcript))
        transcript += "\rPlatform channel [stable]:"
        self.assertEqual(responder(transcript), "stable\n")
        transcript += "\rPlatform channel [stable]:"
        self.assertIsNone(responder(transcript))
        responder.finish()

    def test_ordered_prompt_responder_rejects_unexpected_prompt(self):
        runner = importlib.import_module("runner")
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        with self.assertRaisesRegex(RuntimeError, "unexpected wizard prompt"):
            responder("Unexpected choice [yes/no]:")

    def test_ordered_prompt_responder_rejects_missing_expected_prompt(self):
        runner = importlib.import_module("runner")
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        responder("Project directory:")
        with self.assertRaisesRegex(RuntimeError, "ended before expected prompt: Platform channel"):
            responder.finish()

    def test_pty_prompt_responder_handles_redraws_and_stops_on_unexpected_prompt(self):
        runner = importlib.import_module("runner")
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        child = (
            "import os; "
            "os.write(1, b'Project directory:'); os.read(0, 4096); "
            "os.write(1, b'\\rProject directory:'); "
            "os.write(1, b'\\rPlatform channel [stable]:'); os.read(0, 4096); "
            "os.write(1, b'\\rPlatform channel [stable]:'); "
            "os.write(1, b'\\rUnexpected prompt [yes/no]:'); os.read(0, 4096)"
        )
        with self.assertRaisesRegex(RuntimeError, "unexpected wizard prompt"):
            runner.run_pty_command([sys.executable, "-c", child], timeout=5,
                                   on_output=responder, start_new_session=True)

    def test_secured_commit_scenario_installs_commit_through_marketplace_and_checks_its_group(self):
        runner = importlib.import_module("runner")
        setup = runner.runner_scenario("commit", secured=True)
        self.assertEqual(setup["installer"], "custom")
        self.assertEqual(setup["marketplace_packages"], ("@kb-labs/commit-entry",))
        self.assertEqual(setup["command_groups"], ("commit",))

    def test_local_commit_scenario_uses_commit_installer_scenario(self):
        runner = importlib.import_module("runner")
        setup = runner.runner_scenario("commit", secured=False)
        self.assertEqual(setup["installer"], "commit")
        self.assertEqual(setup["marketplace_packages"], ())
        self.assertEqual(setup["command_groups"], ("commit",))

    def test_selected_step_completion_names_unrun_step_and_claim(self):
        runner = importlib.import_module("runner")
        with patch.object(runner, "FIRST_FAILURE", ""):
            runner.CASE_JOURNEY = Journey("secured", "selected-step-completeness")
            missing = runner.check_selected_step_completion([], {
                "verify bootstrap credential after rollback": ["S-100.1"],
            })
        self.assertEqual(missing, ["verify bootstrap credential after rollback"])
        row = runner.CASE_JOURNEY.steps[-1]
        self.assertIn("selected step never ran", row.message)
        self.assertIn("S-100.1", row.message)
        runner.CASE_JOURNEY = None

    def test_selected_step_completion_passes_when_row_exists(self):
        runner = importlib.import_module("runner")
        self.assertEqual(runner.check_selected_step_completion([{
            "steps": [{"name": "selected owner", "status": "PASS"}],
        }], {"selected owner": ["S-100.1"]}), [])

    def test_state_environment_exports_are_not_read_back_by_runner(self):
        sources = {path.name: path.read_text(encoding="utf-8")
                   for path in Path(__file__).parent.glob("*.py") if not path.name.startswith("test_")}
        self.assertEqual(state_environment_reader_violations(sources), [])

    def test_state_environment_reader_rail_rejects_an_internal_read_mutation(self):
        sources = {path.name: path.read_text(encoding="utf-8")
                   for path in Path(__file__).parent.glob("*.py") if not path.name.startswith("test_")}
        sources["runner.py"] += '\ndef mutated_internal_reader(env):\n    return env["KB_JOURNEY_STATES"]\n'
        self.assertTrue(any("mutated_internal_reader" not in item and "runner.py" in item
                            for item in state_environment_reader_violations(sources)))

    def test_state_environment_reader_rail_rejects_project_identity_read_in_child_entrypoint(self):
        sources = {path.name: path.read_text(encoding="utf-8")
                   for path in Path(__file__).parent.glob("*.py") if not path.name.startswith("test_")}
        sources["service_runner.py"] += (
            '\ndef mutated_child_entrypoint():\n'
            '    return os.environ.get("KB_JOURNEY_PROJECT_ID")\n'
        )
        self.assertTrue(any(item.startswith("service_runner.py:") and "KB_JOURNEY_PROJECT_ID" in item
                            for item in state_environment_reader_violations(sources)))

    def _run_selected_main(self, root: Path, mode: str, *, selection: str | None = None,
                           update_variant_runner=None, selector: str | None = None,
                           scenario: str = "explore", selected_owners: dict | None = None,
                           extra_update_group: dict | None = None,
                           update_runner=None) -> tuple[int, str, dict, dict, dict]:
        runner = importlib.import_module("runner")
        summary = root / "reports" / "journey-summary.md"
        summary.parent.mkdir(parents=True)
        project = root / "fresh-project"
        project.mkdir()
        binary_dir = root / ".local" / "bin"
        binary_dir.mkdir(parents=True)
        (binary_dir / "kb-create").write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        (binary_dir / "kb-create").chmod(0o755)
        (binary_dir / "kb").write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        (binary_dir / "kb").chmod(0o755)
        platform = root / "kb-platform"
        platform_current = platform / "current"
        versioned_platform = platform / "versions" / "2.120.1"
        versioned_platform.mkdir(parents=True)
        platform.mkdir(exist_ok=True)
        platform_current.symlink_to(Path("versions") / "2.120.1")
        receipt = platform_current / ".kb" / "v2" / "receipt.json"
        receipt.parent.mkdir(parents=True)
        receipt.write_text(json.dumps({"id": "receipt-main",
                                       "plan": {"request": {},
                                                "serviceGraph": {"platformVersion": "2.120.1"},
                                                "artifacts": [{"id": "fixture-package", "kind": "plugin",
                                                               "package": "@kb-labs/fixture-package",
                                                               "version": "1.0.0"}]}}),
                           encoding="utf-8")
        selection = selection if selection is not None else "PC-001.2,PC-002.3,PC-002.8"
        mode_variant = "default" if mode == "local" else "human-secured"
        front = "agent" if mode == "local" else "human"
        fixture_calls = []
        host_cycle = []
        events = []

        def fake_run(command, *, env=None, cwd=None, **_kwargs):
            argv = [str(item) for item in command]
            if len(argv) > 1 and argv[1] in {"stop", "start"}:
                host_cycle.append(argv[1])
                events.append(f"host:{argv[1]}")
            output = ""
            if argv[0].endswith("install.sh"):
                output = "installer ready"
            elif argv[-1:] == ["--version"]:
                output = "kb-create 2.120.1"
            elif "version" in argv and "--json" in argv:
                output = json.dumps({"ok": True, "data": {"version": "2.120.1", "commit": "fixture-commit"}})
            elif argv[:2] == ["git", "status"]:
                output = "?? note.txt"
            elif "commit commit" in " ".join(argv):
                output = 'scope "root"'
            elif "doctor" in argv:
                output = json.dumps({"ok": True, "data": {"installed": True, "running": True,
                                    "reachable": True, "platform": {"ok": True}}})
            elif "auth" in argv and "status" in argv:
                output = json.dumps({"ok": True, "data": {"session": {"authenticated": False}}})
            elif "status" in argv:
                output = json.dumps({"ok": True, "data": {"installed": True, "running": True,
                                    "reachable": True, "host": {"state": "running"},
                                    "installation": {"receiptId": "receipt-main"}}})
            elif "project show" in " ".join(argv):
                output = json.dumps({"ok": True, "data": {"project": {"id": "prj_main", "name": "fresh-project"}}})
            elif "project list" in " ".join(argv):
                output = json.dumps({"ok": True, "data": {"projects": [{"id": "prj_main"}]}})
            elif "workflow defs list" in " ".join(argv):
                output = json.dumps({"ok": True, "data": [{"id": "journey-hello"}]})
            elif "workflow crons list" in " ".join(argv):
                output = json.dumps({"ok": True, "data": {"crons": [
                    {"id": "fixture-plugin-cron", "source": "plugin"},
                    {"id": "studio-a", "source": "user"},
                ]}})
            elif any(item.endswith("service_runner.py") for item in argv):
                output_path = Path(argv[argv.index("--output") + 1])
                service_steps = ([{"name": "platform readiness (gateway + project workflow)", "status": "PASS",
                                   "cases": ["PC-002.3"]},
                                  {"name": "registered project runtime identity", "status": "PASS",
                                   "cases": ["PC-002.8"]}]
                                 if "PC-002.3" in selection else [])
                if "S-140.5" in selection:
                    service_steps.append({"name": "project MCP cross-project isolation", "status": "PASS",
                                          "cases": ["S-140.5"]})
                states = {"service-api-session": {"passed": True, "value": None},
                          "project-registered": {"passed": True,
                              "value": {"id": "prj_main", "root": str(project), "name": "fresh-project"}}}
                if not selection or "S-002.1" in selection or "S-140.5" in selection:
                    project_b = root / "project-b"
                    project_b.mkdir()
                    states["fixtures-installed"] = {"passed": True, "evidence": "fixture project registered",
                        "value": {"projectBId": "prj_b", "projectBRoot": str(project_b)}}
                payload = {"status": "PASS", "steps": service_steps, "states": states}
                output_path.write_text(json.dumps(payload), encoding="utf-8")
                output = "service runner passed"
            elif "install" in argv and "--json" in argv:
                result_record = {"ok": True, "data": {"receipt": {"id": "receipt-main", "plan": {
                    "serviceGraph": {"platformVersion": "2.120.1"}, "artifacts": [{"id": "fixture"}],
                }}, "firstAdmin": {"email": "journey-admin@example.test",
                    "password": "JourneyPassw0rd!", "state": "disclosed"}},
                    "meta": {"stream": {"result": True}}}
                terminal = {"ok": True, "data": None, "meta": {"stream": {
                    "terminal": True, "exitCode": 0,
                }}}
                output = json.dumps(result_record) + "\n" + json.dumps(terminal)
            else:
                output = json.dumps({"ok": True, "data": {}})
            return runner.subprocess.CompletedProcess(command, 0, output, ""), 0.0

        def fake_http(_opener, request, **_kwargs):
            url = request.full_url
            if url.endswith("/ready"):
                return 200, b"ok", None
            return 200, json.dumps({"channels": {"stable": "2.120.2", "canary": "2.120.3"}}).encode(), None

        base_env = {
            **runner.BASE_ENV,
            "KB_JOURNEY_SERVICE_PROFILE": "nightly",
            "KB_JOURNEY_SUMMARY": str(summary),
            "KB_JOURNEY_SELECTED_STEPS": selection,
            "KB_JOURNEY_SELECTED_OWNING_STEPS": json.dumps(selected_owners or {}),
            "KB_JOURNEY_SELECTED_CAPABILITIES": (
                "workflow" if "S-002.1" in selection else
                "mcp" if "S-140.5" in selection else "platform.install,gateway,projects"),
            "KB_JOURNEY_GIT_REVISION": "fixture-commit",
            "KB_JOURNEY_RUNNER_SELECTOR": selector or f"service-{mode}:explore",
            "KB_CREATE_DOWNLOAD_BASE_URL": "http://journey.invalid/candidate",
        }
        console = io.StringIO()
        with ExitStack() as stack:
            for name, value in (("HOME", root), ("BIN", binary_dir), ("PLATFORM", platform),
                                ("PLATFORM_CURRENT", platform_current), ("PROJECT", project),
                                ("FRONT_DOOR", front), ("SCENARIO", scenario),
                                ("VARIANT", mode_variant), ("BASE_ENV", base_env)):
                stack.enter_context(patch.object(runner, name, value))
            stack.enter_context(patch.dict("os.environ", base_env, clear=True))
            stack.enter_context(patch.object(runner, "run", side_effect=fake_run))
            stack.enter_context(patch.object(
                runner, "run_command", side_effect=lambda command, **kwargs: fake_run(command, **kwargs)[0]))
            stack.enter_context(patch.object(runner, "open_http", side_effect=fake_http))
            stack.enter_context(patch.object(runner, "write_scoreboards"))
            if extra_update_group:
                original_outer_selection_groups = runner.outer_selection_groups
                def groups_with_update_owner():
                    groups = original_outer_selection_groups()
                    groups["update"] = {
                        **groups.get("update", {}),
                        "cases": tuple(extra_update_group.get("cases", ())),
                        "steps": tuple(extra_update_group.get("steps", ())),
                    }
                    return groups
                stack.enter_context(patch.object(runner, "outer_selection_groups",
                                                  side_effect=groups_with_update_owner))
            def fixture_result(name, claim=None):
                fixture_calls.append(name)
                events.append(name)
                journey = Journey(mode, name)
                if claim:
                    journey.step("selected isolated installer claim", "platform.install",
                                 lambda: None, cases=[claim])
                return journey

            for target, kwargs in (
                ("service_runner.restore_secured_cli_login", {"return_value": None}),
                ("runner.login_and_configure_secured_admin", {"return_value": None}),
                ("runner.run_local_auth_steps", {"return_value": Journey(mode, "local-auth")}),
                ("runner.verify_broken_install_doctor", {}),
                ("runner.verify_listener_ownership", {}),
                ("runner.run_installer_case_steps", {"side_effect": lambda **_kwargs:
                    fixture_result("run_installer_case_steps", "PC-001.4")}),
                ("runner.run_apply_scenario_step", {"side_effect": lambda **_kwargs:
                    fixture_result("run_apply_scenario_step")}),
                ("runner.run_invalid_request_step", {"side_effect": lambda **_kwargs:
                    fixture_result("run_invalid_request_step")}),
                ("runner.run_doctor_root_isolation_step", {"side_effect": lambda **_kwargs:
                    fixture_result("run_doctor_root_isolation_step")}),
                ("runner.run_missing_project_request_steps", {"side_effect": lambda **_kwargs:
                    fixture_result("run_missing_project_request_steps")}),
                ("runner.run_cli_auth_status", {"return_value": None}),
                ("runner.run_platform_update_steps", {"side_effect": update_runner} if update_runner else {}),
                ("runner.run_platform_channel_steps", {"return_value": Journey(mode, "platform-channel")}),
                ("runner.verify_project_mcp", {}),
                ("runner.prepare_workflow_fixtures", {}),
                ("runner.run_workflow_journey", {"side_effect": lambda journey, **_kwargs:
                    journey.step("selected workflow claim", "workflow", lambda: None, cases=["S-002.1"])}),
                ("runner.run_uninstall_group", {"return_value": True}),
                ("runner.prepare_studio_fixtures", {"return_value": {}}),
                ("runner.run_studio_browser_step", {"return_value": (True, 0.0, "")}),
            ):
                stack.enter_context(patch(target, **kwargs))
            stack.enter_context(redirect_stdout(console))
            code = runner.main(update_variant_runner=update_variant_runner)
        service_report = summary.with_name(f"service-{mode}.json")
        payload = json.loads(service_report.read_text()) if service_report.is_file() else {}
        return code, console.getvalue(), payload, runner.CASE_JOURNEY.result().__dict__, {
            "fixture_calls": fixture_calls,
            "host_cycle": host_cycle,
            "events": events,
        }

    def test_main_smoke_selection_completes_in_both_modes(self):
        runner = importlib.import_module("runner")
        isolated_groups = [group.__name__ for group in runner.ISOLATED_FIXTURE_GROUPS]
        for mode in ("local", "secured"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory(prefix="journey-main-smoke-") as temporary:
                code, summary, service, outer, observed = self._run_selected_main(Path(temporary), mode)
                self.assertEqual(code, 0)
                self.assertNotIn("runner crash", summary)
                service_claims = {claim for step in service["steps"] for claim in step.get("cases", [])}
                self.assertTrue({"PC-002.3", "PC-002.8"}.issubset(service_claims))
                self.assertIn("SKIPPED workflow execution journey: not selected", summary)
                self.assertEqual(summary.count("SKIPPED platform update journey: not selected"), 1)
                self.assertIn("SKIPPED installed platform bootstrap: not selected; the install operation ran earlier; this flow is only a selection marker", summary)
                self.assertEqual(observed["fixture_calls"], [])
                self.assertEqual(observed["host_cycle"], ["stop", "start"])
                self.assertEqual(observed["events"], ["host:stop", "host:start"])
                skipped_groups = [group for group in isolated_groups
                                  if f"SKIPPED {group}: not selected" in summary]
                self.assertEqual(skipped_groups, isolated_groups)
                for group in isolated_groups:
                    self.assertEqual(summary.count(f"SKIPPED {group}: not selected"), 1)
                self.assertIn({"claimId": "PC-001.2", "kind": "journey", "status": "PASS"},
                              outer["case_run"]["claims"])

    def test_main_project_b_selection_runs_its_state_handoff(self):
        with tempfile.TemporaryDirectory(prefix="journey-main-project-b-") as temporary:
            code, output, service, outer, _observed = self._run_selected_main(
                Path(temporary), "local", selection="S-140.5")
        self.assertEqual(code, 0)
        self.assertNotIn("runner crash", output)
        self.assertIn("SKIPPED installed platform bootstrap: not selected; the install operation ran earlier; this flow is only a selection marker", output)
        service_claims = {claim for step in service["steps"] for claim in step.get("cases", [])}
        self.assertIn("S-140.5", service_claims)
        self.assertTrue(service["states"]["fixtures-installed"]["value"]["projectBRoot"])

    def test_main_runs_selected_update_journey_owner_and_fails_when_it_registers_no_row(self):
        owner_name = "helper-owned selected claim"
        claim = "S-100.1"
        selected_owners = {owner_name: [claim]}
        update_group = {"cases": [claim], "steps": [owner_name]}

        def update_with_owner(**_kwargs):
            journey = _kwargs["parent_journey"]
            journey.step(owner_name, "platform.update", lambda: None, cases=[claim])
            return journey

        with tempfile.TemporaryDirectory(prefix="journey-main-selected-update-") as temporary:
            code, output, _service, outer, _observed = self._run_selected_main(
                Path(temporary), "local", selection=claim, selector="agent:commit",
                scenario="commit", selected_owners=selected_owners,
                extra_update_group=update_group, update_runner=update_with_owner)
        self.assertEqual(code, 0, output)
        self.assertIn(owner_name, [step["name"] for step in outer["steps"]])
        self.assertNotIn(f"SKIPPED {owner_name}: not selected", output)

        with tempfile.TemporaryDirectory(prefix="journey-main-missing-update-row-") as temporary:
            code, output, _service, outer, _observed = self._run_selected_main(
                Path(temporary), "local", selection=claim, selector="agent:commit",
                scenario="commit", selected_owners=selected_owners,
                extra_update_group=update_group,
                update_runner=lambda **_kwargs: Journey("local", "unregistrable-update-journey"))
        self.assertEqual(code, 1)
        self.assertIn("selected step never ran", output)
        self.assertIn(owner_name, output)
        self.assertIn(claim, output)
        self.assertTrue(any(step["name"].startswith("selected step never ran") for step in outer["steps"]))

    def test_main_full_plan_completes_without_a_claim_selection(self):
        runner = importlib.import_module("runner")
        isolated_groups = [group.__name__ for group in runner.ISOLATED_FIXTURE_GROUPS]
        variant_args = []

        def fake_update_variants(**kwargs):
            variant_args.append(kwargs)
            return Journey("local", "update-variants")

        with tempfile.TemporaryDirectory(prefix="journey-main-full-plan-") as temporary:
            code, output, service, outer, observed = self._run_selected_main(
                Path(temporary), "local", selection="", update_variant_runner=fake_update_variants)
        self.assertEqual(code, 0, output)
        self.assertNotIn("SKIPPED", output)
        self.assertIn({"claimId": "PC-001.2", "kind": "journey", "status": "PASS"},
                      outer["case_run"]["claims"])
        self.assertTrue(service["states"]["fixtures-installed"]["value"]["projectBRoot"])
        self.assertEqual(len(variant_args), 1)
        self.assertEqual(variant_args[0]["project_b"],
                         Path(service["states"]["fixtures-installed"]["value"]["projectBRoot"]))
        self.assertEqual(observed["fixture_calls"], isolated_groups)
        self.assertEqual(observed["host_cycle"], ["stop", "start", "stop", "start"])
        self.assertEqual(observed["events"], [
            "host:stop", "host:start", "host:stop", *isolated_groups, "host:start",
        ])

    def test_main_selected_installer_claim_runs_only_its_fixture_group_and_host_cycle(self):
        runner = importlib.import_module("runner")
        installer_group = runner.INSTALLER_CASE_GROUP.__name__
        with tempfile.TemporaryDirectory(prefix="journey-main-installer-claim-") as temporary:
            code, output, _service, outer, observed = self._run_selected_main(
                Path(temporary), "local", selection="PC-001.4", selector="agent:commit")
        self.assertEqual(code, 0, output)
        self.assertEqual(observed["fixture_calls"], [installer_group])
        self.assertEqual(observed["host_cycle"], ["stop", "start", "stop", "start"])
        self.assertEqual(observed["events"], [
            "host:stop", "host:start", "host:stop", installer_group, "host:start",
        ])
        self.assertEqual(output.count(f"SKIPPED {installer_group}: not selected"), 0)
        claim_status = {claim["claimId"]: claim["status"] for claim in outer["case_run"]["claims"]}
        self.assertEqual(claim_status["PC-001.4"], "PASS")

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
        runner.export_graph_state_to_child_environment(graph, child_env)
        self.assertEqual(json.loads(child_env["KB_JOURNEY_STATES"])["project-registered"]["value"],
                         graph.state_value("project-registered"))
        self.assertEqual(graph.journey.flows["verify installed project registration"][0], "PASS")

    def test_project_producer_dependency_mutation_is_detected(self):
        runner, _service_runner, _journey, graph = self._project_graph(
            selected="PC-002.3,PC-002.8", capabilities="gateway,projects")
        graph.groups["project"]["produces"] = ()
        output = io.StringIO()
        with redirect_stdout(output), patch("runner.run", side_effect=self._project_command):
            runner.establish_project_registration(graph, Path("/tmp/project"), {})

        self.assertEqual(graph.journey.flows["verify installed project registration"][0], "SKIPPED")
        self.assertIn("SKIPPED verify installed project registration: not selected", output.getvalue())
        self.assertIsNot(graph.states.get("project-registered", {}).get("passed"), True)

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
