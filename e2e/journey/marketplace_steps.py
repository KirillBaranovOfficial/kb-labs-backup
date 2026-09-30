"""Installed marketplace and adapter journeys for the shared service runner."""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Callable

from service_steps import Journey


CommandRunner = Callable[..., subprocess.CompletedProcess[str]]


def _run(command: list[str], *, cwd: Path, env: dict[str, str], runner: CommandRunner) -> subprocess.CompletedProcess[str]:
    return runner(command, cwd=str(cwd), env=env, capture_output=True, text=True, timeout=300)


def _output(result: subprocess.CompletedProcess[str], command: list[str]) -> str:
    if result.returncode != 0:
        detail = " ".join((result.stderr or result.stdout).split())[-500:]
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


def run_marketplace_steps(journey: Journey, project_root: str | Path, registry: str,
                          env: dict[str, str] | None = None,
                          runner: CommandRunner = subprocess.run) -> None:
    """Execute S-010, S-047, and S-048 in the isolated installed platform."""
    project = Path(project_root).resolve()
    base_env = dict(os.environ if env is None else env)
    base_env["KB_JOURNEY_PROJECT_ROOT"] = str(project)
    project_b = Path(tempfile.mkdtemp(prefix="kb-marketplace-project-b-", dir=base_env["HOME"]))
    project_b_registered = False
    adapter_bound = False

    def cli(args: list[str], cwd: Path = project) -> subprocess.CompletedProcess[str]:
        return _run(["kb", *args], cwd=cwd, env=base_env, runner=runner)

    try:
        commit_package = "@kb-labs/commit-entry"
        workflow_package = "@kb-labs/workflow-entry"
        commit_version = _registry_version(commit_package, registry, base_env, runner)
        workflow_version = _registry_version(workflow_package, registry, base_env, runner)
        adapter_package = "@kb-labs/adapters-fs"
        adapter_version = _registry_version(adapter_package, registry, base_env, runner)

        platform_list = ["marketplace", "plugins", "list", "--scope", "platform", "--json"]
        platform_plugins = _entries(_json(cli(platform_list), platform_list))
        if any(item.get("id") == commit_package for item in platform_plugins):
            raise RuntimeError(f"{commit_package} is preinstalled at platform scope; choose a registry plugin absent from the fresh platform store")

        # S-047 requires a second project plugin to survive target uninstall.
        install_workflow = ["marketplace", "install", f"{workflow_package}@{workflow_version}", "--json"]
        _output(cli(install_workflow), install_workflow)

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
            unrelated = next((item for item in entries if item.get("id") == workflow_package), None)
            if not unrelated or unrelated.get("version") != workflow_version or unrelated.get("enabled") is not True:
                raise RuntimeError(f"unrelated workflow plugin did not remain installed and enabled: {unrelated!r}")
        journey.step("list confirms target absent and unrelated plugin remains", "marketplace", assert_unrelated_remains,
                     cases=["S-047.2"])
        if journey.steps[-1].status != "PASS":
            return

        def assert_lock_and_workflow() -> None:
            lock = json.loads(lock_file.read_text())
            if commit_package in lock.get("plugins", {}):
                raise RuntimeError("project lock still pins the uninstalled target plugin")
            unavailable = cli(["commit", "open", "--json"])
            if unavailable.returncode == 0:
                raise RuntimeError("`kb commit open --json` still succeeds after its project plugin was uninstalled")
            unavailable_detail = " ".join((unavailable.stdout + " " + unavailable.stderr).split()).lower()
            if "not found" not in unavailable_detail and "no plugin" not in unavailable_detail:
                raise RuntimeError(f"removed commit command failed without a command-not-found result: {unavailable_detail[-400:]}")
            health = ["workflow", "health", "--json"]
            payload = _json(cli(health), health)
            if payload.get("ok") is not True:
                raise RuntimeError(f"unrelated `kb workflow health --json` command failed: {payload!r}")
        journey.step("verify lock removal and invoke unrelated workflow plugin", "marketplace", assert_lock_and_workflow,
                     cases=["S-047.3"])
        if journey.steps[-1].status != "PASS":
            return

        # Project B is initialized and registered so the scoped config comparison
        # exercises the actual CLI project boundary rather than a handcrafted file.
        add_project = ["project", "add", str(project_b), "--init", "--json"]
        added = _json(cli(add_project), add_project)
        if added.get("ok") is not True:
            raise RuntimeError(f"could not initialize the second adapter project: {added!r}")
        project_b_registered = True
        project_b_config, initial_b = _project_config(project_b)
        initial_a_file, initial_a = _project_config(project)

        install_adapter = ["marketplace", "install", f"{adapter_package}@{adapter_version}", "--scope", "platform", "--json"]
        _output(cli(install_adapter), install_adapter)
        bind_dry_run = ["adapter", "bind", "storage", adapter_package, "--dry-run", "--json"]
        def assert_bind_dry_run() -> None:
            payload = _json(cli(bind_dry_run), bind_dry_run)
            if payload.get("ok") is not True or payload.get("dryRun") is not True:
                raise RuntimeError(f"adapter bind dry-run did not report successful validation: {payload!r}")
            _, current_a = _project_config(project)
            if current_a != initial_a:
                raise RuntimeError("adapter bind --dry-run changed project A config")
        journey.step("install machine adapter and dry-run project bind", "adapter", assert_bind_dry_run,
                     cases=["S-048.1"])
        if journey.steps[-1].status != "PASS":
            return

        bind = ["adapter", "bind", "storage", adapter_package, "--json"]
        # Set before running the mutating command so cleanup also runs when the
        # CLI changes config but its output or the following assertion is bad.
        adapter_bound = True
        def assert_bind() -> None:
            payload = _json(cli(bind), bind)
            if payload.get("ok") is not True or payload.get("dryRun") is not False:
                raise RuntimeError(f"adapter bind did not report success: {payload!r}")
            _, config = _project_config(project)
            if config.get("platform", {}).get("adapters", {}).get("storage") != adapter_package:
                raise RuntimeError("project A config does not contain its storage adapter binding")
        journey.step("bind machine storage adapter to project A", "adapter", assert_bind, cases=["S-048.2"])
        if journey.steps[-1].status != "PASS":
            return
        options_value = '{"baseDir":".kb/data"}'
        set_options = ["config", "set", "platform.adapterOptions.storage", options_value, "--json"]
        def assert_options() -> None:
            payload = _json(cli(set_options), set_options)
            if payload.get("ok") is not True or payload.get("scope") != "project":
                raise RuntimeError(f"adapter options were not saved in project scope: {payload!r}")
            _, config = _project_config(project)
            options = config.get("platform", {}).get("adapterOptions", {}).get("storage")
            if options != {"baseDir": ".kb/data"}:
                raise RuntimeError(f"project A config has unexpected storage options: {options!r}")
        journey.step("save project-scoped storage adapter options", "adapter", assert_options, cases=["S-048.3"])
        if journey.steps[-1].status != "PASS":
            return

        def assert_project_isolation() -> None:
            _, config_a = _project_config(project)
            _, config_b = _project_config(project_b)
            if config_b != initial_b:
                raise RuntimeError(f"project B config changed while binding project A: {config_b!r}")
            platform = config_a.get("platform", {})
            if (platform.get("adapters", {}).get("storage") != adapter_package
                    or platform.get("adapterOptions", {}).get("storage") != {"baseDir": ".kb/data"}):
                raise RuntimeError(f"project A config is missing binding or options: {config_a!r}")
        journey.step("compare project A and project B user configs", "adapter", assert_project_isolation,
                     cases=["S-048.4"])
        if journey.steps[-1].status != "PASS":
            return

        def assert_storage_workflow() -> None:
            launch = ["workflow", "run", "--workflow-id=e2e-hello", "--trigger-type=manual", "--json"]
            launch_payload = _json(cli(launch), launch)
            data = launch_payload.get("data")
            run_id = data.get("runId") if isinstance(data, dict) else None
            if launch_payload.get("ok") is not True or not isinstance(run_id, str) or not run_id:
                raise RuntimeError(f"workflow run did not return a run ID: {launch_payload!r}")
            watch = ["workflow", "runs", "watch", run_id]
            watched = _output(cli(watch), watch)
            if "Run SUCCESS" not in watched:
                raise RuntimeError(f"workflow using project storage did not complete successfully: {watched[-500:]!r}")
        journey.step("run and await the project workflow using storage", "adapter", assert_storage_workflow,
                     cases=["S-048.5"])
        if journey.steps[-1].status != "PASS":
            return

        unbind = ["adapter", "unbind", "storage", "--json"]
        def assert_unbound() -> None:
            payload = _json(cli(unbind), unbind)
            if payload.get("ok") is not True:
                raise RuntimeError(f"adapter unbind did not report success: {payload!r}")
            _, config_a = _project_config(project)
            _, config_b = _project_config(project_b)
            platform = config_a.get("platform", {})
            if "storage" in platform.get("adapters", {}):
                raise RuntimeError(f"project A config still binds storage after unbind: {platform!r}")
            if platform.get("adapterOptions", {}).get("storage") != {"baseDir": ".kb/data"}:
                raise RuntimeError("unbinding discarded project adapter options")
            if config_b != initial_b:
                raise RuntimeError("unbinding project A changed project B config")
        journey.step("unbind storage from project A", "adapter", assert_unbound, cases=["S-048.6"])
        if journey.steps[-1].status == "PASS":
            adapter_bound = False
    finally:
        cleanup_errors = []
        if adapter_bound:
            command = ["adapter", "unbind", "storage", "--json"]
            try:
                _output(cli(command), command)
            except Exception as error:
                cleanup_errors.append(f"could not clean up project A storage binding: {error}")
        if project_b_registered:
            command = ["project", "remove", str(project_b), "--yes", "--json"]
            try:
                _output(cli(command), command)
            except Exception as error:
                cleanup_errors.append(f"could not unregister adapter project B: {error}")
        try:
            shutil.rmtree(project_b)
        except FileNotFoundError:
            pass
        except OSError as error:
            cleanup_errors.append(f"could not remove adapter project B files: {error}")
        if cleanup_errors:
            journey.step("clean up adapter journey projects", "adapter", lambda: "; ".join(cleanup_errors))
