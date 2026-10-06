"""Installed marketplace and adapter journeys for the shared service runner."""
from __future__ import annotations

import json
import hashlib
import os
import subprocess
import time
import urllib.request
from pathlib import Path
from typing import Callable

from service_steps import Journey
from invocations import invocation_context, open_http, run_command, run_with_runner
from plugin_author_steps import run_commit_case_steps


CommandRunner = Callable[..., subprocess.CompletedProcess[str]]


def _run(command: list[str], *, cwd: Path, env: dict[str, str], runner: CommandRunner) -> subprocess.CompletedProcess[str]:
    return run_with_runner(runner, command, cwd=cwd, env=env, timeout=300)


def _output(result: subprocess.CompletedProcess[str], command: list[str]) -> str:
    if result.returncode != 0:
        detail = " ".join(f"{result.stdout or ''} {result.stderr or ''}".split())
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


def validate_commit_status_response(status: int, payload: object) -> None:
    data = payload.get("data") if isinstance(payload, dict) else None
    if status != 200 or not isinstance(data, dict) or data.get("scope") != "project":
        raise RuntimeError(f"Commit status route returned an unexpected response: HTTP {status} {payload!r}")


def validate_unmounted_commit_response(status: int, payload: object) -> None:
    failure = payload.get("error") if isinstance(payload, dict) else None
    if (status != 404 or not isinstance(payload, dict) or payload.get("ok") is not False
            or not isinstance(failure, dict)
            or failure.get("code") != "KB_PLUGIN_NOT_INSTALLED"
            or failure.get("message") != "Plugin is not installed in this project."
            or failure.get("hint") != "Run `kb marketplace install @kb-labs/commit-entry` in this project."
            or not isinstance(failure.get("correlationId"), str) or not failure["correlationId"]):
        raise RuntimeError(f"unmounted Commit route returned an unexpected gateway failure: HTTP {status} {payload!r}")


def _registry_version(package_id: str, registry: str, env: dict[str, str], runner: CommandRunner) -> str:
    command = ["npm", "view", package_id, "version", "--json", f"--registry={registry}"]
    result = run_with_runner(runner, command, cwd=env["KB_JOURNEY_PROJECT_ROOT"], env=env,
                             timeout=120)
    raw = _output(result, command)
    try:
        version = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"`{' '.join(command)}` returned invalid version JSON: {raw[:300]!r}") from error
    if not isinstance(version, str) or not version:
        raise RuntimeError(f"`{' '.join(command)}` did not resolve one published version")
    return version


def wait_for_marketplace_ready(project: Path, env: dict[str, str], runner: CommandRunner,
                               *, timeout: float = 60) -> None:
    """Wait for the installed product's marketplace health check before registry reads."""
    command = ["kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"]
    deadline = time.monotonic() + timeout
    last_problem = "health check did not return"
    while time.monotonic() < deadline:
        result = _run(command, cwd=project, env=env, runner=runner)
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError:
            payload = {}
        if result.returncode == 0 and isinstance(payload, dict) and payload.get("ok") is True:
            return
        last_problem = (result.stderr or result.stdout or f"exit {result.returncode}").strip()[-800:]
        time.sleep(0.5)
    raise RuntimeError(
        "marketplace did not become healthy within 60 seconds; "
        f"`{' '.join(command)}` last reported: {last_problem}; "
        "check the journey registry and rerun `e2e/journey/run.sh --no-build`."
    )


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


def validate_shared_failure(result: subprocess.CompletedProcess[str], *, json_mode: bool,
                            code: str, message: str, hint: str) -> None:
    """Validate the common human or machine failure contract used by S-051."""
    if result.returncode == 0:
        raise AssertionError("failing command unexpectedly exited successfully")
    if json_mode:
        payload = json.loads(result.stdout)
        if payload != {"ok": False, "error": payload.get("error")}:
            raise AssertionError("JSON result does not use the shared {ok:false,error} shape")
        error = payload.get("error")
        if not isinstance(error, dict) or (error.get("code"), error.get("message"), error.get("hint")) != (code, message, hint):
            raise AssertionError(f"JSON failure does not match {code}")
        return
    output = " ".join(f"{result.stdout or ''} {result.stderr or ''}".split())
    if f"{message} [{code}]" not in output or "Hint:" not in output or hint not in output:
        raise AssertionError(f"human failure does not match {code}")
    if "Traceback" in output or output.lstrip().startswith("{"):
        raise AssertionError("human failure exposed a stack trace or raw JSON")


def _platform_snapshot(platform_root: Path) -> tuple[str, tuple[tuple[str, str], ...]]:
    """Capture the sealed receipt and every installed package version."""
    receipt = platform_root / ".kb" / "v2" / "receipt.json"
    receipt_hash = hashlib.sha256(receipt.read_bytes()).hexdigest()
    packages = []
    for package_file in sorted((platform_root / "node_modules").rglob("package.json")):
        try:
            package = json.loads(package_file.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        name = package.get("name")
        version = package.get("version")
        if isinstance(name, str) and isinstance(version, str):
            packages.append((str(package_file.relative_to(platform_root / "node_modules")), f"{name}@{version}"))
    return receipt_hash, tuple(packages)


def authenticate_secured_marketplace(journey: Journey, project: Path, env: dict[str, str],
                                     runner: CommandRunner) -> None:
    """Prove the missing-session error, then save the admin CLI session."""
    if journey.mode != "secured":
        return

    def assert_auth_required(step_id: str) -> None:
        command = ["kb", "marketplace", "plugins", "list", "--scope", "platform", "--json"]
        with invocation_context(step_id=step_id):
            result = _run(command, cwd=project, env=env, runner=runner)
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("unauthenticated marketplace list did not return JSON") from error
        failure = payload.get("error") if isinstance(payload, dict) else None
        if (result.returncode == 0 or not isinstance(payload, dict)
                or set(payload) != {"ok", "error"} or payload.get("ok") is not False
                or not isinstance(failure, dict)
                or failure.get("code") != "KB_AUTH_REQUIRED"
                or failure.get("message") != "Authentication is required."
                or failure.get("hint") != f'Run "kb auth login --gateway-url {env.get("KB_JOURNEY_BASE_URL", "<url>")} --email <email> --password <password>".'):
            raise RuntimeError("unauthenticated marketplace list did not return KB_AUTH_REQUIRED with the login hint")

    journey.step("secured marketplace requires a saved CLI session", "marketplace",
                 lambda: assert_auth_required("S-041.7"), cases=["S-041.7"])
    if journey.steps[-1].status != "PASS":
        return

    journey.step("S-010.6 secured marketplace requires the catalogued login error", "marketplace",
                 lambda: assert_auth_required("S-010.6"), cases=["S-010.6"])
    if journey.steps[-1].status != "PASS":
        return

    gateway = env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000")
    email = env.get("KB_JOURNEY_ADMIN_EMAIL", "admin@example.com")
    password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "")
    if not password:
        raise RuntimeError("secured marketplace journey needs KB_JOURNEY_ADMIN_PASSWORD")
    login = ["kb", "auth", "login", "--gateway-url", gateway,
             "--email", email, "--password", password, "--json"]
    with invocation_context(step_id="S-041.12"):
        result = _run(login, cwd=project, env=env, runner=runner)
    if result.returncode != 0:
        raise RuntimeError("secured admin CLI login failed; check the isolated journey credentials")

    status = ["kb", "auth", "status", "--json"]
    with invocation_context(step_id="S-041.12"):
        auth_status = _json(_run(status, cwd=project, env=env, runner=runner), status)
    session = auth_status.get("session")
    if not isinstance(session, dict) or session.get("authenticated") is not True:
        raise RuntimeError("secured marketplace CLI login did not save a user session")

    command = ["kb", "marketplace", "plugins", "list", "--scope", "platform", "--json"]
    with invocation_context(step_id="S-041.12"):
        resumed = _run(command, cwd=project, env=env, runner=runner)
    try:
        payload = json.loads(resumed.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("authenticated marketplace list did not return JSON") from error
    failure = payload.get("error") if isinstance(payload, dict) else None
    if isinstance(failure, dict) and failure.get("code") == "KB_AUTH_REQUIRED":
        raise RuntimeError("authenticated marketplace list still returned KB_AUTH_REQUIRED")


def run_marketplace_steps(journey: Journey, project_root: str | Path, registry: str,
                          env: dict[str, str] | None = None,
                          runner: CommandRunner = run_command,
                          http_opener: Callable[..., object] = urllib.request.urlopen,
                          commit_case_hook: Callable[[], None] | None = None) -> None:
    """Execute S-010 and S-047 in the isolated installed platform."""
    project = Path(project_root).resolve()
    base_env = dict(os.environ if env is None else env)
    base_env["KB_JOURNEY_PROJECT_ROOT"] = str(project)
    platform_root = Path(base_env.get("KB_JOURNEY_PLATFORM_ROOT", str(Path(base_env["HOME"]) / "kb-platform"))).resolve()
    def cli(args: list[str], cwd: Path = project) -> subprocess.CompletedProcess[str]:
        return _run(["kb", *args], cwd=cwd, env=base_env, runner=runner)

    commit_package = "@kb-labs/commit-entry"
    unrelated_package = "@kb-labs/qa-entry"
    authenticate_secured_marketplace(journey, project, base_env, runner)
    if journey.steps and journey.steps[-1].status != "PASS":
        return

    def assert_shared_errors() -> None:
        failures = (
            (["marketplace", "install", "@kb-labs/not-published"], "KB_PLUGIN_NOT_FOUND",
             "Plugin @kb-labs/not-published was not found in the registry.", "kb marketplace search"),
            (["adapter", "bind", "storage", "@kb-labs/adapters-not-installed"], "KB_ADAPTER_NOT_INSTALLED",
             "Adapter package @kb-labs/adapters-not-installed is not installed on this platform.",
             "kb marketplace install @kb-labs/adapters-not-installed --scope platform"),
        )
        for command, code, message, hint in failures:
            for json_mode in (False, True):
                with_flags = [*command, *( ["--json"] if json_mode else [])]
                with invocation_context(step_id="S-051.2" if code == "KB_PLUGIN_NOT_FOUND" else "S-051.3"):
                    result = _run(["kb", *with_flags], cwd=project, env=base_env, runner=runner)
                validate_shared_failure(result, json_mode=json_mode, code=code, message=message, hint=hint)
    journey.step("marketplace and adapter failures use the shared envelope", "marketplace",
                 assert_shared_errors, cases=["S-051.2", "S-051.3", "S-051.4"])
    if journey.steps[-1].status != "PASS":
        return
    with invocation_context(step_id="S-010.1"):
        wait_for_marketplace_ready(project, base_env, runner)
        commit_version = _registry_version(commit_package, registry, base_env, runner)
        unrelated_version = _registry_version(unrelated_package, registry, base_env, runner)
    platform_list = ["marketplace", "plugins", "list", "--scope", "platform", "--json"]
    platform_plugins = _entries(_json(cli(platform_list), platform_list))
    if any(item.get("id") == commit_package for item in platform_plugins):
        raise RuntimeError(f"{commit_package} is preinstalled at platform scope; choose a registry plugin absent from the fresh platform store")

    # S-047 requires a second project plugin to survive target uninstall.
    install_unrelated = ["marketplace", "install", f"{unrelated_package}@{unrelated_version}", "--json"]
    sealed_platform_before_unrelated = _platform_snapshot(platform_root)
    _output(cli(install_unrelated), install_unrelated)
    if _platform_snapshot(platform_root) != sealed_platform_before_unrelated:
        raise RuntimeError("marketplace install changed the platform receipt or installed package versions")

    lock_file = project / ".kb" / "marketplace.lock"
    before = json.loads(lock_file.read_text()) if lock_file.exists() else {"plugins": {}}
    if commit_package in before.get("plugins", {}):
        raise RuntimeError(f"{commit_package} is already pinned in the fresh project before the install case")
    install_commit = ["marketplace", "install", f"{commit_package}@{commit_version}", "--json"]
    install_commit_platform = ["marketplace", "install", f"{commit_package}@{commit_version}",
                              "--scope", "platform", "--json"]
    sealed_platform_before = _platform_snapshot(platform_root)
    def install_exact_commit() -> None:
        project_result = _json(cli(install_commit), install_commit)
        project_installed = project_result.get("installed")
        if (project_result.get("scope") != "project" or not isinstance(project_installed, list)
                or not any(isinstance(item, dict) and item.get("id") == commit_package
                           and item.get("version") == commit_version for item in project_installed)):
            raise RuntimeError("project marketplace install did not return the selected plugin version as JSON")
        platform_result = _json(cli(install_commit_platform), install_commit_platform)
        platform_installed = platform_result.get("installed")
        if (platform_result.get("scope") != "platform" or not isinstance(platform_installed, list)
                or not any(isinstance(item, dict) and item.get("id") == commit_package
                           and item.get("version") == commit_version for item in platform_installed)):
            raise RuntimeError("platform marketplace install did not return the selected plugin version as JSON")
        project_lock = json.loads(lock_file.read_text())
        project_entry = project_lock.get("plugins", {}).get(commit_package)
        if (project_lock.get("schema") != "kb.marketplace/3" or not isinstance(project_entry, dict)
                or project_entry.get("version") != commit_version
                or not str(project_entry.get("resolvedPath", "")).startswith("store:")):
            raise RuntimeError("project marketplace install did not pin the exact plugin from the shared extension store")
        platform_lock_file = platform_root / ".kb" / "marketplace.lock"
        try:
            platform_lock = json.loads(platform_lock_file.read_text())
        except (OSError, json.JSONDecodeError) as error:
            raise RuntimeError("platform marketplace install did not write a readable lock") from error
        platform_entry = platform_lock.get("plugins", {}).get(commit_package)
        if (platform_lock.get("schema") != "kb.marketplace/3" or not isinstance(platform_entry, dict)
                or platform_entry.get("version") != commit_version
                or platform_entry.get("source") != "marketplace"
                or not str(platform_entry.get("resolvedPath", "")).startswith("store:")):
            raise RuntimeError("platform marketplace install did not pin the exact plugin from the shared extension store")
        if _platform_snapshot(platform_root) != sealed_platform_before:
            raise RuntimeError("marketplace install changed the platform receipt or installed package versions")
    journey.step(
        "install exact marketplace plugin into both scopes", "marketplace",
        install_exact_commit,
        cases=["S-010.1"],
    )
    if journey.steps[-1].status != "PASS":
        return

    def prepare_project_only_commit_scope() -> None:
        for scope in ("platform", "project"):
            command = ["marketplace", "uninstall", commit_package, "--scope", scope, "--json"]
            _output(cli(command), command)
        platform_lock = json.loads((platform_root / ".kb" / "marketplace.lock").read_text())
        if commit_package in platform_lock.get("plugins", {}):
            raise RuntimeError("S-192 requires Commit to be absent from the platform marketplace lock")
        if commit_package in json.loads(lock_file.read_text()).get("plugins", {}):
            raise RuntimeError("S-192 project setup did not remove the prior Commit pin")

    journey.step("remove platform and project Commit pins before the S-192 project lifecycle", "marketplace",
                 prepare_project_only_commit_scope)
    if journey.steps[-1].status != "PASS":
        return

    def install_commit_for_project_lifecycle() -> None:
        result = _json(cli(install_commit), install_commit)
        installed = result.get("installed")
        if (result.get("scope") != "project" or not isinstance(installed, list)
                or not any(isinstance(item, dict) and item.get("id") == commit_package
                           and item.get("version") == commit_version for item in installed)):
            raise RuntimeError("project marketplace install did not return the selected plugin version as JSON")

    journey.step("install Commit into the project for the route lifecycle", "marketplace",
                 install_commit_for_project_lifecycle, cases=["S-192.1"])
    if journey.steps[-1].status != "PASS":
        return

    try:
        if commit_case_hook is None:
            run_commit_case_steps(journey, project, Path("kb"), registry, runner, base_env)
        else:
            commit_case_hook()
    except Exception as error:
        journey.step("prepare Commit case fixture state", "marketplace", lambda: str(error))

    project_id = base_env.get("KB_JOURNEY_PROJECT_ID", "")
    gateway = base_env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
    route = f"{gateway}/api/v1/projects/{project_id}/rest/api/v1/plugins/commit/status"

    def assert_commit_status_route() -> None:
        if not project_id:
            raise RuntimeError("S-192 requires KB_JOURNEY_PROJECT_ID from project registration")
        headers = {"Authorization": f"Bearer {base_env.get('KB_JOURNEY_SERVICE_TOKEN', '')}"}
        request = urllib.request.Request(route, headers=headers)
        status, raw, _ = open_http(http_opener, request, timeout=30, env=base_env)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError as error:
            raise RuntimeError("Commit status route did not return JSON") from error
        validate_commit_status_response(status, payload)

    journey.step("GET the installed project Commit status route", "marketplace",
                 assert_commit_status_route, cases=["S-192.2"])

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

    gateway_doctor = ["marketplace", "plugins", "doctor", "--scope", "project", "--json"]
    def assert_cli_plugin_gateway() -> None:
        payload = _json(cli(gateway_doctor), gateway_doctor)
        if payload.get("ok") is not True:
            raise RuntimeError("installed CLI plugin command did not return a successful gateway result")
    journey.step("invoke gateway-backed plugin command through installed CLI", "marketplace",
                 assert_cli_plugin_gateway, cases=["S-010.7"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_stopped_host_error() -> None:
        stop_command = ["kb-create", "stop", "--platform-root", str(platform_root), "--json"]
        stop = _run(stop_command, cwd=platform_root, env=base_env, runner=runner)
        _output(stop, stop_command)
        command = ["kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"]
        try:
            with invocation_context(step_id="S-203.14"):
                result = _run(command, cwd=project, env=base_env, runner=runner)
            try:
                payload = json.loads(result.stdout)
            except json.JSONDecodeError as error:
                raise RuntimeError("gateway-backed plugin command did not return a JSON error envelope") from error
            failure = payload.get("error") if isinstance(payload, dict) else None
            if (result.returncode != 1 or not isinstance(failure, dict)
                    or failure.get("code") != "KB_HOST_UNREACHABLE"
                    or failure.get("hint") != 'Run "kb-create start", or "kb-create doctor" if the host should already be running.'):
                raise RuntimeError(f"stopped host returned an unexpected gateway error: {payload!r}")
        finally:
            start_command = ["kb-create", "start", "--platform-root", str(platform_root), "--json"]
            start = _run(start_command, cwd=platform_root, env=base_env, runner=runner)
            _output(start, start_command)

    journey.step("report catalogued error when the installed host is stopped", "marketplace",
                 assert_stopped_host_error, cases=["S-203.14"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_default_gateway_config() -> None:
        config_path = platform_root / ".kb" / "kb.config.jsonc"
        config = json.loads(config_path.read_text())
        gateway = config.get("gateway")
        if not isinstance(gateway, dict) or "port" in gateway:
            raise RuntimeError("fresh platform fixture must leave gateway.port at its service default")
        command = ["kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"]
        with invocation_context(step_id="S-203.15"):
            result = _run(command, cwd=project, env=base_env, runner=runner)
        payload = _json(result, command)
        if payload.get("ok") is not True:
            raise RuntimeError("gateway-backed CLI plugin command failed with the default gateway port")

    journey.step("resolve the default gateway address for CLI plugins", "marketplace",
                 assert_default_gateway_config, cases=["S-203.15"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_invalid_gateway_port_error() -> None:
        invalid_env = {**base_env, "GATEWAY_PORT": "not-a-port"}
        command = ["kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"]
        with invocation_context(step_id="S-203.16"):
            result = _run(command, cwd=project, env=invalid_env, runner=runner)
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("invalid gateway port did not return a JSON config error") from error
        failure = payload.get("error") if isinstance(payload, dict) else None
        if (result.returncode != 1 or not isinstance(failure, dict)
                or failure.get("code") != "KB_CONFIG_INVALID"
                or "GATEWAY_PORT" not in failure.get("message", "")
                or "integer from 1 through 65535" not in failure.get("message", "")
                or failure.get("hint") != 'Run "unset GATEWAY_PORT", then retry the command to use the service default port.'):
            raise RuntimeError(f"invalid gateway port returned an unexpected config error: {payload!r}")

    journey.step("report a catalogued config error for an invalid gateway port", "marketplace",
                 assert_invalid_gateway_port_error, cases=["S-203.16"])
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

    def set_plugin_enabled(enabled: bool) -> None:
        action = "enable" if enabled else "disable"
        command = ["marketplace", "plugins", action, "@kb-labs/commit"]
        with invocation_context(step_id="S-011.4" if enabled else "S-011.1"):
            result = cli(command)
        expected = f"{'Enabled' if enabled else 'Disabled'} @kb-labs/commit (project)"
        output = " ".join(f"{result.stdout or ''} {result.stderr or ''}".split())
        if result.returncode != 0 or expected not in output:
            raise RuntimeError(f"{command!r} did not report {expected!r}: {output!r}")
        entry = json.loads(lock_file.read_text()).get("plugins", {}).get(commit_package)
        if not isinstance(entry, dict) or entry.get("enabled") is not enabled:
            raise RuntimeError(f"{commit_package} was not {'enabled' if enabled else 'disabled'} in the project lock")

    def assert_disabled() -> None:
        set_plugin_enabled(False)
    journey.step("disable installed Commit plugin", "marketplace", assert_disabled, cases=["S-011.1"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_disabled_list() -> None:
        command = ["marketplace", "plugins", "list"]
        with invocation_context(step_id="S-011.2"):
            result = cli(command)
        output = " ".join(f"{result.stdout or ''} {result.stderr or ''}".split())
        if result.returncode != 0 or "disabled" not in output.lower() or "⏸" not in output:
            raise RuntimeError(f"marketplace list did not show the disabled count and row marker: {output!r}")
    journey.step("confirm disabled Commit appears in list", "marketplace", assert_disabled_list, cases=["S-011.2"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_command_hidden() -> None:
        command = ["commit", "--help"]
        with invocation_context(step_id="S-011.3"):
            result = cli(command)
        if result.returncode == 0:
            raise RuntimeError("Commit commands remain discoverable while the plugin is disabled")
    journey.step("confirm disabled Commit commands are unavailable", "marketplace", assert_command_hidden, cases=["S-011.3"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_enabled() -> None:
        set_plugin_enabled(True)
    journey.step("enable installed Commit plugin", "marketplace", assert_enabled, cases=["S-011.4"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_enabled_list_and_command() -> None:
        list_command = ["marketplace", "plugins", "list"]
        with invocation_context(step_id="S-011.5"):
            list_result = cli(list_command)
        output = " ".join(f"{list_result.stdout or ''} {list_result.stderr or ''}".split())
        if (list_result.returncode != 0 or "enabled" not in output.lower()
                or "✅" not in output or "commit" not in output.lower()):
            raise RuntimeError(f"marketplace list did not show enabled Commit: {output!r}")
        command = ["commit", "--help"]
        with invocation_context(step_id="S-011.5"):
            help_result = cli(command)
        if help_result.returncode != 0:
            raise RuntimeError("Commit commands remain unavailable after enabling the plugin")
    journey.step("confirm enabled Commit appears and commands are available", "marketplace",
                 assert_enabled_list_and_command, cases=["S-011.5"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_missing_disable_preserves_lock() -> None:
        before_lock = lock_file.read_bytes()
        command = ["marketplace", "plugins", "disable", "@kb-labs/not-installed"]
        with invocation_context(step_id="S-011.6"):
            result = cli(command)
        output = " ".join(f"{result.stdout or ''} {result.stderr or ''}".split()).lower()
        if result.returncode == 0 or not any(text in output for text in ("not found", "cannot be found", "not installed")):
            raise RuntimeError(f"disabling an uninstalled package did not report that it is unavailable: {output!r}")
        if lock_file.read_bytes() != before_lock:
            raise RuntimeError("disabling an uninstalled package changed the marketplace lock")
    journey.step("reject disabling an uninstalled plugin without changing the lock", "marketplace",
                 assert_missing_disable_preserves_lock, cases=["S-011.6"])
    if journey.steps[-1].status != "PASS":
        return

    def update_commit() -> None:
        command = ["marketplace", "update", "@kb-labs/commit", "--scope", "project", "--json"]
        with invocation_context(step_id="S-012.1"):
            result = cli(command)
        payload = _json(result, command)
        if (result.returncode != 0 or payload.get("scope") != "project"
                or not isinstance(payload.get("installed"), list)
                or not isinstance(payload.get("warnings"), list)):
            raise RuntimeError("marketplace update did not report a successful result")
        installed = payload["installed"]
        if installed and not any(
                isinstance(item, dict) and item.get("id") in {commit_package, "@kb-labs/commit"}
                and isinstance(item.get("version"), str) and item["version"]
                for item in installed):
            raise RuntimeError("marketplace update did not report the exact updated Commit version")
    journey.step("update installed Commit version", "marketplace", update_commit, cases=["S-012.1"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_updated_pin() -> None:
        command = ["marketplace", "plugins", "list", "--scope", "project", "--json"]
        with invocation_context(step_id="S-012.2"):
            payload = _json(cli(command), command)
        entry = next((item for item in _entries(payload) if item.get("id") == commit_package), None)
        lock_entry = json.loads(lock_file.read_text()).get("plugins", {}).get(commit_package)
        if (not isinstance(entry, dict) or not isinstance(lock_entry, dict)
                or entry.get("version") != lock_entry.get("version") or not lock_entry.get("integrity")):
            raise RuntimeError("plugin list and marketplace lock do not show the same exact version and integrity")
    journey.step("verify updated Commit pin in list and lock", "marketplace", assert_updated_pin, cases=["S-012.2"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_updated_command() -> None:
        command = ["commit", "--help"]
        with invocation_context(step_id="S-012.3"):
            result = cli(command)
        if result.returncode != 0:
            raise RuntimeError("Commit command group is unavailable after update")
    journey.step("confirm Commit commands remain available after update", "marketplace",
                 assert_updated_command, cases=["S-012.3"])
    if journey.steps[-1].status != "PASS":
        return

    def assert_unknown_update_preserves_pin() -> None:
        before = json.loads(lock_file.read_text()).get("plugins", {}).get(commit_package)
        command = ["marketplace", "update", "@kb-labs/not-published", "--scope", "project", "--json"]
        with invocation_context(step_id="S-012.4"):
            result = cli(command)
        payload = json.loads(result.stdout)
        after = json.loads(lock_file.read_text()).get("plugins", {}).get(commit_package)
        warnings = payload.get("warnings") if isinstance(payload, dict) else None
        if (result.returncode != 0 or not isinstance(warnings, list)
                or not any("@kb-labs/not-published" in str(warning) for warning in warnings)
                or before != after):
            raise RuntimeError("unknown package update did not warn while preserving the Commit pin")
    journey.step("reject unknown package update and preserve Commit pin", "marketplace",
                 assert_unknown_update_preserves_pin, cases=["S-012.4"])
    if journey.steps[-1].status != "PASS":
        return

    uninstall_commit = ["marketplace", "uninstall", commit_package, "--json"]
    def uninstall_target() -> None:
        with invocation_context(step_id="S-192.3"):
            _output(cli(uninstall_commit), uninstall_commit)
            if not project_id:
                raise RuntimeError("S-192 requires KB_JOURNEY_PROJECT_ID from project registration")
            request = urllib.request.Request(route, headers={
                "Authorization": f"Bearer {base_env.get('KB_JOURNEY_SERVICE_TOKEN', '')}"})
            status, raw, _ = open_http(http_opener, request, timeout=30, env=base_env)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError as error:
            raise RuntimeError("unmounted Commit status route did not return a JSON gateway envelope") from error
        validate_unmounted_commit_response(status, payload)
    journey.step("uninstall target project plugin", "marketplace",
                 uninstall_target,
                 cases=["S-047.1", "S-192.3"])
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

    def reinstall_commit_and_assert_route() -> None:
        reinstall = ["marketplace", "install", f"{commit_package}@{commit_version}", "--json"]
        installed = _json(cli(reinstall), reinstall)
        entries = installed.get("installed")
        if not isinstance(entries, list) or not any(
            isinstance(item, dict) and item.get("id") == commit_package
            and item.get("version") == commit_version for item in entries
        ):
            raise RuntimeError("S-192 reinstall did not restore the selected Commit version")
        if not project_id:
            raise RuntimeError("S-192 requires KB_JOURNEY_PROJECT_ID from project registration")
        request = urllib.request.Request(route, headers={
            "Authorization": f"Bearer {base_env.get('KB_JOURNEY_SERVICE_TOKEN', '')}"})
        status, raw, _ = open_http(http_opener, request, timeout=30, env=base_env)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError as error:
            raise RuntimeError("reinstalled Commit status route did not return JSON") from error
        validate_commit_status_response(status, payload)
    journey.step("reinstall Commit and call its project REST route", "marketplace",
                 reinstall_commit_and_assert_route, cases=["S-192.4"])
