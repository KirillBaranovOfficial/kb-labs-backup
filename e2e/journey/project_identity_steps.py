from __future__ import annotations

import json
import os
import tempfile
from pathlib import Path

from service_steps import Journey
from invocations import run_command


def run_project_identity_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"

    def project_show_outside_registry() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-project-identity-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-project-identity-cwd-") as cwd:
            env = {**os.environ, "HOME": home}
            env.pop("KB_HOME", None)
            env["KB_PLATFORM_ROOT"] = str(repo_root)
            env.pop("KB_NET_OFFSET", None)
            result = run_command(
                ["node", str(cli), "project", "show", "--json"],
                cwd=cwd,
                env=env,
                capture_output=True,
                text=True,
                check=False,
                timeout=120,
            )
            try:
                payload = json.loads(result.stdout)
            except json.JSONDecodeError:
                return f"project show did not return JSON: {result.stderr or result.stdout}"
            error = payload.get("error", {})
            if result.returncode == 0 or error.get("code") != "KB_PROJECT_UNKNOWN":
                return f"unregistered cwd returned unexpected result: {payload}"
            if "kb project add" not in error.get("hint", ""):
                return f"unknown project hint did not explain registration: {error}"
            if (Path(cwd) / ".kb").exists():
                return "project show created a .kb directory in the unregistered repository"
            return None

    journey.step(
        "reject project identity outside the registry",
        "project",
        project_show_outside_registry,
        cases=["S-204.7"],
    )
