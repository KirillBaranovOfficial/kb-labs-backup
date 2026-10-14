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
from test_catalog_exit_status import catalogued_exit


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


def throwing_runner_state_read_violations(sources: dict[str, str]) -> list[str]:
    violations = []
    for filename in ("runner.py", "service_runner.py"):
        source_text = sources.get(filename, "")
        tree = ast.parse(source_text, filename=filename)
        for node in ast.walk(tree):
            if (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                    and node.func.attr == "state_value"):
                violations.append(f"{filename}:{node.lineno}: runner state reads must use the non-throwing FlowGraph boundary")
    return violations


def state_producer_order_violations(source_text: str, dependencies: dict[str, tuple[str, ...]],
                                    state_producers: dict[str, str]) -> list[str]:
    """Require each runner gate to register a declared missing-state producer first."""
    tree = ast.parse(source_text, filename="runner.py")
    entrypoint = next((node for node in tree.body
                       if isinstance(node, ast.FunctionDef) and node.name == "_main"), None)
    if entrypoint is None:
        return ["runner.py: missing _main entrypoint"]

    producer_lines: dict[str, int] = {}
    for node in ast.walk(entrypoint):
        if (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                and node.func.attr == "register_state_producer" and node.args
                and isinstance(node.args[0], ast.Constant)
                and isinstance(node.args[0].value, str)):
            producer_lines[node.args[0].value] = node.lineno

    violations = []
    for node in ast.walk(entrypoint):
        if not (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                and node.func.attr == "flow" and len(node.args) >= 2
                and isinstance(node.args[1], ast.Constant)
                and isinstance(node.args[1].value, str)):
            continue
        group = node.args[1].value
        for state in dependencies.get(group, ()):
            producer = state_producers.get(state)
            registered_at = producer_lines.get(state)
            if producer is not None and registered_at is not None and node.lineno < registered_at:
                violations.append(
                    f"runner.py:{node.lineno}: gate {group!r} consumes state {state!r} "
                    f"before its {producer!r} producer is scheduled at line {registered_at}")
    return violations


class RunnerInstallFailureTests(unittest.TestCase):
    def test_main_rejects_missing_or_malformed_ownership_before_running_steps(self):
        from journey_selection import JourneySelectionError
        runner = importlib.import_module("runner")
        valid = {
            "KB_JOURNEY_CASES": "S-100.1",
            "KB_JOURNEY_SELECTED_STEPS": "S-100.1",
            "KB_JOURNEY_SELECTED_CAPABILITIES": "auth.accounts",
            "KB_JOURNEY_PREREQUISITE_STEPS": "[]",
            "KB_JOURNEY_PREREQUISITE_FLOWS": "[]",
            "KB_JOURNEY_RUNNER_SELECTOR": "service-secured:explore",
        }
        invalid_owners = (
            (None, r"KB_JOURNEY_SELECTED_OWNING_STEPS is missing"),
            ("[]", r"KB_JOURNEY_SELECTED_OWNING_STEPS must be a JSON object"),
            ('{"rollback scan":"S-100.1"}', r"values must be non-empty arrays of claim IDs"),
        )
        for owner_value, message in invalid_owners:
            with self.subTest(owner_value=owner_value), patch.dict(os.environ, valid, clear=True):
                if owner_value is not None:
                    os.environ["KB_JOURNEY_SELECTED_OWNING_STEPS"] = owner_value
                Journey.reset_run_registry()
                with patch.object(runner, "run", side_effect=AssertionError("step ran before validation")):
                    with self.assertRaisesRegex(JourneySelectionError, message):
                        runner.main()
                self.assertEqual(Journey.run_instances(), ())

    def test_container_entry_records_selection_boundary_failure_before_dispatch(self):
        import entrypoint
        with tempfile.TemporaryDirectory(prefix="journey-missing-selection-owner-") as temporary:
            summary = Path(temporary) / "journey-summary.md"
            with patch.dict(os.environ, {
                    "HOME": temporary,
                    "KB_JOURNEY_CASES": "S-100.1",
                    "KB_JOURNEY_SELECTED_STEPS": "S-100.1",
                    "KB_JOURNEY_SELECTED_CAPABILITIES": "auth.accounts",
                    "KB_JOURNEY_PREREQUISITE_STEPS": "[]",
                    "KB_JOURNEY_PREREQUISITE_FLOWS": "[]",
                    "KB_JOURNEY_RUNNER_SELECTOR": "service-secured:explore",
                    "KB_JOURNEY_VARIANT": "secured",
                    "KB_JOURNEY_SUMMARY": str(summary),
            }, clear=True), patch("entrypoint.run_runner") as dispatch:
                code = entrypoint.main()
            self.assertEqual(code, 1)
            dispatch.assert_not_called()
            report = json.loads((Path(temporary) / "service-secured.json").read_text())
            self.assertEqual(report["status"], "FAIL")
            self.assertIn("KB_JOURNEY_SELECTED_OWNING_STEPS", report["first_failure"]["message"])

    def test_ordered_prompt_responder_consumes_new_pty_output_once(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        transcript = "Project directory (press Enter to install without a project): "
        self.assertIsNone(responder(transcript))
        self.assertEqual(responder(transcript + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        transcript += WIZARD_PROMPT_EVENT
        transcript += "\rPlatform channel [stable] (default stable): "
        self.assertEqual(responder(transcript + WIZARD_PROMPT_EVENT), "stable\n")
        responder.finish()

    def test_ordered_prompt_responder_ignores_real_install_progress_after_last_answer(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
        ])
        prompt = "Project directory (press Enter to install without a project): "
        self.assertEqual(responder(prompt + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        progress = prompt + WIZARD_PROMPT_EVENT + "\nApplying installation…\n"
        self.assertIsNone(responder(progress))
        responder.finish()

    def test_ordered_prompt_responder_rejects_unexpected_prompt_after_last_answer(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([(r"Project directory", "/tmp/project")])
        prompt = "Project directory (press Enter to install without a project): "
        self.assertEqual(responder(prompt + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        output = (prompt + WIZARD_PROMPT_EVENT
                  + "\nApplying installation…\nChoose access mode: " + WIZARD_PROMPT_EVENT)
        with self.assertRaisesRegex(
                RuntimeError,
                r"unexpected wizard prompt.*Choose access mode.*no prompt was expected"):
            responder(output)

    def test_ordered_prompt_responder_rejects_unexpected_prompt_between_expected_prompts(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        prompt = "Project directory (press Enter to install without a project): "
        self.assertEqual(responder(prompt + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        output = prompt + WIZARD_PROMPT_EVENT + "\rChoose access mode: " + WIZARD_PROMPT_EVENT
        with self.assertRaisesRegex(
                RuntimeError,
                r"unexpected wizard prompt.*Choose access mode.*expected prompt 'Platform channel'"):
            responder(output)

    def test_ordered_prompt_responder_reports_expected_prompt_never_shown(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        prompt = "Project directory (press Enter to install without a project): "
        self.assertEqual(responder(prompt + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        progress = prompt + WIZARD_PROMPT_EVENT + "\nApplying installation…\n"
        responder(progress)
        with self.assertRaisesRegex(RuntimeError, r"output tail.*Applying installation.*expected prompt 'Platform channel'"):
            responder.finish()

    def test_ordered_prompt_responder_handles_split_prompt_and_cr_redraw_without_guessing(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        responder("Downloading? 50%\rProject dire")
        transcript = "Downloading? 50%\rProject directory (press Enter to install without a project): "
        self.assertIsNone(responder(transcript))
        self.assertEqual(responder(transcript + WIZARD_PROMPT_EVENT), "/tmp/project\n")
        repeated = transcript + WIZARD_PROMPT_EVENT + "\rProject directory (press Enter to install without a project): " + WIZARD_PROMPT_EVENT
        with self.assertRaisesRegex(RuntimeError, r"unexpected wizard prompt.*Project directory.*expected prompt 'Platform channel'"):
            responder(repeated)

    def test_pty_prompt_responder_handles_redraws_and_stops_on_unexpected_prompt(self):
        runner = importlib.import_module("runner")
        from invocations import WIZARD_PROMPT_EVENT
        responder = runner.OrderedPromptResponder([
            (r"Project directory", "/tmp/project"),
            (r"Platform channel", "stable"),
        ])
        prompt = "Project directory (press Enter to install without a project): " + WIZARD_PROMPT_EVENT
        unexpected = "\rChoose access mode: " + WIZARD_PROMPT_EVENT
        child = (
            "import os; "
            f"os.write(1, {prompt.encode()!r}); "
            "os.read(0, 4096); "
            f"os.write(1, {unexpected.encode()!r}); os.read(0, 4096)"
        )
        with self.assertRaisesRegex(RuntimeError, r"unexpected wizard prompt.*Choose access mode.*expected prompt 'Platform channel'"):
            runner.run_pty_command([sys.executable, "-c", child], timeout=5,
                                   on_output=responder,
                                   idle_interval=0.02, start_new_session=True)

    def test_secured_commit_scenario_installs_commit_through_marketplace_and_checks_its_group(self):
        runner = importlib.import_module("runner")
        setup = runner.runner_scenario("commit", secured=True)
        self.assertEqual(setup["installer"], "custom")
        self.assertEqual(setup["marketplace_packages"], ("@kb-labs/commit-entry",))
        self.assertEqual(setup["command_help_paths"], (("commit", "commit", "--help"),))

    def test_local_commit_scenario_uses_commit_installer_scenario(self):
        runner = importlib.import_module("runner")
        setup = runner.runner_scenario("commit", secured=False)
        self.assertEqual(setup["installer"], "commit")
        self.assertEqual(setup["marketplace_packages"], ())
        self.assertEqual(setup["command_help_paths"], (("commit", "commit", "--help"),))

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

    def test_runner_state_reads_use_non_throwing_flow_graph_boundary(self):
        sources = {path.name: path.read_text(encoding="utf-8")
                   for path in Path(__file__).parent.glob("*.py") if not path.name.startswith("test_")}
        self.assertEqual(throwing_runner_state_read_violations(sources), [])

    def test_runner_state_read_rail_rejects_a_throwing_accessor_mutation(self):
        sources = {path.name: path.read_text(encoding="utf-8")
                   for path in Path(__file__).parent.glob("*.py") if not path.name.startswith("test_")}
        sources["runner.py"] += (
            '\ndef mutated_runner_entrypoint(graph):\n'
            '    return graph.state_value("project-registered")\n'
        )
        self.assertEqual(len(throwing_runner_state_read_violations(sources)), 1)

    def test_state_producers_are_registered_before_runner_gates_that_consume_them(self):
        runner = importlib.import_module("runner")
        source = Path(runner.__file__).read_text(encoding="utf-8")
        self.assertEqual(state_producer_order_violations(
            source, runner.OUTER_GROUP_DEPENDENCIES, runner.FLOW_GRAPH_STATE_PRODUCERS), [])

    def test_state_producer_order_rail_rejects_a_swapped_gate_and_producer(self):
        runner = importlib.import_module("runner")
        group, state = next((group, state)
                            for group, needed in runner.OUTER_GROUP_DEPENDENCIES.items()
                            for state in needed if state in runner.FLOW_GRAPH_STATE_PRODUCERS)
        producer = runner.FLOW_GRAPH_STATE_PRODUCERS[state]
        source = (
            "def _main(graph):\n"
            f"    graph.register_state_producer({state!r}, produce_state)\n"
            f"    graph.flow('consumer gate', {group!r}, 'journey.test', lambda: None)\n")
        self.assertEqual(state_producer_order_violations(
            source, runner.OUTER_GROUP_DEPENDENCIES, runner.FLOW_GRAPH_STATE_PRODUCERS), [])
        mutated = source.replace(
            f"    graph.register_state_producer({state!r}, produce_state)\n"
            f"    graph.flow('consumer gate', {group!r}, 'journey.test', lambda: None)\n",
            f"    graph.flow('consumer gate', {group!r}, 'journey.test', lambda: None)\n"
            f"    graph.register_state_producer({state!r}, produce_state)\n")
        violations = state_producer_order_violations(
            mutated, runner.OUTER_GROUP_DEPENDENCIES, runner.FLOW_GRAPH_STATE_PRODUCERS)
        self.assertEqual(len(violations), 1)
        self.assertIn(producer, violations[0])

    def test_failed_state_probe_blocks_its_producer_without_stopping_independent_flows(self):
        with tempfile.TemporaryDirectory(prefix="journey-state-producer-blocked-") as temporary:
            journey = Journey("local", "state-producer-blocked")
            graph = FlowGraph(
                journey,
                {"fixture-producer": ("project-registered",), "independent": ()},
                states={"project-registered": {
                    "passed": False,
                    "evidence": "kb project show exited 7: secondary shim missing",
                    "group": "project", "step": "kb project show/list", "claim": "no case claim",
                }},
            )
            check_calls = []
            blocked = graph.probe_state(
                "fixtures-installed", "fixture-producer", "project fixture setup",
                lambda: check_calls.append("ran"),
            )
            independent = graph.flow("independent setup", "independent", "journey.setup", lambda: None)
            result = journey.write_json(Path(temporary) / "journey.json")

            self.assertEqual(blocked.status, "BLOCKED")
            self.assertIn("project-registered", blocked.message)
            self.assertIn("secondary shim missing", blocked.message)
            self.assertEqual(check_calls, [])
            self.assertEqual(independent, "PASS")
            self.assertEqual(result.status, "BLOCKED")
            self.assertEqual(importlib.import_module("service_runner").journey_result_exit_code(result), 1)
            self.assertEqual(json.loads((Path(temporary) / "journey.json").read_text())["status"], "BLOCKED")

    def _run_selected_main(self, root: Path, mode: str, *, selection: str | None = None,
                           update_variant_runner=None, selector: str | None = None,
                           scenario: str = "explore", selected_owners: dict | None = None,
                           extra_update_group: dict | None = None,
                           update_runner=None, service_profile: str = "nightly",
                           failed_project_probe: bool = False) -> tuple[int, str, dict, dict, dict]:
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
        if selected_owners is None:
            selected_owners = {
                "PC-001.2,PC-002.3,PC-002.8": {
                    "candidate provenance": ["PC-001.2", "PC-002.3", "PC-002.8"],
                    "clean install leaves a running host and reports its resolved plan": ["PC-001.2"],
                    "platform readiness (gateway + project workflow)": ["PC-002.3"],
                    "registered project runtime identity": ["PC-002.8"],
                },
                "S-140.5": {"project MCP cross-project isolation": ["S-140.5"]},
                "PC-001.4": {"selected isolated installer claim": ["PC-001.4"]},
            }.get(selection, {})
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
                                    "bootstrapCompleted": True,
                                    "installation": {"receiptId": "receipt-main"}}})
            elif "project show" in " ".join(argv):
                if failed_project_probe:
                    return runner.subprocess.CompletedProcess(command, 7, "project show: secondary shim missing", ""), 0.0
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
                if failed_project_probe:
                    service_steps = [
                        {"name": "platform readiness (gateway + project workflow)", "status": "BLOCKED",
                         "message": "blocked by state project-registered: kb project show exited 7: secondary shim missing",
                         "cases": ["PC-002.3"]},
                        {"name": "independent auth check", "status": "PASS", "cases": ["PC-002.8"]},
                    ]
                else:
                    service_steps = ([{"name": "platform readiness (gateway + project workflow)", "status": "PASS",
                                       "cases": ["PC-002.3"]},
                                      {"name": "registered project runtime identity", "status": "PASS",
                                       "cases": ["PC-002.8"]}]
                                     if "PC-002.3" in selection else [])
                if "S-140.5" in selection:
                    service_steps.append({"name": "project MCP cross-project isolation", "status": "PASS",
                                          "cases": ["S-140.5"]})
                states = {"service-api-session": {"passed": True, "value": None},
                          "project-registered": ({"passed": False,
                              "evidence": "kb project show exited 7: secondary shim missing",
                              "group": "project", "step": "kb project show/list", "claim": "no case claim"}
                              if failed_project_probe else {"passed": True,
                              "value": {"id": "prj_main", "root": str(project), "name": "fresh-project"}})}
                project_b = root / "project-b"
                project_b.mkdir(exist_ok=True)
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
            if url.endswith("/health/auth"):
                return 200, json.dumps({"data": {"ok": True, "activeAdmins": 1}}).encode(), None
            if url.endswith("/ready"):
                return 200, b"ok", None
            return 200, json.dumps({"channels": {"stable": "2.120.2", "canary": "2.120.3"}}).encode(), None

        base_env = {
            **runner.BASE_ENV,
            "KB_JOURNEY_SERVICE_PROFILE": service_profile,
            "KB_JOURNEY_SUMMARY": str(summary),
            "KB_JOURNEY_CASES": selection,
            "KB_JOURNEY_SELECTED_STEPS": selection,
            "KB_JOURNEY_SELECTED_OWNING_STEPS": json.dumps(selected_owners or {}),
            "KB_JOURNEY_PREREQUISITE_STEPS": json.dumps(list(selected_owners or {})),
            "KB_JOURNEY_PREREQUISITE_FLOWS": "[]",
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
                ("runner.run_secured_auth_steps", {"return_value": Journey(mode, "secured-auth")}),
                ("runner.create_studio_member", {"return_value": ("member@example.test", "JourneyMember123!")}),
                ("runner.create_studio_access_tokens", {"return_value": (
                    "admin-token-value-123", "member-token-value-456")}),
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
                ("runner.run_platform_update_steps", {
                    "side_effect": update_runner or (lambda **_kwargs: Journey(mode, "platform-update"))}),
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
                self.assertNotIn("SKIPPED platform update journey: not selected", summary)
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

    def test_main_selected_update_runs_only_the_declared_rollback_scan_chain(self):
        runner = importlib.import_module("runner")
        selected_name = "verify disclosed bootstrap credential absent after update and rollback"
        prerequisite_names = [
            "save installed receipt, config, and host status",
            "stage update and leave the running host on its current version",
            "explicitly restart services onto the installed version",
            "roll back to version N with explicit non-TTY confirmation",
            "verify restored receipt, config, host, and project command",
        ]
        owning = {selected_name: ["S-100.1"], **{name: ["S-100.1"] for name in prerequisite_names}}
        executions = []
        results = []

        def selected_update(**kwargs):
            executions.append(kwargs)
            journey = Journey("secured", "selected-update", parent=kwargs["parent_journey"])
            for name in prerequisite_names:
                requires = ()
                if name == prerequisite_names[1]:
                    requires = (f"step:{prerequisite_names[0]}",)
                elif name == prerequisite_names[2]:
                    requires = (f"step:{prerequisite_names[1]}",)
                elif name == prerequisite_names[3]:
                    requires = (f"step:{prerequisite_names[2]}",)
                elif name == prerequisite_names[4]:
                    requires = (f"step:{prerequisite_names[3]}",)
                if name == prerequisite_names[0]:
                    requires = ("state:project-registered",)
                journey.step(name, "platform.update", lambda: None,
                             cases=["S-023.1"], requires=requires)
            journey.step(selected_name, "platform.rollback", lambda: None,
                         cases=["S-100.1"], requires=(f"step:{prerequisite_names[-1]}",))
            journey.step("unselected update claim", "platform.update", lambda: "must not run",
                         cases=["S-023.2"])
            results.append(journey.result().__dict__)
            return journey

        with tempfile.TemporaryDirectory(prefix="journey-main-selected-update-") as temporary:
            with patch("runner.project_b_fixture_from_state",
                       return_value={"projectBId": "prj_b",
                                     "projectBRoot": str(Path(temporary) / "project-b")}):
                code, output, _service, outer, _observed = self._run_selected_main(
                    Path(temporary), "secured", selection="S-100.1",
                    selector="service-secured:explore", selected_owners=owning,
                    update_runner=selected_update, update_variant_runner=lambda **_kwargs: Journey(
                        "secured", "fake-variants"),
                )
        self.assertNotIn("platform update journey", output)
        self.assertNotIn("unknown journey prerequisite", output)
        self.assertEqual(len(executions), 1)
        self.assertEqual(results[0]["status"], "PASS")
        self.assertEqual(next(step["status"] for step in results[0]["steps"]
                              if step["name"] == "unselected update claim"), "SKIPPED")
        self.assertNotIn("unselected update claim", output)
        self.assertEqual(outer["status"], "PASS")

    def test_main_selected_update_blocks_rollback_scan_when_declared_prerequisite_fails(self):
        selected_name = "verify disclosed bootstrap credential absent after update and rollback"
        first_prerequisite = "roll back to version N with explicit non-TTY confirmation"
        owning = {selected_name: ["S-100.1"], first_prerequisite: ["S-100.1"]}

        def failed_update(**kwargs):
            journey = Journey("secured", "selected-update-failure", parent=kwargs["parent_journey"])
            journey.step(first_prerequisite, "platform.rollback", lambda: "rollback command failed",
                         cases=["S-024.2"])
            journey.step(selected_name, "platform.rollback", lambda: None, cases=["S-100.1"],
                         requires=(f"step:{first_prerequisite}",))
            failed_results.append(journey.result().__dict__)
            return journey

        failed_results = []
        with tempfile.TemporaryDirectory(prefix="journey-main-selected-update-failure-") as temporary:
            code, output, _service, outer, _observed = self._run_selected_main(
                Path(temporary), "secured", selection="S-100.1",
                selector="service-secured:explore", selected_owners=owning,
                update_runner=failed_update,
            )
        self.assertEqual(code, 1)
        self.assertIn("platform update journey", output)
        self.assertIn(first_prerequisite, output)
        scan = next(step for step in failed_results[0]["steps"] if step["name"] == selected_name)
        self.assertEqual(scan["status"], "BLOCKED")
        self.assertIn(f"step:{first_prerequisite} ended FAIL", scan["details"]["prerequisiteFailures"][0])

    def test_main_full_secured_update_has_project_registration_state_without_conditional_step(self):
        runner = importlib.import_module("runner")
        captured = []

        def full_update(**kwargs):
            captured.append(kwargs)
            return Journey("secured", "full-update", parent=kwargs["parent_journey"])

        def full_variants(**_kwargs):
            return Journey("secured", "full-update-variants")

        with tempfile.TemporaryDirectory(prefix="journey-main-full-secured-update-") as temporary:
            with patch("runner.project_b_fixture_from_state",
                       return_value={"projectBId": "prj_b",
                                     "projectBRoot": str(Path(temporary) / "project-b")}):
                code, output, _service, _outer, _observed = self._run_selected_main(
                    Path(temporary), "secured", selection="", selector="service-secured:explore",
                    update_runner=full_update, update_variant_runner=full_variants,
                )
        self.assertNotIn("platform update journey", output)
        self.assertNotIn("unknown journey prerequisite", output)
        self.assertEqual(len(captured), 1)
        state = runner.ACTIVE_OUTER_GRAPH.states.get("project-registered")
        self.assertIsNotNone(state)
        self.assertTrue(state["passed"])

    def test_selected_step_is_blocked_by_failed_flow_graph_state_prerequisite(self):
        with patch.dict("os.environ", {
                "KB_JOURNEY_SELECTED_STEPS": "S-100.1",
                "KB_JOURNEY_PREREQUISITE_STEPS": "[]",
                "KB_JOURNEY_PREREQUISITE_FLOWS": "[]",
                "KB_JOURNEY_SELECTED_OWNING_STEPS": "{}",
        }, clear=False):
            parent = Journey("secured", "state-dependent-parent")
            graph = FlowGraph(parent, {}, states={"project-registered": {
                "passed": False, "evidence": "project registration failed",
            }})
            journey = Journey("secured", "state-dependent-child", parent=parent)
            result = journey.step("rollback scan", "platform.rollback", lambda: None,
                                  cases=["S-100.1"], requires=("state:project-registered",))
        self.assertEqual(result.status, "BLOCKED")
        self.assertIn("state:project-registered ended FAIL", result.message)

    def test_failed_project_probe_does_not_crash_runner_and_still_writes_summary(self):
        with tempfile.TemporaryDirectory(prefix="journey-failed-project-state-") as temporary:
            code, output, service, _outer, _observed = self._run_selected_main(
                Path(temporary), "local", selection="PC-002.3,PC-002.8",
                selected_owners={
                    "platform readiness (gateway + project workflow)": ["PC-002.3"],
                    "registered project runtime identity": ["PC-002.8"],
                },
                failed_project_probe=True,
            )
            self.assertEqual(code, 1)
            self.assertNotIn("runner crash", output)
            self.assertTrue((Path(temporary) / "reports" / "journey-summary.md").is_file())
            self.assertIs(service["states"]["project-registered"]["passed"], False)
            dependent = next(step for step in service["steps"]
                             if step["name"] == "platform readiness (gateway + project workflow)")
            independent = next(step for step in service["steps"]
                               if step["name"] == "independent auth check")
            self.assertEqual(dependent["status"], "BLOCKED")
            self.assertIn("project-registered", dependent["message"])
            self.assertIn("secondary shim missing", dependent["message"])
            self.assertEqual(independent["status"], "PASS")

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
            service_graph.dispatch_step(claim, group, capability, lambda: None, cases=[claim])

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
            selected_claims = selection.split(",")
            selected_owners = {f"claim {claim_id}": [claim_id] for claim_id in selected_claims}
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
                    "KB_JOURNEY_CASES": selection,
                    "KB_JOURNEY_SELECTED_STEPS": selection,
                    "KB_JOURNEY_SELECTED_OWNING_STEPS": json.dumps(selected_owners),
                    "KB_JOURNEY_SELECTED_CAPABILITIES": "platform.install",
                    "KB_JOURNEY_PREREQUISITE_STEPS": "[]",
                    "KB_JOURNEY_PREREQUISITE_FLOWS": "[]",
                    "KB_JOURNEY_RUNNER_SELECTOR": "agent:commit",
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
        self.assertIn("selected step never ran: claim S-042.1 (claim S-042.1)", summary)
        self.assertEqual(
            first_failure,
            "selected step never ran: claim S-042.1 (claim S-042.1)",
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
        process_status = 1
        self.assertEqual(process_status, catalogued_exit(envelope["code"]))
        result = runner.subprocess.CompletedProcess(
            ["kb-create", "install", "--json"], process_status,
            stdout="\n".join((
                json.dumps({"ok": False, "error": envelope}),
                json.dumps({"ok": True, "data": None,
                            "meta": {"stream": {"terminal": True, "exitCode": process_status}}}),
            )) + "\n", stderr="",
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
