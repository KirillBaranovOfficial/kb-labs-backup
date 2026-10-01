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
        raise RuntimeError(f"{label}: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}{_log_tail(result.stdout)}")
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{label}: expected JSON output") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"{label}: expected a JSON object")
    return value


def _log_tail(stdout: str) -> str:
    """The end of the operation log named by a failed launcher result, so the journey report shows the cause."""
    try:
        log_path = json.loads(stdout).get("logPath")
        text = Path(log_path).read_text(encoding="utf-8", errors="replace") if log_path else ""
    except (json.JSONDecodeError, AttributeError, OSError, TypeError):
        return ""
    return f"; operation log tail: {' '.join(text.split())[-1500:]}" if text else ""


def _hash(path: Path) -> str | None:
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except FileNotFoundError:
        return None


def _changed_paths(previous: object, current: object, prefix: str = "plan") -> list[str]:
    if isinstance(previous, dict) and isinstance(current, dict):
        return [path for key in sorted(set(previous) | set(current))
                for path in _changed_paths(previous.get(key), current.get(key), f"{prefix}.{key}")]
    if isinstance(previous, list) and isinstance(current, list):
        paths = [path for index, (left, right) in enumerate(zip(previous, current))
                 for path in _changed_paths(left, right, f"{prefix}[{index}]")]
        if len(previous) != len(current):
            paths.append(f"{prefix}.length")
        return paths
    return [] if previous == current else [prefix]


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
    config_paths = [platform_root / ".kb/kb.config.jsonc"]
    snapshot_id = ""
    updated_plan_hash = ""
    updated_plan: dict = {}
    config_checkpoints: dict[str, str | None] = {}

    def checkpoint_details() -> dict[str, dict[str, str | None]]:
        return {"configSha256": dict(config_checkpoints)}

    def record_config_checkpoint(name: str) -> None:
        digest = _hash(config_paths[0])
        config_checkpoints[name] = digest

    def save_known_good_state() -> None:
        receipt = json.loads(receipt_path.read_text())
        actual = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if actual != base_version:
            raise RuntimeError(f"active receipt is {actual!r}, expected version {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "initial status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("initial kb-create status did not confirm a running, reachable host")
        before_update = _hash(config_paths[0])
        config_checkpoints["beforeUpdate"] = before_update
        if before_update is None:
            raise RuntimeError(f"generated platform config is missing: {config_paths[0]}")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"initial admin login failed: {' '.join(login.stdout.split())[-400:]}")

    journey.step("save installed receipt, config, and host status", "platform.update", save_known_good_state,
                 cases=["S-023.1"], details=checkpoint_details)

    def plan_new_release() -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(update_index),
                                          "--input", str(request_file)], "plan update")
        graph = plan.get("plan", {}).get("serviceGraph", {})
        if plan.get("ok") is not True or graph.get("platformVersion") != updated_version:
            raise RuntimeError(f"update plan did not resolve platform {updated_version}: {graph.get('platformVersion')!r}")

    journey.step("plan the newer sealed release", "platform.update", plan_new_release, cases=["S-023.2"])

    def update() -> None:
        nonlocal snapshot_id, updated_plan_hash, updated_plan
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
        updated_plan = receipt["plan"]
        if result.get("hostRestarted") is not True:
            raise RuntimeError("update did not report that the running host restarted")
        if not result.get("logPath"):
            raise RuntimeError("update result did not include its operation log path")
        snapshot_id = str(snapshot["id"])
        record_config_checkpoint("afterUpdateHostRestart")

    journey.step("update the running host and save its recovery snapshot", "platform.update", update,
                 cases=["S-023.3", "S-023.7", "S-024.1"], details=checkpoint_details)

    def verify_updated_install() -> None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != updated_version:
            raise RuntimeError(f"active receipt is {version!r}, expected {updated_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "updated status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("kb-create status did not confirm the updated host is healthy")
        config_hash = _hash(config_paths[0])
        verified_hash = receipt.get("verification", {}).get("configSha256")
        if not config_hash or config_hash != verified_hash:
            raise RuntimeError("generated config is missing or does not match the updated receipt verification hash")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"admin login failed after update: {' '.join(login.stdout.split())[-400:]}")

    journey.step("verify updated receipt, config, and host status", "platform.update", verify_updated_install,
                 cases=["S-023.4"])

    def project_command() -> None:
        result = run_command([kb_binary, "project", "list", "--json"])
        if result.returncode != 0:
            raise RuntimeError(f"project list after update: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("project list after update did not return JSON") from error
        if payload.get("ok") is not True or not isinstance(payload.get("projects"), list):
            raise RuntimeError("project list after update did not return ok=true and a projects array")

    journey.step("run the core project list command after update", "platform.update", project_command,
                 cases=["S-023.5"])

    def repeat_update() -> None:
        preview = _json_command(run_command, [launcher, "plan", "--json", "--index", str(update_index),
                                              "--input", str(request_file)], "repeat update plan")
        preview_plan = preview.get("plan", {})
        preview_hash = str(preview_plan.get("planHash") or "")
        if preview_hash != updated_plan_hash:
            changed_fields = _changed_paths(updated_plan, preview_plan)
            raise RuntimeError(
                f"repeat update plan drift before mutation: first={updated_plan_hash!r}, "
                f"preview={preview_hash!r}, changed fields={changed_fields}"
            )
        result = _json_command(run_command, [launcher, "update", "--json", "--index", str(update_index),
                                            "--input", str(request_file)], "repeat update")
        receipt = result.get("receipt")
        snapshot = result.get("snapshot")
        if result.get("ok") is not True or not isinstance(receipt, dict) or not isinstance(snapshot, dict):
            raise RuntimeError("repeat update did not return a verified receipt and snapshot")
        plan = receipt.get("plan", {})
        if plan.get("serviceGraph", {}).get("platformVersion") != updated_version:
            raise RuntimeError("repeat update changed the resolved platform version")
        repeated_plan_hash = str(plan.get("planHash") or "")
        if repeated_plan_hash != updated_plan_hash:
            changed_fields = _changed_paths(updated_plan, plan)
            raise RuntimeError(
                f"repeat update changed the resolved plan hash: first={updated_plan_hash!r}, "
                f"repeat={repeated_plan_hash!r}, changed fields={changed_fields}"
            )
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
        record_config_checkpoint("afterRollbackHostRestart")

    journey.step("roll back to version N with explicit non-TTY confirmation", "platform.rollback", rollback,
                 cases=["S-024.2"], details=checkpoint_details)

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
        record_config_checkpoint("afterMissingSnapshotRecovery")

    journey.step("reject a missing snapshot without changing the active install", "platform.rollback",
                 reject_unknown_snapshot, cases=["S-024.4"], details=checkpoint_details)

    def verify_rollback() -> str | None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != base_version:
            raise RuntimeError(f"restored receipt is {version!r}, expected {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "restored status")
        if status.get("ok") is not True or status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("kb-create status did not confirm the restored host")
        before_update = config_checkpoints.get("beforeUpdate")
        after_rollback = config_checkpoints.get("afterRollbackHostRestart")
        after_recovery = config_checkpoints.get("afterMissingSnapshotRecovery")
        if before_update is None:
            return "BLOCKED: before-update config SHA-256 is unavailable because the initial config/status checkpoint failed"
        if after_rollback is None:
            return "BLOCKED: post-rollback config SHA-256 is unavailable because rollback or its host restart did not complete"
        if after_rollback != before_update:
            raise RuntimeError(
                "generated config first differed at after successful rollback plus host restart: "
                f"beforeUpdate={before_update}, afterRollbackHostRestart={after_rollback}"
            )
        if after_recovery is None:
            return "BLOCKED: post-recovery config SHA-256 is unavailable because missing-snapshot recovery did not complete"
        if after_recovery != after_rollback:
            raise RuntimeError(
                "generated config first differed at after missing-snapshot recovery: "
                f"afterRollbackHostRestart={after_rollback}, afterMissingSnapshotRecovery={after_recovery}"
            )
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"admin login failed after rollback: {' '.join(login.stdout.split())[-400:]}")
        command = run_command([kb_binary, "project", "list", "--json"])
        if command.returncode != 0:
            raise RuntimeError(f"project list after rollback: exit {command.returncode}; {' '.join(command.stdout.split())[-500:]}")
        try:
            payload = json.loads(command.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError("project list after rollback did not return JSON") from error
        if payload.get("ok") is not True or not isinstance(payload.get("projects"), list):
            raise RuntimeError("project list after rollback did not return ok=true and a projects array")

    journey.step("verify restored receipt, config, host, and project command", "platform.rollback", verify_rollback,
                 cases=["S-024.3"], details=checkpoint_details)

    def doctor() -> None:
        result = _json_command(run_command, [launcher, "doctor", "--platform-root", root, "--json"], "doctor")
        if result.get("ok") is not True or result.get("platform", {}).get("ok") is not True:
            raise RuntimeError("kb-create doctor did not report a healthy restored platform")

    journey.step("diagnose the restored platform", "platform.rollback", doctor, cases=["S-024.5"])
    return journey
