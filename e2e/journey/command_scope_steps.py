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

    def platform_commands_work_without_project_or_config() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-platform-command-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-platform-command-cwd-") as cwd:
            env = {**os.environ, "HOME": home, "KB_HOME": str(Path(home) / ".kb")}
            env.pop("KB_PLATFORM_ROOT", None)
            env.pop("KB_NET_OFFSET", None)
            for args in (["hello", "--json"], ["logs", "query", "--json"]):
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
                    return f"platform command {args!r} failed outside a registered project: {result.stderr or result.stdout}"
                try:
                    payload = json.loads(result.stdout)
                except json.JSONDecodeError:
                    return f"platform command {args!r} did not return JSON: {result.stderr or result.stdout}"
                if args[0] == "hello":
                    if payload.get("message") != "Hello World" or payload.get("status") != "ready":
                        return f"platform command {args!r} returned unexpected greeting: {payload}"
                elif payload.get("ok") is not True:
                    return f"platform command {args!r} returned failure: {payload}"
            if (Path(cwd) / ".kb").exists():
                return "platform commands created project state in the unregistered cwd"
            return None

    journey.step(
        "run greeting and platform log query without project configuration",
        "cli",
        platform_commands_work_without_project_or_config,
        cases=["PC-002.7", "S-203.8"],
    )


def run_config_failure_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"

    def invalid_project_configs_fail_before_handler() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-config-failure-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-config-failure-cwd-") as cwd, \
                tempfile.TemporaryDirectory(prefix="kb-config-failure-projects-") as projects:
            env = {**os.environ, "HOME": home, "KB_HOME": str(Path(home) / ".kb")}
            env.pop("KB_PLATFORM_ROOT", None)
            env.pop("KB_NET_OFFSET", None)

            def invoke(args: list[str], command_cwd: Path = cwd) -> tuple[int, dict]:
                result = run_command(
                    ["node", str(cli), *args], cwd=command_cwd, env=env,
                    capture_output=True, text=True, check=False, timeout=120,
                )
                try:
                    return result.returncode, json.loads(result.stdout)
                except json.JSONDecodeError:
                    raise AssertionError(f"{args!r} did not return JSON: {result.stderr or result.stdout}")

            project = Path(projects) / "malformed"
            project.mkdir()
            (project / ".kb").mkdir()
            code, added = invoke(["project", "add", str(project), "--json"])
            if code != 0 or added.get("ok") is not True:
                return f"could not register config fixture: {added}"
            malformed = project / ".kb/kb.config.json"
            malformed.write_text('{ "platform": ', encoding="utf-8")
            code, result = invoke(["config", "show", "--json"], project)
            error = result.get("error", {})
            if code != 1 or result.get("ok") is not False or error.get("code") != "KB_CONFIG_INVALID":
                return f"malformed config did not fail with KB_CONFIG_INVALID: {result}"
            if str(malformed) not in error.get("message", "") or not error.get("hint"):
                return f"malformed config error omitted its path or repair hint: {error}"

            unreadable_project = Path(projects) / "unreadable"
            unreadable_project.mkdir()
            (unreadable_project / ".kb").mkdir()
            code, added = invoke(["project", "add", str(unreadable_project), "--json"])
            if code != 0 or added.get("ok") is not True:
                return f"could not register unreadable-config fixture: {added}"
            unreadable = unreadable_project / ".kb/kb.config.json"
            unreadable.mkdir()
            code, result = invoke(["config", "show", "--json"], unreadable_project)
            error = result.get("error", {})
            if code != 1 or result.get("ok") is not False or error.get("code") != "KB_CONFIG_INVALID":
                return f"unreadable config did not fail with KB_CONFIG_INVALID: {result}"
            if str(unreadable) not in error.get("message", "") or not error.get("hint"):
                return f"unreadable config error omitted its path or repair hint: {error}"
            return None

    journey.step(
        "report malformed and unreadable config before command execution",
        "cli",
        invalid_project_configs_fail_before_handler,
        cases=["S-203.7"],
    )
