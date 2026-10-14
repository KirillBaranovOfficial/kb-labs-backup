from __future__ import annotations

import os
import shlex
import subprocess
import sys
import unittest
from pathlib import Path


JOURNEY_DIR = Path(__file__).resolve().parent
REPOSITORY_ROOT = JOURNEY_DIR.parents[1]
RUNNER_DOCKERFILE = JOURNEY_DIR / "Dockerfile.runner"


def _module_name(source: Path) -> str:
    relative = source.relative_to(JOURNEY_DIR).with_suffix("")
    parts = list(relative.parts)
    if parts[-1] == "__init__":
        parts.pop()
    return ".".join(parts)


def _docker_installed_runner_modules() -> set[str]:
    modules: set[str] = set()
    for line in RUNNER_DOCKERFILE.read_text(encoding="utf-8").splitlines():
        if not line.lstrip().upper().startswith("COPY "):
            continue
        instruction = shlex.split(line, comments=True)
        sources, destination = instruction[1:-1], instruction[-1]
        if not destination.startswith("/usr/local/bin/"):
            continue
        for source in sources:
            source_path = (REPOSITORY_ROOT / source).resolve()
            if source_path.suffix == ".py" and source_path.is_relative_to(JOURNEY_DIR):
                modules.add(_module_name(source_path))
    return modules


def _journey_modules() -> list[str]:
    modules = {
        _module_name(source)
        for source in JOURNEY_DIR.rglob("*.py")
        if "__pycache__" not in source.parts
    }
    modules.update(_docker_installed_runner_modules())
    return sorted(module for module in modules if module)


class JourneyImportOrderTests(unittest.TestCase):
    def test_every_journey_module_imports_first_in_a_fresh_interpreter(self):
        env = os.environ.copy()
        env["PYTHONPATH"] = str(JOURNEY_DIR)
        for module in _journey_modules():
            with self.subTest(module=module):
                result = subprocess.run(
                    [sys.executable, "-B", "-c", f"import {module}"],
                    cwd=JOURNEY_DIR,
                    env=env,
                    text=True,
                    capture_output=True,
                    check=False,
                )
                self.assertEqual(
                    result.returncode,
                    0,
                    f"first import of journey module {module!r} failed\n"
                    f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}",
                )


if __name__ == "__main__":
    unittest.main()
