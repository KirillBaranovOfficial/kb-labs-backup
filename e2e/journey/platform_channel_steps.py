"""Installed-platform channel switch and rollback journey steps."""
from __future__ import annotations

import json
import subprocess
from pathlib import Path
from typing import Callable

from platform_update_steps import _json_command
from invocations import decode_ux_error_result, decode_ux_success_result, read_ux_cli_result
from journey_resources import active_platform_root
from service_steps import Journey


CommandRunner = Callable[[list[str]], subprocess.CompletedProcess]


def run_platform_channel_steps(
    *, mode: str, launcher: str, platform_root: Path, stable_index: Path,
    canary_index: Path, request_file: Path, stable_version: str,
    canary_version: str, fresh_root: Path, project_root: Path,
    run_command: CommandRunner,
) -> Journey:
    journey = Journey(mode, f"platform-channels-{mode}")
    root = str(platform_root)
    receipt_path = active_platform_root(platform_root) / ".kb/v2/receipt.json"
    request = json.loads(request_file.read_text())
    resource_block_reason: str | None = None

    def blocked_by_canary_preflight() -> str | None:
        if resource_block_reason is None:
            return None
        return f"BLOCKED: {resource_block_reason}"

    def strict_exact_matrix_plan() -> None:
        strict_request = json.loads(request_file.read_text())
        strict_request["platform"] = {"version": stable_version}
        strict_request["sdk"] = {"version": stable_version}
        strict_request["policy"] = "strict"
        strict_path = request_file.with_name("strict-exact-request.json")
        strict_path.write_text(json.dumps(strict_request) + "\n")
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                                           "--input", str(strict_path)], "strict exact plan")
        resolved = plan.get("plan", {})
        if resolved.get("request", {}).get("policy") != "strict":
            raise RuntimeError("strict policy did not resolve the exact matrix request")
        if resolved.get("serviceGraph", {}).get("platformVersion") != stable_version:
            raise RuntimeError("strict policy plan did not resolve the exact stable platform")
        if not any(item.get("kind") == "sdk" and item.get("version") == stable_version for item in resolved.get("artifacts", [])):
            raise RuntimeError("strict policy plan did not resolve the exact matrix SDK")

    journey.step("resolve exact platform and SDK relation under strict policy", "platform.compatibility",
                 strict_exact_matrix_plan, cases=["S-053.1"])

    def apply_fresh_canary() -> str | None:
        nonlocal resource_block_reason
        stopped = run_command([launcher, "stop", "--platform-root", root, "--json"])
        if stopped.returncode != 0:
            raise RuntimeError("stable host could not stop before the fresh canary install")
        try:
            args = [launcher, "apply", "--json", "--index", str(canary_index),
                    "--platform-channel", "canary", "--policy", "compatible",
                    "--sdk-version", canary_version,
                    "--platform-root", str(fresh_root),
                    "--project-root", str(project_root)]
            command = run_command(args)
            try:
                envelope = read_ux_cli_result(command.stdout, command.returncode)
                result = (decode_ux_success_result(envelope) if command.returncode == 0
                          else decode_ux_error_result(envelope))
            except ValueError as error:
                raise RuntimeError(f"apply canary: expected an ADR-0049 result: {error}") from error
            if not isinstance(result, dict):
                raise RuntimeError("apply canary: expected a JSON object")
            if command.returncode != 0:
                error = result
                if isinstance(error, dict) and error.get("code") == "KB_INSTALL_MEMORY_INSUFFICIENT":
                    details = error.get("meta", {})
                    available = details.get("available", "unknown") if isinstance(details, dict) else "unknown"
                    minimum = details.get("minimum", "unknown") if isinstance(details, dict) else "unknown"
                    resource_block_reason = (
                        f"canary install preflight requires {minimum} available memory; {available} is available. "
                        "Free memory or increase machine memory, then rerun `kb-create install`."
                    )
                    restarted = run_command([launcher, "start", "--platform-root", root, "--json"])
                    if restarted.returncode != 0:
                        raise RuntimeError("stable host could not restart after the canary memory preflight block")
                    return f"BLOCKED: {resource_block_reason}"
                if isinstance(error, dict):
                    code, message, hint = error.get("code", "unknown error"), error.get("message", ""), error.get("hint", "")
                    raise RuntimeError(f"apply canary failed ({code}): {message} {hint}".strip())
                raise RuntimeError(f"apply canary failed with exit {command.returncode}")
            plan = result.get("receipt", {}).get("plan", {})
            if plan.get("platformChannel") != "canary" or plan.get("serviceGraph", {}).get("platformVersion") != canary_version:
                raise RuntimeError("fresh apply did not record the exact canary channel and version")
            artifacts = plan.get("artifacts", [])
            if not any(item.get("kind") == "platform" and item.get("version") == canary_version and item.get("sha256") for item in artifacts):
                raise RuntimeError("fresh canary receipt omitted the immutable platform artifact hash")
            if not any(item.get("kind") == "sdk" and item.get("version") == canary_version and item.get("sha256") for item in artifacts):
                raise RuntimeError("fresh canary receipt omitted the immutable SDK artifact hash")
        except Exception:
            run_command([launcher, "uninstall", "--platform-root", str(fresh_root), "--yes", "--json"])
            run_command([launcher, "start", "--platform-root", root, "--json"])
            raise

    journey.step("apply a fresh installation from the sealed canary channel", "platform.install",
                 apply_fresh_canary, cases=["S-052.1"])

    def verify_fresh_status_and_doctor() -> None:
        blocked = blocked_by_canary_preflight()
        if blocked:
            return blocked
        fresh = str(fresh_root)
        try:
            fresh_receipt = json.loads((active_platform_root(fresh_root) / ".kb/v2/receipt.json").read_text())
            plan = fresh_receipt.get("plan", {})
            if plan.get("platformChannel") != "canary" or plan.get("serviceGraph", {}).get("platformVersion") != canary_version:
                raise RuntimeError("fresh canary receipt does not record channel and exact version")
            status = _json_command(run_command, [launcher, "status", "--platform-root", fresh, "--json"], "fresh status")
            doctor = _json_command(run_command, [launcher, "doctor", "--platform-root", fresh, "--json"], "fresh doctor")
            for label, output in (("status", status.get("installation", status)), ("doctor", doctor)):
                if output.get("platformChannel") != "canary" or output.get("platformVersion") != canary_version:
                    raise RuntimeError(f"fresh {label} JSON does not report channel and exact version")
            for command in ("status", "doctor"):
                output = run_command([launcher, command, "--platform-root", fresh])
                if output.returncode != 0 or "canary" not in output.stdout or canary_version not in output.stdout:
                    raise RuntimeError(f"fresh {command} text does not report channel and exact version")
        finally:
            removed = run_command([launcher, "uninstall", "--platform-root", fresh, "--yes", "--json"])
            if removed.returncode != 0:
                raise RuntimeError("fresh canary installation could not be removed")
            restarted = run_command([launcher, "start", "--platform-root", root, "--json"])
            if restarted.returncode != 0:
                raise RuntimeError("stable host did not restart after the fresh canary check")

    journey.step("verify canary channel and version in receipt, status, and doctor", "platform.status",
                 verify_fresh_status_and_doctor, cases=["S-052.2", "S-203.10"])

    def switch_to_canary() -> None:
        blocked = blocked_by_canary_preflight()
        if blocked:
            return blocked
        nonlocal request
        request = json.loads(request_file.read_text())
        request["platform"] = {"channel": "canary"}
        request["sdk"] = {"version": canary_version}
        request_file.write_text(json.dumps(request) + "\n")
        human = run_command([launcher, "update", "--index", str(canary_index), "--input", str(request_file)])
        if human.returncode != 0 or "Platform update completed." not in human.stdout:
            raise RuntimeError("human update did not show its completion summary")
        result = _json_command(run_command, [launcher, "update", "--json", "--index", str(canary_index),
                                             "--input", str(request_file)], "update to canary")
        receipt = result.get("receipt", {})
        plan = receipt.get("plan", {})
        if result.get("hostRestarted") is not True:
            raise RuntimeError("canary update did not restart the installed host")
        if plan.get("platformChannel") != "canary" or plan.get("serviceGraph", {}).get("platformVersion") != canary_version:
            raise RuntimeError("canary update receipt did not identify its channel and exact version")

    journey.step("update the installed host from stable to canary", "platform.update", switch_to_canary,
                 cases=["S-052.3", "S-052.6", "S-203.10"])

    def verify_channel_fields() -> None:
        blocked = blocked_by_canary_preflight()
        if blocked:
            return blocked
        receipt = json.loads(receipt_path.read_text())
        plan = receipt.get("plan", {})
        if plan.get("platformChannel") != "canary" or plan.get("serviceGraph", {}).get("platformVersion") != canary_version:
            raise RuntimeError("active receipt does not identify canary and its exact version")
        status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"], "canary status")
        doctor = _json_command(run_command, [launcher, "doctor", "--platform-root", root, "--json"], "canary doctor")
        for label, output in (("status", status.get("installation", status)), ("doctor", doctor)):
            if output.get("platformChannel") != "canary" or output.get("platformVersion") != canary_version:
                raise RuntimeError(f"{label} JSON does not identify canary and its exact version")
        text_status = run_command([launcher, "status", "--platform-root", root])
        text_doctor = run_command([launcher, "doctor", "--platform-root", root])
        for label, output in (("status", text_status), ("doctor", text_doctor)):
            if output.returncode != 0 or "canary" not in output.stdout or canary_version not in output.stdout:
                raise RuntimeError(f"{label} text does not identify canary and its exact version")

    journey.step("verify receipt, status, and doctor show channel and version", "platform.status",
                 verify_channel_fields, cases=["S-052.2", "S-203.10"])

    def reject_stable_downgrade() -> None:
        blocked = blocked_by_canary_preflight()
        if blocked:
            return blocked
        before = receipt_path.read_bytes()
        request["platform"] = {"channel": "stable"}
        request["sdk"] = {"version": stable_version}
        request_file.write_text(json.dumps(request) + "\n")
        result = run_command([launcher, "update", "--json", "--index", str(stable_index), "--input", str(request_file)])
        try:
            envelope = read_ux_cli_result(result.stdout, result.returncode)
            failure = decode_ux_error_result(envelope)
        except ValueError as error:
            raise RuntimeError(f"stable downgrade refusal returned an invalid ADR-0049 result: {error}") from error
        if result.returncode == 0 or failure.get("code") != "KB_UPDATE_DOWNGRADE_REFUSED":
            raise RuntimeError("update did not refuse the stable downgrade with KB_UPDATE_DOWNGRADE_REFUSED")
        if not failure.get("hint"):
            raise RuntimeError("stable downgrade error did not direct the user to kb-create rollback")
        details = failure.get("meta", {})
        if details.get("installedVersion") != canary_version or details.get("targetVersion") != stable_version:
            raise RuntimeError("stable downgrade error omitted installedVersion or targetVersion")
        if receipt_path.read_bytes() != before:
            raise RuntimeError("refused stable downgrade changed the active receipt")

    journey.step("refuse update from canary to older stable release", "platform.update",
                 reject_stable_downgrade, cases=["S-052.4"])

    def rollback_to_stable() -> None:
        blocked = blocked_by_canary_preflight()
        if blocked:
            return blocked
        human = run_command([launcher, "rollback", "--platform-root", root, "--yes"])
        if human.returncode != 0 or "Platform rollback completed." not in human.stdout:
            raise RuntimeError("human rollback did not show its completion summary")
        result = _json_command(run_command, [launcher, "rollback", "--platform-root", root, "--yes", "--json"],
                               "rollback from canary")
        if result.get("hostRestarted") is not True:
            raise RuntimeError("rollback did not restore and restart the stable host")
        receipt = json.loads(receipt_path.read_text())
        plan = receipt.get("plan", {})
        if plan.get("platformChannel") != "stable" or plan.get("serviceGraph", {}).get("platformVersion") != stable_version:
            raise RuntimeError("rollback did not restore the exact stable receipt")

    journey.step("roll back from canary to the exact stable snapshot", "platform.rollback",
                 rollback_to_stable, cases=["S-052.5", "S-203.10"])
    return journey
