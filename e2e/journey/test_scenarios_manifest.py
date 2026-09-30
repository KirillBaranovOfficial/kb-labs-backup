from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(Path(__file__).resolve().parent))

from scenarios import load_manifest, selectors, validate  # noqa: E402


class ScenarioManifestTests(unittest.TestCase):
    def test_every_scenario_has_a_step_or_explicit_not_covered_reason(self):
        manifest = load_manifest()
        self.assertEqual(validate(manifest), [])
        for item in manifest["scenarios"]:
            if item["coverage"] == "not_covered":
                self.assertIsNone(item["step"])
                self.assertTrue(item["reason"])
                self.assertTrue(item["estimate"])
            else:
                self.assertTrue(item["step"])
        builtins = manifest["builtInScenarios"]
        catalog = ROOT / builtins["directory"]
        catalog_ids = {path.stem for path in catalog.glob("*.json")} - set(builtins["exclude"])
        declared_builtins = {value.removeprefix("agent:") for value in selectors(manifest)
                             if value.startswith("agent:")}
        self.assertTrue(catalog_ids <= declared_builtins)

    def test_local_release_and_nightly_gates_use_the_same_manifest(self):
        local_gate = (ROOT / "scripts/local-gate.sh").read_text()
        runner = (ROOT / "e2e/journey/run.sh").read_text()
        reusable = (ROOT / ".github/workflows/reusable-installer-journey.yml").read_text()
        release = (ROOT / ".github/workflows/release-build-candidate.yml").read_text()
        nightly = (ROOT / ".github/workflows/nightly-user-journey.yml").read_text()
        self.assertIn("e2e/journey/scenarios.py validate", local_gate)
        self.assertIn('e2e/journey/scenarios.py","list', runner)
        self.assertIn("e2e/journey/scenarios.py validate", reusable)
        self.assertIn("SCENARIO_LEVEL: ${{ inputs.scenario-level }}", reusable)
        self.assertIn("e2e/journey/scenarios.py list", reusable)
        self.assertIn("reusable-installer-journey.yml", release)
        self.assertIn("reusable-installer-journey.yml", nightly)
        self.assertIn("scenario-level: release", release)
        self.assertIn("scenario-level: nightly", nightly)
        self.assertNotIn("e2e-platform.yml", nightly)

    def test_uncovered_scenarios_fail_contract_validation(self):
        result = __import__("subprocess").run(
            [sys.executable, str(ROOT / "e2e/journey/scenarios.py"), "validate", "--level", "release"],
            cwd=ROOT, text=True, capture_output=True,
        )
        manifest = json.loads((ROOT / "e2e/journey/scenarios.json").read_text())
        uncovered = [item for item in manifest["scenarios"] if item["coverage"] == "not_covered"]
        self.assertTrue(uncovered)
        self.assertNotEqual(result.returncode, 0)
        for item in uncovered:
            self.assertIn(item["id"], result.stderr)

    def test_scaffold_http_probe_is_not_a_scenario_step(self):
        service_runner = (ROOT / "e2e/journey/service_runner.py").read_text()
        self.assertNotIn("/api/v1/scaffold/templates", service_runner)
        scaffold = next(item for item in load_manifest()["scenarios"]
                        if item["id"] == "scaffold.plugin-author-loop")
        self.assertEqual(scaffold["coverage"], "not_covered")


if __name__ == "__main__":
    unittest.main(verbosity=2)
