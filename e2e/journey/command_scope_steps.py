from __future__ import annotations

import json
import os
import tempfile
from pathlib import Path

from invocations import run_command
from service_steps import Journey


def run_command_scope_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"

    def platform_project_commands_work_outside_registry() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-command-scope-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-command-scope-cwd-") as cwd, \
                tempfile.TemporaryDirectory(prefix="kb-command-scope-project-") as project:
            env = {**os.environ, "HOME": home, "KB_HOME": str(Path(home) / ".kb")}
            env.pop("KB_PLATFORM_ROOT", None)
            env.pop("KB_NET_OFFSET", None)

            def invoke(args: list[str]) -> tuple[int, dict]:
                result = run_command(
                    ["node", str(cli), *args],
                    cwd=cwd,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                    timeout=120,
                )
                try:
                    return result.returncode, json.loads(result.stdout)
                except json.JSONDecodeError:
                    raise AssertionError(f"{args!r} did not return JSON: {result.stderr or result.stdout}")

            code, added = invoke(["project", "add", project, "--json"])
            if code != 0 or added.get("ok") is not True:
                return f"platform-scoped project add failed outside registry: {added}"
            project_id = added.get("project", {}).get("id")
            if not project_id:
                return f"project add did not return an ID: {added}"

            code, listed = invoke(["project", "list", "--json"])
            if code != 0 or listed.get("ok") is not True:
                return f"platform-scoped project list failed outside registry: {listed}"
            if not any(row.get("id") == project_id for row in listed.get("projects", [])):
                return f"project list omitted the registered project: {listed}"

            code, shown = invoke(["project", "show", project_id, "--json"])
            if code != 0 or shown.get("ok") is not True:
                return f"platform-scoped project show failed outside registry: {shown}"
            if shown.get("project", {}).get("id") != project_id:
                return f"project show resolved the wrong explicit reference: {shown}"

            code, unknown = invoke(["config", "show", "--json"])
            error = unknown.get("error", {})
            if code == 0 or unknown.get("ok") is not False or error.get("code") != "KB_PROJECT_UNKNOWN":
                return f"project-scoped command did not return KB_PROJECT_UNKNOWN: {unknown}"
            hint = error.get("hint", "")
            if "kb project add" not in hint:
                return f"project-unknown hint lacks next action: {error}"
            return None

    journey.step(
        "resolve declared command scopes outside registered projects",
        "project",
        platform_project_commands_work_outside_registry,
        cases=["S-124.10"],
    )

    def global_help_and_version_do_not_initialize_a_project() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-global-flags-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-global-flags-cwd-") as cwd:
            env = {**os.environ, "HOME": home, "KB_HOME": str(Path(home) / ".kb")}
            env.pop("KB_PLATFORM_ROOT", None)
            env.pop("KB_NET_OFFSET", None)
            for args in (["--help"], ["--version"]):
                result = run_command(
                    ["node", str(cli), *args],
                    cwd=cwd,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                    timeout=120,
                )
                if result.returncode != 0:
                    return f"{args!r} failed outside project registry: {result.stderr or result.stdout}"
                if not result.stdout.strip():
                    return f"{args!r} produced no output"
                if (Path(cwd) / ".kb").exists():
                    return f"{args!r} created .kb in an unregistered directory"
            return None

    journey.step(
        "run global help and version outside registered projects",
        "cli",
        global_help_and_version_do_not_initialize_a_project,
        cases=["S-054.17", "PC-002.6"],
    )
