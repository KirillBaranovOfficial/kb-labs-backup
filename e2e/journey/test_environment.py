from __future__ import annotations

import os
import shutil
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


def stage_cli_root(repository_root: Path, target_root: Path) -> Path:
    """Stage the built CLI under an isolated installed root without workspace config."""
    shutil.copytree(repository_root / "cli" / "bin" / "dist", target_root / "cli" / "bin" / "dist")
    package_modules = target_root / "cli" / "bin" / "node_modules"
    package_modules.symlink_to(repository_root / "cli" / "bin" / "node_modules", target_is_directory=True)
    (target_root / "node_modules").symlink_to(repository_root / "node_modules", target_is_directory=True)
    return target_root
