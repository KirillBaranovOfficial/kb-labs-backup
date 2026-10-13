"""Keep selected-case routing complete for every active journey claim."""
from __future__ import annotations

import ast
import json
import os
import re
import shutil
import tempfile
import unittest
import shlex
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import patch

from dispatch_registry import RUNNERS, runners_for_source
from hermetic_cli import run_hermetic_cli
from select_from_cases import (read_coverage_result, selection_from_coverage,
                               _case_declarations,
                               _step_declaration, _source_step_declarations,
                               _imported_journey_sources, selectors_from_coverage,
                               fixture_group_declarations,
                               validate_dependency_declarations,
                               lint_direct_module_global_state_handoffs,
                               validate_runner_conditions, validate_journey_environment_reads,
                               full_runner_plans, resolve_selection, journey_source_group_declaration)
from platform_update_steps import run_platform_update_steps
from platform_update_variant_steps import run_platform_update_variant_steps
from journey_dependencies import Dependency, FLOW_GRAPH_STATE_PRODUCERS
from service_steps import FlowGraph, Journey


def helper_read(memory, key):
    return memory.get(key)


class DispatchRegistryTests(unittest.TestCase):
    def test_literal_kb_command_paths_resolve_to_registered_leaf_commands(self):
        root = Path(__file__).resolve().parents[2]
        registered = set()
        for manifest in root.glob("plugins/**/entry/src/manifest.ts"):
            text = manifest.read_text(encoding="utf-8")
            group_meta = set(re.findall(r"groupMeta\s*:\s*\[([^\]]*)\]", text, re.S))
            group_paths = {path for block in group_meta
                           for path in re.findall(r"path\s*:\s*['\"]([^'\"]+)['\"]", block)}
            command_paths = set(re.findall(r"path\s*:\s*['\"]([a-zA-Z0-9][a-zA-Z0-9 -]*)['\"]", text))
            registered.update(command_paths - group_paths)
        for manifest in (root / "e2e/journey/fixtures").glob("**/manifest.js"):
            text = manifest.read_text(encoding="utf-8")
            group_paths = set(re.findall(r"groupMeta\s*:\s*\[([^\]]*)\]", text, re.S))
            group_paths = {path for block in group_paths
                           for path in re.findall(r"path\s*:\s*['\"]([^'\"]+)['\"]", block)}
            command_paths = set(re.findall(r"path\s*:\s*['\"]([a-zA-Z0-9][a-zA-Z0-9 -]*)['\"]", text))
            registered.update(command_paths - group_paths)

        system_commands = {}
        system_root = root / "cli/commands/src/commands/system"
        for source_path in system_root.rglob("*.ts"):
            text = source_path.read_text(encoding="utf-8")
            for variable, name in re.findall(
                    r"export const (\w+)\s*=\s*defineSystemCommand[\s\S]*?\{\s*name\s*:\s*['\"]([^'\"]+)['\"]",
                    text):
                system_commands[variable] = name
        groups_text = (system_root / "groups.ts").read_text(encoding="utf-8")
        for group, members in re.findall(
                r"defineSystemCommandGroup\(\s*['\"]([^'\"]+)['\"][^\[]*\[([^\]]*)\]", groups_text, re.S):
            for member in re.findall(r"\b[A-Za-z_$][\w$]*\b", members):
                if member in system_commands:
                    registered.add(f"{group} {system_commands[member]}")

        paths = {}
        for directory in (root / "e2e", root / "scripts"):
            for path in directory.rglob("*"):
                if not path.is_file() or "node_modules" in path.parts:
                    continue
                if path.suffix not in {".py", ".sh", ".js", ".mjs", ".ts", ".yaml", ".yml"}:
                    continue
                text = path.read_text(encoding="utf-8", errors="replace")
                relative = path.relative_to(root).as_posix()
                if path.suffix == ".py":
                    try:
                        tree = ast.parse(text, filename=relative)
                    except SyntaxError:
                        continue
                    parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
                    for node in ast.walk(tree):
                        values = ([item.value if isinstance(item, ast.Constant) else None for item in node.elts]
                                  if isinstance(node, ast.List) else [])
                        if values and values[0] == "kb":
                            parent = parents.get(node)
                            if isinstance(parent, (ast.Compare, ast.Subscript, ast.Set)):
                                continue
                            command = []
                            for value in values[1:]:
                                if not isinstance(value, str) or value.startswith("-"):
                                    break
                                command.append(value)
                            if command:
                                paths.setdefault(" ".join(command), []).append(f"{relative}:{node.lineno}")
                    for node in ast.walk(tree):
                        if not isinstance(node, ast.Constant) or not isinstance(node.value, str):
                            continue
                        current = node
                        execution_context = False
                        while current in parents:
                            current = parents[current]
                            if isinstance(current, ast.Call):
                                function_name = (current.func.id if isinstance(current.func, ast.Name)
                                                 else current.func.attr if isinstance(current.func, ast.Attribute)
                                                 else "")
                                execution_context = function_name in {
                                    "run", "run_command", "run_pty_command", "_run", "exec", "spawn",
                                    "execFile", "execFileSync", "spawnSync",
                                }
                                break
                        if not execution_context:
                            continue
                        for match in re.finditer(r"(?<![\w-])kb((?:\s+[A-Za-z0-9_-]+){1,6})", node.value):
                            tokens = shlex.split(match.group(0))
                            command = []
                            for token in tokens[1:]:
                                if token.startswith("-") or token.startswith("$"):
                                    break
                                command.append(token)
                            if command:
                                paths.setdefault(" ".join(command), []).append(f"{relative}:{node.lineno}")
                elif path.suffix == ".sh":
                    for line_number, line in enumerate(text.splitlines(), start=1):
                        if line.lstrip().startswith("#"):
                            continue
                        for match in re.finditer(r"(?<![\w-])kb((?:\s+[A-Za-z0-9_-]+){1,6})", line):
                            tokens = shlex.split(match.group(0))
                            command = []
                            for token in tokens[1:]:
                                if token.startswith("-") or token.startswith("$"):
                                    break
                                command.append(token)
                            if command:
                                paths.setdefault(" ".join(command), []).append(f"{relative}:{line_number}")
                elif path.suffix in {".mjs", ".js", ".ts"}:
                    for match in re.finditer(
                            r"\b(?:exec|execFile|execFileSync|spawn|spawnSync|execa)\s*\(\s*['\"]kb['\"]\s*,\s*\[([^\]]*)\]",
                            text):
                        command = re.findall(r"['\"]([^'\"]+)['\"]", match.group(1))
                        command = [item for item in command if not item.startswith("-")]
                        if command:
                            line = text.count("\n", 0, match.start()) + 1
                            paths.setdefault(" ".join(command), []).append(f"{relative}:{line}")

        failures = {}
        for command, locations in paths.items():
            tokens = command.split()
            resolved = next((" ".join(tokens[:index]) for index in range(len(tokens), 0, -1)
                             if " ".join(tokens[:index]) in registered), None)
            if resolved is None:
                failures[command] = locations
        self.assertEqual(failures, {}, str(list(failures.items())[:30]))

    def test_fixture_default_runner_comes_from_registered_mode(self):
        with tempfile.TemporaryDirectory(prefix="journey-fixture-mode-") as temporary:
            root = Path(temporary)
            source_path = root / "e2e/journey/fixture_group.py"
            source_path.parent.mkdir(parents=True)
            source_path.write_text(
                "def selected_fixture(journey):\n"
                "    journey.step('secured fixture claim', 'test', lambda: None, cases=['TEST-MODE.1'])\n"
            )
            namespace = {"__file__": str(source_path)}
            exec(compile(source_path.read_text(), str(source_path), "exec"), namespace)
            registered_runner = SimpleNamespace(
                journey_sources=("e2e/journey/fixture_group.py",),
                mode="secured",
                claim_family="journey",
            )
            local_runner = SimpleNamespace(
                journey_sources=("e2e/journey/fixture_group.py",),
                mode="local",
                claim_family="journey",
            )
            with patch.dict("dispatch_registry.RUNNERS", {
                    "runner:blue": registered_runner,
                    "runner:green": local_runner,
            }, clear=True):
                declarations = fixture_group_declarations(
                    (namespace["selected_fixture"],), "runner:blue", root)
                local_declarations = fixture_group_declarations(
                    (namespace["selected_fixture"],), "runner:green", root)
                full_declarations = fixture_group_declarations(
                    (namespace["selected_fixture"],), "", root)

            self.assertEqual(declarations["selected_fixture"]["cases"], ("TEST-MODE.1",))
            self.assertEqual(local_declarations["selected_fixture"]["cases"], ("TEST-MODE.1",))
            self.assertEqual(full_declarations, declarations)

    def test_installer_fixture_claims_are_declared_for_the_outer_graph(self):
        from installer_case_steps import (run_apply_scenario_step, run_doctor_root_isolation_step,
                                          run_installer_case_steps, run_invalid_request_step,
                                          run_missing_project_request_steps)
        root = Path(__file__).resolve().parents[2]
        groups = (run_doctor_root_isolation_step, run_installer_case_steps,
                  run_apply_scenario_step, run_invalid_request_step,
                  run_missing_project_request_steps)
        declarations = fixture_group_declarations(groups, "agent:commit", root)
        self.assertIn("PC-001.4", declarations[run_installer_case_steps.__name__]["cases"])
        self.assertEqual(declarations[run_apply_scenario_step.__name__]["cases"], ("S-039.1",))
        self.assertIn("run_installer_case_steps", declarations)

    def test_fixture_claim_prerequisites_use_the_shared_resolver_steps(self):
        from tempfile import TemporaryDirectory
        with TemporaryDirectory(prefix="journey-fixture-registry-") as temporary:
            root = Path(temporary)
            source_path = root / "fixture_group.py"
            source_path.write_text(
                "def fixture_producer(journey):\n"
                "    journey.step('fixture producer', 'test', lambda: None, "
                "runners=['agent:commit'])\n"
                "def selected_consumer(journey):\n"
                "    journey.step('selected consumer', 'test', lambda: None, "
                "cases=['TEST-CLAIM.1'], requires=('step:fixture producer',), "
                "runners=['agent:commit'])\n"
            )
            namespace = {"__file__": str(source_path)}
            exec(compile(source_path.read_text(), str(source_path), "exec"), namespace)
            with patch("select_from_cases.RUNNERS", {
                    "agent:commit": SimpleNamespace(journey_sources=("fixture_group.py",), mode="local")}):
                declarations = fixture_group_declarations(
                    (namespace["fixture_producer"], namespace["selected_consumer"]), "agent:commit", root)
            self.assertEqual(declarations["selected_consumer"]["cases"], ("TEST-CLAIM.1",))
            self.assertIn("selected consumer", declarations["selected_consumer"]["steps"])
            self.assertIn("fixture producer", declarations["fixture_producer"]["steps"])

            with patch.dict("os.environ", {
                    "KB_JOURNEY_SELECTED_STEPS": "TEST-CLAIM.1",
                    "KB_JOURNEY_PREREQUISITE_STEPS": '["fixture producer"]',
            }, clear=True):
                journey = Journey("local", "fixture-group-selection")
                graph = FlowGraph(journey, {}, groups={
                    f"isolated-fixture:{name}": {
                        "cases": declaration["cases"],
                        "steps": declaration["steps"],
                    } for name, declaration in declarations.items()
                })
            self.assertTrue(graph.group_selected("isolated-fixture:selected_consumer"))
            self.assertTrue(graph.group_selected("isolated-fixture:fixture_producer"))

    def test_fixture_execution_and_host_cycle_use_outer_flow_graph(self):
        root = Path(__file__).resolve().parents[2]
        source = ast.parse((root / "e2e/journey/runner.py").read_text())
        calls = [node for node in ast.walk(source) if isinstance(node, ast.Call)]
        self.assertTrue(any(isinstance(call.func, ast.Attribute) and call.func.attr == "flow"
                            and isinstance(call.func.value, ast.Name) and call.func.value.id == "outer_graph"
                            and call.args and isinstance(call.args[0], ast.Attribute)
                            and call.args[0].attr == "__name__" for call in calls))
        self.assertTrue(any(isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                            and node.func.attr == "group_selected" for node in ast.walk(source)))
        host_commands = {call.args[0].elts[1].value for call in calls
                         if isinstance(call.func, ast.Name) and call.func.id == "run"
                         and call.args and isinstance(call.args[0], ast.List)
                         and len(call.args[0].elts) > 1 and isinstance(call.args[0].elts[1], ast.Constant)}
        self.assertTrue({"stop", "start"}.issubset(host_commands))

    def test_registered_memory_rejects_helper_read_without_prerequisite(self):
        from service_steps import JourneyMemory, JourneyContractError
        memory = JourneyMemory({"setup": ((), ("project",))})
        with memory.activate("consumer", (), ()):
            with self.assertRaisesRegex(JourneyContractError, "undeclared dependency.*consumer.*project.*setup"):
                helper_read(memory, "project")

    def test_registered_memory_rejects_attribute_held_state_without_prerequisite(self):
        from service_steps import JourneyMemory, JourneyContractError
        holder = type("Holder", (), {})()
        holder.memory = JourneyMemory({"setup": ((), ("project",))})
        with holder.memory.activate("consumer", (), ()):
            with self.assertRaisesRegex(JourneyContractError, "undeclared dependency.*consumer.*project.*setup"):
                holder.memory.get("project")

    def test_registered_memory_rejects_undeclared_write(self):
        from service_steps import JourneyMemory, JourneyContractError
        memory = JourneyMemory({})
        with memory.activate("writer", (), ()):
            with self.assertRaisesRegex(JourneyContractError, "undeclared write.*writer.*project"):
                memory.set("project", "registered")

    def test_journey_records_undeclared_memory_read_as_harness_failure(self):
        journey = Journey("local", "memory-contract")
        journey.step("project setup", "test",
                     lambda: journey.memory.set("project", "registered"),
                     provides=("project",))
        result = journey.step("project consumer", "test",
                              lambda: helper_read(journey.memory, "project"))
        self.assertEqual(result.status, "FAIL")
        self.assertRegex(result.message,
                         r"harness defect: undeclared dependency: reader 'project consumer' "
                         r"reads key 'project' from producer 'project setup'")

    def test_module_global_state_lint_detects_shared_mutable_global(self):
        with tempfile.TemporaryDirectory(prefix="journey-global-state-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'shared = {}\n'
                'def produce():\n'
                '    shared["project"] = "registered"\n'
                'def check():\n'
                '    return shared.get("project")\n'
                'journey.step("project setup", "test", produce)\n'
                'journey.step("consumer", "test", check)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "module-global-state lint detected"):
                lint_direct_module_global_state_handoffs({"fixture_steps.py"}, root)

    def test_module_global_state_lint_detects_attribute_held_global(self):
        with tempfile.TemporaryDirectory(prefix="journey-attribute-state-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'class Holder: pass\n'
                'state = Holder()\n'
                'state.values = {}\n'
                'def produce():\n'
                '    state.values["project"] = "registered"\n'
                'def check():\n'
                '    return state.values.get("project")\n'
                'journey.step("project setup", "test", produce)\n'
                'journey.step("consumer", "test", check)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "module-global-state lint detected"):
                lint_direct_module_global_state_handoffs({"fixture_steps.py"}, root)

    def test_module_global_state_lint_detects_helper_mediated_global_read(self):
        with tempfile.TemporaryDirectory(prefix="journey-helper-state-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'shared = {}\n'
                'def produce():\n'
                '    shared["project"] = "registered"\n'
                'def helper():\n'
                '    return shared.get("project")\n'
                'def check():\n'
                '    return helper()\n'
                'journey.step("project setup", "test", produce)\n'
                'journey.step("consumer", "test", check)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "module-global-state lint detected"):
                lint_direct_module_global_state_handoffs({"fixture_steps.py"}, root)

    def test_environment_lint_detects_literal_kb_journey_read_outside_accessor(self):
        with tempfile.TemporaryDirectory(prefix="journey-env-state-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'import os\n'
                'value = os.environ.get("KB_JOURNEY_PROJECT_ID")\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "KB_JOURNEY_PROJECT_ID.*journey_values"):
                validate_journey_environment_reads({"fixture_steps.py"}, root)

    def test_environment_lint_rejects_computed_environment_key(self):
        with tempfile.TemporaryDirectory(prefix="journey-dynamic-env-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'import os\n'
                'def check(key):\n'
                '    return os.environ.get(key)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "computed environment keys"):
                validate_journey_environment_reads({"fixture_steps.py"}, root)

    def test_journey_unknown_required_producer_is_runtime_contract_failure(self):
        journey = Journey("local", "unknown-memory-producer")
        result = journey.step("project consumer", "test", lambda: None,
                              cases=("S-042.1",), requires=("step:missing project producer",))
        self.assertEqual(result.status, "FAIL")
        self.assertIn("unknown journey prerequisite 'missing project producer'", result.message)
        self.assertEqual(journey.result().status, "FAIL")

    def test_dependency_lint_rejects_unknown_required_producer(self):
        with tempfile.TemporaryDirectory(prefix="journey-unknown-producer-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'def check(): return None\n'
                'journey.step("project consumer", "test", check, '
                'requires=("step:missing project producer",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(ValueError, "does not resolve to a declared"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_full_unselected_plan_matches_checked_in_golden(self):
        root = Path(__file__).resolve().parents[2]
        baseline_path = root / "e2e/journey/full-plan-baseline.json"
        expected = json.loads(baseline_path.read_text(encoding="utf-8"))
        actual = full_runner_plans(root)
        self.assertEqual(
            actual, expected,
            "full journey plan drifted; run python3 e2e/journey/select_from_cases.py "
            "--write-plan-baseline, then review the ordered plan diff before updating the golden",
        )

    def test_service_readiness_claim_uses_registered_project_state(self):
        resolved = selection_from_coverage(self._coverage(), "PC-002.3")
        self.assertEqual(resolved["prerequisiteSteps"], [])
        self.assertIn("e2e/journey/service_runner.py", resolved["stepSources"])

    def test_fresh_install_claim_resolves_candidate_provenance(self):
        resolved = selection_from_coverage(self._coverage(), "PC-001.2")
        self.assertEqual(resolved["prerequisiteSteps"], ["candidate provenance"])

    def test_installed_smoke_claims_resolve_to_one_service_runner_per_mode(self):
        root = Path(__file__).resolve().parents[2]
        smoke_source = (root / "e2e/journey/smoke.sh").read_text(encoding="utf-8")
        smoke_selection = re.search(r'^export KB_JOURNEY_CASES="([^"]+)"$', smoke_source, re.MULTILINE)
        self.assertIsNotNone(smoke_selection)
        selected_claims = set(smoke_selection.group(1).split(","))
        registrations: dict[str, list[tuple[str, dict]]] = {}
        runner_sources = {}
        for selector, runner in RUNNERS.items():
            if not runner.service_profile:
                continue
            runner_sources[selector] = _imported_journey_sources(set(runner.journey_sources), root)
        for source in set().union(*runner_sources.values()):
            declarations_by_signature = {}
            for declaration in _case_declarations(source, root).values():
                signature = (declaration["name"], tuple(declaration.get("cases") or ()),
                             tuple(declaration["requires"]), tuple(declaration["runners"]))
                declarations_by_signature[signature] = declaration
            for declaration in declarations_by_signature.values():
                for claim_id in declaration.get("cases") or ():
                    registrations.setdefault(claim_id, []).append((source, declaration))

        for mode in ("local", "secured"):
            mode_branch = re.search(
                rf"(?ms)^  {mode}\)\s+export KB_JOURNEY_ONLY=\"([^\"]+)\"",
                smoke_source,
            )
            self.assertIsNotNone(mode_branch)
            selector = mode_branch.group(1)
            runner = RUNNERS[selector]
            declarations = _source_step_declarations(runner_sources[selector], root)
            claim_prerequisites = set()
            for claim_id in selected_claims:
                with self.subTest(mode=mode, claim=claim_id):
                    owners = []
                    for source, declaration in registrations.get(claim_id, []):
                        declared_runners = declaration["runners"] or runners_for_source(source, mode)
                        if selector in declared_runners and source in runner_sources[selector]:
                            owners.append((source, declaration))
                    self.assertEqual(len(owners), 1, f"{claim_id} must have one registration on {selector}")
                    claim_source, declaration = owners[0]
                    for prerequisite_name in declaration["requires"]:
                        prerequisite = declarations[prerequisite_name]
                        self.assertTrue(prerequisite["sources"].intersection(runner_sources[selector]))
                        claim_prerequisites.add(prerequisite_name)

            self.assertEqual(
                {claim_id for claim_id in registrations if any(
                    selector in (declaration["runners"] or runners_for_source(source, mode))
                    for source, declaration in registrations[claim_id]
                    if source in runner_sources[selector])}.intersection(selected_claims),
                selected_claims,
            )
            self.assertTrue(claim_prerequisites)

    def test_every_active_journey_claim_resolves_its_runner_and_prerequisites(self):
        results = []
        root = Path(__file__).resolve().parents[2]
        node = shutil.which("node")
        self.assertIsNotNone(node, "Node.js 24 is required to run the workspace CLI")
        for mode in ("local", "secured"):
            command = [node, str(root / "cli/bin/dist/bin.js"), "qa", "coverage", "show", "--level", "all",
                       "--mode", mode, "--json"]
            result = run_hermetic_cli(command, cwd=root, env=os.environ, timeout=120)
            self.assertEqual(result.returncode, 0, result.stderr)
            results.append(read_coverage_result(result.stdout, result.returncode))

        registered: dict[str, dict[str, set[tuple[str, int]]]] = {}
        root = Path(__file__).resolve().parents[2]
        for coverage in results:
            mode = coverage["mode"]
            for case in coverage.get("cases", []):
                if case.get("status") != "active":
                    continue
                for step in case.get("steps", []):
                    step_id = step.get("stepId")
                    for claim in step.get("claims", []):
                        if claim.get("kind") not in {"journey", "e2e"}:
                            continue
                        source = str(claim.get("source", ""))
                        if source:
                            registered.setdefault(mode, {}).setdefault(step_id, set()).add(
                                (source, int(claim.get("line", 0))))

        self.assertTrue(registered, "QA claim registry returned no active journey claims")
        resolved_counts = {mode: 0 for mode in registered}
        refused_claims: dict[str, dict[str, str]] = {mode: {} for mode in registered}
        for mode, claims_by_step in registered.items():
            coverage = next(item for item in results if item["mode"] == mode)
            for step_id, claims in claims_by_step.items():
                with self.subTest(mode=mode, step_id=step_id):
                    declarations = [_step_declaration(source, line, root)
                                    for source, line in claims if source.endswith(".py")]
                    expected_routes = set()
                    for source, line in claims:
                        declaration = _step_declaration(source, line, root) if source.endswith(".py") else {}
                        routes = declaration.get("runners") or runners_for_source(source, mode)
                        expected_routes.update(selector for selector in routes
                                               if selector in RUNNERS and
                                               RUNNERS[selector].mode == mode)
                    try:
                        resolved = selection_from_coverage([coverage], step_id)
                    except ValueError as error:
                        self.assertFalse(expected_routes, f"{step_id} has executable routes but was refused: {error}")
                        self.assertIn("no executable registration", str(error),
                                      f"{step_id} was refused for an unrelated reason: {error}")
                        refused_claims[mode][step_id] = str(error)
                        continue
                    resolved_counts[mode] += 1
                    self.assertTrue(expected_routes, f"{step_id} resolved without an eligible registration")
                    self.assertIn(step_id, resolved["steps"])
                    self.assertEqual(set(resolved["runners"]), expected_routes)
                    self.assertTrue(all(selector in RUNNERS for selector in resolved["runners"]))
                    for selector in resolved["runners"]:
                        self.assertIn(step_id, resolved["owners"].get(selector, []),
                                      f"plan routed {step_id} to {selector} without its emitter")
                    direct_prerequisites = {
                        str(dependency)
                        for item in declarations
                        for reference in item.get("requires", [])
                        if (dependency := Dependency.parse(reference)).kind in {"step", "flow"}
                    }
                    prerequisites = {f"step:{name}" for name in resolved["prerequisiteSteps"]}
                    prerequisites.update(f"flow:{name}" for name in resolved["prerequisiteFlows"])
                    self.assertTrue(direct_prerequisites.issubset(prerequisites))
                    graph = _source_step_declarations(set(resolved["stepSources"]), root)
                    for name in prerequisites:
                        with self.subTest(mode=mode, step_id=step_id, prerequisite=name):
                            self.assertIn(name, graph, f"declared prerequisite {name!r} has no step declaration")
                            self.assertTrue(graph[name]["sources"].intersection(resolved["stepSources"]))
                            required = {Dependency.parse(reference)
                                        for reference in graph[name]["requires"]}
                            self.assertTrue({str(item) for item in required if item.kind in {"step", "flow"}}
                                            .issubset(prerequisites),
                                            f"prerequisite closure omitted a dependency of {name!r}")
                            self.assertTrue({item.name for item in required if item.kind == "state"}
                                            .issubset(FLOW_GRAPH_STATE_PRODUCERS),
                                            f"prerequisite closure names an unregistered state of {name!r}")

        for mode, claims_by_step in registered.items():
            print(f"whole-coverage resolution {mode}: {len(claims_by_step)} claims, "
                  f"{resolved_counts[mode]} routed, {len(refused_claims[mode])} refused")
            for step_id, reason in sorted(refused_claims[mode].items()):
                print(f"refused {mode} {step_id}: {reason}")

        default_matrix = selectors_from_coverage(results)
        for selector in default_matrix:
            runner = RUNNERS[selector]
            reachable = set().union(*(_imported_journey_sources({source}, root)
                                      for source in runner.journey_sources))
            registrations = _source_step_declarations(reachable, root)
            full_plan = [(key, declaration) for key, declaration in registrations.items()
                         if declaration.get("name") is not None
                         and (not declaration.get("runners") or selector in declaration["runners"])]
            with self.subTest(full_run=selector), patch.dict(os.environ, {
                    "KB_JOURNEY_CASES": "", "KB_JOURNEY_SELECTED_STEPS": "",
                    "KB_JOURNEY_RUNNER_SELECTOR": selector,
            }, clear=False):
                journey = Journey(runner.mode, f"full-plan-{selector}")
                for _, declaration in full_plan:
                    journey.step(declaration["name"], "journey.registry", lambda: None)
                self.assertEqual([step.name for step in journey.steps],
                                 [declaration["name"] for _, declaration in full_plan])
                self.assertTrue(all(step.status == "PASS" for step in journey.steps))

    def test_resolver_names_update_owners_and_prerequisites(self):
        resolved = resolve_selection(
            ["S-023.2"],
            {"S-023.2": {"e2e/journey/platform_update_steps.py"}},
            {"S-023.2": [{"kind": "step", "name": "plan the newer sealed release",
                           "requires": ["step:candidate provenance"]}]},
            {"step:candidate provenance": {"sources": {"e2e/journey/runner.py"}, "requires": []}},
            {"S-023.2": {"agent:commit": {"S-023.2"}}},
            "",
        )
        self.assertEqual(resolved["selectedOwningSteps"], {
            "candidate provenance": ["S-023.2"],
            "plan the newer sealed release": ["S-023.2"],
        })

    def test_flow_graph_uses_resolved_step_names_to_select_update_journey(self):
        with patch.dict(os.environ, {
                "KB_JOURNEY_SELECTED_STEPS": "S-023.2",
                "KB_JOURNEY_SELECTED_OWNING_STEPS": json.dumps({"plan the newer sealed release": ["S-023.2"]}),
                "KB_JOURNEY_PREREQUISITE_STEPS": '["candidate provenance"]',
        }, clear=True):
            journey = Journey("local", "update-step-selection")
            graph = FlowGraph(journey, {}, groups={"update": {
                "cases": ("S-023.2",), "steps": ("plan the newer sealed release",),
            }})
            self.assertTrue(graph.group_selected("update"))
            self.assertTrue(graph.selects_steps("plan the newer sealed release"))

    def test_resolver_group_declaration_follows_local_helper_imports(self):
        with tempfile.TemporaryDirectory(prefix="journey-source-group-") as temporary:
            root = Path(temporary)
            helper = root / "e2e/journey/owner_steps.py"
            runner = root / "e2e/journey/update_steps.py"
            helper.parent.mkdir(parents=True)
            helper.write_text(
                "def register(journey):\n"
                "    journey.step('helper-owned claim', 'update', lambda: None, cases=['TEST-UPDATE.1'])\n",
                encoding="utf-8",
            )
            runner.write_text("from owner_steps import register\ndef run(journey):\n    register(journey)\n",
                              encoding="utf-8")
            declaration = journey_source_group_declaration("e2e/journey/update_steps.py", root)
        self.assertEqual(declaration, {
            "cases": ("TEST-UPDATE.1",), "steps": ("helper-owned claim",),
        })

    def test_source_file_selection_gate_is_removed(self):
        root = Path(__file__).resolve().parents[2]
        source = (root / "e2e/journey/runner.py").read_text()
        values = (root / "e2e/journey/service_steps.py").read_text()
        shell = (root / "e2e/journey/run.sh").read_text()
        self.assertNotIn("def source_selected", source)
        self.assertNotIn("source_selected", values)
        self.assertNotIn("KB_JOURNEY_SELECTED_SOURCES", shell + values)
        self.assertIn("journey_source_group_declaration", source)

    def test_selected_owning_steps_are_forwarded_to_the_runner_container(self):
        root = Path(__file__).resolve().parents[2]
        shell = (root / "e2e/journey/run.sh").read_text(encoding="utf-8")
        self.assertIn("export KB_JOURNEY_SELECTED_OWNING_STEPS=", shell)
        self.assertIn("-e KB_JOURNEY_SELECTED_OWNING_STEPS", shell)

    def test_selected_update_and_marketplace_claims_select_the_declared_units(self):
        coverage = self._coverage()
        base_update = selection_from_coverage(coverage, "S-023.2")
        self.assertIn("start platform host", base_update["prerequisiteFlows"])
        env = {
            "KB_JOURNEY_SELECTED_STEPS": ",".join(base_update["steps"]),
            "KB_JOURNEY_PREREQUISITE_STEPS": json.dumps(base_update["prerequisiteSteps"]),
            "KB_JOURNEY_PREREQUISITE_FLOWS": json.dumps(base_update["prerequisiteFlows"]),
        }
        with patch.dict(os.environ, env, clear=False):
            journey = Journey("local", "selected-flow-closure")
            graph = FlowGraph(journey, {})
            status = graph.flow(
                "start platform host", "host", "platform.host",
                lambda: journey.step("start host", "platform.host", lambda: None, cases=["PC-002.1"]),
                cases=["PC-002.1"],
            )
            self.assertEqual(status, "PASS")
            self.assertEqual(next(step.status for step in journey.steps if step.name == "start host"), "PASS")
            self.assertEqual(journey.flows["start platform host"][0], "PASS")

        variant = selection_from_coverage(coverage, "S-023.9")
        self.assertIn("platform_update_variant_steps.py", ",".join(variant["stepSources"]))
        self.assertIn("start platform host", variant["prerequisiteFlows"])
        marketplace = selection_from_coverage(coverage, "S-010.4")
        self.assertIn("install Commit into the project for the route lifecycle",
                      marketplace["prerequisiteSteps"])
        self.assertTrue(any(source.endswith("marketplace_steps.py") for source in marketplace["stepSources"]))

    def test_static_dependency_rail_reports_unresolvable_name_at_declaration(self):
        with tempfile.TemporaryDirectory(prefix="journey-dependency-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step("consumer", "test", check, requires=("step:typo",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(ValueError, r"fixture_steps.py:1: 'step:typo'.*does not resolve"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_runner_conditional_step_prerequisite(self):
        with tempfile.TemporaryDirectory(prefix="journey-conditional-prerequisite-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'def register(journey, check):\n'
                '    if FRONT == "agent":\n'
                '        journey.step("conditional project registration", "test", lambda: None)\n'
                '    journey.step("consumer", "test", lambda: None, requires=("step:conditional project registration",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:3: step prerequisite 'conditional project registration'.*runner condition"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_resolves_registered_flow_graph_state(self):
        with tempfile.TemporaryDirectory(prefix="journey-state-prerequisite-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step("consumer", "test", lambda: None, requires=("state:project-registered",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                declarations = validate_dependency_declarations({"fixture_steps.py"}, root)
            self.assertIn("step:consumer", declarations)

    def test_static_dependency_rail_rejects_untyped_dependency_names(self):
        with tempfile.TemporaryDirectory(prefix="journey-untyped-dependency-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step("consumer", "test", check, requires=("candidate provenance",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: invalid journey dependency.*use 'step:<name>'.*'state:<name>'"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_checks_edges_when_step_name_is_dynamic(self):
        with tempfile.TemporaryDirectory(prefix="journey-dynamic-name-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step(name, "test", check, requires=("step:missing",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: 'step:missing'.*does not resolve"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_dynamic_step_name_is_not_a_selector_producer(self):
        with tempfile.TemporaryDirectory(prefix="journey-dynamic-producer-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step(name, "test", check)\n'
                'journey.step("consumer", "test", check, requires=("step:runtime-name",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:2: 'step:runtime-name'.*does not resolve"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_follows_forwarded_requires_to_call_sites(self):
        with tempfile.TemporaryDirectory(prefix="journey-forwarded-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'def record(journey, name, check, *, requires=()):\n'
                '    journey.step(name, "test", check, requires=requires)\n'
                'def run(journey, check):\n'
                '    record(journey, "consumer", check, requires=("step:misspelled",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:4: 'step:misspelled'.*does not resolve"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_computed_requires(self):
        with tempfile.TemporaryDirectory(prefix="journey-computed-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step("consumer", "test", check, requires=dependencies)\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: dependency declaration is not statically resolvable"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_forwarded_computed_requires_fails_at_call_site(self):
        with tempfile.TemporaryDirectory(prefix="journey-forwarded-computed-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'def record(journey, name, check, *, requires=()):\n'
                '    journey.step(name, "test", check, requires=requires)\n'
                'def run(journey, check, dependencies):\n'
                '    record(journey, "consumer", check, requires=dependencies)\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:4: dependency declaration is not statically resolvable"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_keyword_expansion(self):
        with tempfile.TemporaryDirectory(prefix="journey-kwargs-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'journey.step("consumer", "test", check, **options)\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: dependency declaration is not statically resolvable"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_module_global_state_lint_detects_shared_state_read(self):
        with tempfile.TemporaryDirectory(prefix="journey-state-dependency-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'shared_state = {}\n'
                'def produce():\n'
                '    shared_state["project"] = "registered"\n'
                'def consume():\n'
                '    return shared_state.get("project")\n'
                'journey.step("project setup", "test", produce)\n'
                'journey.step("project consumer", "test", consume)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, r"module-global-state lint detected.*'shared_state'"):
                lint_direct_module_global_state_handoffs({"fixture_steps.py"}, root)

    def test_module_global_state_lint_detects_dynamic_key_read(self):
        with tempfile.TemporaryDirectory(prefix="journey-dynamic-state-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'shared_state = {}\n'
                'def produce():\n'
                '    shared_state["project"] = "registered"\n'
                'def consume():\n'
                '    return shared_state[key]\n'
                'journey.step("project setup", "test", produce)\n'
                'journey.step("project consumer", "test", consume)\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, r"module-global-state lint detected.*'shared_state'"):
                lint_direct_module_global_state_handoffs({"fixture_steps.py"}, root)

    def test_runner_condition_rail_rejects_unowned_conditional_registration(self):
        with tempfile.TemporaryDirectory(prefix="journey-runner-condition-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'if VARIANT == "interactive":\n'
                '    journey.step("conditional claim", "test", check, cases=["S-900.1"])\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, r"fixture_steps.py:2: claim registration is conditional"):
                validate_runner_conditions({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_partial_recorder(self):
        with tempfile.TemporaryDirectory(prefix="journey-partial-recorder-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'from functools import partial\n'
                'record = partial(journey.step, "consumer", "test", check, requires=("step:missing",))\n'
                'record()\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:2: recorder must be called directly so its dependencies are statically visible"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_recorder_alias(self):
        with tempfile.TemporaryDirectory(prefix="journey-alias-recorder-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'rec = journey.step\n'
                'rec("consumer", "test", check, requires=("step:missing",))\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: recorder must be called directly so its dependencies are statically visible"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def test_static_dependency_rail_rejects_recorder_callback(self):
        with tempfile.TemporaryDirectory(prefix="journey-callback-recorder-rail-") as directory:
            root = Path(directory)
            (root / "fixture_steps.py").write_text(
                'register_callback(journey.step)\n',
                encoding="utf-8",
            )
            with patch("select_from_cases.RUNNERS", {
                    "fixture": SimpleNamespace(journey_sources=("fixture_steps.py",))}):
                with self.assertRaisesRegex(
                        ValueError,
                        r"fixture_steps.py:1: recorder must be called directly so its dependencies are statically visible"):
                    validate_dependency_declarations({"fixture_steps.py"}, root)

    def _coverage(self):
        root = Path(__file__).resolve().parents[2]
        node = shutil.which("node")
        self.assertIsNotNone(node)
        results = []
        for mode in ("local", "secured"):
            command = [node, str(root / "cli/bin/dist/bin.js"), "qa", "coverage", "show", "--level", "all",
                       "--mode", mode, "--json"]
            result = run_hermetic_cli(command, cwd=root, env=os.environ, timeout=120)
            self.assertEqual(result.returncode, 0, result.stderr)
            results.append(read_coverage_result(result.stdout, result.returncode))
        return results


if __name__ == "__main__":
    unittest.main()
