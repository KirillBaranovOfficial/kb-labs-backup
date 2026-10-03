"""Prepare real project attachments used by the Studio browser journey."""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

from invocations import invocation_context, run_command, run_with_runner


GREET_WORKFLOW = """name: greet
version: 1.0.0
inputs:
  name:
    type: string
    required: true
on:
  manual: true
jobs:
  engineering:
    runsOn: local
    steps:
      - name: Greet the supplied name
        id: greet
        uses: builtin:shell
        with:
          command: 'echo Hello ${{ inputs.name }}'
"""


def _cli(command: list[str], *, cwd: Path, env: dict[str, str], runner) -> subprocess.CompletedProcess[str]:
    with invocation_context(step_id="journey.setup"):
        return run_with_runner(runner, command, cwd=cwd, env=env, timeout=180,
                               capture_output=True, text=True, check=False)


def _install(package: str, *, cwd: Path, env: dict[str, str], runner) -> None:
    command = ["kb", "marketplace", "install", package, "--json"]
    result = _cli(command, cwd=cwd, env=env, runner=runner)
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"marketplace install {package} did not return JSON") from error
    data = payload.get("data", {}) if isinstance(payload, dict) else {}
    installed = data.get("installed", []) if isinstance(data, dict) else []
    if (result.returncode != 0 or not isinstance(payload, dict) or payload.get("ok") is not True
            or not isinstance(installed, list)
            or not any(isinstance(item, dict) and item.get("id") == package for item in installed)):
        detail = " ".join((result.stderr or result.stdout).split())[-400:]
        raise RuntimeError(f"marketplace install {package} failed: {detail or 'plugin was not installed'}")


def _verify_greet(*, cwd: Path, env: dict[str, str], runner) -> None:
    command = ["kb", "workflow", "defs", "list", "--json"]
    result = _cli(command, cwd=cwd, env=env, runner=runner)
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("workflow defs list did not return JSON after installing greet") from error
    data = payload.get("data") if isinstance(payload, dict) else None
    if (result.returncode != 0 or not isinstance(payload, dict) or payload.get("ok") is not True
            or not isinstance(data, list)
            or not any(isinstance(item, dict) and item.get("id") == "greet"
                       and item.get("status") == "active" for item in data)):
        raise RuntimeError("workflow defs list did not report greet as active")


def _ensure_only_plugin(package: str, *, cwd: Path, env: dict[str, str], runner) -> None:
    list_command = ["kb", "marketplace", "plugins", "list", "--json"]
    listed = _cli(list_command, cwd=cwd, env=env, runner=runner)
    try:
        payload = json.loads(listed.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("marketplace plugins list did not return JSON") from error
    data = payload.get("data") if isinstance(payload, dict) else None
    entries = data.get("entries") if isinstance(data, dict) else None
    if (listed.returncode != 0 or not isinstance(payload, dict) or payload.get("ok") is not True
            or not isinstance(entries, list)
            or any(not isinstance(item, dict) or not isinstance(item.get("id"), str) for item in entries)):
        raise RuntimeError("marketplace plugins list returned no valid project entries")
    for entry in entries:
        installed_id = entry["id"]
        if installed_id == package:
            continue
        uninstall = ["kb", "marketplace", "uninstall", installed_id, "--json"]
        removed = _cli(uninstall, cwd=cwd, env=env, runner=runner)
        try:
            removal = json.loads(removed.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError(f"marketplace uninstall {installed_id} did not return JSON") from error
        if removed.returncode != 0 or not isinstance(removal, dict) or removal.get("ok") is not True:
            detail = " ".join((removed.stderr or removed.stdout).split())[-400:]
            raise RuntimeError(f"marketplace uninstall {installed_id} failed: {detail or 'unsuccessful result'}")
    verified = _cli(list_command, cwd=cwd, env=env, runner=runner)
    try:
        final_payload = json.loads(verified.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("marketplace plugins list did not return JSON after attachment setup") from error
    final_data = final_payload.get("data") if isinstance(final_payload, dict) else None
    final_entries = final_data.get("entries") if isinstance(final_data, dict) else None
    if (verified.returncode != 0 or not isinstance(final_payload, dict)
            or final_payload.get("ok") is not True or not isinstance(final_entries, list)
            or [item.get("id") for item in final_entries if isinstance(item, dict)] != [package]):
        raise RuntimeError(f"project plugin attachments do not contain only {package}")


def prepare_studio_fixtures(service_env: dict[str, str], *, project_a: Path, project_b: Path,
                            runner=run_command) -> dict[str, str]:
    """Install project plugins and a real active greet workflow via the installed CLI."""
    project_b_id = service_env.get("KB_JOURNEY_PROJECT_B_ID", "")
    if not project_b_id:
        raise RuntimeError("KB_JOURNEY_PROJECT_B_ID is required for Studio project fixtures")
    if not project_a.is_dir() or not project_b.is_dir() or project_a.resolve() == project_b.resolve():
        raise RuntimeError("Studio fixtures require two distinct registered project roots")

    env = dict(os.environ)
    env.update(service_env)
    _install("@kb-labs/workflow-entry", cwd=project_a, env=env, runner=runner)
    _install("@kb-labs/commit-entry", cwd=project_b, env=env, runner=runner)
    _ensure_only_plugin("@kb-labs/workflow-entry", cwd=project_a, env=env, runner=runner)
    _ensure_only_plugin("@kb-labs/commit-entry", cwd=project_b, env=env, runner=runner)
    workflow_dir = project_a / ".kb" / "workflows"
    workflow_dir.mkdir(parents=True, exist_ok=True)
    (workflow_dir / "greet.yml").write_text(GREET_WORKFLOW, encoding="utf-8")
    _verify_greet(cwd=project_a, env=env, runner=runner)

    return {
        "KB_STUDIO_GREET_WORKFLOW_ID": "greet",
    }
