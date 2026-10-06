"""Installed-platform update and rollback journey steps."""
from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path
from typing import Callable

from service_steps import Journey
from invocations import (decode_ux_error_result, decode_ux_success_result, read_ux_cli_result,
                         run_command as invoke_command, run_with_runner)


CommandRunner = Callable[[list[str]], subprocess.CompletedProcess]


def _json_command(run_command: CommandRunner, args: list[str], label: str) -> dict:
    result = run_command(args)
    try:
        value = read_ux_cli_result(result.stdout, result.returncode)
        if result.returncode != 0:
            error = decode_ux_error_result(value)
            raise RuntimeError(f"{label}: {error!r}{_log_tail(result.stdout, result.returncode)}")
        payload = decode_ux_success_result(value)
    except ValueError as error:
        raise RuntimeError(f"{label}: expected a valid ADR-0049 result: {error}") from error
    if not isinstance(payload, dict):
        raise RuntimeError(f"{label}: result envelope did not contain an object payload")
    return payload


def _log_tail(stdout: str, returncode: int) -> str:
    """The end of the operation log named by a failed launcher result, so the journey report shows the cause."""
    try:
        envelope = read_ux_cli_result(stdout, returncode)
        payload = decode_ux_error_result(envelope) if returncode else decode_ux_success_result(envelope)
        log_path = payload.get("logPath")
        text = Path(log_path).read_text(encoding="utf-8", errors="replace") if log_path else ""
    except (ValueError, AttributeError, OSError, TypeError):
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
    run_command: CommandRunner = invoke_command,
    verify_user: Callable[[], subprocess.CompletedProcess] | None = None,
) -> Journey:
    command_runner = run_command
    def run_command(args: list[str]) -> subprocess.CompletedProcess:
        result = run_with_runner(command_runner, args)
        return subprocess.CompletedProcess(args, result.returncode,
                                            stdout=(result.stdout or "") + (result.stderr or ""),
                                            stderr=result.stderr)
    """Update the running installation, exercise a project command, and roll back."""
    journey = Journey(mode, f"platform-update-{mode}")
    root = str(platform_root)
    receipt_path = platform_root / ".kb/v2/receipt.json"
    config_paths = [platform_root / ".kb/kb.config.jsonc"]
    secrets_path = platform_root / ".kb/v2/secrets.env"
    snapshot_id = ""
    updated_plan_hash = ""
    updated_plan: dict = {}
    config_checkpoints: dict[str, str | None] = {}
    secret_checkpoints: dict[str, str | None] = {}
    saved_user_session_established = False
    saved_session_succeeded_after_update = False

    def checkpoint_details() -> dict[str, dict[str, str | None]]:
        return {"configSha256": dict(config_checkpoints),
                "installationSecretsSha256": dict(secret_checkpoints)}

    def record_config_checkpoint(name: str) -> None:
        digest = _hash(config_paths[0])
        config_checkpoints[name] = digest

    def record_secret_checkpoint(name: str) -> None:
        secret_checkpoints[name] = _hash(secrets_path)

    def save_known_good_state() -> None:
        nonlocal saved_user_session_established
        receipt = json.loads(receipt_path.read_text())
        actual = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if actual != base_version:
            raise RuntimeError(f"active receipt is {actual!r}, expected version {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "initial status")
        if status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("initial kb-create status did not confirm a running, reachable host")
        before_update = _hash(config_paths[0])
        config_checkpoints["beforeUpdate"] = before_update
        record_secret_checkpoint("beforeUpdate")
        if before_update is None:
            raise RuntimeError(f"generated platform config is missing: {config_paths[0]}")
        if verify_user is not None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"initial admin login failed: {' '.join(login.stdout.split())[-400:]}")
            saved_user_session_established = True

    journey.step("save installed receipt, config, and host status", "platform.update", save_known_good_state,
                 cases=["S-023.1"], details=checkpoint_details)

    def plan_new_release() -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(update_index),
                                          "--input", str(request_file)], "plan update")
        graph = plan.get("plan", {}).get("serviceGraph", {})
        if graph.get("platformVersion") != updated_version:
            raise RuntimeError(f"update plan did not resolve platform {updated_version}: {graph.get('platformVersion')!r}")

    journey.step("plan the newer sealed release", "platform.update", plan_new_release, cases=["S-023.2"])

    def update() -> None:
        nonlocal snapshot_id, updated_plan_hash, updated_plan
        result = _json_command(run_command, [launcher, "update", "--json", "--index", str(update_index),
                                            "--input", str(request_file)], "update platform")
        snapshot = result.get("snapshot")
        receipt = result.get("receipt")
        if not isinstance(snapshot, dict) or not snapshot.get("id"):
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
        record_secret_checkpoint("afterUpdateHostRestart")
        human = run_command([launcher, "update", "--index", str(update_index), "--input", str(request_file)])
        if human.returncode != 0 or "Platform update completed." not in human.stdout:
            raise RuntimeError("human update did not show its completion summary")

    journey.step("update the running host and save its recovery snapshot", "platform.update", update,
                 cases=["S-023.3", "S-023.7", "S-024.1", "S-203.10"], details=checkpoint_details)

    def verify_updated_install() -> None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != updated_version:
            raise RuntimeError(f"active receipt is {version!r}, expected {updated_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "updated status")
        if status.get("running") is not True or status.get("reachable") is not True:
            raise RuntimeError("kb-create status did not confirm the updated host is healthy")
        config_hash = _hash(config_paths[0])
        verified_hash = receipt.get("verification", {}).get("configSha256")
        if not config_hash or config_hash != verified_hash:
            raise RuntimeError("generated config is missing or does not match the updated receipt verification hash")
        before_secret = secret_checkpoints.get("beforeUpdate")
        after_secret = secret_checkpoints.get("afterUpdateHostRestart")
        if before_secret is None or after_secret is None:
            raise RuntimeError("installation-secret fingerprint is unavailable before or after update")
        if before_secret != after_secret:
            raise RuntimeError(
                "installation-secret fingerprint changed during update: "
                f"beforeUpdate={before_secret}, afterUpdateHostRestart={after_secret}"
            )

    journey.step("verify updated receipt, config, and host status", "platform.update", verify_updated_install,
                 cases=["S-023.4"])

    def project_command() -> None:
        nonlocal saved_session_succeeded_after_update
        result = run_command([kb_binary, "project", "list", "--json"])
        try:
            envelope = read_ux_cli_result(result.stdout, result.returncode)
            if result.returncode != 0:
                failure = decode_ux_error_result(envelope)
                raise RuntimeError(f"project list after update was rejected: {failure!r}")
            payload = decode_ux_success_result(envelope)
        except ValueError as error:
            raise RuntimeError(f"project list after update returned an invalid UX result: {error}") from error
        if not isinstance(payload, dict) or not isinstance(payload.get("projects"), list):
            raise RuntimeError("project list after update did not return a projects payload")
        saved_session_succeeded_after_update = True

    journey.step("run the core project list command after update", "platform.update", project_command,
                 cases=["S-023.4", "S-023.5"])

    if verify_user is not None:
        def login_after_update() -> None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"existing admin login failed after update: {' '.join(login.stdout.split())[-400:]}")

        journey.step("existing admin can log in after update", "platform.update", login_after_update,
                     cases=["S-023.4"])

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
        if not isinstance(receipt, dict) or not isinstance(snapshot, dict):
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
        human = run_command([launcher, "rollback", "--platform-root", root,
                             "--snapshot", snapshot_id, "--yes"])
        if human.returncode != 0 or "Platform rollback completed." not in human.stdout:
            raise RuntimeError("human rollback did not show its completion summary")
        result = _json_command(run_command, [launcher, "rollback", "--platform-root", root,
                                            "--snapshot", snapshot_id, "--yes", "--json"], "rollback platform")
        snapshot = result.get("snapshot")
        if not isinstance(snapshot, dict) or not snapshot.get("id"):
            raise RuntimeError("rollback did not return ok=true and the restored snapshot")
        if result.get("hostRestarted") is not True or not result.get("logPath"):
            raise RuntimeError("rollback did not report host restart and an operation log")
        record_config_checkpoint("afterRollbackHostRestart")
        record_secret_checkpoint("afterRollbackHostRestart")

    journey.step("roll back to version N with explicit non-TTY confirmation", "platform.rollback", rollback,
                 cases=["S-024.2", "S-203.10"], details=checkpoint_details)

    def reject_unknown_snapshot() -> None:
        before = receipt_path.read_bytes()
        result = run_command([launcher, "rollback", "--platform-root", root, "--snapshot",
                              "missing-journey-snapshot", "--yes", "--json"])
        if result.returncode == 0:
            raise RuntimeError("rollback unexpectedly accepted an unknown snapshot")
        try:
            envelope = read_ux_cli_result(result.stdout, result.returncode)
            error = decode_ux_error_result(envelope)
        except ValueError as error:
            raise RuntimeError(f"unknown-snapshot rollback returned an invalid ADR-0049 result: {error}") from error
        if not isinstance(error, dict) or not error.get("code") or not error.get("hint") or not error.get("correlationId"):
            raise RuntimeError("unknown-snapshot rollback omitted its structured recovery error or hint")
        if receipt_path.read_bytes() != before:
            raise RuntimeError("unknown-snapshot rollback changed the active receipt")
        record_config_checkpoint("afterMissingSnapshotRecovery")
        record_secret_checkpoint("afterMissingSnapshotRecovery")

    journey.step("reject a missing snapshot without changing the active install", "platform.rollback",
                 reject_unknown_snapshot, cases=["S-024.4", "S-203.11"], details=checkpoint_details)

    def verify_rollback() -> str | None:
        receipt = json.loads(receipt_path.read_text())
        version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
        if version != base_version:
            raise RuntimeError(f"restored receipt is {version!r}, expected {base_version}")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "restored status")
        if status.get("running") is not True or status.get("reachable") is not True:
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
        before_secret = secret_checkpoints.get("beforeUpdate")
        after_update_secret = secret_checkpoints.get("afterUpdateHostRestart")
        after_rollback_secret = secret_checkpoints.get("afterRollbackHostRestart")
        after_recovery_secret = secret_checkpoints.get("afterMissingSnapshotRecovery")
        if before_secret is None:
            return "BLOCKED: installation-secret fingerprint is unavailable before update"
        if after_update_secret is None or after_rollback_secret is None or after_recovery_secret is None:
            return "BLOCKED: installation-secret fingerprint is unavailable at an update or rollback checkpoint"
        if before_secret != after_update_secret:
            raise RuntimeError(
                "installation-secret fingerprint changed during update: "
                f"beforeUpdate={before_secret}, afterUpdateHostRestart={after_update_secret}"
            )
        if before_secret != after_rollback_secret:
            raise RuntimeError(
                "installation-secret fingerprint changed during rollback: "
                f"beforeUpdate={before_secret}, afterRollbackHostRestart={after_rollback_secret}"
            )
        if after_recovery_secret != after_rollback_secret:
            raise RuntimeError(
                "installation-secret fingerprint changed during missing-snapshot recovery: "
                f"afterRollbackHostRestart={after_rollback_secret}, "
                f"afterMissingSnapshotRecovery={after_recovery_secret}"
            )
        if verify_user is not None:
            if not saved_user_session_established:
                raise RuntimeError("saved user session was not established before update")
            if not saved_session_succeeded_after_update:
                raise RuntimeError("saved user session did not run a protected command successfully after update")
        command = run_command([kb_binary, "project", "list", "--json"])
        try:
            envelope = read_ux_cli_result(command.stdout, command.returncode)
            if command.returncode != 0:
                failure = decode_ux_error_result(envelope)
                raise RuntimeError(f"project list after rollback was rejected: {failure!r}")
            payload = decode_ux_success_result(envelope)
        except ValueError as error:
            raise RuntimeError(f"project list after rollback returned an invalid UX result: {error}") from error
        if not isinstance(payload, dict) or not isinstance(payload.get("projects"), list):
            raise RuntimeError("project list after rollback did not return a projects payload")

    journey.step("verify restored receipt, config, host, and project command", "platform.rollback", verify_rollback,
                 cases=["S-024.3", "S-041.4"] if verify_user is not None else ["S-024.3"], details=checkpoint_details)

    if verify_user is not None:
        def login_after_rollback() -> None:
            login = verify_user()
            if login.returncode != 0:
                raise RuntimeError(f"existing admin login failed after rollback: {' '.join(login.stdout.split())[-400:]}")

        journey.step("existing admin can log in after rollback", "platform.rollback", login_after_rollback,
                     cases=["S-024.3"])

    def doctor() -> None:
        result = _json_command(run_command, [launcher, "doctor", "--platform-root", root, "--json"], "doctor")
        if result.get("platform", {}).get("ok") is not True:
            raise RuntimeError("kb-create doctor did not report a healthy restored platform")

    journey.step("diagnose the restored platform", "platform.rollback", doctor, cases=["S-024.5", "S-203.10"])
    return journey
