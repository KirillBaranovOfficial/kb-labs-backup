from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from test_docker_build_inputs import copy_instructions


JOURNEY_DIR = Path(__file__).resolve().parent
REPOSITORY_ROOT = JOURNEY_DIR.parents[1]
RUNNER_DOCKERFILE = JOURNEY_DIR / "Dockerfile.runner"


def _docker_copy_resources() -> list[tuple[str, str]]:
    return copy_instructions(RUNNER_DOCKERFILE, final_stage_only=True)


def _docker_installed_runner_modules(resources: list[tuple[str, str]], journey_root: Path) -> set[str]:
    modules: set[str] = set()
    for source, destination in resources:
        if not destination.startswith("/usr/local/bin/"):
            continue
        source_path = Path(source)
        if source_path.suffix == ".py" and source_path.is_relative_to(Path("e2e/journey")):
            modules.add(_module_name(journey_root / source_path.relative_to("e2e/journey"), journey_root))
    return modules


def _module_name(source: Path, journey_root: Path = JOURNEY_DIR) -> str:
    relative = source.relative_to(journey_root).with_suffix("")
    parts = list(relative.parts)
    if parts[-1] == "__init__":
        parts.pop()
    return ".".join(parts)


def _journey_modules(journey_root: Path = JOURNEY_DIR,
                     resources: list[tuple[str, str]] | None = None) -> list[str]:
    modules = {
        _module_name(source, journey_root)
        for source in journey_root.rglob("*.py")
        if "__pycache__" not in source.parts
    }
    modules.update(_docker_installed_runner_modules(resources or _docker_copy_resources(), journey_root))
    return sorted(module for module in modules if module)


class JourneyImportOrderTests(unittest.TestCase):
    def test_runner_inputs_are_materialized_by_the_runner_stage(self):
        staged = subprocess.run(
            ["bash", str(JOURNEY_DIR / "stage-runner-inputs.sh")],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(
            staged.returncode,
            0,
            f"runner input stage failed\nstdout:\n{staged.stdout}\nstderr:\n{staged.stderr}",
        )

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

    def test_dockerfile_copies_match_the_declared_runner_resources(self):
        from journey_resources import RUNNER_IMAGE_RESOURCES, workspace_resource

        # The declared runtime image inputs are the inventory; other workspace
        # tools can be build-time-only and do not belong in this container.
        declared = {(resource.source, resource.destination) for resource in RUNNER_IMAGE_RESOURCES}
        copied = set(_docker_copy_resources())
        self.assertEqual(
            copied,
            declared,
            f"Dockerfile.runner COPY resources differ from journey_resources.py; "
            f"undeclared={sorted(copied - declared)}, not_copied={sorted(declared - copied)}",
        )

    def test_copied_runner_tree_resolves_resources_and_imports_its_modules(self):
        staged = subprocess.run(
            ["bash", str(JOURNEY_DIR / "stage-runner-inputs.sh")],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(staged.returncode, 0, staged.stderr or staged.stdout)
        resources = _docker_copy_resources()
        with tempfile.TemporaryDirectory(prefix="journey-runner-image-") as temporary:
            image_root = Path(temporary)
            for source, destination in resources:
                source_path = REPOSITORY_ROOT / source
                target_path = image_root / destination.lstrip("/")
                if source_path.is_dir():
                    shutil.copytree(source_path, target_path, dirs_exist_ok=True)
                else:
                    target_path.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(source_path, target_path)

            journey_root = image_root / "opt/kb-journey/e2e/journey"
            env = os.environ.copy()
            env["PYTHONPATH"] = str(journey_root)
            probe = "\n".join((
                "import sys",
                "from pathlib import Path",
                "from pathlib import PurePosixPath",
                "from journey_resources import RUNNER_IMAGE_RESOURCES, workspace_resource",
                "image_root = Path(sys.argv[1])",
                "for resource in RUNNER_IMAGE_RESOURCES:",
                "    if resource.destination.startswith('/opt/kb-journey/'):",
                "        relative = resource.destination.removeprefix('/opt/kb-journey/')",
                "        path = workspace_resource(*PurePosixPath(relative).parts)",
                "    else:",
                "        path = image_root / resource.destination.lstrip('/')",
                "    if not path.exists():",
                "        raise SystemExit(f'missing runner image resource {resource.source} at {resource.destination}')",
            ))
            result = subprocess.run(
                [sys.executable, "-B", "-c", probe, str(image_root)], cwd=journey_root,
                env=env, text=True, capture_output=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr or result.stdout)

            for module in _journey_modules(journey_root, resources):
                with self.subTest(module=module):
                    imported = subprocess.run(
                        [sys.executable, "-B", "-c", f"import {module}"], cwd=journey_root,
                        env=env, text=True, capture_output=True, check=False,
                    )
                    self.assertEqual(
                        imported.returncode,
                        0,
                        f"first import in the Docker COPY tree failed for {module!r}\n"
                        f"stdout:\n{imported.stdout}\nstderr:\n{imported.stderr}",
                    )


if __name__ == "__main__":
    unittest.main()
