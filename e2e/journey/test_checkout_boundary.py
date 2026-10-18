from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

from test_environment import isolated_environment


ROOT = Path(__file__).resolve().parents[2]


def is_outside_checkout_workspace(path: str | bytes, checkout: Path) -> bool:
    candidate = Path(path.decode() if isinstance(path, bytes) else path).resolve()
    workspace = checkout.parent.resolve()
    try:
        candidate.relative_to(workspace)
    except ValueError:
        return False
    try:
        candidate.relative_to(checkout.resolve())
    except ValueError:
        return True
    return False


def _guard_checkout_boundary(event, args):
    if event == "open" and args and isinstance(args[0], (str, bytes)):
        if is_outside_checkout_workspace(args[0], ROOT):
            raise PermissionError(f"journey unit tests may not read outside checkout: {args[0]!r}")


sys.addaudithook(_guard_checkout_boundary)


class CheckoutBoundaryTests(unittest.TestCase):
    def test_workspace_sibling_is_outside_checkout(self):
        sibling = ROOT.parent / "reports" / "x53-cutover" / "journey-batch.sh"
        self.assertTrue(is_outside_checkout_workspace(str(sibling), ROOT))

    def test_checkout_and_system_temp_are_not_workspace_siblings(self):
        self.assertFalse(is_outside_checkout_workspace(str(ROOT / "e2e/journey/batch.sh"), ROOT))
        self.assertFalse(is_outside_checkout_workspace("/tmp/journey-test-fixture", ROOT))

    def test_opening_a_workspace_sibling_is_rejected_by_the_audit_rail(self):
        sibling = ROOT.parent / "sibling-fixture" / "data.json"
        with self.assertRaisesRegex(PermissionError, "may not read outside checkout"):
            sibling.open("rb")

    def test_subprocess_environment_contains_only_declared_inputs(self):
        with tempfile.TemporaryDirectory(prefix="journey-env-") as temporary:
            home = Path(temporary) / "home"
            fixture_bin = Path(temporary) / "bin"
            fixture_bin.mkdir()
            env = isolated_environment(home, fixture_bin)
        self.assertEqual(set(env), {"HOME", "PATH", "LANG", "TERM"})
        self.assertNotIn("CI", env)
        self.assertNotIn("KB_JOURNEY_CI", env)
        self.assertNotIn("BATCH_BASELINE", env)


if __name__ == "__main__":
    unittest.main()
