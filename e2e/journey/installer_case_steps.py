"""Additional install, repair, and request-selection case claims."""
from __future__ import annotations
from journey_values import journey_value

import json
import hashlib
import os
import platform
import re
import shutil
import shlex
import socket
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path
from typing import Callable
from urllib.parse import urlsplit

from platform_update_steps import _json_command
from invocations import (decode_ux_error_result, decode_ux_success_result, read_ux_cli_result,
                         read_ux_cli_stream, run_command as evidence_run_command)
from journey_resources import (installer_case_fixture_root, installer_scenario_resources,
                               active_platform_root, create_fixture_install_container,
                               snapshot_tree, workspace_resource)
from service_port_assertions import (assert_root_offset, service_ports,
                                     service_transport_ports)
from service_steps import Journey

CommandRunner = Callable[..., subprocess.CompletedProcess]


def parse_install_refusal(stdout: str) -> dict | None:
    """Return the typed product envelope from a failed JSON install stream."""
    for line in stdout.splitlines():
        try:
            return decode_ux_error_result(json.loads(line))
        except (json.JSONDecodeError, ValueError, AttributeError):
            continue
    return None


def _hint_has_argv(text: str, expected: list[str]) -> bool:
    for command in re.findall(r"`([^`]*)`", text):
        try:
            if shlex.split(command)[:len(expected)] == expected:
                return True
        except ValueError:
            continue
    return False


class InstallerHostLifecycle:
    """Single owner for inspecting, changing, and restoring fixture host state."""

    def __init__(self, launcher: str, run_command: CommandRunner):
        self.launcher = launcher
        self.run_command = run_command

    def state(self, root: Path) -> tuple[bool, dict]:
        status = _json_command(self.run_command,
                               [self.launcher, "status", "--platform-root", str(root), "--json"],
                               f"host lifecycle status for {root}")
        host = status.get("host", {})
        return isinstance(host, dict) and host.get("state") == "running", status

    def set_running(self, root: Path, running: bool) -> None:
        operation = "start" if running else "stop"
        _json_command(self.run_command,
                      [self.launcher, operation, "--platform-root", str(root), "--json"],
                      f"{operation} host for {root}")

    @contextmanager
    def preserve(self, roots: list[Path]):
        initial = {root: self.state(root)[0] for root in roots}
        action_error: Exception | None = None
        try:
            yield initial
        except Exception as error:
            action_error = error
        finally:
            failures = []
            for root, was_running in reversed(list(initial.items())):
                try:
                    running, _ = self.state(root)
                    if running != was_running:
                        self.set_running(root, was_running)
                except Exception as error:
                    failures.append(f"{root}: {error}")
            if failures:
                message = "could not restore installer host state: " + "; ".join(failures)
                if action_error is not None:
                    raise RuntimeError(f"{action_error}; {message}") from action_error
                raise RuntimeError(message)
        if action_error is not None:
            raise action_error


def _data(result: subprocess.CompletedProcess, label: str) -> dict:
    _, payload = _payload(result, label)
    if result.returncode == 0 and isinstance(payload, dict):
        return payload
    raise RuntimeError(f"{label}: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")


def _payload(result: subprocess.CompletedProcess, label: str) -> tuple[dict, dict]:
    try:
        envelope = read_ux_cli_result(result.stdout, result.returncode)
        data = (decode_ux_success_result(envelope) if result.returncode == 0
                else decode_ux_error_result(envelope))
    except ValueError as error:
        try:
            records = read_ux_cli_stream(result.stdout, result.returncode)
        except ValueError:
            raise RuntimeError(f"{label}: expected an ADR-0049 JSON result or stream: {error}") from error
        if result.returncode == 0:
            envelope = next((record for record in reversed(records[:-1])
                             if record.get("meta", {}).get("stream", {}).get("result") is True), None)
            if envelope is None:
                raise RuntimeError(f"{label}: successful CLI stream omitted its result record")
            data = decode_ux_success_result(envelope)
        else:
            envelope = next((record for record in records[:-1] if record.get("ok") is False), None)
            if envelope is None:
                raise RuntimeError(f"{label}: failed CLI stream omitted its error result")
            data = decode_ux_error_result(envelope)
    return envelope, data if isinstance(data, dict) else {}


def _diagnostic(result: subprocess.CompletedProcess, label: str) -> tuple[dict, dict]:
    envelope, error = _payload(result, label)
    if result.returncode == 0 or not isinstance(error, dict):
        raise RuntimeError(f"{label}: expected a structured error")
    return envelope, error


def run_port_conflict_step(*, mode: str, launcher: str, stable_index: Path,
                           platform_root: Path, run_command: CommandRunner) -> Journey:
    """Prove apply reports a foreign owner for the Gateway port it resolved."""
    journey = Journey(mode, f"port-conflict-{mode}")
    conflict_root = platform_root.parent / "pc001-port-conflict-root"

    def port_conflict_ux() -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                                           "--platform-root", str(conflict_root),
                                           "--service-profile", "default", "--scenario", "commit"],
                             "PC-001 port-conflict plan").get("plan", {})
        health_url = plan.get("host", {}).get("healthUrl")
        port = urlsplit(health_url).port if isinstance(health_url, str) else None
        if port is None:
            raise RuntimeError("PC-001 port-conflict plan omitted its Gateway port")
        with socket.socket() as owner:
            owner.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            owner.bind(("127.0.0.1", port))
            owner.listen(1)
            result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                  "--platform-root", str(conflict_root),
                                  "--service-profile", "default", "--scenario", "commit"])
        envelope, error = _diagnostic(result, "PC-001 port conflict")
        details = error.get("details", error.get("meta", {}))
        if (result.returncode == 0 or envelope.get("ok") is not False
                or error.get("code") != "KB_INSTALL_PORT_IN_USE"
                or not isinstance(details, dict)
                or details.get("port") != str(port) or not details.get("pid") or not details.get("command")
                or "kb-create apply" not in error.get("hint", "")
                or str(details["pid"]) not in error.get("hint", "")
                or str(details["command"]) not in error.get("hint", "")):
            raise RuntimeError(f"port conflict omitted its catalogued code, owner, or apply hint: {error}")

    journey.step("report the owning process when apply cannot claim a required port", "platform.install",
                 port_conflict_ux, cases=["PC-001.9"])
    return journey


def run_platform_cli(platform_root: Path, args: list[str], run_command: CommandRunner) -> subprocess.CompletedProcess:
    """Run the CLI installed for the requested root, independent of PATH shim ownership."""
    executable = active_platform_root(platform_root) / ".kb/v2/bin/kb"
    if not executable.is_file():
        raise RuntimeError(f"platform CLI is missing for {platform_root}: {executable}")
    return run_command([str(executable), *args])


def installer_primary_host_guard(root: Path, launcher: str,
                                 run_command: CommandRunner) -> Callable[[str, Callable[[], str | None]], str | None]:
    """Keep the primary host's run-state stable across each installer-case step."""
    lifecycle = InstallerHostLifecycle(launcher, run_command)

    def listener_context(payload: dict) -> str:
        host = payload.get("host", {})
        pid = host.get("pid", "unknown") if isinstance(host, dict) else "unknown"
        ports = []
        try:
            receipt = json.loads((active_platform_root(root) / ".kb/v2/receipt.json").read_text())
            plan = receipt.get("plan", {})
            host_url = plan.get("host", {}).get("healthUrl")
            if host_url and urlsplit(host_url).port:
                ports.append(str(urlsplit(host_url).port))
            ports.extend(str(port) for port in service_ports(plan).values())
        except (OSError, json.JSONDecodeError, AttributeError, TypeError) as error:
            return f"root {root}, pid {pid}, port(s) unavailable: {error}"
        listeners = listener_state(ports)
        rendered_listeners = ", ".join(f"{port}/{owner_pid} ({command})"
                                        for port, owner_pid, command in listeners) or "none"
        return (f"root {root}, pid {pid}, port(s) {', '.join(sorted(set(ports))) or 'unknown'}, "
                f"listeners {rendered_listeners}")

    def listener_state(ports: list[str]) -> tuple[tuple[str, str, str], ...]:
        owners = []
        for port in sorted(set(ports)):
            result = run_command(["lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN", "-Fp", "-Fc"])
            if result.returncode == 1:
                continue
            if result.returncode != 0:
                raise RuntimeError(f"could not inspect listener ownership for port {port}: {result.stderr.strip()}")
            pid = None
            command = None
            for line in result.stdout.splitlines():
                if line.startswith("p"):
                    pid = line[1:]
                elif line.startswith("c") and pid:
                    command = line[1:]
                    owners.append((port, pid, command))
                    pid = None
                    command = None
            if pid is not None:
                owners.append((port, pid, command or "unknown"))
            if not result.stdout.strip():
                continue
        return tuple(sorted(owners))

    def signature(payload: dict) -> tuple[bool, str, str, tuple[tuple[str, str, str], ...]]:
        host = payload.get("host", {})
        running = isinstance(host, dict) and host.get("state") == "running"
        pid = str(host.get("pid", "unknown")) if isinstance(host, dict) else "unknown"
        try:
            receipt = json.loads((active_platform_root(root) / ".kb/v2/receipt.json").read_text())
            plan = receipt.get("plan", {})
            ports = []
            host_url = plan.get("host", {}).get("healthUrl")
            if host_url and urlsplit(host_url).port:
                ports.append(str(urlsplit(host_url).port))
            ports.extend(str(port) for port in service_ports(plan).values())
            listeners = listener_state(ports)
        except (OSError, json.JSONDecodeError, AttributeError, TypeError) as error:
            return running, pid if running else "stopped", f"listener state unavailable: {error}", ()
        return running, pid if running else "stopped", ",".join(sorted(set(ports))), listeners

    def guard(name: str, check: Callable[[], str | None]) -> str | None:
        before_running, before = lifecycle.state(root)
        before_signature = signature(before)
        action_error: Exception | None = None
        message: str | None = None
        try:
            message = check()
        except Exception as error:
            action_error = error
        _, after = lifecycle.state(root)
        after_signature = signature(after)
        after_running = isinstance(after.get("host"), dict) and after["host"].get("state") == "running"
        violation = None
        if after_signature != before_signature:
            violation = (f"installer step {name!r} changed primary host run-state "
                         f"from {listener_context(before)} to {listener_context(after)}")
            try:
                if after_running != before_running:
                    lifecycle.set_running(root, before_running)
            except Exception as restore_error:
                violation += f"; restoration failed: {restore_error}"
        if action_error is not None:
            if violation:
                raise RuntimeError(f"{action_error}; {violation}") from action_error
            raise action_error
        if violation:
            raise RuntimeError(violation)
        return message

    return guard


def run_fixture_group(journey: Journey, label: str, run_fixture: Callable[[], Journey]) -> None:
    """Run a nested fixture journey inside the owning journey's host guard and keep its rows.

    The fixture always runs and always reports its claims: a guard that cannot
    observe the host is one failed row, not a reason to drop the group.
    """
    collected: list[Journey] = []
    failures: list[str] = []
    started = False

    def run() -> None:
        nonlocal started
        started = True
        collected.append(run_fixture())

    for attempt in (lambda: journey.step_guard(label, run), run):
        if started:
            break
        try:
            attempt()
        except Exception as error:
            failures.append(str(error) or error.__class__.__name__)
    for failure in failures:
        journey.step(f"installer fixture group: {label}", "journey.orchestration",
                     lambda failure=failure: failure, role="lifecycle")
    for fixture in collected:
        journey.steps.extend(fixture.steps)


def stop_installer_fixture_host(root: Path, launcher: str, run_command: CommandRunner) -> None:
    """Stop an installed fixture host before another journey action or cleanup."""
    try:
        active = active_platform_root(root)
    except FileNotFoundError:
        return
    if (active / ".kb/v2/receipt.json").is_file():
        lifecycle = InstallerHostLifecycle(launcher, run_command)
        if lifecycle.state(root)[0]:
            lifecycle.set_running(root, False)


def cleanup_installer_fixture_root(root: Path, launcher: str, run_command: CommandRunner) -> None:
    """Stop an installed fixture host before removing its platform files."""
    stop_installer_fixture_host(root, launcher, run_command)
    try:
        active = active_platform_root(root)
    except FileNotFoundError:
        active = None
    if active is not None and (active / ".kb/v2/receipt.json").is_file():
        _json_command(run_command, [launcher, "uninstall", "--platform-root", str(root), "--yes", "--json"],
                      f"uninstall installer fixture root {root}")
    shutil.rmtree(root, ignore_errors=True)


def _seed_doctor_fixture(source_root: Path, target_root: Path, *, requirement: dict | None = None,
                         missing_projection: bool = False, receipt_requirement: dict | None = None) -> Path:
    """Copy a real receipt identity and materialize only its manifest projections."""
    source_root = active_platform_root(source_root)
    active = json.loads((source_root / ".kb/v2/receipt.json").read_text())
    artifacts = active.get("plan", {}).get("artifacts", [])
    packages = [item for item in artifacts if item.get("kind") != "binary"]
    if not packages:
        raise RuntimeError("active receipt has no package artifacts for a doctor fixture")
    target_active = create_fixture_install_container(target_root)
    target_active.joinpath(".kb/v2").mkdir(parents=True, exist_ok=True)
    target_active.joinpath(".kb/kb.config.jsonc").write_text("{}\n")
    effective_requirement = receipt_requirement or requirement
    if effective_requirement is not None:
        selected = {"id": "journey-doctor-fixture", "kind": "plugin", "package": "@kb-labs/journey-doctor-fixture",
                    "version": "1.0.0", "config": [effective_requirement]}
        artifacts.append(selected)
        packages.append(selected)
    else:
        selected = packages[0]
    active.setdefault("plan", {}).setdefault("request", {})["platformRoot"] = str(target_active)
    target_active.joinpath(".kb/v2/receipt.json").write_text(json.dumps(active, indent=2) + "\n")
    for item in packages:
        package = item["package"]
        package_root = target_active / "node_modules" / package
        package_root.mkdir(parents=True, exist_ok=True)
        (package_root / "package.json").write_text(json.dumps({"name": package, "version": item["version"]}) + "\n")
        if package == "@kb-labs/cli-bin":
            source_dist = source_root / "node_modules" / package / "dist"
            shutil.copytree(source_dist, package_root / "dist", symlinks=True)
        if missing_projection and item is selected:
            continue
        manifest = {"schema": "kb.create.artifact-manifest/v2", "id": item["id"],
                    "package": package, "version": item["version"], "requirements": []}
        if effective_requirement is not None and item is selected:
            manifest["requirements"] = [effective_requirement]
        (package_root / "kb-create.manifest.json").write_text(json.dumps(manifest) + "\n")
    return selected


def _seed_update_fixture(target_root: Path, *, kind: str, component: str) -> Path:
    target_active = create_fixture_install_container(target_root)
    target_active.joinpath(".kb/v2").mkdir(parents=True, exist_ok=True)
    receipt_path = target_active / ".kb/v2/receipt.json"
    artifact = {"id": component, "kind": kind, "package": f"@kb/{component}", "version": "1.0.0",
                "sha256": "fixture-artifact", "platformRange": "<3.2.0"}
    receipt = {"schema": "kb.create.receipt/v2", "id": f"fixture-{kind}",
               "plan": {"schema": "kb.create.resolved-plan/v2", "artifacts": [artifact],
                        "serviceGraph": {"platformVersion": "3.0.0"},
                        "request": {"platformRoot": str(target_active)}}}
    receipt_path.write_text(json.dumps(receipt, indent=2) + "\n")
    return receipt_path


def run_uninstall_decline_step(*, mode: str, args: list[str], receipt_path: Path,
                               run_pty: Callable[..., tuple[subprocess.CompletedProcess, float]]) -> Journey:
    """Confirm terminal uninstall cancellation preserves the active receipt."""
    journey = Journey(mode, f"uninstall-decline-{mode}")

    def decline() -> None:
        before = receipt_path.read_bytes()
        prompt_seen = False

        def answer_no(transcript: str):
            nonlocal prompt_seen
            if not prompt_seen and "Continue? [y/N]" in transcript:
                prompt_seen = True
                return "n\n"
            return None

        result, _ = run_pty(args, on_output=answer_no)
        if (not prompt_seen or result.returncode != 1
                or "KB_INSTALL_OPERATION_CANCELLED" not in result.stdout
                or receipt_path.read_bytes() != before):
            raise RuntimeError("terminal decline did not preserve the receipt with KB_INSTALL_OPERATION_CANCELLED")

    journey.step("decline the terminal uninstall confirmation", "platform.uninstall", decline,
                 cases=["S-050.3"])
    return journey


def run_missing_project_request_steps(*, mode: str, launcher: str, stable_index: Path,
                                      home: Path, run_command: CommandRunner) -> Journey:
    """Reject a missing project in every request command before platform mutation."""
    journey = Journey(mode, f"missing-project-install-{mode}")

    def reject_missing_project(operations: tuple[str, ...]) -> None:
        def path_state(path: Path) -> tuple[str, bytes | str | None] | None:
            if path.is_symlink():
                return "symlink", str(path.readlink())
            if path.is_file():
                return "file", path.read_bytes()
            if path.is_dir():
                return "directory", None
            if path.exists():
                return "other", str(path.stat().st_mode)
            return None

        shim_path = Path(launcher).resolve().parent / "kb"
        registry_path = home / ".kb" / "projects.json"
        shim_before = path_state(shim_path)
        registry_before = path_state(registry_path)
        with tempfile.TemporaryDirectory(prefix="journey-missing-project-install-") as temp:
            root = Path(temp)
            project = root / "missing-project"
            for operation in operations:
                target = root / operation
                result = run_command([launcher, operation, "--json", "--index", str(stable_index),
                                      "--platform-root", str(target), "--project-root", str(project)])
                _, error = _diagnostic(result, f"{operation} with a missing project directory")
                hint = error.get("hint", "")
                receipt_path = target / ".kb" / "v2" / "receipt.json"
                scenario_path = target / ".kb" / "v2" / "scenarios" / "custom.json"
                shim_changed = path_state(shim_path) != shim_before
                registry_changed = path_state(registry_path) != registry_before
                if (result.returncode == 0 or error.get("code") != "KB_INSTALL_INPUT_REQUIRED"
                        or "must exist" not in error.get("message", "")
                        or "mkdir -p" not in hint or "existing project directory" not in hint
                        or target.exists() or receipt_path.exists() or shim_changed or registry_changed
                        or scenario_path.exists() or Path(str(scenario_path) + ".tmp").exists()):
                    raise RuntimeError(f"missing project {operation} was not rejected before mutation: "
                                       f"exit={result.returncode}, code={error.get('code')}, "
                                       f"platform root exists={target.exists()}, receipt exists={receipt_path.exists()}, "
                                       f"shim changed={shim_changed}, project registry changed={registry_changed}, "
                                       f"scenario state exists={scenario_path.exists()}")

    journey.step("reject installation when the selected project directory is missing",
                 "platform.install", lambda: reject_missing_project(("install",)), cases=["S-187.7"])
    journey.step("reject plan and apply when the selected project directory is missing",
                 "platform.install", lambda: reject_missing_project(("plan", "apply")), cases=["S-187.8"])
    return journey


def _without_jsonc_comments(source: str) -> str:
    """Remove JSONC comments without changing comment markers inside strings."""
    output: list[str] = []
    quoted = escaped = False
    index = 0
    while index < len(source):
        char = source[index]
        following = source[index + 1] if index + 1 < len(source) else ""
        if quoted:
            output.append(char)
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == '"':
                quoted = False
            index += 1
        elif char == '"':
            quoted = True
            output.append(char)
            index += 1
        elif char == "/" and following == "/":
            index += 2
            while index < len(source) and source[index] not in "\r\n":
                index += 1
        elif char == "/" and following == "*":
            index += 2
            while index + 1 < len(source) and source[index:index + 2] != "*/":
                index += 1
            index = min(len(source), index + 2)
        elif char == ",":
            lookahead = index + 1
            while lookahead < len(source) and source[lookahead].isspace():
                lookahead += 1
            if lookahead < len(source) and source[lookahead] in "]}":
                index += 1
            else:
                output.append(char)
                index += 1
        else:
            output.append(char)
            index += 1
    return "".join(output)


def run_analytics_invalid_retention_step(*, mode: str, launcher: str, platform_root: Path,
                                         run_command: CommandRunner) -> Journey:
    """Prove invalid SQLite retention config fails a real installed host start."""
    journey = Journey(mode, f"analytics-invalid-retention-{mode}")

    def invalid_retention() -> None:
        config_path = active_platform_root(platform_root) / ".kb/kb.config.jsonc"
        original_config = config_path.read_bytes()
        lifecycle = InstallerHostLifecycle(launcher, run_command)
        with lifecycle.preserve([platform_root]) as initial:
            action_error: Exception | None = None
            try:
                config = json.loads(_without_jsonc_comments(original_config.decode()))
                platform_config = config.setdefault("platform", {})
                options = platform_config.setdefault("adapterOptions", {})
                options.setdefault("analytics", {})["retentionDays"] = 0
                config_path.write_text(json.dumps(config, indent=2) + "\n")
                if initial[platform_root]:
                    lifecycle.set_running(platform_root, False)
                result = run_command([launcher, "start", "--platform-root", str(platform_root), "--json"])
                envelope, error = _diagnostic(result, "invalid analytics retention")
                detail = json.dumps(error, sort_keys=True)
                expected_hint = (
                    "kb config set --scope platform "
                    "platform.adapterOptions.analytics.retentionDays 90"
                )
                if (result.returncode != 1 or envelope.get("ok") is not False
                        or error.get("code") != "KB_CONFIG_INVALID"
                        or "platform.adapterOptions.analytics.retentionDays" not in detail
                        or "positive integer" not in detail
                        or expected_hint not in str(error.get("hint", ""))
                        or error.get("code") == "KB_RUNTIME_UNEXPECTED"):
                    raise RuntimeError(f"invalid analytics retention did not return its catalogued config error: {error}")
            except Exception as error:
                action_error = error
            try:
                config_path.write_bytes(original_config)
            except Exception as restore_error:
                if action_error is not None:
                    raise RuntimeError(f"{action_error}; could not restore the platform config: {restore_error}") from action_error
                raise
            if action_error is not None:
                raise action_error

    journey.step("reject invalid SQLite retention during installed host startup", "platform.install",
                 invalid_retention, cases=["S-191.7"])
    return journey


def run_offline_plan_step(*, mode: str, launcher: str, stable_index: Path,
                          platform_root: Path, run_command: CommandRunner) -> Journey:
    """Populate a dedicated root's verified artifact cache, then resolve offline from it."""
    journey = Journey(mode, f"offline-plan-{mode}")

    def offline_plan() -> None:
        root = platform_root.parent / "s181-offline-root"
        lifecycle = InstallerHostLifecycle(launcher, run_command)
        with lifecycle.preserve([platform_root]) as initial:
            primary_was_running = initial[platform_root]
            if primary_was_running:
                lifecycle.set_running(platform_root, False)
            _data(run_command([launcher, "apply", "--json", "--index", str(stable_index),
                               "--platform-channel", "stable", "--platform-root", str(root)]),
                  "online cache priming apply")
            plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                                               "--platform-channel", "stable", "--offline",
                                               "--platform-root", str(root)], "offline plan").get("plan", {})
            artifacts = plan.get("artifacts", [])
            if (plan.get("request", {}).get("source") != "offline" or not artifacts
                    or any(item.get("resolvedFrom") != "cache" or item.get("resolvedUrl") for item in artifacts)):
                raise RuntimeError("offline plan omitted cache provenance or claimed registry/network resolution")

    journey.step("resolve a fresh request from the local artifact cache", "platform.install", offline_plan,
                 cases=["S-181.4"])
    return journey


def run_secondary_root_gateway_isolation_step(*, mode: str, launcher: str, primary_root: Path,
                                             secondary_root: Path, run_command: CommandRunner) -> Journey:
    """Check the secondary root while live, then verify its stopped-host error."""
    journey = Journey(mode, f"secondary-gateway-isolation-{mode}")
    try:
        primary_receipt = active_platform_root(primary_root) / ".kb/v2/receipt.json"
        secondary_receipt = active_platform_root(secondary_root) / ".kb/v2/receipt.json"
    except FileNotFoundError as error:
        journey.step("isolate the secondary Gateway from the primary root", "platform.install",
                     lambda: f"prerequisite installation is missing: {error}", cases=["PC-001.12"])
        return journey

    def gateway_isolation() -> None:
        primary_installation = json.loads(primary_receipt.read_text()).get("installationId")
        secondary_data = json.loads(secondary_receipt.read_text())
        secondary_installation = secondary_data.get("installationId")
        gateway_url = secondary_data.get("verification", {}).get("hostHealthUrl", "")
        primary_gateway = json.loads(primary_receipt.read_text()).get("verification", {}).get("hostHealthUrl", "")
        primary_port, secondary_port = urlsplit(primary_gateway).port, urlsplit(gateway_url).port
        if not primary_port or not secondary_port or secondary_port == primary_port:
            raise RuntimeError(f"secondary root receipt does not declare a distinct Gateway address: {gateway_url}")

        lifecycle = InstallerHostLifecycle(launcher, run_command)
        with lifecycle.preserve([primary_root, secondary_root]) as initial:
            for root in (primary_root, secondary_root):
                if not initial[root]:
                    lifecycle.set_running(root, True)

            live = run_platform_cli(secondary_root, ["adapter", "list", "--json"], run_command)
            _, live_data = _payload(live, "secondary root adapter list while running")
            if not isinstance(live_data.get("adapters"), list):
                raise RuntimeError("running secondary root adapter list omitted adapter state")

            lifecycle.set_running(secondary_root, False)
            stopped = run_platform_cli(secondary_root, ["adapter", "list", "--json"], run_command)
            _, error = _diagnostic(stopped, "secondary root adapter list while stopped")
            unreachable_hint = f'Run "kb-create start --platform-root {secondary_root}" and retry the command.'
            unreachable = error.get("code") == "KB_HOST_UNREACHABLE" and error.get("hint") == unreachable_hint
            primary_answers = (error.get("code") == "KB_HOST_IDENTITY_MISMATCH"
                               and error.get("details", {}).get("actualInstallationId") == primary_installation
                               and error.get("details", {}).get("expectedInstallationId") == secondary_installation)
            if stopped.returncode == 0 or not (unreachable or primary_answers):
                raise RuntimeError(f"stopped secondary root did not identify its own unavailable Gateway: {error}")

    journey.step("verify the secondary root's Gateway while running and stopped", "platform.install",
                 gateway_isolation, cases=["PC-001.12"])
    return journey


def run_gateway_allocation_step(*, mode: str, launcher: str, stable_index: Path,
                                primary_root: Path, automatic_root: Path,
                                explicit_root: Path, run_command: CommandRunner) -> Journey:
    """Verify the automatically assigned root address and explicit override."""
    journey = Journey(mode, f"gateway-allocation-{mode}")

    def verify_gateway_addresses() -> None:
        primary_receipt = json.loads((active_platform_root(primary_root) / ".kb/v2/receipt.json").read_text())
        automatic_receipt = json.loads((active_platform_root(automatic_root) / ".kb/v2/receipt.json").read_text())
        primary_url = primary_receipt.get("verification", {}).get("hostHealthUrl", "")
        automatic_url = automatic_receipt.get("verification", {}).get("hostHealthUrl", "")
        primary_port, automatic_port = urlsplit(primary_url).port, urlsplit(automatic_url).port
        if not primary_port or automatic_port != primary_port + 100:
            raise RuntimeError(f"no-flag apply did not allocate the next Gateway offset: {automatic_url}")
        automatic_plan = automatic_receipt.get("plan", {})
        automatic_services = automatic_plan.get("serviceGraph", {}).get("services", [])
        primary_plan = primary_receipt.get("plan", {})
        primary_ports = service_ports(primary_plan)
        primary_transport_ports = service_transport_ports(primary_plan)
        assert_root_offset(automatic_plan, primary_ports, primary_transport_ports, 100,
                           "secondary receipt")

        InstallerHostLifecycle(launcher, run_command).set_running(automatic_root, False)

        explicit_installed = False
        try:
            result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                  "--platform-channel", "stable", "--platform-root", str(explicit_root),
                                  "--gateway-url", "http://127.0.0.1:4200"])
            explicit_installed = (active_platform_root(explicit_root) / ".kb/v2/receipt.json").is_file()
            data = _data(result, "explicit Gateway apply")
            explicit_receipt = data.get("receipt", {})
            explicit_url = explicit_receipt.get("verification", {}).get("hostHealthUrl", "")
            if explicit_url != "http://127.0.0.1:4200/health":
                raise RuntimeError(f"explicit Gateway override was not recorded in the receipt: {explicit_url}")

            next_plan = _data(run_command([launcher, "plan", "--json", "--index", str(stable_index),
                                           "--platform-channel", "stable", "--platform-root",
                                           str(explicit_root.parent / "root-d")]), "plan after stopped root allocation").get("plan", {})
            next_url = next_plan.get("request", {}).get("gatewayUrl", "")
            if urlsplit(next_url).port != automatic_port + 200:
                raise RuntimeError(f"stopped root did not retain its allocation: new root planned {next_url}")
            assert_root_offset(next_plan, primary_ports, primary_transport_ports, 300,
                               "next planned root")

            planned = _data(run_command([launcher, "plan", "--json", "--index", str(stable_index),
                                         "--platform-channel", "stable", "--platform-root",
                                         str(automatic_root)]), "repeat automatic Gateway plan").get("plan", {})
            if (planned.get("request", {}).get("gatewayUrl") != automatic_url.removesuffix("/health")
                    or planned.get("host", {}).get("healthUrl") != automatic_url
                    or planned.get("serviceGraph", {}).get("services") != automatic_services):
                raise RuntimeError(f"replanning changed the stopped root's Gateway or service ports: {planned}")
        finally:
            if explicit_installed:
                stop_installer_fixture_host(explicit_root, launcher, run_command)

    journey.step("allocate an unused Gateway address by default and honor an explicit override",
                 "platform.install", verify_gateway_addresses, cases=["PC-001.10", "PC-001.16"])
    return journey


def run_json_install_project_step(*, mode: str, launcher: str, stable_index: Path,
                                  platform_root: Path, project_root: Path,
                                  run_command: CommandRunner) -> Journey:
    """Install a custom scenario in JSON mode and verify its project registry entry."""
    journey = Journey(mode, f"json-install-project-{mode}")
    installed = False

    def install_and_show() -> None:
        nonlocal installed
        platform_root.parent.mkdir(parents=True, exist_ok=True)
        project_root.mkdir(parents=True, exist_ok=True)
        try:
            result = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                  "--platform-root", str(platform_root), "--service-profile", "default",
                                  "--scenario", "custom", "--scenario-answers", '{"access.mode":"local"}',
                                  "--project-root", str(project_root)])
            _data(result, "JSON custom-scenario install with project")
            shown = _data(run_platform_cli(platform_root, ["project", "show", str(project_root), "--json"], run_command),
                          "registered project after JSON install").get("project", {})
            if shown.get("path") != str(project_root.resolve()) or not shown.get("id"):
                raise RuntimeError(f"JSON install did not register the canonical project path: {shown}")
            repeated = _data(run_platform_cli(platform_root,
                                              ["project", "add", str(project_root), "--json"], run_command),
                             "idempotent add of installer-registered project")
            if repeated.get("alreadyExisted") is not True or repeated.get("project", {}).get("id") != shown.get("id"):
                raise RuntimeError("duplicate project add did not return the existing registration")
            if (project_root / ".kb").exists():
                raise RuntimeError("JSON project registration wrote KB Labs files into the project repository")
            installed = True
        except Exception:
            cleanup_installer_fixture_root(platform_root, launcher, run_command)
            raise

    def update_after_project_removed() -> None:
        if not installed:
            raise RuntimeError("project install fixture did not complete")
        request_path = platform_root.parent / "s187-receipt-request.json"
        try:
            receipt = json.loads((active_platform_root(platform_root) / ".kb/v2/receipt.json").read_text())
            request_path.write_text(json.dumps(receipt["plan"]["request"]))
            shutil.rmtree(project_root)
            updated = _data(run_command([launcher, "update", "--json", "--index", str(stable_index),
                                         "--input", str(request_path)]),
                            "update after registered project directory was removed")
            if updated.get("ok") is not True or not updated.get("alreadyUpToDate"):
                raise RuntimeError(f"update did not accept the receipt request after project removal: {updated}")
        finally:
            request_path.unlink(missing_ok=True)
            cleanup_installer_fixture_root(platform_root, launcher, run_command)

    def reject_changed_project_selection() -> None:
        selected = platform_root.parent / "s187-changed-project"
        selected.mkdir(parents=True, exist_ok=True)
        receipt_path = active_platform_root(platform_root) / ".kb/v2/receipt.json"
        before = receipt_path.read_bytes()
        result = run_command([launcher, "install", "--json", "--index", str(stable_index),
                              "--platform-root", str(platform_root), "--project-root", str(selected)])
        _, failure = _diagnostic(result, "install with a changed project selection")
        if (failure.get("code") != "KB_INSTALL_PROJECT_SELECTION_CHANGED"
                or not _hint_has_argv(failure.get("hint", ""), ["kb", "project", "add"])
                or not _hint_has_argv(failure.get("hint", ""), ["kb", "project", "show"])
                or receipt_path.read_bytes() != before):
            raise RuntimeError("changed project selection was not refused before the active receipt changed")
        apply_result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                    "--platform-root", str(platform_root), "--project-root", str(selected)])
        _, apply_failure = _diagnostic(apply_result, "apply with a changed project selection")
        if (apply_failure.get("code") != "KB_INSTALL_PROJECT_SELECTION_CHANGED"
                or not _hint_has_argv(apply_failure.get("hint", ""), ["kb", "project", "add"])
                or not _hint_has_argv(apply_failure.get("hint", ""), ["kb", "project", "show"])
                or receipt_path.read_bytes() != before):
            raise RuntimeError("apply changed the active receipt instead of refusing the selection")

    def accept_existing_project_selections() -> None:
        root = platform_root.parent / "s187-repeat-install"
        selected_project = platform_root.parent / "s187-repeat-project"
        root.mkdir(parents=True, exist_ok=True)
        selected_project.mkdir(parents=True, exist_ok=True)
        registry_path = Path.home() / ".kb/projects.json"
        try:
            initial = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                   "--platform-root", str(root), "--service-profile", "default",
                                   "--scenario", "custom", "--scenario-answers", '{"access.mode":"local"}',
                                   "--project-root", str(selected_project)])
            _data(initial, "initial install for repeated project selection")
            before = json.loads(registry_path.read_text()) if registry_path.is_file() else {"projects": []}
            repeated = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                    "--platform-root", str(root), "--service-profile", "default",
                                    "--scenario", "custom", "--scenario-answers", '{"access.mode":"local"}',
                                    "--project-root", str(selected_project)])
            _data(repeated, "repeat install with the recorded project")
            omitted = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                   "--platform-root", str(root), "--service-profile", "default",
                                   "--scenario", "custom", "--scenario-answers", '{"access.mode":"local"}'])
            _data(omitted, "repeat install with no selected project")
            after = json.loads(registry_path.read_text()) if registry_path.is_file() else {"projects": []}
            before_paths = [entry.get("path") for entry in before.get("projects", [])]
            after_paths = [entry.get("path") for entry in after.get("projects", [])]
            if (initial.returncode != 0 or repeated.returncode != 0 or omitted.returncode != 0
                    or after_paths.count(str(selected_project.resolve())) != 1
                    or before_paths.count(str(selected_project.resolve())) != 1):
                raise RuntimeError("an existing recorded or omitted selection triggered project registration")
        finally:
            cleanup_installer_fixture_root(root, launcher, run_command)

    def install_secured_project_without_session() -> None:
        root = platform_root.parent / "s187-secured"
        project = platform_root.parent / "s187-secured-project"
        admin_password = journey_value("KB_JOURNEY_ADMIN_PASSWORD", "JourneyInstallSecret1!")
        root.mkdir(parents=True, exist_ok=True)
        project.mkdir(parents=True, exist_ok=True)
        try:
            result = run_command([launcher, "install", "--index", str(stable_index),
                                  "--platform-root", str(root), "--service-profile", "default",
                                  "--scenario", "custom", "--scenario-answers",
                                  '{"access.mode":"secured","admin.email":"x18-admin@example.test","admin.tenant":"default"}',
                                  "--secret-env", "gateway.bootstrap.password=KB_JOURNEY_ADMIN_PASSWORD",
                                  "--project-root", str(project)])
            if result.returncode != 0 or "First administrator credentials (shown once)" not in result.stdout:
                raise RuntimeError("secured install did not complete or disclose the first administrator once")
            if not _hint_has_argv(result.stdout, ["kb", "project", "show", str(project.resolve())]):
                raise RuntimeError("secured install omitted its project verification command")
            registry_path = Path.home() / ".kb/projects.json"
            registry = json.loads(registry_path.read_text())
            registered = [entry for entry in registry.get("projects", [])
                          if entry.get("path") == str(project.resolve())]
            if len(registered) != 1 or (Path.home() / ".kb/session.json").exists():
                raise RuntimeError("secured install did not register once or left a CLI session")
            if (project / ".kb").exists():
                raise RuntimeError("secured project registration wrote into the project repository")
            active_root = active_platform_root(root)
            receipt = json.loads((active_root / ".kb/v2/receipt.json").read_text())
            gateway_url = receipt.get("verification", {}).get("hostHealthUrl", "")
            gateway_origin = urlsplit(gateway_url)
            gateway_url = f"{gateway_origin.scheme}://{gateway_origin.netloc}"
            login = evidence_run_command([str(active_root / ".kb/v2/bin/kb"), "auth", "login",
                                          "--gateway-url", gateway_url, "--email", "x18-admin@example.test",
                                          "--password-stdin"], input=admin_password + "\n")
            if login.returncode != 0 or not (Path.home() / ".kb/session.json").is_file():
                raise RuntimeError("explicit administrator login did not create a CLI session")
            shown = _data(run_platform_cli(root, ["project", "show", str(project), "--json"], run_command),
                          "printed project verification command after explicit login").get("project", {})
            if shown.get("path") != str(project.resolve()) or shown.get("id") != registered[0].get("id"):
                raise RuntimeError("the printed project verification command did not show the registered project")
            (Path.home() / ".kb/session.json").unlink(missing_ok=True)
            repeat = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                  "--platform-root", str(root), "--service-profile", "default",
                                  "--scenario", "custom", "--scenario-answers",
                                  '{"access.mode":"secured","admin.email":"x18-admin@example.test","admin.tenant":"default"}',
                                  "--project-root", str(project)])
            _data(repeat, "repeat secured install with its recorded project")
            omitted = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                   "--platform-root", str(root), "--service-profile", "default",
                                   "--scenario", "custom", "--scenario-answers",
                                   '{"access.mode":"secured","admin.email":"x18-admin@example.test","admin.tenant":"default"}'])
            _data(omitted, "repeat secured install without a project selection")
            if repeat.returncode != 0 or omitted.returncode != 0:
                raise RuntimeError("secured existing project selections unexpectedly required login")
            receipt_path = active_platform_root(root) / ".kb/v2/receipt.json"
            before = receipt_path.read_bytes()
            changed = platform_root.parent / "s187-secured-changed-project"
            changed.mkdir(parents=True, exist_ok=True)
            refusal = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                  "--platform-root", str(root), "--project-root", str(changed)])
            _, failure = _diagnostic(refusal, "secured install with changed project selection")
            if (failure.get("code") != "KB_INSTALL_PROJECT_SELECTION_CHANGED"
                    or not _hint_has_argv(failure.get("hint", ""), ["kb", "auth", "login", "--gateway-url"])
                    or not _hint_has_argv(failure.get("hint", ""), ["kb", "project", "add"])
                    or not _hint_has_argv(failure.get("hint", ""), ["kb", "project", "show"])
                    or receipt_path.read_bytes() != before):
                raise RuntimeError("secured changed project selection was not refused before apply")
            apply_refusal = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                         "--platform-root", str(root), "--project-root", str(changed)])
            _, apply_failure = _diagnostic(apply_refusal, "secured apply with changed project selection")
            if (apply_failure.get("code") != "KB_INSTALL_PROJECT_SELECTION_CHANGED"
                    or not _hint_has_argv(apply_failure.get("hint", ""), ["kb", "auth", "login", "--gateway-url"])
                    or not _hint_has_argv(apply_failure.get("hint", ""), ["kb", "project", "add"])
                    or not _hint_has_argv(apply_failure.get("hint", ""), ["kb", "project", "show"])
                    or receipt_path.read_bytes() != before):
                raise RuntimeError("secured apply changed the active receipt instead of refusing the selection")
        finally:
            (Path.home() / ".kb/session.json").unlink(missing_ok=True)
            cleanup_installer_fixture_root(root, launcher, run_command)

    journey.step("install a custom scenario in JSON mode and register its project",
                 "platform.install", install_and_show, cases=["S-187.6"])
    journey.step("make project registration idempotent for the same canonical path",
                 "platform.install", install_and_show, cases=["S-124.14"])
    if mode == "local":
        journey.step("refuse a changed project selection before applying an existing installation",
                     "platform.install", reject_changed_project_selection, cases=["S-187.11"],
                     runners=("agent:commit", "service-local:explore"))
    if mode == "local":
        journey.step("accept the recorded or omitted project selection without registering again",
                     "platform.install", accept_existing_project_selections, cases=["S-187.12"],
                     runners=("agent:commit", "service-local:explore"))
    if mode == "secured":
        journey.step("register the selected project during secured first install without saving a session",
                     "platform.install", install_secured_project_without_session,
                     cases=["S-187.10", "S-187.11", "S-187.12"],
                     runners=("agent-secured:commit", "service-secured:explore"))
    journey.step("update after the registered project directory was removed",
                 "platform.update", update_after_project_removed, cases=["S-187.9"])
    return journey


def run_repeat_plan_readonly_step(*, mode: str, launcher: str, stable_index: Path,
                                  platform_root: Path, run_command: CommandRunner) -> Journey:
    """Compare repeat plans while the host runs, excluding declared runtime state."""
    journey = Journey(mode, f"repeat-plan-readonly-{mode}")
    root = str(platform_root)

    def repeat_plan() -> None:
        lifecycle = InstallerHostLifecycle(launcher, run_command)
        with lifecycle.preserve([platform_root]) as initial:
            if not initial[platform_root]:
                lifecycle.set_running(platform_root, True)
            before = _snapshot_installation_owned_tree(platform_root)
            args = [launcher, "plan", "--json", "--index", str(stable_index),
                    "--platform-root", root, "--service-profile", "default",
                    "--scenario", "commit"]
            first = _json_command(run_command, args, "first clean-install plan").get("plan", {})
            second = _json_command(run_command, args, "repeat clean-install plan").get("plan", {})
            if any(first.get(key) != second.get(key) for key in ("planHash", "artifacts", "serviceGraph")):
                raise RuntimeError("repeat plan changed planHash, artifacts, or serviceGraph")
            after = _snapshot_installation_owned_tree(platform_root)
            changes = _snapshot_changes(before, after)
            if any(changes.values()):
                raise RuntimeError(_format_snapshot_changes(changes))
            status = _json_command(run_command, [launcher, "status", "--platform-root", root, "--json"],
                                   "status after repeat plan")
            host = status.get("host", {})
            if (not isinstance(host, dict) or host.get("state") != "running"
                    or status.get("running") is not True or status.get("reachable") is not True):
                raise RuntimeError("host health check failed after PC-001.8: expected running and reachable")

    journey.step("repeat install planning without changing the installed root", "platform.install",
                 repeat_plan, cases=["PC-001.8"])
    return journey


def _runtime_state_paths() -> tuple[str, ...]:
    layout_path = workspace_resource("tools", "kb-create", "v2", "contracts", "platform-layout.json")
    layout = json.loads(layout_path.read_text(encoding="utf-8"))
    return tuple(layout["runtimeStatePaths"]) + (layout["platformDataRoot"]["statePath"],)


def _snapshot_installation_owned_tree(root: Path) -> dict[str, tuple[str, bytes | str | None]]:
    layout_path = workspace_resource("tools", "kb-create", "v2", "contracts", "platform-layout.json")
    layout = json.loads(layout_path.read_text(encoding="utf-8"))
    runtime_paths = tuple(Path(path).parts for path in _runtime_state_paths())
    runtime_paths += tuple(Path(item["statePath"]).parts for item in layout.get("sharedRuntimePaths", []))
    shared_platform_paths = (Path(layout["platformDataRoot"]["platformPath"]).parts,)
    shared_platform_paths += tuple(Path(item["platformPath"]).parts for item in layout.get("sharedRuntimePaths", []))
    version_root = Path(layout["versionRoots"]["path"]).parts
    version_state_paths = tuple(Path(path).parts for path in layout.get("versionStatePaths", []))
    version_state_paths += shared_platform_paths
    snapshot = snapshot_tree(root)
    return {
        path: value for path, value in snapshot.items()
        if not any(Path(path).parts[:len(runtime_path)] == runtime_path for runtime_path in runtime_paths)
        and not any(Path(path).parts[:len(version_state)] == version_state
                    for version_state in version_state_paths)
        and not any(parts[index + len(version_root) + 1:][:len(version_state)] == version_state
                    for version_state in version_state_paths
                    for parts in (Path(path).parts,)
                    for index in range(len(parts) - len(version_root) + 1)
                    if parts[index:index + len(version_root)] == version_root)
    }


def _service_ids_match(planned_graph: dict, declared_graph: dict) -> bool:
    planned = planned_graph.get("services", [])
    declared = declared_graph.get("services", [])
    if not planned or not declared:
        return False
    planned_ids = [service.get("id") for service in planned]
    declared_ids = [service.get("id") for service in declared]
    return all(planned_ids) and all(declared_ids) and len(planned_ids) == len(set(planned_ids)) \
        and len(declared_ids) == len(set(declared_ids)) and set(planned_ids) == set(declared_ids)


def _snapshot_changes(before: dict[str, tuple[str, bytes | str | None]],
                      after: dict[str, tuple[str, bytes | str | None]]) -> dict[str, list[str]]:
    old_paths, new_paths = set(before), set(after)
    added = sorted(new_paths - old_paths)
    removed = sorted(old_paths - new_paths)
    changed = sorted(path for path in old_paths & new_paths if before[path] != after[path])
    return {"changed": changed, "added": added, "removed": removed}


def _format_snapshot_changes(changes: dict[str, list[str]]) -> str:
    total = sum(len(paths) for paths in changes.values())
    remaining = 20
    categories = []
    for category in ("changed", "added", "removed"):
        paths = changes[category]
        visible = paths[:remaining]
        remaining -= len(visible)
        categories.append(f"{category} ({len(paths)}): {', '.join(visible) if visible else 'none'}")
    shown = min(total, 20)
    return ("planning changed the active platform root: " + "; ".join(categories)
            + f"; {total} path(s) total, showing {shown} of {total}")


def run_invalid_request_step(*, mode: str, launcher: str, stable_index: Path,
                             platform_root: Path, run_command: CommandRunner) -> Journey:
    """Prove invalid request validation is actionable and does not mutate an install."""
    journey = Journey(mode, f"invalid-request-{mode}")

    def invalid_request_actionable() -> None:
        request_file = platform_root.parent / f"s039-invalid-request-{mode}.json"
        request_file.write_text(json.dumps({"schema": "kb.create/v2", "platformRoot": ""}) + "\n")
        before = _snapshot_installation_owned_tree(platform_root)
        try:
            result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                  "--input", str(request_file)])
            envelope, error = _diagnostic(result, "invalid install request")
            after = _snapshot_installation_owned_tree(platform_root)
            hint = error.get("hint", "")
            retry = (f"kb-create apply --index '{stable_index}' --input '{request_file}' --json")
            if (result.returncode == 0 or envelope.get("ok") is not False
                    or error.get("code") != "KB_INSTALL_INPUT_REQUIRED"
                    or "platformRoot" not in hint or str(request_file) not in hint
                    or retry not in hint
                    or before != after):
                raise RuntimeError(f"invalid request omitted field repair guidance or exact retry command, or changed installed state: {error}")
        finally:
            request_file.unlink(missing_ok=True)

    journey.step("report the invalid request field and preserve installed state", "platform.install",
                 invalid_request_actionable, cases=["S-039.4"])
    return journey


def run_apply_scenario_step(*, mode: str, launcher: str, stable_index: Path,
                            platform_root: Path, run_command: CommandRunner) -> Journey:
    """Prove direct JSON apply resolves scenario answers into its receipt."""
    journey = Journey(mode, f"apply-scenario-{mode}")
    apply_root = platform_root.parent / f"s039-apply-scenario-{mode}"

    def apply_scenario_and_check_receipt() -> None:
        answers = json.dumps({"cache": "state-broker"}, separators=(",", ":"))
        result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                              "--platform-root", str(apply_root), "--service-profile", "default",
                              "--scenario", "commit", "--scenario-answers", answers])
        try:
            records = [read_ux_cli_result(line, result.returncode)
                       for line in result.stdout.splitlines() if line.strip()]
            terminal = records[-1] if records else {}
            stream = terminal.get("meta", {}).get("stream", {})
            apply_results = [decode_ux_success_result(record) for record in records[:-1]
                             if record.get("ok") is True
                             and record.get("meta", {}).get("stream", {}).get("result") is True]
            if (result.returncode != 0 or not stream.get("terminal") or stream.get("exitCode") != 0
                    or terminal.get("ok") is not True or terminal.get("data", "not-null") is not None
                    or len(apply_results) != 1):
                raise RuntimeError("apply JSON stream omitted its successful apply result or terminal record")
            apply_result = apply_results[0]
            request = apply_result.get("receipt", {}).get("plan", {}).get("request", {})
            if (apply_result.get("operation") != "apply" or request.get("platformRoot") != str(apply_root)
                    or request.get("providerPreferences", {}).get("cache") != "state-broker"
                    or not apply_result.get("logPath")):
                raise RuntimeError("apply result receipt omitted the isolated root or selected cache provider")
        finally:
            cleanup_installer_fixture_root(apply_root, launcher, run_command)

    journey.step("apply scenario answers and verify the selected provider in the receipt",
                 "platform.install", apply_scenario_and_check_receipt, cases=["S-039.1"])
    return journey


def run_installer_case_steps(*, mode: str, launcher: str, stable_index: Path,
                             update_index: Path, jump_index: Path,
                             canary_index: Path, platform_root: Path,
                             secret_value: str, run_command: CommandRunner) -> Journey:
    """Claim request, configuration, doctor-repair, and compatibility cases.

    Install roots used by the request-selection steps are children of the
    journey's already isolated HOME; callers must pass that HOME's paths.
    """
    journey = Journey(mode, f"installer-cases-{mode}")
    host_os = "darwin" if sys.platform == "darwin" else "linux" if sys.platform.startswith("linux") else "unsupported"
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
    try:
        installed_root = active_platform_root(platform_root)
    except FileNotFoundError:
        journey.step("preflight installer case fixtures", "journey.orchestration",
                     lambda: f"no installed platform at {platform_root}")
        return journey
    installed_cli_entry = installed_root / "node_modules/@kb-labs/cli-bin/dist/bin.js"
    required_paths = [*installer_scenario_resources(host_os, host_arch), installed_cli_entry]
    catalog_root = required_paths[0]
    missing_resources = [str(path) for path in required_paths
                         if not (path.is_dir() if path == catalog_root else path.is_file())]
    if missing_resources:
        journey.step("preflight installer case fixtures", "journey.orchestration",
                     lambda: "missing installer journey resource(s): " + ", ".join(missing_resources))
        return journey

    journey.step_guard = installer_primary_host_guard(platform_root, launcher, run_command)

    fixture_dir = installer_case_fixture_root()
    root = str(platform_root)
    run_fixture_group(journey, "PC-001 port ownership", lambda: run_port_conflict_step(
        mode=mode, launcher=launcher, stable_index=stable_index,
        platform_root=platform_root, run_command=run_command))

    # PC-001.4 applies an explicit fresh fixture so the end-to-end installer
    # remains the single main journey install and can continue serving later cases.
    apply_root = platform_root.parent / "pc001-apply-root"
    apply_root.mkdir(parents=True, exist_ok=True)
    selected_plan: dict = {}

    def apply_and_check() -> None:
        selected_plan["serviceGraph"] = _json_command(
            run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                          "--platform-root", str(apply_root),
                          "--service-profile", "default", "--scenario", "commit"],
            "PC-001 selected profile plan").get("plan", {}).get("serviceGraph", {})
        result = _data(run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                   "--platform-root", str(apply_root),
                                   "--service-profile", "default", "--scenario", "commit"]),
                      "PC-001 apply")
        if result.get("operation") != "apply" or not result.get("receipt") or not result.get("logPath"):
            raise RuntimeError("apply result omitted operation, receipt, or logPath")
        status = _data(run_command([launcher, "status", "--json", "--platform-root", str(apply_root)]),
                       "PC-001 apply status")
        host = status.get("host", {})
        if (status.get("installed") is not True or status.get("running") is not True
                or status.get("reachable") is not True or host.get("state") != "running"):
            raise RuntimeError(f"successful apply did not leave a running, reachable host: {status}")

    journey.step("apply the resolved default service profile", "platform.install", apply_and_check,
                 cases=["PC-001.4"])

    def isolated_container_install() -> None:
        with tempfile.TemporaryDirectory(prefix="pc001-container-") as temporary:
            parent = Path(temporary)
            install_root = parent / "install"
            request_handle = tempfile.NamedTemporaryFile(prefix="s023-container-update-", suffix=".json")
            request_path = Path(request_handle.name)
            request_handle.close()
            try:
                result = _data(run_command([launcher, "install", "--json", "--index", str(stable_index),
                                            "--platform-root", str(install_root),
                                            "--service-profile", "default", "--scenario", "commit"]),
                               "PC-001 install container")
                current = install_root / "current"
                versions = install_root / "versions"
                current_target = Path(current.readlink()) if current.is_symlink() else Path()
                if (result.get("operation") != "apply" or sorted(path.name for path in parent.iterdir()) != ["install"]
                        or not current.is_symlink() or current_target.is_absolute()):
                    raise RuntimeError("fresh install wrote outside its container or omitted the relative current link")
                resolved = current.resolve(strict=True)
                if resolved.parent != versions.resolve(strict=True) or not (resolved / ".kb/v2/receipt.json").is_file():
                    raise RuntimeError("current did not resolve to a complete version root inside the container")
                receipt = json.loads((active_platform_root(install_root) / ".kb/v2/receipt.json").read_text())
                original_version = receipt.get("plan", {}).get("serviceGraph", {}).get("platformVersion")
                for label, index in (("first", update_index), ("second", jump_index)):
                    index_payload = json.loads(index.read_text())
                    target = index_payload.get("channels", {}).get("stable")
                    if not target:
                        raise RuntimeError(f"{label} container update index has no stable platform release")
                    request = dict(receipt.get("plan", {}).get("request", {}))
                    platform_request = dict(request.get("platform", {}))
                    platform_request["version"] = target
                    platform_request.pop("channel", None)
                    request["platform"] = platform_request
                    request["sdk"] = {"version": target}
                    request_path.write_text(json.dumps(request) + "\n")
                    updated = _data(run_command([launcher, "update", "--json", "--index", str(index),
                                                 "--input", str(request_path), "--platform-root", str(install_root)]),
                                    f"S-023.17 {label} update")
                    pending = updated.get("pendingRestart")
                    if (updated.get("hostRestarted") is not False
                            or not isinstance(pending, dict) or pending.get("installedVersion") != target
                            or sorted(path.name for path in parent.iterdir()) != ["install"]):
                        raise RuntimeError(f"{label} update changed outside the install root or restarted the host")
                    receipt = json.loads((active_platform_root(install_root) / ".kb/v2/receipt.json").read_text())
                    status = _data(run_command([launcher, "status", "--platform-root", str(install_root), "--json"]),
                                   f"S-023.17 {label} status")
                    if (status.get("runningVersion") != original_version
                            or status.get("installedVersion") != target
                            or sorted(path.name for path in parent.iterdir()) != ["install"]):
                        raise RuntimeError(f"{label} update changed the running version or wrote outside the root")
                restarted = _data(run_command([launcher, "restart", "--platform-root", str(install_root), "--json"]),
                                  "S-023.17 restart after repeated updates")
                if (restarted.get("pendingRestart") is not None
                        or sorted(path.name for path in parent.iterdir()) != ["install"]):
                    raise RuntimeError("restart did not clear pendingRestart or changed the parent directory")
            finally:
                request_path.unlink(missing_ok=True)
                cleanup_installer_fixture_root(install_root, launcher, run_command)
            if list(parent.iterdir()):
                raise RuntimeError("uninstall left an installation-owned path outside or inside the temporary parent")

        with tempfile.TemporaryDirectory(prefix="pc001-interrupted-install-") as temporary:
            parent = Path(temporary)
            install_root = parent / "install"
            (install_root / "versions" / ".stage-interrupted").mkdir(parents=True)
            (install_root / "ownership.json").write_text(
                json.dumps({"schema": "kb.create.installation-owner/v1", "installationId": "a" * 32}) + "\n")
            retried = _data(run_command([launcher, "install", "--json", "--index", str(stable_index),
                                         "--platform-root", str(install_root),
                                         "--service-profile", "default", "--scenario", "commit"]),
                            "PC-001 interrupted install retry")
            if (retried.get("operation") != "apply" or not (install_root / "current").is_symlink()
                    or (install_root / "versions" / ".stage-interrupted").exists()
                    or sorted(path.name for path in parent.iterdir()) != ["install"]):
                raise RuntimeError("retry did not recover the owned interrupted install container")
            cleanup_installer_fixture_root(install_root, launcher, run_command)
            if list(parent.iterdir()):
                raise RuntimeError("interrupted install retry cleanup left owned paths behind")

        with tempfile.TemporaryDirectory(prefix="pc001-empty-install-root-") as temporary:
            parent = Path(temporary)
            install_root = parent / "install"
            install_root.mkdir()
            try:
                result = _data(run_command([launcher, "install", "--json", "--index", str(stable_index),
                                            "--platform-root", str(install_root),
                                            "--service-profile", "default", "--scenario", "commit"]),
                               "PC-001 empty-root install")
                if (result.get("operation") != "apply" or not (install_root / "current").is_symlink()
                        or sorted(path.name for path in parent.iterdir()) != ["install"]):
                    raise RuntimeError("install into an empty target did not create only its versioned container")
            except Exception as error:
                try:
                    cleanup_installer_fixture_root(install_root, launcher, run_command)
                except Exception as cleanup_error:
                    error.add_note(f"empty-root install cleanup also failed: {cleanup_error}")
                raise
            else:
                cleanup_installer_fixture_root(install_root, launcher, run_command)
            if list(parent.iterdir()):
                raise RuntimeError("empty-root install cleanup left owned paths behind")

        with tempfile.TemporaryDirectory(prefix="pc001-interrupted-uninstall-") as temporary:
            parent = Path(temporary)
            install_root = parent / "install"
            (install_root / "versions" / ".stage-interrupted").mkdir(parents=True)
            (install_root / "ownership.json").write_text(
                json.dumps({"schema": "kb.create.installation-owner/v1", "installationId": "b" * 32}) + "\n")
            foreign = install_root / "foreign.txt"
            foreign.write_text("preserve me\n")
            removed = _data(run_command([launcher, "uninstall", "--yes", "--json", "--platform-root",
                                         str(install_root)]), "PC-001 interrupted install uninstall")
            if (removed.get("operation") != "uninstall" or foreign.read_text() != "preserve me\n"
                    or sorted(path.name for path in install_root.iterdir()) != ["foreign.txt"]):
                raise RuntimeError("uninstall did not remove interrupted owned entries and preserve foreign data")
        with tempfile.TemporaryDirectory(prefix="pc001-foreign-target-") as temporary:
            foreign_root = Path(temporary) / "foreign"
            foreign_root.mkdir()
            foreign_file = foreign_root / "keep.txt"
            foreign_file.write_text("user data\n")
            refused = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                   "--platform-root", str(foreign_root),
                                   "--service-profile", "default", "--scenario", "commit"])
            _, error = _diagnostic(refused, "PC-001 foreign target")
            if (error.get("code") != "KB_INSTALL_LAYOUT_UNSUPPORTED"
                    or "kb-create install --platform-root" not in error.get("hint", "")
                    or foreign_file.read_text() != "user data\n"):
                raise RuntimeError("fresh install did not refuse a foreign non-empty directory unchanged")

        with tempfile.TemporaryDirectory(prefix="pc001-damaged-target-") as temporary:
            damaged_root = Path(temporary) / "damaged"
            (damaged_root / "current").mkdir(parents=True)
            before = snapshot_tree(damaged_root)
            refused = run_command([launcher, "install", "--json", "--index", str(stable_index),
                                   "--platform-root", str(damaged_root),
                                   "--service-profile", "default", "--scenario", "commit"])
            _, error = _diagnostic(refused, "PC-001 damaged target")
            if (error.get("code") != "KB_INSTALL_LAYOUT_UNSUPPORTED"
                    or "kb-create install --platform-root" not in error.get("hint", "")
                    or snapshot_tree(damaged_root) != before):
                raise RuntimeError("install did not refuse the damaged current entry unchanged")

        with tempfile.TemporaryDirectory(prefix="s050-foreign-container-") as temporary:
            parent = Path(temporary)
            install_root = parent / "install"
            sibling = parent / "unrelated-sibling"
            sibling.mkdir()
            marker = sibling / "keep.txt"
            marker.write_text("sibling user data\n")
            _data(run_command([launcher, "apply", "--json", "--index", str(stable_index),
                               "--platform-root", str(install_root), "--service-profile", "default",
                               "--scenario", "commit"]), "S-050.8 install")
            foreign = install_root / "keep.txt"
            foreign.write_text("installation-root user data\n")
            removed = _data(run_command([launcher, "uninstall", "--json", "--yes",
                                         "--platform-root", str(install_root)]), "S-050.8 uninstall")
            if (foreign.read_text() != "installation-root user data\n"
                    or marker.read_text() != "sibling user data\n"
                    or removed.get("preservedEntries") != [str(foreign)]
                    or sorted(path.name for path in parent.iterdir()) != ["install", "unrelated-sibling"]):
                raise RuntimeError("uninstall did not preserve and name foreign entries byte-for-byte")
            foreign.unlink()
            install_root.rmdir()

    journey.step("install, update twice, restart, and uninstall inside a fresh container", "platform.update",
                 isolated_container_install, cases=["PC-001.17", "PC-001.18", "S-023.17", "S-050.8", "S-223.4"])

    def inspect_generated_config() -> None:
        config_path = active_platform_root(apply_root) / ".kb/kb.config.jsonc"
        if not config_path.is_file():
            raise RuntimeError("apply did not generate the runtime config")
        config = json.loads(_without_jsonc_comments(config_path.read_text()))
        if not isinstance(config, dict):
            raise RuntimeError("generated runtime config is not a JSON object")
        receipt = json.loads((active_platform_root(apply_root) / ".kb/v2/receipt.json").read_text())
        graph = receipt.get("plan", {}).get("serviceGraph", {})
        if (selected_plan["serviceGraph"].get("profile") != "default"
                or graph.get("profile") != "default"
                or not _service_ids_match(selected_plan["serviceGraph"], graph)):
            raise RuntimeError("receipt service declarations do not equal the selected profile plan")
        layout = json.loads(workspace_resource("tools", "kb-create", "v2", "contracts", "platform-layout.json").read_text())
        roots_contract = layout.get("versionRoots", {})
        version_dir = apply_root / roots_contract.get("path", "versions")
        if not apply_root.is_dir() or not (apply_root / "current").is_symlink() or not (version_dir / (apply_root / "current").resolve().name).is_dir():
            raise RuntimeError("fresh install did not create the install container, stable link, and contract-declared version root")
        doctor = _data(run_command([launcher, "doctor", "--json", "--platform-root", str(apply_root)]),
                       "PC-001 platform layout doctor")
        retained = doctor.get("retention", {})
        if retained.get("versionsDirectory") != str(version_dir):
            raise RuntimeError("doctor did not report the platform-layout version root directory")
        with tempfile.TemporaryDirectory(prefix="pc001-unversioned-") as temporary:
            legacy_root = Path(temporary) / "legacy-platform"
            shutil.copytree(apply_root.resolve(), legacy_root, symlinks=True)
            legacy_request = Path(temporary) / "request.json"
            legacy_receipt = legacy_root / ".kb/v2/receipt.json"
            request = json.loads(legacy_receipt.read_text())["plan"]["request"]
            request["platformRoot"] = str(legacy_root)
            legacy_request.write_text(json.dumps(request) + "\n")
            before = hashlib.sha256(legacy_receipt.read_bytes()).digest()
            refused = run_command([launcher, "update", "--json", "--index", str(stable_index),
                                   "--input", str(legacy_request)])
            error = decode_ux_error_result(read_ux_cli_result(refused.stdout, refused.returncode))
            after = hashlib.sha256(legacy_receipt.read_bytes()).digest()
            if (refused.returncode == 0 or error.get("code") != "KB_INSTALL_LAYOUT_UNSUPPORTED"
                    or "kb-create install --platform-root" not in error.get("hint", "") or before != after):
                raise RuntimeError("update did not reject the unversioned root with a reinstall command and preserved bytes")
            for operation, arguments in (
                ("restart", ["--platform-root", str(legacy_root), "--json"]),
                ("rollback", ["--platform-root", str(legacy_root), "--snapshot", "snapshot-from-copy", "--yes", "--json"]),
            ):
                refused = run_command([launcher, operation, *arguments])
                error = decode_ux_error_result(read_ux_cli_result(refused.stdout, refused.returncode))
                if (refused.returncode == 0 or error.get("code") != "KB_INSTALL_LAYOUT_UNSUPPORTED"
                        or "kb-create install --platform-root" not in error.get("hint", "")
                        or hashlib.sha256(legacy_receipt.read_bytes()).digest() != before):
                    raise RuntimeError(f"{operation} did not refuse the unversioned root unchanged with a reinstall command")

    journey.step("inspect generated runtime config and service declarations", "platform.install",
                 inspect_generated_config, cases=["PC-001.5", "S-023.16", "S-223.3"],
                 requires=("step:apply the resolved default service profile",))

    run_fixture_group(journey, "analytics retention validation", lambda: run_analytics_invalid_retention_step(
        mode=mode, launcher=launcher, platform_root=apply_root, run_command=run_command))

    run_fixture_group(journey, "repeat plan", lambda: run_repeat_plan_readonly_step(
        mode=mode, launcher=launcher, stable_index=stable_index,
        platform_root=platform_root, run_command=run_command))

    def channel_plan(channel: str, index: Path, root_name: str) -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(index),
                                           "--platform-channel", channel, "--policy", "compatible",
                                           "--platform-root", str(platform_root.parent / root_name)],
                             f"{channel} plan").get("plan", {})
        artifacts = plan.get("artifacts", [])
        if (plan.get("platformChannel") != channel or not plan.get("serviceGraph", {}).get("platformVersion")
                or not artifacts or not all(item.get("sha256") for item in artifacts)):
            raise RuntimeError(f"{channel} plan did not resolve an exact version and immutable hashes")
        applied = _data(run_command([launcher, "apply", "--json", "--index", str(index),
                                     "--platform-channel", channel, "--policy", "compatible",
                                     "--platform-root", str(platform_root.parent / root_name)]),
                        f"{channel} apply")
        if applied.get("shim") != {"linked": False, "owner": str(platform_root)}:
            raise RuntimeError(f"{channel} apply changed the live command owner: {applied.get('shim')}")
        status = _json_command(run_command, [launcher, "status", "--json", "--platform-root",
                                             str(platform_root.parent / root_name)], f"{channel} status")
        installation = status.get("installation", status)
        exact_version = plan["serviceGraph"]["platformVersion"]
        if (not applied.get("receipt") or installation.get("platformChannel") != channel
                or installation.get("platformVersion") != exact_version):
            raise RuntimeError(f"receipt-backed status did not confirm {channel} {exact_version}")

    journey.step("plan, apply, and verify stable install", "platform.install",
                 lambda: channel_plan("stable", stable_index, "s181-stable-root"), cases=["S-181.1"])
    journey.step("plan, apply, and verify canary install", "platform.install",
                 lambda: channel_plan("canary", canary_index, "s181-canary-root"), cases=["S-181.2"])

    secret_root = platform_root.parent / "s181-secret-root"
    explicit_gateway_root = platform_root.parent / "pc001-explicit-gateway-root"
    def secret_apply() -> None:
        if not secret_value:
            raise RuntimeError("isolated journey secret is unavailable for S-181.3")
        apply_result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                    "--platform-channel", "stable", "--platform-root", str(secret_root),
                                    "--scenario", "custom", "--scenario-answers",
                                    json.dumps({"access.mode": "secured", "admin.email": "journey-admin@example.test",
                                                "admin.tenant": "journey"}),
                                    "--secret-env", "gateway.bootstrap.password=KB_JOURNEY_ADMIN_PASSWORD"])
        payload = _data(apply_result, "secret apply")
        if payload.get("shim") != {"linked": False, "owner": str(platform_root)}:
            raise RuntimeError(f"secret apply changed the live command owner: {payload.get('shim')}")
        secondary_receipt = json.loads((active_platform_root(secret_root) / ".kb/v2/receipt.json").read_text())
        if secondary_receipt.get("verification", {}).get("hostHealthUrl") != "http://127.0.0.1:4100/health":
            raise RuntimeError("secondary apply did not record its automatically allocated Gateway address")
        shim_path = Path(shutil.which(launcher) or launcher).absolute().parent / "kb"
        if not shim_path.is_symlink() or shim_path.readlink() != platform_root / "current/.kb/v2/bin/kb":
            raise RuntimeError("secondary apply moved the kb shim away from the primary platform root")
        cli = run_platform_cli(platform_root, ["--help"], run_command)
        if cli.returncode != 0:
            raise RuntimeError(f"the existing kb shim did not run the primary platform CLI: {cli.stdout[-500:]}")
        receipt_path = active_platform_root(secret_root) / ".kb/v2/receipt.json"
        serialized = apply_result.stdout + (receipt_path.read_text() if receipt_path.is_file() else "")
        if secret_value in serialized or "KB_JOURNEY_ADMIN_PASSWORD" in serialized:
            raise RuntimeError("secret apply serialized the secret value or environment-variable name")

    journey.step("apply a secret through an environment reference", "platform.install", secret_apply,
                 cases=["S-181.3", "PC-001.10"])

    run_fixture_group(journey, "gateway allocation", lambda: run_gateway_allocation_step(
        mode=mode, launcher=launcher, stable_index=stable_index,
        primary_root=platform_root, automatic_root=secret_root,
        explicit_root=explicit_gateway_root, run_command=run_command))

    run_fixture_group(journey, "secondary root gateway isolation", lambda: run_secondary_root_gateway_isolation_step(
        mode=mode, launcher=launcher, primary_root=platform_root,
        secondary_root=secret_root, run_command=run_command))

    def repair_dangling_shim() -> None:
        shim_path = Path(shutil.which(launcher) or launcher).absolute().parent / "kb"
        _data(run_command([launcher, "doctor", "--fix", "--platform-root", str(secret_root), "--json"]),
              "doctor fix live non-owner")
        if not shim_path.is_symlink() or shim_path.readlink() != platform_root / "current/.kb/v2/bin/kb":
            raise RuntimeError("doctor --fix changed a shim owned by another live platform")
        dangling_root = platform_root.parent / "removed-shim-owner"
        shim_path.unlink(missing_ok=True)
        shim_path.symlink_to(dangling_root / "current/.kb/v2/bin/kb")
        diagnosis = _data(run_command([launcher, "doctor", "--platform-root", str(platform_root), "--json"]),
                          "doctor dangling shim")
        finding = next((item for item in diagnosis.get("findings", [])
                        if item.get("code") == "KB_INSTALL_SHIM_DANGLING"), None)
        expected_fix = f'kb-create doctor --fix --platform-root "{platform_root}"'
        if not finding or expected_fix not in finding.get("hint", ""):
            raise RuntimeError(f"doctor omitted the dangling shim finding or exact repair command: {diagnosis}")
        _data(run_command([launcher, "doctor", "--fix", "--platform-root", str(platform_root), "--json"]),
              "doctor fix dangling shim")
        if shim_path.resolve() != (platform_root / "current/.kb/v2/bin/kb").resolve():
            raise RuntimeError("doctor --fix did not link the shim to its requested live platform root")

    journey.step("diagnose and repair a dangling managed kb command", "platform.install",
                 repair_dangling_shim, cases=["S-025.9"])

    def uninstall_non_owner() -> None:
        _data(run_command([launcher, "uninstall", "--platform-root", str(secret_root), "--yes", "--json"]),
              "uninstall non-owning root")
        shim_path = Path(shutil.which(launcher) or launcher).absolute().parent / "kb"
        if not shim_path.is_symlink() or shim_path.readlink() != platform_root / "current/.kb/v2/bin/kb":
            raise RuntimeError("uninstall changed the live owner's kb shim")
        if not (active_platform_root(platform_root) / ".kb/v2/bin/kb").is_file():
            raise RuntimeError("uninstall removed the owning platform CLI")

    journey.step("uninstall a platform that does not own the kb command", "platform.install",
                 uninstall_non_owner, cases=["PC-001.11"])

    run_fixture_group(journey, "offline plan", lambda: run_offline_plan_step(
        mode=mode, launcher=launcher, stable_index=stable_index,
        platform_root=platform_root, run_command=run_command))

    for generated_root in (apply_root, platform_root.parent / "s181-stable-root",
                           platform_root.parent / "s181-canary-root", secret_root,
                           explicit_gateway_root,
                           platform_root.parent / "removed-shim-owner",
                           platform_root.parent / "s181-offline-root"):
        shutil.rmtree(generated_root, ignore_errors=True)
    run_fixture_group(journey, "installer fixtures", lambda: run_installer_fixture_steps(
        mode=mode, launcher=launcher, platform_root=platform_root,
        stable_index=stable_index, run_command=run_command))
    return journey


def run_installer_fixture_steps(*, mode: str, launcher: str, platform_root: Path,
                                stable_index: Path, run_command: CommandRunner) -> Journey:
    """Exercise isolated doctor, compatibility, and project-install fixtures."""
    journey = Journey(mode, f"installer-fixtures-{mode}")
    fixture_dir = installer_case_fixture_root()
    host_os = "darwin" if sys.platform == "darwin" else "linux" if sys.platform.startswith("linux") else "unsupported"
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
    if host_os == "unsupported":
        raise RuntimeError("installer compatibility fixtures support only Linux and macOS journey runners")
    matrix = fixture_dir / f"compatibility-matrix-{host_os}-{host_arch}.json"
    missing_binary = fixture_dir / f"missing-host-binary-{host_os}-{host_arch}.json"
    scratch = platform_root.parent / "installer-case-fixtures"
    scratch.mkdir(parents=True, exist_ok=True)

    def strict_matrix_and_range() -> None:
        strict = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.2.0",
                              "--policy", "strict", "--platform-root", str(scratch / "strict-sdk")])
        _, error = _diagnostic(strict, "strict SDK matrix plan")
        meta = error.get("details", {})
        if strict.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("strict SDK plan did not return KB_INSTALL_INCOMPATIBLE_COMPONENTS")
        if any(meta.get(key) != value for key, value in (("kind", "sdk"), ("id", "sdk"),
                                                          ("selectedVersion", "3.2.0"),
                                                          ("requiredRange", ">=3.0.0 <4.0.0"),
                                                          ("policy", "strict"))):
            raise RuntimeError(f"strict SDK error details are incomplete: {meta}")
        if error.get("hint") != "Use an exact platform/SDK pair from the release matrix or choose --policy compatible.":
            raise RuntimeError("strict SDK refusal omitted its exact correction hint")
        compatible = _data(run_command([launcher, "plan", "--json", "--index", str(matrix),
                                        "--platform-version", "3.2.0", "--sdk-version", "3.2.0",
                                        "--policy", "compatible", "--platform-root", str(scratch / "compatible-sdk")]),
                           "compatible SDK matrix plan")
        if compatible.get("plan", {}).get("serviceGraph", {}).get("platformVersion") != "3.2.0":
            raise RuntimeError("compatible policy did not resolve the indexed SDK in the declared range")

    journey.step("compare strict and compatible SDK matrix selection", "platform.install", strict_matrix_and_range, cases=["S-053.2"])

    def incompatible_adapter() -> None:
        result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--adapters", "fixture-adapter@1.0.0", "--policy", "compatible",
                              "--platform-root", str(scratch / "adapter-range")])
        _, error = _diagnostic(result, "adapter range plan")
        meta = error.get("details", {})
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("out-of-range adapter plan did not return KB_INSTALL_INCOMPATIBLE_COMPONENTS")
        if (meta.get("kind") != "adapter" or meta.get("id") != "fixture-adapter"
                or meta.get("selectedVersion") != "3.2.0" or meta.get("requiredRange") != "<3.2.0"):
            raise RuntimeError(f"adapter range refusal omitted its component details: {meta}")
        if error.get("hint") != "Choose compatible versions or pass an explicit supported provider.":
            raise RuntimeError("adapter range refusal omitted its correction hint")
        if (scratch / "adapter-range").exists():
            raise RuntimeError("adapter range refusal changed the isolated install root")

    journey.step("refuse an adapter outside its platform range", "platform.install", incompatible_adapter, cases=["S-053.4"])

    def installed_component_update(kind: str, component: str, claim: str) -> None:
        root = scratch / f"{claim.replace('.', '-')}-root"
        receipt_path = _seed_update_fixture(root, kind=kind, component=component)
        before_receipt = receipt_path.read_bytes()
        before_files = {str(path.relative_to(root)): path.read_bytes() for path in root.rglob("*") if path.is_file()}
        before_directories = {str(path.relative_to(root)) for path in root.rglob("*") if path.is_dir()}
        result = run_command([launcher, "update", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--policy", "compatible", "--platform-root", str(root)])
        _, error = _diagnostic(result, "installed component update")
        meta = error.get("details", {})
        expected_code = "KB_UPDATE_INCOMPATIBLE"
        expected_hint = f"Update or remove {kind} {component} first, then rerun `kb-create update`."
        after_files = {str(path.relative_to(root)): path.read_bytes() for path in root.rglob("*") if path.is_file()}
        after_directories = {str(path.relative_to(root)) for path in root.rglob("*") if path.is_dir()}
        if result.returncode == 0 or error.get("code") != expected_code or not isinstance(meta, dict):
            raise RuntimeError(f"incompatible installed {kind} update did not return {expected_code}: {error}")
        if (meta.get("kind") != kind or meta.get("id") != component or meta.get("selectedVersion") != "3.2.0"
                or meta.get("requiredRange") != "<3.2.0" or error.get("hint") != expected_hint):
            raise RuntimeError(f"installed {kind} refusal omitted its range or correction details: {error}")
        if (receipt_path.read_bytes() != before_receipt or before_files != after_files
                or before_directories != after_directories):
            raise RuntimeError(f"incompatible {kind} update mutated the active receipt or install root")

    journey.step("refuse update beyond an installed plugin range", "platform.update", lambda: installed_component_update("plugin", "fixture-compat", "S-053.3"), cases=["S-053.3"])

    def sdk_and_binary_ranges() -> None:
        sdk_result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                                  "--platform-version", "3.2.0", "--sdk-version", "4.0.0",
                                  "--policy", "compatible", "--platform-root", str(scratch / "sdk-outside-range")])
        _, sdk_error = _diagnostic(sdk_result, "out-of-range SDK plan")
        sdk_meta = sdk_error.get("details", {})
        if sdk_result.returncode == 0 or sdk_error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(sdk_meta, dict):
            raise RuntimeError("out-of-range SDK plan did not fail with the typed incompatibility")
        if (sdk_meta.get("kind") != "sdk" or sdk_meta.get("id") != "sdk"
                or sdk_meta.get("selectedVersion") != "4.0.0"
                or sdk_meta.get("requiredRange") != ">=3.0.0 <4.0.0"):
            raise RuntimeError(f"out-of-range SDK refusal omitted component, version, or range: {sdk_meta}")
        if not sdk_error.get("hint"):
            raise RuntimeError("out-of-range SDK refusal omitted its correction hint")
        binary_result = run_command([launcher, "plan", "--json", "--index", str(missing_binary),
                                     "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                                     "--policy", "compatible", "--platform-root", str(scratch / "missing-binary")])
        _, binary_error = _diagnostic(binary_result, "missing binary plan")
        binary_meta = binary_error.get("details", {})
        if binary_result.returncode == 0 or binary_error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(binary_meta, dict):
            raise RuntimeError("missing host binary plan did not fail with the typed incompatibility")
        if (binary_meta.get("kind") != "binary"
                or binary_meta.get("id") != "kb-create"
                or binary_meta.get("target") != f"{host_os}/{host_arch}"
                or not binary_error.get("hint")):
            raise RuntimeError(f"missing binary error omitted its binary identity, target, or hint: {binary_error}")

    journey.step("refuse an SDK outside range and a release missing the host binary", "platform.install", sdk_and_binary_ranges, cases=["S-053.5"])

    journey.step("refuse update beyond an installed adapter range", "platform.update", lambda: installed_component_update("adapter", "fixture-adapter", "S-053.6"), cases=["S-053.6"])

    def missing_host_binary_before_mutation() -> None:
        target = scratch / "missing-host-root"
        before_exists = target.exists()
        before = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob("*") if path.is_file()} if target.exists() else {}
        before_directories = {str(path.relative_to(target)) for path in target.rglob("*") if path.is_dir()} if target.exists() else set()
        result = run_command([launcher, "plan", "--json", "--index", str(missing_binary),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--policy", "compatible", "--platform-root", str(target)])
        _, error = _diagnostic(result, "missing host binary plan")
        meta = error.get("details", {})
        after = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob("*") if path.is_file()} if target.exists() else {}
        after_directories = {str(path.relative_to(target)) for path in target.rglob("*") if path.is_dir()} if target.exists() else set()
        after_exists = target.exists()
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("missing host binary did not refuse planning with the typed incompatibility")
        if (meta.get("kind") != "binary"
                or meta.get("id") != "kb-create"
                or meta.get("target") != f"{host_os}/{host_arch}"
                or not error.get("hint")):
            raise RuntimeError("missing binary refusal omitted its kind/id, target, or correction hint")
        if before != after or before_directories != after_directories or before_exists != after_exists:
            raise RuntimeError("missing binary refusal changed the isolated install root")

    journey.step("refuse planning without the runner binary", "platform.install", missing_host_binary_before_mutation, cases=["S-053.7"])

    def incompatible_plugin_sdk_range() -> None:
        for label, platform_selection in (("explicit-version", ["--platform-version", "3.2.0"]),
                                          ("channel", ["--platform-channel", "stable"])):
            target = scratch / f"plugin-sdk-range-{label}"
            result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                                  *platform_selection,
                                  "--plugins", "fixture-sdk-incompat@1.0.0", "--policy", "compatible",
                                  "--platform-root", str(target)])
            _, error = _diagnostic(result, f"plugin sdk-range plan selected by {label}")
            meta = error.get("details", {})
            if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
                raise RuntimeError(f"incompatible plugin SDK range did not refuse planning with the typed incompatibility: {error}")
            if (meta.get("kind") != "plugin" or meta.get("id") != "fixture-sdk-incompat"
                    or meta.get("selectedVersion") != "3.0.0" or meta.get("requiredRange") != "<3.0.0"):
                raise RuntimeError(f"plugin SDK range refusal omitted its component details: {meta}")
            if error.get("hint") != "Choose compatible versions or pass an explicit supported provider.":
                raise RuntimeError("plugin SDK range refusal omitted its correction hint")
            if target.exists():
                raise RuntimeError("plugin SDK range refusal changed the isolated install root")

    journey.step("refuse a plugin outside its channel-selected SDK range", "platform.install", incompatible_plugin_sdk_range, cases=["S-053.8"])

    def incompatible_plugin_request() -> None:
        result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--plugins", "fixture-plugin@1.0.0", "--policy", "strict",
                              "--platform-root", str(scratch / "plugin-request")])
        _, error = _diagnostic(result, "incompatible plugin request")
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or "fixture-plugin" not in str(error):
            raise RuntimeError(f"strict incompatible plugin request was not refused: {error}")
        if not error.get("hint"):
            raise RuntimeError("strict incompatible plugin request omitted its correction hint")

    journey.step("refuse a strict incompatible plugin request before mutation", "platform.install", incompatible_plugin_request, cases=["S-181.5"])

    def doctor_finding(*, secret: bool = False) -> None:
        root = scratch / ("s025-secret" if secret else "s025-required")
        requirement = {"id": "fixture.required", "path": "/fixture/required", "required": True,
                       "secret": secret, "hint": "provide the fixture value"}
        _seed_doctor_fixture(platform_root, root, requirement=requirement)
        result = run_command([launcher, "doctor", "--platform-root", str(root), "--json"])
        envelope, payload = _payload(result, "doctor required-input fixture")
        findings = payload.get("findings", [])
        finding = next((item for item in findings if isinstance(item, dict) and item.get("path") == "/fixture/required"), None)
        if (result.returncode != 0 or envelope.get("ok") is not True or payload.get("operation") != "doctor"
                or payload.get("status") != "unhealthy" or not isinstance(finding, dict)):
            raise RuntimeError("doctor did not return an unhealthy report for the manifest-declared required input")
        if not finding.get("component") or not finding.get("code") or not finding.get("hint"):
            raise RuntimeError(f"doctor finding omitted component, code, or hint: {finding}")
        if not isinstance(payload.get("installation"), dict):
            raise RuntimeError("doctor unhealthy result omitted its structured installation report")
        if secret:
            fixture_text = result.stdout + "".join(
                path.read_text(errors="replace") for path in root.rglob("*") if path.is_file())
            if any(value in fixture_text for value in ("fixture-secret-value", "fixture.required=value")):
                raise RuntimeError("doctor output or isolated files serialized secret material or its value assignment")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("diagnose a missing required non-secret config value", "platform.install", lambda: doctor_finding(), cases=["S-025.1"])
    journey.step("diagnose missing secret input without exposing a value", "platform.install", lambda: doctor_finding(secret=True), cases=["S-025.2"])

    def doctor_hostspec_invalid() -> None:
        root = scratch / "s025-hostspec-invalid"
        _seed_doctor_fixture(platform_root, root)
        receipt_path = active_platform_root(root) / ".kb/v2/receipt.json"
        active = json.loads(receipt_path.read_text())
        active["plan"]["host"]["command"] = ""
        receipt_path.write_text(json.dumps(active, indent=2) + "\n")
        result = run_command([launcher, "doctor", "--platform-root", str(root), "--json"])
        envelope, payload = _payload(result, "doctor host-spec mismatch")
        installation = payload.get("installation", {})
        checks = installation.get("checks", []) if isinstance(installation, dict) else []
        failed = [item for item in checks if isinstance(item, dict) and item.get("id") == "host-spec" and item.get("ok") is False]
        if (result.returncode != 0 or envelope.get("ok") is not True
                or payload.get("operation") != "doctor" or payload.get("status") != "unhealthy"
                or not failed or failed[0].get("code") != "KB_HOST_SPEC_INVALID"
                or "command is required" not in str(failed[0].get("detail", ""))
                or "kb-create apply --platform-root" not in str(failed[0].get("hint", ""))):
            raise RuntimeError("doctor did not report the invalid receipt host specification as an unhealthy diagnosis")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("diagnose an invalid host specification from the active receipt", "platform.install", doctor_hostspec_invalid, cases=["S-025.3"])

    def doctor_safe_default() -> None:
        root = scratch / "s025-safe-default"
        requirement = {"id": "fixture.default", "path": "/fixture/default", "required": True,
                       "default": "\"manifest-safe-value\"", "hint": "safe default"}
        _seed_doctor_fixture(platform_root, root, requirement=requirement)
        result = run_command([launcher, "doctor", "--fix", "--platform-root", str(root), "--json"])
        payload = _data(result, "doctor safe-default repair")
        config = (active_platform_root(root) / ".kb/kb.config.jsonc").read_text()
        if payload.get("operation") != "doctor-fix" or not payload.get("snapshot", {}).get("id") or "hostRestarted" not in payload:
            raise RuntimeError("doctor --fix omitted operation, snapshot, or host restart result")
        if "manifest-safe-value" not in config:
            raise RuntimeError("doctor --fix did not write the manifest-declared safe default")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("apply only a manifest-declared safe default", "platform.install", doctor_safe_default, cases=["S-025.4"])

    def doctor_required_input_after_projection_repair() -> None:
        root = scratch / "s025-required-after-fix"
        requirement = {"id": "fixture.secret", "path": "/fixture/secret", "required": True,
                       "secret": True, "hint": "supply the secret separately"}
        _seed_doctor_fixture(platform_root, root, missing_projection=True, receipt_requirement=requirement)
        result = run_command([launcher, "doctor", "--fix", "--platform-root", str(root), "--json"])
        envelope, payload = _payload(result, "doctor fix unresolved input")
        repair = payload.get("repair", {})
        required = repair.get("requiredInput", []) if isinstance(repair, dict) else []
        if result.returncode != 0 or envelope.get("ok") is not True or not required:
            raise RuntimeError("doctor --fix did not preserve unresolved requiredInput in its result")
        if not any(item.get("path") == "/fixture/secret" for item in required if isinstance(item, dict)):
            raise RuntimeError(f"doctor --fix returned the wrong required-input finding: {required}")
        if "fixture-secret-value" in result.stdout:
            raise RuntimeError("doctor --fix serialized a secret value")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("preserve unresolved user input in doctor-fix JSON", "platform.install", doctor_required_input_after_projection_repair, cases=["S-025.5"])

    def doctor_failure_dossier() -> None:
        root = scratch / "s025-failure"
        _seed_doctor_fixture(platform_root, root)
        result = run_command([launcher, "rollback", "--platform-root", str(root), "--snapshot",
                              "fixture-missing-snapshot", "--yes", "--json"])
        envelope, error = _diagnostic(result, "rollback recovery failure")
        if result.returncode == 0 or envelope.get("ok") is not False:
            raise RuntimeError("rollback unexpectedly accepted a missing recovery snapshot")
        if not all(error.get(key) for key in ("code", "stage", "message", "hint")):
            raise RuntimeError(f"recovery error omitted structured diagnostics: {error}")
        details = error.get("details", {})
        if not details.get("logPath") or not details.get("diagnosticPath"):
            raise RuntimeError("recovery error omitted its log or diagnostic dossier path")
        dossier = Path(details["diagnosticPath"]).read_text()
        if "fixture-secret-value" in dossier:
            raise RuntimeError("diagnostic dossier serialized a secret value")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("inspect structured recovery failure diagnostics", "platform.recovery", doctor_failure_dossier, cases=["S-025.6"])

    def install_with_project() -> None:
        root, project = scratch / "s187-with-project", scratch / "s187-project"
        project.mkdir(parents=True, exist_ok=True)
        before = set(project.iterdir())
        try:
            result = run_command([launcher, "install", "--index", str(stable_index),
                                  "--platform-root", str(root), "--project-root", str(project)])
            canonical = str(project.resolve())
            if (result.returncode != 0 or "Verification: platform installed at" not in result.stdout
                    or canonical not in result.stdout or not (active_platform_root(root) / ".kb/v2/receipt.json").is_file()):
                raise RuntimeError("non-interactive install did not verify and register the canonical project path")
            if set(project.iterdir()) != before:
                raise RuntimeError("project registration wrote KB Labs files into the project repository")
        finally:
            cleanup_installer_fixture_root(root, launcher, run_command)

    journey.step("install non-interactively and register a project", "platform.install", install_with_project, cases=["S-187.4"])

    def install_without_project() -> None:
        root = scratch / "s187-no-project"
        try:
            result = run_command([launcher, "install", "--index", str(stable_index), "--platform-root", str(root)])
            if result.returncode != 0 or not _hint_has_argv(result.stdout, ["kb", "project", "add", "<path>"]):
                raise RuntimeError("install without a project omitted the exact `kb project add <path>` next step")
            receipt = json.loads((active_platform_root(root) / ".kb/v2/receipt.json").read_text())
            if receipt.get("project") or receipt.get("projects"):
                raise RuntimeError("install without a project unexpectedly registered one")
        finally:
            cleanup_installer_fixture_root(root, launcher, run_command)

    journey.step("install without a project and print its registration command", "platform.install", install_without_project, cases=["S-187.5"])

    json_install_journey = run_json_install_project_step(
        mode=mode, launcher=launcher, stable_index=stable_index,
        platform_root=scratch / "s187-json-with-project",
        project_root=scratch / "s187-json-project", run_command=run_command)
    journey.steps.extend(json_install_journey.steps)

    shutil.rmtree(scratch, ignore_errors=True)
    return journey


def run_doctor_root_isolation_step(*, mode: str, launcher: str, platform_root: Path,
                                   run_command: CommandRunner) -> Journey:
    """Prove the per-user launcher never controls a different platform root."""
    journey = Journey(mode, f"doctor-root-isolation-{mode}")
    root = platform_root.parent / "installer-case-fixtures" / "s025-root-isolation"
    main_receipt_id = json.loads((platform_root / "current/.kb/v2/receipt.json").read_text()).get("id")

    def repair_fixture_without_stopping_main() -> None:
        requirement = {"id": "fixture.default", "path": "/fixture/default", "required": True,
                       "default": "\"manifest-safe-value\"", "hint": "safe default"}
        _seed_doctor_fixture(platform_root, root, requirement=requirement)
        result = run_command([launcher, "doctor", "--fix", "--platform-root", str(root), "--json"])
        payload = _data(result, "isolated-root doctor repair")
        if payload.get("hostRestarted") is not False:
            raise RuntimeError("doctor --fix treated another platform root's host as the fixture host")
        fixture_status = _data(run_command([launcher, "status", "--platform-root", str(root), "--json"]),
                               "isolated platform status")
        if fixture_status.get("host", {}).get("state") != "stopped":
            raise RuntimeError("status for platform B reported platform A's host")
        main_status = _data(run_command([launcher, "status", "--platform-root", str(platform_root), "--json"]),
                            "main platform status after isolated-root repair")
        if main_status.get("host", {}).get("state") != "running":
            raise RuntimeError("repairing an isolated root stopped or replaced the main platform host")
        for spelling in (platform_root, platform_root / "current", (platform_root / "current").resolve()):
            spelling_status = _data(run_command([launcher, "status", "--platform-root", str(spelling), "--json"]),
                                    f"main platform status through {spelling}")
            if (spelling_status.get("host", {}).get("state") != "running"
                    or spelling_status.get("installation", {}).get("receiptId") != main_receipt_id):
                raise RuntimeError(f"status through {spelling} did not identify platform A's original running host")

    journey.step("repair an isolated root without stopping the running platform", "platform.install",
                 repair_fixture_without_stopping_main, cases=["S-025.7"])

    def refuse_start_for_other_root() -> None:
        lifecycle = InstallerHostLifecycle(launcher, run_command)
        with lifecycle.preserve([platform_root]) as initial:
            result = run_command([launcher, "start", "--platform-root", str(root), "--json"])
            envelope, error = _diagnostic(result, "cross-root host start")
            details = error.get("meta", error.get("details", {}))
            hint = error.get("hint", "")
            commands = re.findall(r"`([^`]+)`", hint)
            stop_argv = shlex.split(commands[0]) if commands else []
            expected_stop = ["kb-create", "stop", "--platform-root", str(platform_root.resolve())]
            if (result.returncode == 0 or envelope.get("ok") is not False
                    or error.get("code") != "KB_HOST_ALREADY_RUNNING"
                    or str(platform_root.resolve()) not in error.get("message", "")
                    or not isinstance(details, dict) or not details.get("pid")
                    or stop_argv != expected_stop):
                raise RuntimeError(f"cross-root start did not identify the active platform and runnable stop command: {error}")
            stopped = run_command(stop_argv + ["--json"])
            _data(stopped, "execute printed cross-root stop command")
            main_status = _data(run_command([launcher, "status", "--platform-root", str(platform_root), "--json"]),
                                "main status after suggested stop command")
            if main_status.get("host", {}).get("state") != "stopped":
                raise RuntimeError("executing the printed stop command did not stop platform A")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("refuse to start a second platform while the user's host is running", "platform.install",
                 refuse_start_for_other_root, cases=["S-025.8"])
    return journey
