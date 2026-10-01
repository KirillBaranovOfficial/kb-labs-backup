"""Installed marketplace and adapter journeys for the shared service runner."""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path
from typing import Callable

from service_steps import Journey


CommandRunner = Callable[..., subprocess.CompletedProcess[str]]


def _run(command: list[str], *, cwd: Path, env: dict[str, str], runner: CommandRunner) -> subprocess.CompletedProcess[str]:
    return runner(command, cwd=str(cwd), env=env, capture_output=True, text=True, timeout=300)


def _output(result: subprocess.CompletedProcess[str], command: list[str]) -> str:
    if result.returncode != 0:
        detail = " ".join((result.stderr or result.stdout).split())
        if len(detail) > 2200:
            detail = f"{detail[:1700]} … {detail[-500:]}"
        raise RuntimeError(f"`{' '.join(command)}` exited {result.returncode}: {detail}")
    return result.stdout


def _json(result: subprocess.CompletedProcess[str], command: list[str]) -> dict:
    raw = _output(result, command)
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"`{' '.join(command)}` did not return JSON: {raw[:300]!r}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"`{' '.join(command)}` returned a non-object JSON value")
    return value


def _registry_version(package_id: str, registry: str, env: dict[str, str], runner: CommandRunner) -> str:
    command = ["npm", "view", package_id, "version", "--json", f"--registry={registry}"]
    result = runner(command, cwd=env["KB_JOURNEY_PROJECT_ROOT"], env=env,
                    capture_output=True, text=True, timeout=120)
    raw = _output(result, command)
    try:
        version = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"`{' '.join(command)}` returned invalid version JSON: {raw[:300]!r}") from error
    if not isinstance(version, str) or not version:
        raise RuntimeError(f"`{' '.join(command)}` did not resolve one published version")
    return version


def _project_config(root: Path) -> tuple[Path, dict]:
    config_file = root / ".kb" / "kb.config.json"
    try:
        value = json.loads(config_file.read_text())
    except FileNotFoundError:
        value = {}
    except json.JSONDecodeError as error:
        raise RuntimeError(f"project config is invalid JSON at {config_file}: {error}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"project config must contain a JSON object: {config_file}")
    return config_file, value


def _entries(payload: dict) -> list[dict]:
    data = payload.get("data", payload)
    entries = data.get("entries") if isinstance(data, dict) else None
    if not isinstance(entries, list):
        raise RuntimeError("`kb marketplace plugins list --json` returned no entries array")
    return entries


def authenticate_secured_marketplace(journey: Journey, project: Path, env: dict[str, str],
                                     runner: CommandRunner) -> None:
    """Prove the missing-session error, then save the admin CLI session."""
    if journey.mode != "secured":
        return

    def assert_auth_required() -> None:
        command = ["kb", "marketplace", "plugins", "list", "--scope", "platform", "--json"]
        result = _run(command, cwd=project, env=env, runner=runner)
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("unauthenticated marketplace list did not return JSON") from error
        failure = payload.get("error") if isinstance(payload, dict) else None
        if (result.returncode == 0 or not isinstance(failure, dict)
                or failure.get("code") != "KB_AUTH_REQUIRED"
                or failure.get("message") != "Authentication is required."
                or failure.get("hint") != 'Run "kb auth login".'):
            raise RuntimeError("unauthenticated marketplace list did not return KB_AUTH_REQUIRED with the login hint")

    journey.step("secured marketplace requires a saved CLI session", "marketplace",
                 assert_auth_required, cases=["S-010.6"])
    if journey.steps[-1].status != "PASS":
        return

    gateway = env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000")
    email = env.get("KB_JOURNEY_ADMIN_EMAIL", "admin@example.com")
    password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "")
    if not password:
        raise RuntimeError("secured marketplace journey needs KB_JOURNEY_ADMIN_PASSWORD")
    login = ["kb", "auth", "login", "--gateway-url", gateway,
             "--email", email, "--password", password, "--json"]
    result = _run(login, cwd=project, env=env, runner=runner)
    if result.returncode != 0:
        raise RuntimeError("secured admin CLI login failed; check the isolated journey credentials")

    status = ["kb", "auth", "status", "--json"]
    auth_status = _json(_run(status, cwd=project, env=env, runner=runner), status)
    session = auth_status.get("session")
    if not isinstance(session, dict) or session.get("authenticated") is not True:
        raise RuntimeError("secured marketplace CLI login did not save a user session")


def run_marketplace_steps(journey: Journey, project_root: str | Path, registry: str,
                          env: dict[str, str] | None = None,
                          runner: CommandRunner = subprocess.run) -> None:
    """Execute S-010 and S-047 in the isolated installed platform."""
    project = Path(project_root).resolve()
    base_env = dict(os.environ if env is None else env)
    base_env["KB_JOURNEY_PROJECT_ROOT"] = str(project)
    def cli(args: list[str], cwd: Path = project) -> subprocess.CompletedProcess[str]:
        return _run(["kb", *args], cwd=cwd, env=base_env, runner=runner)

    commit_package = "@kb-labs/commit-entry"
    unrelated_package = "@kb-labs/qa-entry"
    commit_version = _registry_version(commit_package, registry, base_env, runner)
    unrelated_version = _registry_version(unrelated_package, registry, base_env, runner)
    authenticate_secured_marketplace(journey, project, base_env, runner)
    if journey.steps and journey.steps[-1].status != "PASS":
        return
    platform_list = ["marketplace", "plugins", "list", "--scope", "platform", "--json"]
    platform_plugins = _entries(_json(cli(platform_list), platform_list))
    if any(item.get("id") == commit_package for item in platform_plugins):
        raise RuntimeError(f"{commit_package} is preinstalled at platform scope; choose a registry plugin absent from the fresh platform store")

    # S-047 requires a second project plugin to survive target uninstall.
    install_unrelated = ["marketplace", "install", f"{unrelated_package}@{unrelated_version}", "--json"]
    _output(cli(install_unrelated), install_unrelated)

    lock_file = project / ".kb" / "marketplace.lock"
    before = json.loads(lock_file.read_text()) if lock_file.exists() else {"plugins": {}}
    if commit_package in before.get("plugins", {}):
        raise RuntimeError(f"{commit_package} is already pinned in the fresh project before the install case")
    install_commit = ["marketplace", "install", f"{commit_package}@{commit_version}", "--json"]
    def install_exact_commit() -> None:
        _output(cli(install_commit), install_commit)
    journey.step(
        "install exact marketplace plugin into the project store", "marketplace",
        install_exact_commit,
        cases=["S-010.1"],
    )
    if journey.steps[-1].status != "PASS":
        return

    listed = ["marketplace", "plugins", "list", "--json"]
    def assert_marketplace_list() -> None:
        payload = _json(cli(listed), listed)
        entry = next((item for item in _entries(payload) if item.get("id") == commit_package), None)
        if not entry or entry.get("version") != commit_version or entry.get("enabled") is not True:
            raise RuntimeError(f"plugin list omitted the enabled exact pin {commit_package}@{commit_version}")
    journey.step("list installed project plugins", "marketplace", assert_marketplace_list, cases=["S-010.2"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_commit_lock() -> None:
        lock = json.loads(lock_file.read_text())
        entry = lock.get("plugins", {}).get(commit_package)
        if lock.get("schema") != "kb.marketplace/3" or not isinstance(entry, dict):
            raise RuntimeError("project marketplace lock is not schema v3 or has no commit plugin pin")
        if (entry.get("version") != commit_version or not entry.get("integrity")
                or entry.get("source") != "marketplace" or entry.get("enabled") is not True):
            raise RuntimeError(f"project lock entry does not preserve exact version, integrity, source and enabled state: {entry!r}")
    journey.step("inspect exact v3 marketplace lock entry", "marketplace", assert_commit_lock, cases=["S-010.3"])
    if journey.steps[-1].status != "PASS":
        return

    commit_open = ["commit", "open", "--json"]
    def assert_commit_command() -> None:
        payload = _json(cli(commit_open), commit_open)
        if payload.get("hasPlan") is not False:
            raise RuntimeError(f"`kb commit open --json` did not return its documented empty-plan result: {payload!r}")
    journey.step("invoke documented installed commit command", "marketplace", assert_commit_command, cases=["S-010.4"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_reinstall_idempotent() -> None:
        _output(cli(install_commit), install_commit)
        lock = json.loads(lock_file.read_text())
        if sum(1 for package_id in lock.get("plugins", {}) if package_id == commit_package) != 1:
            raise RuntimeError("reinstall created a duplicate project marketplace lock entry")
        entry = lock["plugins"][commit_package]
        if entry.get("version") != commit_version:
            raise RuntimeError("reinstall changed the requested exact plugin version")
    journey.step("repeat exact install without duplicate pin", "marketplace", assert_reinstall_idempotent, cases=["S-010.5"])
    if journey.steps[-1].status != "PASS":
        return

    uninstall_commit = ["marketplace", "uninstall", commit_package, "--json"]
    def uninstall_target() -> None:
        _output(cli(uninstall_commit), uninstall_commit)
    journey.step("uninstall target project plugin", "marketplace",
                 uninstall_target,
                 cases=["S-047.1"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_unrelated_remains() -> None:
        payload = _json(cli(listed), listed)
        entries = _entries(payload)
        if any(item.get("id") == commit_package for item in entries):
            raise RuntimeError(f"uninstalled target {commit_package} remains in the project plugin list")
        unrelated = next((item for item in entries if item.get("id") == unrelated_package), None)
        if not unrelated or unrelated.get("version") != unrelated_version or unrelated.get("enabled") is not True:
            raise RuntimeError(f"unrelated project plugin did not remain installed and enabled: {unrelated!r}")
    journey.step("list confirms target absent and unrelated plugin remains", "marketplace", assert_unrelated_remains,
                 cases=["S-047.2"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_lock_and_unrelated_plugin() -> None:
        lock = json.loads(lock_file.read_text())
        if commit_package in lock.get("plugins", {}):
            raise RuntimeError("project lock still pins the uninstalled target plugin")
        unavailable = cli(["commit", "open", "--json"])
        if unavailable.returncode == 0:
            raise RuntimeError("`kb commit open --json` still succeeds after its project plugin was uninstalled")
        unavailable_detail = " ".join((unavailable.stdout + " " + unavailable.stderr).split()).lower()
        if "not found" not in unavailable_detail and "no plugin" not in unavailable_detail:
            raise RuntimeError(f"removed commit command failed without a command-not-found result: {unavailable_detail[-400:]}")
        qa_list = ["qa", "cases", "list", "--json"]
        payload = _json(cli(qa_list), qa_list)
        if (payload.get("schema") != "kb.qa-case-list/v1"
                or not isinstance(payload.get("cases"), list)
                or not isinstance(payload.get("diagnostics"), list)):
            raise RuntimeError(f"unrelated `kb qa cases list --json` command returned an invalid case-list result: {payload!r}")
    journey.step("verify lock removal and invoke unrelated project plugin", "marketplace", assert_lock_and_unrelated_plugin,
             cases=["S-047.3"])
    if journey.steps[-1].status != "PASS":
        return
