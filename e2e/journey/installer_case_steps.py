"""Additional install, repair, and request-selection case claims."""
from __future__ import annotations

import json
import re
import shutil
import subprocess
from pathlib import Path
from typing import Callable

from platform_update_steps import _json_command
from service_steps import Journey

CommandRunner = Callable[[list[str]], subprocess.CompletedProcess]


def _data(result: subprocess.CompletedProcess, label: str) -> dict:
    if result.returncode == 0:
        try:
            envelope = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise RuntimeError(f"{label}: expected JSON output") from error
        payload = envelope.get("data", {}) if isinstance(envelope, dict) else {}
        if isinstance(envelope, dict) and envelope.get("ok") is True and isinstance(payload, dict):
            return payload
    raise RuntimeError(f"{label}: exit {result.returncode}; {' '.join(result.stdout.split())[-500:]}")


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

    journey.step("refuse a strict incompatible plugin request before mutation", "platform.install",
                 lambda: "BLOCKED: no sealed incompatible plugin fixture is present in the stable release index",
                 cases=["S-181.5"])
    for generated_root in (apply_root, platform_root.parent / "s181-stable-root",
                           platform_root.parent / "s181-canary-root", secret_root,
                           platform_root.parent / "s181-offline-root"):
        shutil.rmtree(generated_root, ignore_errors=True)
    return journey
