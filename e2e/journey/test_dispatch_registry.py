"""Keep selected-case routing complete for every active journey claim."""
from __future__ import annotations

import json
import os
import re
import shutil
import tempfile
import unittest
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import patch

from dispatch_registry import RUNNERS, runners_for_source
from hermetic_cli import run_hermetic_cli
from select_from_cases import (read_coverage_result, selection_from_coverage,
                               _case_declarations,
                               _step_declaration, _source_step_declarations,
                               _imported_journey_sources, selectors_from_coverage,
                               validate_dependency_declarations,
                               lint_direct_module_global_state_handoffs,
                               validate_runner_conditions, validate_journey_environment_reads,
                               full_runner_plans)
from platform_update_steps import run_platform_update_steps
from platform_update_variant_steps import run_platform_update_variant_steps
from service_steps import FlowGraph, Journey, source_selected


def helper_read(memory, key):
    return memory.get(key)


class DispatchRegistryTests(unittest.TestCase):
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

    def test_service_readiness_claim_resolves_registered_project_setup(self):
        resolved = selection_from_coverage(self._coverage(), "PC-002.3")
        self.assertEqual(resolved["prerequisiteSteps"], ["registered project setup"])
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
                    self.assertTrue(declaration["requires"], f"{claim_id} must declare its setup producer")
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
                                               RUNNERS[selector].variant.endswith("secured") == (mode == "secured"))
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
                    direct_prerequisites = set().union(*(set(item.get("requires", [])) for item in declarations))
                    prerequisites = {f"step:{name}" for name in resolved["prerequisiteSteps"]}
                    prerequisites.update(f"flow:{name}" for name in resolved["prerequisiteFlows"])
                    self.assertTrue(direct_prerequisites.issubset(prerequisites))
                    graph = _source_step_declarations(set(resolved["stepSources"]), root)
                    for name in prerequisites:
                        with self.subTest(mode=mode, step_id=step_id, prerequisite=name):
                            self.assertIn(name, graph, f"declared prerequisite {name!r} has no step declaration")
                            self.assertTrue(graph[name]["sources"].intersection(resolved["stepSources"]))
                            self.assertTrue(set(graph[name]["requires"]).issubset(prerequisites),
                                            f"prerequisite closure omitted a dependency of {name!r}")

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
                journey = Journey("secured" if runner.variant.endswith("secured") else "local",
                                  f"full-plan-{selector}")
                for _, declaration in full_plan:
                    journey.step(declaration["name"], "journey.registry", lambda: None)
                self.assertEqual([step.name for step in journey.steps],
                                 [declaration["name"] for _, declaration in full_plan])
                self.assertTrue(all(step.status == "PASS" for step in journey.steps))

    def test_base_update_and_variant_claims_dispatch_both_step_modules(self):
        resolved = selection_from_coverage(self._coverage(), "S-023.2,S-023.9")
        self.assertIn("e2e/journey/platform_update_steps.py", resolved["stepSources"])
        self.assertIn("e2e/journey/platform_update_variant_steps.py", resolved["stepSources"])
        self.assertTrue({"candidate provenance", "launcher version", "start host"}.issubset(
            set(resolved["prerequisiteSteps"])))
        self.assertIn("start platform host", resolved["prerequisiteFlows"])
        env = {"KB_JOURNEY_SELECTED_STEPS": ",".join(resolved["steps"]),
               "KB_JOURNEY_SELECTED_SOURCES": ",".join(resolved["stepSources"])}
        with patch.dict(os.environ, env, clear=False):
            self.assertTrue(source_selected(run_platform_update_steps.__code__.co_filename))
            self.assertTrue(source_selected(run_platform_update_variant_steps.__code__.co_filename))

    def test_selected_update_and_marketplace_claims_select_the_declared_units(self):
        coverage = self._coverage()
        base_update = selection_from_coverage(coverage, "S-023.2")
        self.assertIn("start platform host", base_update["prerequisiteFlows"])
        env = {
            "KB_JOURNEY_SELECTED_STEPS": ",".join(base_update["steps"]),
            "KB_JOURNEY_PREREQUISITE_STEPS": ",".join(base_update["prerequisiteSteps"]),
            "KB_JOURNEY_PREREQUISITE_FLOWS": ",".join(base_update["prerequisiteFlows"]),
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
                        r"fixture_steps.py:1: invalid journey dependency.*use 'step:<name>' or 'flow:<name>'"):
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
