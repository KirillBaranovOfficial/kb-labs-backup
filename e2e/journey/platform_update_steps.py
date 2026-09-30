"""Installed-platform update and rollback journey steps."""
from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path
from typing import Callable

from service_steps import Journey


CommandRunner = Callable[[list[str]], subprocess.CompletedProcess]


def _run(args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)


def _json_command(run_command: CommandRunner, args: list[str], label: str) -> dict:
    result = run_command(args)
    if result.returncode != 0:
        raise RuntimeError(f"{label}: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{label}: expected JSON output") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"{label}: expected a JSON object")
    return value


def _hash(path: Path) -> str | None:
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except FileNotFoundError:
        return None


def run_platform_update_steps(
    *,
    mode: str,
    launcher: str,
    kb_binary: str,
    platform_root: Path,
    update_index: Path,
    request_file: Path,
    base_version: str,
    updated_version: str,
    run_command: CommandRunner = _run,
    verify_user: Callable[[], subprocess.CompletedProcess] | None = None,
) -> Journey:
    """Update the running installation, exercise a project command, and roll back."""
    journey = Journey(mode, f"platform-update-{mode}")
    root = str(platform_root)
    receipt_path = platform_root / ".kb/v2/receipt.json"
    config_paths = [platform_root / ".kb/kb.config.jsonc", platform_root / ".kb/devservices.yaml"]
    snapshot_id = ""
    updated_plan_hash = ""
    config_hashes: dict[str, str | None] = {}

    def save_known_good_state() -> None:
        nonlocal config_hashes
        receipt = json.loads(receipt_path.read_text())
        actual = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if actual != base_version:
            raise RuntimeError(f"active receipt is {actual!r}, expected version {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "initial status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("initial kb-create status did not confirm a running, reachable host")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"initial admin login failed: {' '.join(login.stdout.split())[-400:]}")
        config_hashes = {str(path): _hash(path) for path in config_paths}
        missing = [path for path, digest in config_hashes.items() if digest is None]
        if missing:
            raise RuntimeError(f"generated platform config is missing: {', '.join(missing)}")

    journey.step("save installed receipt, config, and host status", "platform.update", save_known_good_state,
                 cases=["S-023.1"])

    def plan_new_release() -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(update_index),
                                          "--input", str(request_file)], "plan update")
        graph = plan.get("plan", {}).get("serviceGraph", {})
        if plan.get("ok") is not True or graph.get("platformVersion") != updated_version:
            raise RuntimeError(f"update plan did not resolve platform {updated_version}: {graph.get('platformVersion')!r}")

    journey.step("plan the newer sealed release", "platform.update", plan_new_release, cases=["S-023.2"])

    def update() -> None:
        nonlocal snapshot_id, updated_plan_hash
        result = _json_command(run_command, [launcher, "update", "--json", "--index", str(update_index),
                                            "--input", str(request_file)], "update platform")
        snapshot = result.get("snapshot")
        receipt = result.get("receipt")
        if result.get("ok") is not True or not isinstance(snapshot, dict) or not snapshot.get("id"):
            raise RuntimeError("update did not return ok=true and a recovery snapshot ID")
        if not isinstance(receipt, dict) or receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion") != updated_version:
            raise RuntimeError(f"update receipt does not identify platform {updated_version}")
        updated_plan_hash = str(receipt.get("plan", {}).get("planHash") or "")
        if not updated_plan_hash:
            raise RuntimeError("update receipt did not include its resolved plan hash")
        if result.get("hostRestarted") is not True:
            raise RuntimeError("update did not report that the running host restarted")
        if not result.get("logPath"):
            raise RuntimeError("update result did not include its operation log path")
        snapshot_id = str(snapshot["id"])

    journey.step("update the running host and save its recovery snapshot", "platform.update", update,
                 cases=["S-023.3", "S-024.1"])

    def verify_updated_install() -> None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != updated_version:
            raise RuntimeError(f"active receipt is {version!r}, expected {updated_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "updated status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("kb-create status did not confirm the updated host is healthy")
        changed = [str(path) for path in config_paths if config_hashes.get(str(path)) != _hash(path)]
        if changed:
            raise RuntimeError(f"generated config changed during update: {', '.join(changed)}")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"admin login failed after update: {' '.join(login.stdout.split())[-400:]}")

    journey.step("verify updated receipt, config, and host status", "platform.update", verify_updated_install,
                 cases=["S-023.4"])

    def project_command() -> None:
        result = run_command([kb_binary, "commit", "--dry-run", "--scope", "root"])
        if result.returncode != 0:
            raise RuntimeError(f"project command after update: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")
        if 'scope "root"' not in result.stdout:
            raise RuntimeError("project command after update did not report the configured root scope")

    journey.step("run the project commit dry-run after update", "platform.update", project_command,
                 cases=["S-023.5"])

    def repeat_update() -> None:
        result = _json_command(run_command, [launcher, "update", "--json", "--index", str(update_index),
                                            "--input", str(request_file)], "repeat update")
        receipt = result.get("receipt")
        snapshot = result.get("snapshot")
        if result.get("ok") is not True or not isinstance(receipt, dict) or not isinstance(snapshot, dict):
            raise RuntimeError("repeat update did not return a verified receipt and snapshot")
        plan = receipt.get("plan", {})
        if plan.get("serviceGraph", {}).get("platformVersion") != updated_version:
            raise RuntimeError("repeat update changed the resolved platform version")
        if plan.get("planHash") != updated_plan_hash:
            raise RuntimeError("repeat update changed the resolved plan hash")
        if result.get("hostRestarted") is not True:
            raise RuntimeError("repeat update did not report that the running host restarted")
        if not snapshot.get("id"):
            raise RuntimeError("repeat update did not return its own snapshot ID")

    journey.step("repeat the update without version drift", "platform.update", repeat_update,
                 cases=["S-023.6"])

    def rollback() -> None:
        if not snapshot_id:
            raise RuntimeError("update did not produce a snapshot ID for rollback")
        result = _json_command(run_command, [launcher, "rollback", "--platform-root", root,
                                            "--snapshot", snapshot_id, "--yes", "--json"], "rollback platform")
        snapshot = result.get("snapshot")
        if result.get("ok") is not True or not isinstance(snapshot, dict) or not snapshot.get("id"):
            raise RuntimeError("rollback did not return ok=true and the restored snapshot")
        if result.get("hostRestarted") is not True or not result.get("logPath"):
            raise RuntimeError("rollback did not report host restart and an operation log")

    journey.step("roll back to version N with explicit non-TTY confirmation", "platform.rollback", rollback,
                 cases=["S-024.2"])

    def reject_unknown_snapshot() -> None:
        before = receipt_path.read_bytes()
        result = run_command([launcher, "rollback", "--platform-root", root, "--snapshot",
                              "missing-journey-snapshot", "--yes", "--json"])
        if result.returncode == 0:
            raise RuntimeError("rollback unexpectedly accepted an unknown snapshot")
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("unknown-snapshot rollback returned non-JSON output") from error
        error = payload.get("error", {}) if isinstance(payload, dict) else {}
        if not isinstance(error, dict) or not error.get("code") or not error.get("hint"):
            raise RuntimeError("unknown-snapshot rollback omitted its structured recovery error or hint")
        if receipt_path.read_bytes() != before:
            raise RuntimeError("unknown-snapshot rollback changed the active receipt")

    journey.step("reject a missing snapshot without changing the active install", "platform.rollback",
                 reject_unknown_snapshot, cases=["S-024.4"])

    def verify_rollback() -> None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != base_version:
            raise RuntimeError(f"restored receipt is {version!r}, expected {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "restored status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("kb-create status did not confirm the restored host")
        changed = [str(path) for path in config_paths if config_hashes.get(str(path)) != _hash(path)]
        if changed:
            raise RuntimeError(f"generated config changed during rollback: {', '.join(changed)}")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"admin login failed after rollback: {' '.join(login.stdout.split())[-400:]}")
        command = run_command([kb_binary, "commit", "--dry-run", "--scope", "root"])
        if command.returncode != 0:
            raise RuntimeError(f"project command after rollback: exit {command.returncode}; {' '.join(command.stdout.split())[-500:]}")

    journey.step("verify restored receipt, config, host, and project command", "platform.rollback", verify_rollback,
                 cases=["S-024.3"])

    def doctor() -> None:
        result = _json_command(run_command, [launcher, "doctor", "--platform-root", root, "--json"], "doctor")
        if result.get("ok") is not True or result.get("platform", {}).get("ok") is not True:
            raise RuntimeError("kb-create doctor did not report a healthy restored platform")

    journey.step("diagnose the restored platform", "platform.rollback", doctor, cases=["S-024.5"])
    return journey
