from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from runner import project_fixture_handoff


class ProjectFixtureHandoffTests(unittest.TestCase):
    def test_missing_handoff_returns_a_reportable_failure_instead_of_partial_values(self):
        with tempfile.TemporaryDirectory(prefix="journey-project-handoff-") as temp:
            path = Path(temp) / "project-fixtures.json"
            path.write_text("{}", encoding="utf-8")

            result = project_fixture_handoff(path)

        self.assertIsInstance(result, str)
        self.assertIn("valid project B ID or canonical root", result)

    def test_valid_handoff_returns_both_values(self):
        with tempfile.TemporaryDirectory(prefix="journey-project-handoff-") as temp:
            path = Path(temp) / "project-fixtures.json"
            root = str((Path(temp) / "project-b").resolve())
            path.write_text(json.dumps({"projectBId": "project-b", "projectBRoot": root}), encoding="utf-8")

            self.assertEqual(project_fixture_handoff(path), ("project-b", root))


if __name__ == "__main__":
    unittest.main()
