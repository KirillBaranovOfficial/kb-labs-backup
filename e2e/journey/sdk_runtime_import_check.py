from __future__ import annotations

from pathlib import Path

from invocations import run_command


def check_installed_runtime_imports(platform_root: Path):
    checker = Path(__file__).with_name("sdk_runtime_import_check.mjs")
    return run_command(["node", str(checker), str(platform_root)], cwd=platform_root, timeout=180)
