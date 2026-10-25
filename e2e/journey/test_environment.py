from __future__ import annotations

import os
import shutil
import subprocess
import unittest
from unittest.mock import patch
from pathlib import Path


def isolated_environment(home: Path, fixture_bin: Path, *, required_tools=(), overrides=None) -> dict[str, str]:
    """Build child env from explicit tools and fixtures, never the caller env."""
    tool_bin = home / ".journey-test-path"
    tool_bin.mkdir(parents=True, exist_ok=True)
    for name in required_tools:
        resolved = shutil.which(name)
        if resolved is None:
            raise FileNotFoundError(f"required journey test tool is unavailable: {name}")
        source = Path(resolved).resolve()
        target = tool_bin / name
        if not target.exists():
            target.symlink_to(source)
    env = {
        "HOME": str(home),
        "PATH": os.pathsep.join((str(fixture_bin), str(tool_bin))),
        "LANG": "C.UTF-8",
        "TERM": "xterm-256color",
    }
    env.update(overrides or {})
    return env


def require_compose_command(test_case) -> list[str]:
    """Return a supported Compose command or skip with its missing precondition."""
    docker = shutil.which("docker")
    if docker is not None and subprocess.run(
            [docker, "compose", "version"], capture_output=True, check=False).returncode == 0:
        return ["docker", "compose"]
    if shutil.which("docker-compose") is not None:
        return ["docker-compose"]
    test_case.skipTest("Docker Compose is unavailable; install the Docker Compose v2 plugin or docker-compose")


class ComposeCommandTests(unittest.TestCase):
    def test_compose_v2_is_resolved_as_a_docker_subcommand(self):
        with patch("test_environment.shutil.which", return_value="/usr/bin/docker"), \
                patch("test_environment.subprocess.run") as run:
            run.return_value.returncode = 0
            self.assertEqual(require_compose_command(self), ["docker", "compose"])
            run.assert_called_once_with(["/usr/bin/docker", "compose", "version"],
                                        capture_output=True, check=False)

    def test_standalone_compose_is_used_when_v2_plugin_is_unavailable(self):
        with patch("test_environment.shutil.which", side_effect=("/usr/bin/docker", "/usr/bin/docker-compose")), \
                patch("test_environment.subprocess.run") as run:
            run.return_value.returncode = 1
            self.assertEqual(require_compose_command(self), ["docker-compose"])

    def test_missing_compose_is_a_named_skip_precondition(self):
        with patch("test_environment.shutil.which", return_value=None):
            with self.assertRaises(unittest.SkipTest) as skipped:
                require_compose_command(self)
        self.assertEqual(str(skipped.exception),
                         "Docker Compose is unavailable; install the Docker Compose v2 plugin or docker-compose")


def stage_cli_root(repository_root: Path, target_root: Path) -> Path:
    """Stage the built CLI under an isolated installed root without workspace config."""
    shutil.copytree(repository_root / "cli" / "bin" / "dist", target_root / "cli" / "bin" / "dist")
    package_modules = target_root / "cli" / "bin" / "node_modules"
    package_modules.symlink_to(repository_root / "cli" / "bin" / "node_modules", target_is_directory=True)
    (target_root / "node_modules").symlink_to(repository_root / "node_modules", target_is_directory=True)
    return target_root
