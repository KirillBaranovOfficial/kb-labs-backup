"""Keep selected-case routing complete for every active journey claim."""
from __future__ import annotations

import os
import shutil
import tempfile
import unittest
from types import SimpleNamespace
from pathlib import Path
from unittest.mock import patch

from dispatch_registry import RUNNERS, runners_for_source
from hermetic_cli import run_hermetic_cli
from select_from_cases import (read_coverage_result, selection_from_coverage,
                               _step_declaration, _source_step_declarations,
                               validate_dependency_declarations)
from platform_update_steps import run_platform_update_steps
from platform_update_variant_steps import run_platform_update_variant_steps
from service_steps import FlowGraph, Journey, source_selected


class DispatchRegistryTests(unittest.TestCase):
    def test_every_active_journey_claim_resolves_its_runner_and_prerequisites(self):
        results = []
        root = Path(__file__).resolve().parents[2]
        node = shutil.which("node")
        self.assertIsNotNone(node, "Node.js 24 is required to run the workspace CLI")
        for mode in ("local", "secured"):
            command = [node, str(root / "cli/bin/dist/bin.js"), "qa", "cases", "coverage", "--level", "all",
                       "--mode", mode, "--json"]
            result = run_hermetic_cli(command, cwd=root, env=os.environ, timeout=120)
            self.assertEqual(result.returncode, 0, result.stderr)
            results.append(read_coverage_result(result.stdout, result.returncode))

        registered: dict[str, set[tuple[str, str, int]]] = {}
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
                            registered.setdefault(step_id, set()).add((mode, source, int(claim.get("line", 0))))

        self.assertTrue(registered, "QA claim registry returned no active journey claims")
        for step_id, claims in registered.items():
            with self.subTest(step_id=step_id):
                resolved = selection_from_coverage(results, step_id)
                self.assertIn(step_id, resolved["steps"])
                self.assertTrue(resolved["runners"])
                self.assertTrue(all(selector in RUNNERS for selector in resolved["runners"]))
                expected_routes = {
                    selector
                    for mode, source, _ in claims
                    for selector in runners_for_source(source, mode)
                }
                self.assertTrue(expected_routes, f"no route registered for {step_id}: {claims}")
                self.assertTrue(expected_routes.intersection(resolved["runners"]))
                declarations = [_step_declaration(source, line, root)
                                for _, source, line in claims if source.startswith("e2e/")]
                direct_prerequisites = set().union(*(set(item.get("requires", [])) for item in declarations))
                prerequisites = {f"step:{name}" for name in resolved["prerequisiteSteps"]}
                prerequisites.update(f"flow:{name}" for name in resolved["prerequisiteFlows"])
                self.assertTrue(direct_prerequisites.issubset(prerequisites))
                graph = _source_step_declarations(set(resolved["stepSources"]), root)
                for name in prerequisites:
                    with self.subTest(step_id=step_id, prerequisite=name):
                        self.assertIn(name, graph, f"declared prerequisite {name!r} has no step declaration")
                        self.assertTrue(graph[name]["sources"].intersection(resolved["stepSources"]))
                        self.assertTrue(set(graph[name]["requires"]).issubset(prerequisites),
                                        f"prerequisite closure omitted a dependency of {name!r}")

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
            command = [node, str(root / "cli/bin/dist/bin.js"), "qa", "cases", "coverage", "--level", "all",
                       "--mode", mode, "--json"]
            result = run_hermetic_cli(command, cwd=root, env=os.environ, timeout=120)
            self.assertEqual(result.returncode, 0, result.stderr)
            results.append(read_coverage_result(result.stdout, result.returncode))
        return results


if __name__ == "__main__":
    unittest.main()
