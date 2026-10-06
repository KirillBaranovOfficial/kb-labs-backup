"""Additional install, repair, and request-selection case claims."""
from __future__ import annotations

import json
import platform
import re
import shutil
import socket
import subprocess
import sys
from pathlib import Path
from typing import Callable

from platform_update_steps import _json_command
from invocations import (decode_ux_error_result, decode_ux_success_result, read_ux_cli_result)
from service_steps import Journey

CommandRunner = Callable[[list[str]], subprocess.CompletedProcess]


def _data(result: subprocess.CompletedProcess, label: str) -> dict:
    try:
        envelope = read_ux_cli_result(result.stdout, result.returncode)
        payload = decode_ux_success_result(envelope) if result.returncode == 0 else decode_ux_error_result(envelope)
    except ValueError as error:
        raise RuntimeError(f"{label}: expected an ADR-0049 JSON result: {error}") from error
    if result.returncode == 0 and isinstance(payload, dict):
        return payload
    raise RuntimeError(f"{label}: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")


def _payload(result: subprocess.CompletedProcess, label: str) -> tuple[dict, dict]:
    try:
        envelope = read_ux_cli_result(result.stdout, result.returncode)
        data = (decode_ux_success_result(envelope) if result.returncode == 0
                else decode_ux_error_result(envelope))
    except ValueError as error:
        raise RuntimeError(f"{label}: expected an ADR-0049 JSON result: {error}") from error
    return envelope, data if isinstance(data, dict) else {}


def _diagnostic(result: subprocess.CompletedProcess, label: str) -> tuple[dict, dict]:
    envelope, error = _payload(result, label)
    if result.returncode == 0 or not isinstance(error, dict):
        raise RuntimeError(f"{label}: expected a structured error")
    return envelope, error


def _seed_doctor_fixture(source_root: Path, target_root: Path, *, requirement: dict | None = None,
                         missing_projection: bool = False, receipt_requirement: dict | None = None) -> Path:
    """Copy a real receipt identity and materialize only its manifest projections."""
    active = json.loads((source_root / ".kb/v2/receipt.json").read_text())
    artifacts = active.get("plan", {}).get("artifacts", [])
    packages = [item for item in artifacts if item.get("kind") != "binary"]
    if not packages:
        raise RuntimeError("active receipt has no package artifacts for a doctor fixture")
    target_root.mkdir(parents=True, exist_ok=True)
    target_root.joinpath(".kb/v2").mkdir(parents=True, exist_ok=True)
    target_root.joinpath(".kb/kb.config.jsonc").write_text("{}\n")
    effective_requirement = receipt_requirement or requirement
    if effective_requirement is not None:
        selected = {"id": "journey-doctor-fixture", "kind": "plugin", "package": "@kb-labs/journey-doctor-fixture",
                    "version": "1.0.0", "config": [effective_requirement]}
        artifacts.append(selected)
        packages.append(selected)
    else:
        selected = packages[0]
    target_root.joinpath(".kb/v2/receipt.json").write_text(json.dumps(active, indent=2) + "\n")
    for item in packages:
        package = item["package"]
        package_root = target_root / "node_modules" / package
        package_root.mkdir(parents=True, exist_ok=True)
        (package_root / "package.json").write_text(json.dumps({"name": package, "version": item["version"]}) + "\n")
        if missing_projection and item is selected:
            continue
        manifest = {"schema": "kb.create.artifact-manifest/v2", "id": item["id"],
                    "package": package, "version": item["version"], "requirements": []}
        if effective_requirement is not None and item is selected:
            manifest["requirements"] = [effective_requirement]
        (package_root / "kb-create.manifest.json").write_text(json.dumps(manifest) + "\n")
    return selected


def _seed_update_fixture(target_root: Path, *, kind: str, component: str) -> Path:
    target_root.joinpath(".kb/v2").mkdir(parents=True, exist_ok=True)
    receipt_path = target_root / ".kb/v2/receipt.json"
    artifact = {"id": component, "kind": kind, "package": f"@kb/{component}", "version": "1.0.0",
                "sha256": "fixture-artifact", "platformRange": "<3.2.0"}
    receipt = {"schema": "kb.create.receipt/v2", "id": f"fixture-{kind}",
               "plan": {"schema": "kb.create.resolved-plan/v2", "artifacts": [artifact],
                        "serviceGraph": {"platformVersion": "3.0.0"},
                        "request": {"platformRoot": str(target_root)}}}
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


def run_installer_case_steps(*, mode: str, launcher: str, stable_index: Path,
                             canary_index: Path, platform_root: Path,
                             secret_value: str, run_command: CommandRunner) -> Journey:
    """Claim request, configuration, doctor-repair, and compatibility cases.

    Install roots used by the request-selection steps are children of the
    journey's already isolated HOME; callers must pass that HOME's paths.
    """
    journey = Journey(mode, f"installer-cases-{mode}")
    root = str(platform_root)
    port_conflict_root = platform_root.parent / "pc001-port-conflict-root"

    def port_conflict_ux() -> None:
        with socket.socket() as owner:
            owner.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            owner.bind(("127.0.0.1", 4000))
            owner.listen(1)
            result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                  "--request-platform-root", str(port_conflict_root),
                                  "--service-profile", "default", "--scenario", "commit"])
        envelope, error = _diagnostic(result, "PC-001 port conflict")
        details = error.get("details", error.get("meta", {}))
        if (result.returncode == 0 or envelope.get("ok") is not False
                or error.get("code") != "KB_INSTALL_PORT_IN_USE"
                or not isinstance(details, dict)
                or details.get("port") != "4000" or not details.get("pid") or not details.get("command")
                or "kb-create apply" not in error.get("hint", "")
                or str(details["pid"]) not in error.get("hint", "")
                or str(details["command"]) not in error.get("hint", "")):
            raise RuntimeError(f"port conflict omitted its catalogued code, owner, or apply hint: {error}")

    journey.step("report the owning process when apply cannot claim a required port", "platform.install",
                 port_conflict_ux, cases=["PC-001.9"])

    # PC-001.4 applies an explicit fresh fixture so the end-to-end installer
    # remains the single main journey install and can continue serving later cases.
    apply_root = platform_root.parent / "pc001-apply-root"
    apply_root.mkdir(parents=True, exist_ok=True)

    def apply_and_check() -> None:
        result = _data(run_command([launcher, "apply", "--json", "--index", str(stable_index),
                                   "--request-platform-root", str(apply_root),
                                   "--service-profile", "default", "--scenario", "commit"]),
                      "PC-001 apply")
        if result.get("operation") != "apply" or not result.get("receipt") or not result.get("logPath"):
            raise RuntimeError("apply result omitted operation, receipt, or logPath")

    journey.step("apply the resolved default service profile", "platform.install", apply_and_check,
                 cases=["PC-001.4"])

    def inspect_generated_config() -> None:
        config_path = apply_root / ".kb/kb.config.jsonc"
        services_path = apply_root / ".kb/devservices.yaml"
        if not config_path.is_file() or not services_path.is_file():
            raise RuntimeError("apply did not generate both runtime config and service declarations")
        config = json.loads(_without_jsonc_comments(config_path.read_text()))
        services = services_path.read_text()
        if not isinstance(config, dict) or not services.strip():
            raise RuntimeError("generated runtime config or service declarations are empty")
        receipt = json.loads((apply_root / ".kb/v2/receipt.json").read_text())
        graph = receipt.get("plan", {}).get("serviceGraph", {})
        declarations = graph.get("services", [])
        for service in declarations:
            name = service if isinstance(service, str) else service.get("id", service.get("name", ""))
            if name and not re.search(rf"(?m)^\s*{re.escape(str(name))}\s*:", services):
                raise RuntimeError(f"generated devservices.yaml omits selected service {name}")

    journey.step("inspect generated runtime config and service declarations", "platform.install",
                 inspect_generated_config, cases=["PC-001.5"])

    def repeat_plan() -> None:
        before = {str(path.relative_to(platform_root)): path.read_bytes()
                  for path in platform_root.rglob("*") if path.is_file()}
        first = _json_command(run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                                            "--request-platform-root", root, "--service-profile", "default",
                                            "--scenario", "commit"], "first clean-install plan").get("plan", {})
        second = _json_command(run_command, [launcher, "plan", "--json", "--index", str(stable_index),
                                             "--request-platform-root", root, "--service-profile", "default",
                                             "--scenario", "commit"], "repeat clean-install plan").get("plan", {})
        if any(first.get(key) != second.get(key) for key in ("planHash", "artifacts", "serviceGraph")):
            raise RuntimeError("repeat plan changed planHash, artifacts, or serviceGraph")
        after = {str(path.relative_to(platform_root)): path.read_bytes()
                 for path in platform_root.rglob("*") if path.is_file()}
        if before != after:
            raise RuntimeError("planning wrote or changed files under the active platform root")

    journey.step("repeat install planning without changing the installed root", "platform.install",
                 repeat_plan, cases=["PC-001.8"])

    def channel_plan(channel: str, index: Path, root_name: str) -> None:
        plan = _json_command(run_command, [launcher, "plan", "--json", "--index", str(index),
                                           "--platform-channel", channel, "--policy", "compatible",
                                           "--request-platform-root", str(platform_root.parent / root_name)],
                             f"{channel} plan").get("plan", {})
        artifacts = plan.get("artifacts", [])
        if (plan.get("platformChannel") != channel or not plan.get("serviceGraph", {}).get("platformVersion")
                or not artifacts or not all(item.get("sha256") for item in artifacts)):
            raise RuntimeError(f"{channel} plan did not resolve an exact version and immutable hashes")
        applied = _data(run_command([launcher, "apply", "--json", "--index", str(index),
                                     "--platform-channel", channel, "--policy", "compatible",
                                     "--request-platform-root", str(platform_root.parent / root_name)]),
                        f"{channel} apply")
        status = _json_command(run_command, [launcher, "status", "--json", "--platform-root",
                                             str(platform_root.parent / root_name)], f"{channel} status")
        installation = status.get("installation", status)
        exact_version = plan["serviceGraph"]["platformVersion"]
        if (not applied.get("receipt") or installation.get("platformChannel") != channel
                or installation.get("platformVersion") != exact_version):
            raise RuntimeError(f"receipt-backed status did not confirm {channel} {exact_version}")

    for channel, index, root_name, claim in (
        ("stable", stable_index, "s181-stable-root", "S-181.1"),
        ("canary", canary_index, "s181-canary-root", "S-181.2"),
    ):
        journey.step(f"plan, apply, and verify {channel} install", "platform.install",
                     lambda c=channel, i=index, r=root_name: channel_plan(c, i, r), cases=[claim])

    secret_root = platform_root.parent / "s181-secret-root"
    def secret_apply() -> None:
        if not secret_value:
            raise RuntimeError("isolated journey secret is unavailable for S-181.3")
        result = run_command([launcher, "apply", "--json", "--index", str(stable_index),
                              "--platform-channel", "stable", "--request-platform-root", str(secret_root),
                              "--scenario", "custom", "--scenario-answers",
                              json.dumps({"access.mode": "secured", "admin.email": "journey-admin@example.test",
                                          "admin.tenant": "journey"}),
                              "--secret-env", "gateway.bootstrap.password=KB_JOURNEY_ADMIN_PASSWORD"])
        if result.returncode != 0:
            raise RuntimeError(f"secret apply: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")
        receipt_path = secret_root / ".kb/v2/receipt.json"
        serialized = result.stdout + (receipt_path.read_text() if receipt_path.is_file() else "")
        if secret_value in serialized or "KB_JOURNEY_ADMIN_PASSWORD" in serialized:
            raise RuntimeError("secret apply serialized the secret value or environment-variable name")

    journey.step("apply a secret through an environment reference", "platform.install", secret_apply,
                 cases=["S-181.3"])

    def offline_plan() -> None:
        plan = _json_command(run_command([launcher, "plan", "--json", "--index", str(stable_index),
                                          "--platform-channel", "stable", "--offline",
                                          "--request-platform-root", str(platform_root.parent / "s181-offline-root")]),
                             "offline plan").get("plan", {})
        sources = {item.get("source") for item in plan.get("artifacts", [])}
        if not sources or sources != {"offline"}:
            raise RuntimeError(f"offline plan did not identify only offline artifacts: {sorted(map(str, sources))}")

    journey.step("resolve a fresh request from the local artifact cache", "platform.install", offline_plan,
                 cases=["S-181.4"])

    for generated_root in (apply_root, platform_root.parent / "s181-stable-root",
                           platform_root.parent / "s181-canary-root", secret_root,
                           platform_root.parent / "s181-offline-root"):
        shutil.rmtree(generated_root, ignore_errors=True)
    for step in run_installer_fixture_steps(mode=mode, launcher=launcher, platform_root=platform_root,
                                            stable_index=stable_index, run_command=run_command).steps:
        journey.step(step.name, step.capability,
                     lambda step=step: None if step.status == "PASS" else step.message,
                     cases=step.cases)
    return journey


def run_installer_fixture_steps(*, mode: str, launcher: str, platform_root: Path,
                                stable_index: Path, run_command: CommandRunner) -> Journey:
    """Exercise isolated doctor, compatibility, and project-install fixtures."""
    journey = Journey(mode, f"installer-fixtures-{mode}")
    fixture_dir = Path(__file__).resolve().parents[2] / "e2e/platform/fixtures/installer-cases"
    host_os = "darwin" if sys.platform == "darwin" else "linux" if sys.platform.startswith("linux") else "unsupported"
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower(), platform.machine().lower())
    if host_os == "unsupported":
        raise RuntimeError("installer compatibility fixtures support only Linux and macOS journey runners")
    matrix = fixture_dir / f"compatibility-matrix-{host_os}-{host_arch}.json"
    missing_binary = fixture_dir / f"missing-host-binary-{host_os}-{host_arch}.json"
    scratch = platform_root.parent / "installer-case-fixtures"
    scratch.mkdir(parents=True, exist_ok=True)

    def exact_step(step_id: str, action: Callable[[], None], name: str, capability: str = "platform.install") -> None:
        journey.step(name, capability, action, cases=[step_id])

    def strict_matrix_and_range() -> None:
        strict = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.2.0",
                              "--policy", "strict", "--request-platform-root", str(scratch / "strict-sdk")])
        _, error = _diagnostic(strict, "strict SDK matrix plan")
        meta = error.get("meta", {})
        if strict.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("strict SDK plan did not return KB_INSTALL_INCOMPATIBLE_COMPONENTS")
        if any(meta.get(key) != value for key, value in (("component", "SDK"), ("version", "3.2.0"),
                                                          ("range", ">=3.0.0 <4.0.0"), ("policy", "strict"))):
            raise RuntimeError(f"strict SDK error details are incomplete: {meta}")
        if error.get("hint") != "Use an exact platform/SDK pair from the release matrix or choose --policy compatible.":
            raise RuntimeError("strict SDK refusal omitted its exact correction hint")
        compatible = _data(run_command([launcher, "plan", "--json", "--index", str(matrix),
                                        "--platform-version", "3.2.0", "--sdk-version", "3.2.0",
                                        "--policy", "compatible", "--request-platform-root", str(scratch / "compatible-sdk")]),
                           "compatible SDK matrix plan")
        if compatible.get("plan", {}).get("serviceGraph", {}).get("platformVersion") != "3.2.0":
            raise RuntimeError("compatible policy did not resolve the indexed SDK in the declared range")

    exact_step("S-053.2", strict_matrix_and_range, "compare strict and compatible SDK matrix selection")

    def incompatible_adapter() -> None:
        result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--adapters", "fixture-adapter@1.0.0", "--policy", "compatible",
                              "--request-platform-root", str(scratch / "adapter-range")])
        _, error = _diagnostic(result, "adapter range plan")
        meta = error.get("meta", {})
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("out-of-range adapter plan did not return KB_INSTALL_INCOMPATIBLE_COMPONENTS")
        if meta.get("component") != "adapter fixture-adapter platform" or meta.get("version") != "3.2.0" or meta.get("range") != "<3.2.0":
            raise RuntimeError(f"adapter range refusal omitted its component details: {meta}")
        if error.get("hint") != "Choose compatible versions or pass an explicit supported provider.":
            raise RuntimeError("adapter range refusal omitted its correction hint")

    exact_step("S-053.4", incompatible_adapter, "refuse an adapter outside its platform range")

    def installed_component_update(kind: str, component: str, claim: str) -> None:
        root = scratch / f"{claim.replace('.', '-')}-root"
        receipt_path = _seed_update_fixture(root, kind=kind, component=component)
        before_receipt = receipt_path.read_bytes()
        before_files = {str(path.relative_to(root)): path.read_bytes() for path in root.rglob("*") if path.is_file()}
        before_directories = {str(path.relative_to(root)) for path in root.rglob("*") if path.is_dir()}
        result = run_command([launcher, "update", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--policy", "compatible", "--request-platform-root", str(root)])
        _, error = _diagnostic(result, "installed component update")
        meta = error.get("meta", {})
        expected_code = "KB_UPDATE_INCOMPATIBLE"
        expected_hint = f"Update or remove {kind} {component} first, then rerun `kb-create update`."
        after_files = {str(path.relative_to(root)): path.read_bytes() for path in root.rglob("*") if path.is_file()}
        after_directories = {str(path.relative_to(root)) for path in root.rglob("*") if path.is_dir()}
        if result.returncode == 0 or error.get("code") != expected_code or not isinstance(meta, dict):
            raise RuntimeError(f"incompatible installed {kind} update did not return {expected_code}: {error}")
        if (meta.get("component") != component or meta.get("kind") != kind or meta.get("version") != "3.2.0"
                or meta.get("range") != "<3.2.0" or error.get("hint") != expected_hint):
            raise RuntimeError(f"installed {kind} refusal omitted its range or correction details: {error}")
        if (receipt_path.read_bytes() != before_receipt or before_files != after_files
                or before_directories != after_directories):
            raise RuntimeError(f"incompatible {kind} update mutated the active receipt or install root")

    exact_step("S-053.3", lambda: installed_component_update("plugin", "fixture-compat", "S-053.3"),
               "refuse update beyond an installed plugin range", "platform.update")

    def sdk_and_binary_ranges() -> None:
        sdk_result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                                  "--platform-version", "3.2.0", "--sdk-version", "4.0.0",
                                  "--policy", "compatible", "--request-platform-root", str(scratch / "sdk-outside-range")])
        _, sdk_error = _diagnostic(sdk_result, "out-of-range SDK plan")
        sdk_meta = sdk_error.get("meta", {})
        if sdk_result.returncode == 0 or sdk_error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(sdk_meta, dict):
            raise RuntimeError("out-of-range SDK plan did not fail with the typed incompatibility")
        if sdk_meta.get("component") != "platform SDK" or sdk_meta.get("version") != "4.0.0" or sdk_meta.get("range") != ">=3.0.0 <4.0.0":
            raise RuntimeError(f"out-of-range SDK refusal omitted component, version, or range: {sdk_meta}")
        binary_result = run_command([launcher, "plan", "--json", "--index", str(missing_binary),
                                     "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                                     "--policy", "compatible", "--request-platform-root", str(scratch / "missing-binary")])
        _, binary_error = _diagnostic(binary_result, "missing binary plan")
        binary_meta = binary_error.get("meta", {})
        if binary_result.returncode == 0 or binary_error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(binary_meta, dict):
            raise RuntimeError("missing host binary plan did not fail with the typed incompatibility")
        if binary_meta.get("subject") != "binary" or not binary_meta.get("value") or not binary_error.get("hint"):
            raise RuntimeError(f"missing binary error omitted subject, OS/architecture, or hint: {binary_error}")

    exact_step("S-053.5", sdk_and_binary_ranges, "refuse an SDK outside range and a release missing the host binary")

    exact_step("S-053.6", lambda: installed_component_update("adapter", "fixture-adapter", "S-053.6"),
               "refuse update beyond an installed adapter range", "platform.update")

    def missing_host_binary_before_mutation() -> None:
        target = scratch / "missing-host-root"
        before_exists = target.exists()
        before = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob("*") if path.is_file()} if target.exists() else {}
        result = run_command([launcher, "plan", "--json", "--index", str(missing_binary),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--policy", "compatible", "--request-platform-root", str(target)])
        _, error = _diagnostic(result, "missing host binary plan")
        meta = error.get("meta", {})
        after = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob("*") if path.is_file()} if target.exists() else {}
        after_exists = target.exists()
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or not isinstance(meta, dict):
            raise RuntimeError("missing host binary did not refuse planning with the typed incompatibility")
        if meta.get("subject") != "binary" or not meta.get("value") or before != after or before_exists != after_exists:
            raise RuntimeError("missing binary refusal omitted its subject/value or changed the isolated install root")

    exact_step("S-053.7", missing_host_binary_before_mutation, "refuse planning without the runner binary")

    def incompatible_plugin_request() -> None:
        result = run_command([launcher, "plan", "--json", "--index", str(matrix),
                              "--platform-version", "3.2.0", "--sdk-version", "3.0.0",
                              "--plugins", "fixture-plugin@1.0.0", "--policy", "strict",
                              "--request-platform-root", str(scratch / "plugin-request")])
        _, error = _diagnostic(result, "incompatible plugin request")
        if result.returncode == 0 or error.get("code") != "KB_INSTALL_INCOMPATIBLE_COMPONENTS" or "fixture-plugin" not in str(error):
            raise RuntimeError(f"strict incompatible plugin request was not refused: {error}")
        if not error.get("hint"):
            raise RuntimeError("strict incompatible plugin request omitted its correction hint")

    exact_step("S-181.5", incompatible_plugin_request, "refuse a strict incompatible plugin request before mutation")

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

    exact_step("S-025.1", lambda: doctor_finding(), "diagnose a missing required non-secret config value")
    exact_step("S-025.2", lambda: doctor_finding(secret=True), "diagnose missing secret input without exposing a value")

    def doctor_hostspec_invalid() -> None:
        root = scratch / "s025-hostspec-invalid"
        _seed_doctor_fixture(platform_root, root)
        receipt_path = root / ".kb/v2/receipt.json"
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

    exact_step("S-025.3", doctor_hostspec_invalid, "diagnose an invalid host specification from the active receipt")

    def doctor_safe_default() -> None:
        root = scratch / "s025-safe-default"
        requirement = {"id": "fixture.default", "path": "/fixture/default", "required": True,
                       "default": "\"manifest-safe-value\"", "hint": "safe default"}
        _seed_doctor_fixture(platform_root, root, requirement=requirement)
        result = run_command([launcher, "doctor", "--fix", "--platform-root", str(root), "--json"])
        payload = _data(result, "doctor safe-default repair")
        config = (root / ".kb/kb.config.jsonc").read_text()
        if payload.get("operation") != "doctor-fix" or not payload.get("snapshot", {}).get("id") or "hostRestarted" not in payload:
            raise RuntimeError("doctor --fix omitted operation, snapshot, or host restart result")
        if "manifest-safe-value" not in config:
            raise RuntimeError("doctor --fix did not write the manifest-declared safe default")
        shutil.rmtree(root, ignore_errors=True)

    exact_step("S-025.4", doctor_safe_default, "apply only a manifest-declared safe default")

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

    exact_step("S-025.5", doctor_required_input_after_projection_repair, "preserve unresolved user input in doctor-fix JSON")

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
        if not envelope.get("logPath") or not envelope.get("diagnosticPath"):
            raise RuntimeError("recovery error omitted its log or diagnostic dossier path")
        dossier = Path(envelope["diagnosticPath"]).read_text()
        if "fixture-secret-value" in dossier:
            raise RuntimeError("diagnostic dossier serialized a secret value")
        shutil.rmtree(root, ignore_errors=True)

    exact_step("S-025.6", doctor_failure_dossier, "inspect structured recovery failure diagnostics", "platform.recovery")

    def install_with_project() -> None:
        root, project = scratch / "s187-with-project", scratch / "s187-project"
        project.mkdir(parents=True, exist_ok=True)
        before = set(project.iterdir())
        result = run_command([launcher, "install", "--index", str(stable_index),
                              "--platform-root", str(root), "--project-root", str(project)])
        canonical = str(project.resolve())
        if (result.returncode != 0 or "Verification: platform installed at" not in result.stdout
                or canonical not in result.stdout or not (root / ".kb/v2/receipt.json").is_file()):
            raise RuntimeError("non-interactive install did not verify and register the canonical project path")
        if set(project.iterdir()) != before:
            raise RuntimeError("project registration wrote KB Labs files into the project repository")
        shutil.rmtree(root, ignore_errors=True)

    exact_step("S-187.4", install_with_project, "install non-interactively and register a project", "platform.install")

    def install_without_project() -> None:
        root = scratch / "s187-no-project"
        result = run_command([launcher, "install", "--index", str(stable_index), "--platform-root", str(root)])
        if result.returncode != 0 or "kb project add <path>" not in result.stdout:
            raise RuntimeError("install without a project omitted the exact `kb project add <path>` next step")
        receipt = json.loads((root / ".kb/v2/receipt.json").read_text())
        if receipt.get("project") or receipt.get("projects"):
            raise RuntimeError("install without a project unexpectedly registered one")
        shutil.rmtree(root, ignore_errors=True)

    exact_step("S-187.5", install_without_project, "install without a project and print its registration command", "platform.install")

    shutil.rmtree(scratch, ignore_errors=True)
    return journey


def run_doctor_root_isolation_step(*, mode: str, launcher: str, platform_root: Path,
                                   run_command: CommandRunner) -> Journey:
    """Prove the per-user launcher never controls a different platform root."""
    journey = Journey(mode, f"doctor-root-isolation-{mode}")
    root = platform_root.parent / "installer-case-fixtures" / "s025-root-isolation"

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

    journey.step("repair an isolated root without stopping the running platform", "platform.install",
                 repair_fixture_without_stopping_main, cases=["S-025.7"])

    def refuse_start_for_other_root() -> None:
        result = run_command([launcher, "start", "--platform-root", str(root), "--json"])
        envelope, error = _diagnostic(result, "cross-root host start")
        details = error.get("meta", error.get("details", {}))
        if (result.returncode == 0 or envelope.get("ok") is not False
                or error.get("code") != "KB_HOST_ALREADY_RUNNING"
                or str(platform_root.resolve()) not in error.get("message", "")
                or not isinstance(details, dict) or not details.get("pid")
                or f"kb-create stop --platform-root {platform_root}" not in error.get("hint", "")):
            raise RuntimeError(f"cross-root start did not identify the active platform and stop command: {error}")
        main_status = _data(run_command([launcher, "status", "--platform-root", str(platform_root), "--json"]),
                            "main status after refused cross-root start")
        if main_status.get("host", {}).get("state") != "running":
            raise RuntimeError("refused start for platform B affected platform A")
        shutil.rmtree(root, ignore_errors=True)

    journey.step("refuse to start a second platform while the user's host is running", "platform.install",
                 refuse_start_for_other_root, cases=["S-025.8"])
    return journey
