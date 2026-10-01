from __future__ import annotations

import json
import os
import subprocess
import tempfile
from pathlib import Path

from service_steps import Journey


def run_workspace_cli_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"
    with tempfile.TemporaryDirectory(prefix="kb-workspace-cli-home-") as home:
        env = {**os.environ, "HOME": home}
        env.pop("KB_PLATFORM_ROOT", None)
        env.pop("KB_HOME", None)
        env.pop("KB_NET_OFFSET", None)

        def run(*args: str) -> subprocess.CompletedProcess[str]:
            return subprocess.run(
                ["node", str(cli), *args], cwd=repo_root, env=env,
                capture_output=True, text=True, check=False, timeout=120,
            )

        def validate_cases() -> str | None:
            result = run("qa", "cases", "validate", "--json")
            if result.returncode != 0:
                return f"workspace QA case validation failed (exit {result.returncode}): {result.stderr or result.stdout}"
            return None

        journey.step("validate QA cases with the workspace CLI", "workspace-cli", validate_cases, cases=["S-179.2"])

        def list_plugin_commands() -> str | None:
            result = run("--help")
            if result.returncode != 0:
                return f"workspace CLI help failed (exit {result.returncode}): {result.stderr or result.stdout}"
            expected_groups = ("qa", "marketplace", "workflow", "commit", "release", "review", "mind", "agent")
            missing = [group for group in expected_groups if group not in result.stdout]
            if missing:
                return f"workspace CLI help is missing plugin groups: {', '.join(missing)}"
            return None

        journey.step("list workspace plugin command groups", "workspace-cli", list_plugin_commands, cases=["S-179.3"])

        def verify_synced_workspace_lock() -> str | None:
            try:
                lock = json.loads((repo_root / ".kb/marketplace.lock").read_text())
            except (OSError, json.JSONDecodeError) as error:
                return f"workspace marketplace lock could not be read: {error}"
            expected_plugins = ("@kb-labs/release", "@kb-labs/review", "@kb-labs/mind", "@kb-labs/agents")
            missing_plugins = [plugin for plugin in expected_plugins if plugin not in lock.get("plugins", {})]
            adapters = lock.get("adapters", {})
            if lock.get("schema") != "kb.marketplace/3":
                return "workspace marketplace lock does not use schema kb.marketplace/3"
            if missing_plugins:
                return f"workspace marketplace lock is missing plugin entries: {', '.join(missing_plugins)}"
            if not adapters:
                return "workspace marketplace lock has no machine-level adapter entries"
            return None

        journey.step("verify synced workspace plugins and adapters in the v3 lock", "workspace-cli", verify_synced_workspace_lock, cases=["S-179.4"])
