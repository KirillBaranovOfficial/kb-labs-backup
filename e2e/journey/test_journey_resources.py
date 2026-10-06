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
