from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from journey_resources import (case_catalog_root, installer_case_fixture_root,
                               workspace_resource, installed_platform_resource,
                               read_installed_platform_resource)


class JourneyResourcesTest(unittest.TestCase):
    def test_resources_share_repository_relative_layout(self):
        root = Path(__file__).resolve().parents[2]

        self.assertEqual(case_catalog_root(), root / "docs/qa/scenarios")
        self.assertEqual(installer_case_fixture_root(), root / "e2e/platform/fixtures/installer-cases")
        self.assertEqual(workspace_resource("contracts/ux-result/machine-result.schema.json"),
                         root / "contracts/ux-result/machine-result.schema.json")

    def test_installed_contract_path_is_explicit_and_has_no_repository_fallback(self):
        self.assertEqual(installed_platform_resource("ux-result/machine-result.schema.json"),
                         Path("/opt/kb-contracts/ux-result/machine-result.schema.json"))

    def test_missing_installed_contract_raises_at_installed_boundary(self):
        with self.assertRaises(FileNotFoundError):
            read_installed_platform_resource("missing-contract.json")


if __name__ == "__main__":
    unittest.main()


class JourneyScriptPathsTest(unittest.TestCase):
    """The runner image mirrors the repository, so scripts must resolve through journey_module."""

    def test_no_hardcoded_image_paths_for_journey_scripts(self):
        journey_dir = Path(__file__).resolve().parent
        offenders = []
        for source in sorted(journey_dir.glob("*.py")):
            if source.name.startswith("test_"):
                continue
            for number, line in enumerate(source.read_text().splitlines(), 1):
                if "/opt/kb-journey" in line:
                    offenders.append(f"{source.name}:{number}")
        self.assertEqual(offenders, [], "use journey_resources.journey_module() instead of image paths")

    def test_runner_scripts_resolve_to_existing_files(self):
        from journey_resources import journey_module
        for name in ("runner.py", "service_runner.py", "entrypoint.py"):
            self.assertTrue(journey_module(name).is_file(), name)
