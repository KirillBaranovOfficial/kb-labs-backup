from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from journey_resources import stage_studio_playwright_specs


JOURNEY_DIR = Path(__file__).resolve().parent
REPOSITORY_ROOT = JOURNEY_DIR.parents[1]


class StudioSpecBundleTests(unittest.TestCase):
    def test_workspace_imports_load_from_the_reduced_studio_image_tree(self):
        with tempfile.TemporaryDirectory(prefix="studio-spec-image-") as temporary:
            image_root = Path(temporary)
            specs_dir = image_root / "specs"
            staged_specs = stage_studio_playwright_specs(specs_dir)

            playwright_package = (REPOSITORY_ROOT / "e2e/studio/node_modules/@playwright/test").resolve()
            module_dir = image_root / "node_modules/@playwright"
            module_dir.mkdir(parents=True)
            (module_dir / "test").symlink_to(playwright_package, target_is_directory=True)
            config = image_root / "playwright.config.mjs"
            config.write_text(
                "export default { testDir: " + repr(str(specs_dir))
                + ", testMatch: ['**/*.spec.js', '**/*.spec.ts'] }\n",
                encoding="utf-8",
            )

            playwright = REPOSITORY_ROOT / "e2e/studio/node_modules/.bin/playwright"
            result = subprocess.run(
                [str(playwright), "test", "--list", f"--config={config}"],
                cwd=image_root,
                env={**os.environ, "HOME": temporary},
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(
                result.returncode,
                0,
                f"Playwright could not load the bundled spec tree\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}",
            )
            self.assertIn("Total:", result.stdout)
            self.assertGreater(len(list(specs_dir.rglob("*.spec.js"))), 0)
            self.assertEqual(staged_specs, tuple(sorted(specs_dir.rglob("*.spec.js"))))
            self.assertEqual(sorted(path.name for path in (image_root / "node_modules").rglob("*")),
                             ["@playwright", "test"])


if __name__ == "__main__":
    unittest.main()
