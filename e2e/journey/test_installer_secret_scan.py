from __future__ import annotations

import tempfile
import tracemalloc
import unittest
from pathlib import Path

from installer_case_steps import _fixture_contains_secret_material


class InstallerSecretScanTests(unittest.TestCase):
    def test_fixture_secret_scan_uses_bounded_memory_and_finds_markers(self):
        with tempfile.TemporaryDirectory(prefix="journey-secret-scan-") as directory:
            root = Path(directory)
            fixture = root / "large.log"
            with fixture.open("w", encoding="utf-8") as output:
                for _ in range(256):
                    output.write("x" * 65536 + "\n")

            tracemalloc.start()
            self.assertFalse(_fixture_contains_secret_material(root))
            _, peak = tracemalloc.get_traced_memory()
            tracemalloc.stop()
            self.assertLess(peak, 2 * 1024 * 1024)

            fixture.write_text("before\nfixture-secret-value\nafter\n", encoding="utf-8")
            self.assertTrue(_fixture_contains_secret_material(root))


if __name__ == "__main__":
    unittest.main()
