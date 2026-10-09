"""Keep selected-case routing complete for every active journey claim."""
from __future__ import annotations

import os
import shutil
import unittest
from pathlib import Path
from unittest.mock import patch

from dispatch_registry import RUNNERS, runners_for_source
from hermetic_cli import run_hermetic_cli
from select_from_cases import (read_coverage_result, selection_from_coverage,
                               _step_declaration, _source_step_declarations)
from platform_update_steps import run_platform_update_steps
from platform_update_variant_steps import run_platform_update_variant_steps
from service_steps import source_selected


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
                prerequisites = set(resolved["prerequisiteSteps"])
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
        env = {"KB_JOURNEY_SELECTED_STEPS": ",".join(resolved["steps"]),
               "KB_JOURNEY_SELECTED_SOURCES": ",".join(resolved["stepSources"])}
        with patch.dict(os.environ, env, clear=False):
            self.assertTrue(source_selected(run_platform_update_steps.__code__.co_filename))
            self.assertTrue(source_selected(run_platform_update_variant_steps.__code__.co_filename))

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
