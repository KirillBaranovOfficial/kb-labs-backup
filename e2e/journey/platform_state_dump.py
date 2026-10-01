"""Bounded, secret-redacted snapshot of an installed journey platform."""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Mapping
from typing import Any

MAX_DUMP_BYTES = 5 * 1024 * 1024
CONTENT_BUDGET = MAX_DUMP_BYTES - 64 * 1024
MAX_FILE_BYTES = 1024 * 1024
TAIL_LINES = 400
SECRET_KEY = re.compile(r"(?i)(password|token|secret|apiKey|key)")
UNKNOWN_COMMAND = re.compile(r"Unknown command:\s*([^\n\r]+)")


class PlatformStateDumpOnce:
    """Ensure one runner invocation writes at most one platform snapshot."""

    def __init__(self) -> None:
        self._dumped = False

    def reset(self) -> None:
        self._dumped = False

    def dump(self, *args: Any, **kwargs: Any) -> Path | None:
        if self._dumped:
            return None
        self._dumped = True
        return dump_platform_state(*args, **kwargs)


def _redact_json(value):
    if isinstance(value, dict):
        return {key: "[REDACTED]" if SECRET_KEY.search(str(key)) else _redact_json(item)
                for key, item in value.items()}
    if isinstance(value, list):
        return [_redact_json(item) for item in value]
    return value


def _redact_text(text: str, admin_password: str) -> str:
    if admin_password:
        text = text.replace(admin_password, "[REDACTED]")
    text = re.sub(
        r"(?i)(\b[A-Z0-9_]*(?:PASSWORD|TOKEN|SECRET|API[_-]?KEY|PRIVATE[_-]?KEY)[A-Z0-9_]*\s*=\s*)[^\s]+",
        r"\1[REDACTED]",
        text,
    )
    # JSONC is not strict JSON; redact scalar values for secret-named keys.
    return re.sub(
        r'(?i)(["\']?(?:password|token|secret|apiKey|key)["\']?\s*:\s*)("(?:\\.|[^"\\])*"|\'[^\']*\'|[^,}\]\s]+)',
        r'\1"[REDACTED]"', text,
    )


def _safe_copy(source: Path, target: Path, admin_password: str, *, tail: bool = False) -> int:
    try:
        if not source.is_file():
            return 0
        size = source.stat().st_size
        if size > MAX_FILE_BYTES and not tail:
            return 0
        content = source.read_text(errors="replace")
        if tail:
            content = "\n".join(content.splitlines()[-TAIL_LINES:]) + "\n"
            # A pathological single line must not defeat the per-file and dump caps.
            content = content[-MAX_FILE_BYTES:]
        if source.suffix.lower() in {".json", ".jsonc"}:
            try:
                content = _redact_text(json.dumps(_redact_json(json.loads(content)), indent=2) + "\n",
                                       admin_password)
            except json.JSONDecodeError:
                content = _redact_text(content, admin_password)
        else:
            content = _redact_text(content, admin_password)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
        return target.stat().st_size
    except OSError:
        return 0


def _command(output: Path, label: str, args: list[str], *, env: Mapping[str, str], cwd: Path,
             admin_password: str, timeout: int = 30, budget: int = MAX_DUMP_BYTES) -> int:
    try:
        from invocations import run_command
        result = run_command(args, cwd=cwd, env=env, timeout=timeout)
        body = f"exit_code: {result.returncode}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
    except (OSError, subprocess.TimeoutExpired) as error:
        body = f"error: {error.__class__.__name__}: {error}"
    body = _redact_text(body, admin_password)
    encoded = f"{label}\n{body}\n".encode()
    if len(encoded) > budget:
        encoded = encoded[:budget]
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(encoded)
    return len(encoded)


def dump_platform_state(platform_root: str | Path, reports_dir: str | Path, *, mode: str,
                        front: str, scenario: str, variant: str, first_failure: str,
                        env: Mapping[str, str] | None = None, cwd: str | Path | None = None,
                        admin_password: str = "") -> Path:
    """Write a best-effort platform snapshot and index; never raise dump errors."""
    root = Path(platform_root)
    reports = Path(reports_dir)
    destination = reports / f"platform-state-{mode}-{front}-{scenario}-{variant}"
    try:
        destination.mkdir(parents=True, exist_ok=True)
        used = 0
        entries: list[str] = []

        def add(source: Path, relative: Path, *, tail: bool = False) -> None:
            nonlocal used
            if used >= CONTENT_BUDGET:
                return
            written = _safe_copy(source, destination / relative, admin_password, tail=tail)
            if written and used + written <= CONTENT_BUDGET:
                used += written
                entries.append(f"- `{relative}` ({written} bytes)")
            elif written:
                (destination / relative).unlink(missing_ok=True)

        for relative in (Path(".kb/marketplace.lock"), Path(".kb/kb.config.jsonc"),
                         Path(".kb/v2/receipt.json")):
            add(root / relative, relative)
        for source in sorted((root / ".kb/v2/snapshots").glob("*.json")):
            add(source, Path(".kb/v2/snapshots") / source.name)
        home_value = (env or os.environ).get("HOME")
        launcher_root = Path(home_value) / ".kb" / "launcher" if home_value else None
        launcher_logs = []
        if launcher_root:
            try:
                launcher_logs = sorted(path for path in launcher_root.rglob("*.log") if path.is_file())
            except OSError:
                pass
        for source in launcher_logs:
            if used >= CONTENT_BUDGET:
                break
            relative = Path("launcher-logs") / source.relative_to(launcher_root)
            add(source, relative, tail=True)
        receipt_path = root / ".kb/v2/receipt.json"
        try:
            receipt_value = json.loads(receipt_path.read_text())
            artifacts = receipt_value.get("plan", {}).get("artifacts", [])
            artifact_manifests = []
            for artifact in artifacts:
                package = artifact.get("package")
                if not isinstance(package, str) or not package:
                    continue
                manifest_path = root / "node_modules" / Path(*package.split("/")) / "kb-create.manifest.json"
                artifact_manifests.append({
                    "id": artifact.get("id"),
                    "package": package,
                    "version": artifact.get("version"),
                    "manifestExists": manifest_path.is_file(),
                })
            if artifact_manifests:
                target = destination / "receipt-artifact-manifests.json"
                encoded = json.dumps(artifact_manifests, indent=2) + "\n"
                if used + len(encoded.encode()) <= CONTENT_BUDGET:
                    target.write_text(encoded)
                    used += target.stat().st_size
                    entries.append(f"- `receipt-artifact-manifests.json` ({target.stat().st_size} bytes)")
        except (OSError, json.JSONDecodeError, AttributeError, TypeError):
            pass
        for directory in ("logs", "diagnostics"):
            files = sorted((root / ".kb" / directory).glob("*"),
                           key=lambda path: path.stat().st_mtime if path.exists() else 0, reverse=True)
            for source in files[:3]:
                add(source, Path(".kb") / directory / source.name, tail=directory == "logs")

        # Workflow state belongs to the registered project, which is a sibling
        # of the installed platform in the journey container. Keep the search
        # shallow and targeted so diagnostics never copy unrelated user files.
        evidence_roots = [root]
        try:
            evidence_roots.extend(
                candidate for candidate in root.parent.iterdir()
                if candidate.is_dir() and candidate != root
            )
        except OSError:
            pass
        for evidence_root in evidence_roots:
            evidence_prefix = Path("workflow-evidence") / evidence_root.name
            workflow_records = evidence_root / ".kb" / "data" / "workflows"
            try:
                records = sorted(workflow_records.glob("*.json"),
                                 key=lambda path: path.stat().st_mtime, reverse=True)
            except OSError:
                records = []
            for source in records[:20]:
                add(source, evidence_prefix / ".kb" / "data" / "workflows" / source.name)

            for directory in ("logs", "diagnostics"):
                log_root = evidence_root / ".kb" / directory
                try:
                    logs = sorted(
                        (path for path in log_root.glob("*")
                         if path.is_file() and re.search(r"workflow|daemon", path.name, re.I)),
                        key=lambda path: path.stat().st_mtime,
                        reverse=True,
                    )
                except OSError:
                    logs = []
                for source in logs[:3]:
                    add(source, evidence_prefix / ".kb" / directory / source.name, tail=True)

        packages = []
        scope = root / "node_modules" / "@kb-labs"
        try:
            for package in sorted(scope.iterdir()):
                manifest = package / "package.json"
                if manifest.is_file():
                    try:
                        data = json.loads(manifest.read_text())
                    except (OSError, json.JSONDecodeError):
                        continue
                    packages.append(f"{package.name}@{data.get('version', 'unknown')}")
        except OSError:
            pass
        if packages:
            listing = "\n".join(packages) + "\n"
            target = destination / "kb-labs-packages.txt"
            if used + len(listing.encode()) <= CONTENT_BUDGET:
                target.write_text(listing)
                used += target.stat().st_size
                entries.append(f"- `kb-labs-packages.txt` ({target.stat().st_size} bytes)")

        command_env = dict(env or os.environ)
        command_cwd = Path(cwd) if cwd else root
        kb_executable = shutil.which("kb", path=command_env.get("PATH"))
        if not kb_executable and (root.parent / ".local" / "bin" / "kb").is_file():
            kb_executable = str(root.parent / ".local" / "bin" / "kb")
        kb_executable = kb_executable or "kb"
        for scope in ("platform", "project"):
            target = destination / f"kb-marketplace-plugins-{scope}.txt"
            args = [kb_executable, "marketplace", "plugins", "list", "--scope", scope, "--json"]
            used += _command(target, "$ " + " ".join(args[1:]), args,
                             env=command_env, cwd=command_cwd, admin_password=admin_password,
                             budget=CONTENT_BUDGET - used)
            entries.append(f"- `{target.name}` ({target.stat().st_size} bytes)")
        unknown = UNKNOWN_COMMAND.search(first_failure)
        if unknown and used < CONTENT_BUDGET:
            command = unknown.group(1).strip().strip("'\"` ")
            target = destination / "kb-command-diagnostic.txt"
            args = [kb_executable, "diag", "--command", command, "--json"]
            used += _command(target, "$ kb diag --command " + command + " --json", args,
                             env=command_env, cwd=command_cwd, admin_password=admin_password,
                             budget=CONTENT_BUDGET - used)
            entries.append(f"- `kb-command-diagnostic.txt` ({target.stat().st_size} bytes)")

        launcher = shutil.which("kb-create", path=command_env.get("PATH"))
        if not launcher and (root.parent / ".local" / "bin" / "kb-create").is_file():
            launcher = str(root.parent / ".local" / "bin" / "kb-create")
        launcher = launcher or "kb-create"
        for name in ("status", "doctor"):
            if used >= CONTENT_BUDGET:
                break
            target = destination / f"kb-create-{name}.txt"
            args = [launcher, name, "--platform-root", str(root), "--json"]
            used += _command(target, "$ kb-create " + " ".join(args[1:]), args,
                             env=command_env, cwd=command_cwd, admin_password=admin_password,
                             budget=CONTENT_BUDGET - used)
            entries.append(f"- `{target.name}` ({target.stat().st_size} bytes)")

        if used < CONTENT_BUDGET:
            target = destination / "kb-adapter-list.txt"
            used += _command(target, "$ kb adapter list --json", [kb_executable, "adapter", "list", "--json"],
                             env=command_env, cwd=command_cwd, admin_password=admin_password,
                             budget=CONTENT_BUDGET - used)
            entries.append(f"- `kb-adapter-list.txt` ({target.stat().st_size} bytes)")

        if not root.exists():
            entries.append("- Platform root was already removed; only remaining runner evidence was captured.")
        if used > CONTENT_BUDGET:
            raise AssertionError("platform state dump exceeded its size cap")
        failure_excerpt = _redact_text(first_failure, admin_password)[:20_000]
        revision = command_env.get("KB_JOURNEY_GIT_REVISION", "unknown")
        index = "# Platform state dump\n\nJourney git revision: " + _redact_text(revision, admin_password)
        index += "\n\nFirst failure: " + failure_excerpt
        index += "\n\nFiles:\n" + ("\n".join(entries) if entries else "- No platform files or commands were available.") + "\n"
        (destination / "index.md").write_text(index)
    except Exception as error:  # diagnostics must never mask the journey failure
        try:
            destination.mkdir(parents=True, exist_ok=True)
            (destination / "index.md").write_text(
                "# Platform state dump\n\nDump failed: "
                + _redact_text(f"{error.__class__.__name__}: {error}", admin_password) + "\n"
            )
        except OSError:
            pass
    return destination
