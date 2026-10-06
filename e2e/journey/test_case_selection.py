from __future__ import annotations

import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from select_from_cases import read_coverage_result, selectors_from_coverage


def coverage(mode: str, *, source: str, kind: str = "journey", status: str = "active", release: bool = True):
    return {
        "mode": mode,
        "cases": [{
            "status": status,
            "release": release,
            "steps": [{"claims": [{"source": source, "kind": kind}]}],
        }],
    }


class CaseJourneySelectionTests(unittest.TestCase):
    # case: S-203.1
    def test_coverage_cli_reader_requires_success_envelope(self):
        self.assertEqual(read_coverage_result(
            '{"ok":true,"data":{"mode":"local","cases":[]}}', 0,
        ), {"mode": "local", "cases": []})
        for stdout, returncode in (
            ('{"mode":"local","cases":[]}', 0),
            ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', 1),
            ('{"ok":true,"data":{}}', 1),
        ):
            with self.subTest(stdout=stdout, returncode=returncode), self.assertRaises(ValueError):
                read_coverage_result(stdout, returncode)

    def test_selects_only_entry_points_with_active_release_journey_claims(self):
        results = [
            coverage("local", source="e2e/journey/service_runner.py"),
            coverage("secured", source="e2e/journey/service_runner.py"),
            coverage("local", source="e2e/journey/runner.py", kind="unit"),
            coverage("local", source="e2e/journey/runner.py", status="historical"),
            coverage("local", source="e2e/journey/runner.py", release=False),
        ]
        self.assertEqual(selectors_from_coverage(results), ["service-local:explore", "service-secured:explore"])

    def test_selects_the_installer_entry_point_for_a_journey_claim(self):
        self.assertEqual(
            selectors_from_coverage([coverage("local", source="e2e/journey/runner.py")]),
            ["agent:commit"],
        )

    def test_rejects_unknown_coverage_modes(self):
        with self.assertRaisesRegex(ValueError, "unsupported QA coverage mode"):
            selectors_from_coverage([coverage("nightly", source="e2e/journey/service_runner.py")])


if __name__ == "__main__":
    unittest.main()
