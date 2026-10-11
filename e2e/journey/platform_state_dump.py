"""Bounded, secret-redacted snapshot of an installed journey platform."""
from __future__ import annotations

import hashlib
import json
import os
import re
import shutil
import sqlite3
import subprocess
import time
from pathlib import Path
from typing import Mapping
from typing import Any

MAX_DUMP_BYTES = 5 * 1024 * 1024
CONTENT_BUDGET = MAX_DUMP_BYTES - 64 * 1024
MAX_FILE_BYTES = 1024 * 1024
TAIL_LINES = 400
SECRET_KEY = re.compile(r"(?i)(password|token|secret|apiKey|key)")
UNKNOWN_COMMAND = re.compile(r"Unknown command:\s*([^\n\r]+)")
SERVICE_LOG_BUDGET = 2 * 1024 * 1024


def failure_dump_environment(active_main: Mapping[str, str] | None,
                             last_command: Mapping[str, str] | None,
                             fallback: Mapping[str, str]) -> Mapping[str, str]:
    """Keep failure dumps pointed at the main install after fixture commands."""
    if fallback.get("KB_JOURNEY_HOME_LABEL") == "main" and fallback.get("HOME"):
        return fallback
    return active_main or last_command or fallback


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


def _write_evidence_text(target: Path, value: str, *, env: Mapping[str, str] | None = None,
                         secrets: tuple[str, ...] = (), append: bool = False) -> str:
    from invocations import write_report_text
    return write_report_text(target, value, env=env, secrets=secrets, append=append)


def _write_evidence_json(target: Path, value: Any, *, env: Mapping[str, str] | None = None,
                         secrets: tuple[str, ...] = ()) -> Any:
    from invocations import write_report_json
    return write_report_json(target, value, env=env, secrets=secrets)


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
        target.parent.mkdir(parents=True, exist_ok=True)
        if source.suffix.lower() == ".json":
            try:
                _write_evidence_json(target, json.loads(content), secrets=(admin_password,))
                return target.stat().st_size
            except json.JSONDecodeError:
                pass
        _write_evidence_text(target, content, secrets=(admin_password,))
        return target.stat().st_size
    except OSError:
        return 0


def _write_secret_fingerprints(source: Path, target: Path) -> int:
    """Record secret names and one-way fingerprints without copying secret values."""
    try:
        entries = []
        for line in source.read_text().splitlines():
            name, separator, value = line.partition("=")
            if separator and name and value:
                entries.append({"name": name, "sha256": hashlib.sha256(value.encode()).hexdigest()})
        if not entries:
            return 0
        target.parent.mkdir(parents=True, exist_ok=True)
        _write_evidence_json(target, {"entries": entries})
        return target.stat().st_size
    except OSError:
        return 0


def _write_auth_state_probe(platform_root: Path, target: Path) -> int:
    """Write aggregate session health and database identity without credential material."""
    config_path = platform_root / ".kb/kb.config.jsonc"
    try:
        config = json.loads(config_path.read_text())
        filename = config["platform"]["adapterOptions"]["documentDatabase"]["filename"]
        database = Path(filename)
        if not database.is_absolute():
            database = platform_root / database
        stat = database.stat()
        connection = sqlite3.connect(f"{database.as_uri()}?mode=ro", uri=True, timeout=1)
        try:
            families = [json.loads(row[0]) for row in connection.execute(
                "SELECT data FROM doc_session_families")]
            refreshes = [json.loads(row[0]) for row in connection.execute(
                "SELECT data FROM doc_refresh_tokens")]
        finally:
            connection.close()
        refresh_by_family: dict[str, list[dict[str, Any]]] = {}
        for refresh in refreshes:
            refresh_by_family.setdefault(str(refresh.get("familyId", "")), []).append(refresh)
        users: dict[str, dict[str, Any]] = {}
        now_ms = time.time_ns() // 1_000_000
        for family in families:
            user_id = str(family.get("userId", "unknown"))
            family_refreshes = refresh_by_family.get(str(family.get("familyId", "")), [])
            active = any(not item.get("consumedAt") and item.get("expiresAt", 0) > now_ms
                         for item in family_refreshes)
            user = users.setdefault(user_id, {"sessionFamilyCount": 0, "active": 0, "inactive": 0})
            user["sessionFamilyCount"] += 1
            user["active" if active else "inactive"] += 1
        payload = {
            "sessionStorePath": str(database),
            "databaseFile": {"device": stat.st_dev, "inode": stat.st_ino,
                             "sizeBytes": stat.st_size, "modifiedTimeNs": stat.st_mtime_ns},
            "users": users,
        }
        target.parent.mkdir(parents=True, exist_ok=True)
        _write_evidence_json(target, payload)
        return target.stat().st_size
    except (OSError, sqlite3.Error, KeyError, TypeError, ValueError) as error:
        target.parent.mkdir(parents=True, exist_ok=True)
        _write_evidence_json(target, {"available": False, "reason": error.__class__.__name__})
        return target.stat().st_size


def archive_service_logs(kb_home: str | Path, reports_dir: str | Path, *, platform_root: str | Path,
                         mode: str, run_id: str) -> Path:
    """Copy bounded structured service logs from installed runtime state to the run report."""
    home = Path(kb_home)
    destination = Path(reports_dir) / f"service-logs-{mode}-{run_id}"
    destination.mkdir(parents=True, exist_ok=True)
    used = 0
    platform_database = Path(platform_root) / ".kb" / "database" / "kb.sqlite"
    databases = {*home.glob("state/*/database/kb.sqlite"), platform_database}
    for database in sorted(databases):
        try:
            connection = sqlite3.connect(f"{database.as_uri()}?mode=ro", uri=True, timeout=1)
            try:
                rows = connection.execute(
                    "SELECT id, data FROM doc_logs ORDER BY updated_at DESC LIMIT 400"
                ).fetchall()
            finally:
                connection.close()
        except sqlite3.Error:
            continue
        grouped: dict[str, list[dict[str, Any]]] = {}
        for row_id, serialized in rows:
            try:
                record = json.loads(serialized)
            except (TypeError, json.JSONDecodeError):
                continue
            if not isinstance(record, dict):
                continue
            fields = record.get("fields")
            fields = fields if isinstance(fields, dict) else {}
            service = str(fields.get("serviceId") or fields.get("service_id") or record.get("source", ""))
            app_id = str(fields.get("appId") or fields.get("app_id") or "")
            component = str(fields.get("component") or "")
            application_id = str(fields.get("applicationId") or fields.get("application_id") or "")
            normalized = f"{service} {app_id} {component} {application_id} {record.get('source', '')}".lower()
            category = next((candidate for needle, candidate in (
                ("cli", "cli"),
                ("workflow", "workflow-daemon"), ("project-runtime", "project-runtime"),
                ("gateway", "gateway"), ("host", "host"),
            ) if needle in normalized), None)
            if category is None:
                continue
            record.setdefault("logId", row_id)
            grouped.setdefault(category, []).append(record)
        for category, records in grouped.items():
            target = destination / database.parents[1].name / f"{category}.jsonl"
            encoded_records = []
            category_bytes = 0
            for record in records:
                line = (json.dumps(record, separators=(",", ":")) + "\n").encode()
                if used + category_bytes + len(line) > SERVICE_LOG_BUDGET:
                    break
                encoded_records.append(line)
                category_bytes += len(line)
            encoded = b"".join(encoded_records)
            if encoded:
                rendered = _write_evidence_text(target, encoded.decode(errors="replace"))
                used += len(rendered.encode())
            if used >= SERVICE_LOG_BUDGET:
                return destination
    return destination


def _command(output: Path, label: str, args: list[str], *, env: Mapping[str, str], cwd: Path,
             admin_password: str, timeout: int = 30, budget: int = MAX_DUMP_BYTES) -> int:
    try:
        from invocations import run_command
        result = run_command(args, cwd=cwd, env=env, timeout=timeout)
        body = f"exit_code: {result.returncode}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
    except (OSError, subprocess.TimeoutExpired) as error:
        body = f"error: {error.__class__.__name__}: {error}"
    encoded = f"{label}\n{body}\n".encode()
    if len(encoded) > budget:
        encoded = encoded[:budget]
    output.parent.mkdir(parents=True, exist_ok=True)
    rendered = _write_evidence_text(output, encoded.decode(errors="replace"), secrets=(admin_password,))
    return len(rendered.encode())


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
        fingerprint_bytes = _write_secret_fingerprints(
            root / ".kb/v2/secrets.env", destination / "installation-secret-fingerprints.json"
        )
        if fingerprint_bytes and used + fingerprint_bytes <= CONTENT_BUDGET:
            used += fingerprint_bytes
            entries.append(f"- `installation-secret-fingerprints.json` ({fingerprint_bytes} bytes)")
        elif fingerprint_bytes:
            (destination / "installation-secret-fingerprints.json").unlink(missing_ok=True)
        auth_probe_bytes = _write_auth_state_probe(root, destination / "auth-state-probe.json")
        if auth_probe_bytes and used + auth_probe_bytes <= CONTENT_BUDGET:
            used += auth_probe_bytes
            entries.append(f"- `auth-state-probe.json` ({auth_probe_bytes} bytes)")
        elif auth_probe_bytes:
            (destination / "auth-state-probe.json").unlink(missing_ok=True)
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
                    _write_evidence_json(target, artifact_manifests, env=env)
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
                _write_evidence_text(target, listing, env=env)
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
        failure_excerpt = first_failure[:20_000]
        revision = command_env.get("KB_JOURNEY_GIT_REVISION", "unknown")
        index = "# Platform state dump\n\nJourney git revision: " + revision
        index += "\n\nFirst failure: " + failure_excerpt
        index += "\n\nFiles:\n" + ("\n".join(entries) if entries else "- No platform files or commands were available.") + "\n"
        _write_evidence_text(destination / "index.md", index, env=command_env,
                             secrets=(admin_password,))
    except Exception as error:  # diagnostics must never mask the journey failure
        try:
            destination.mkdir(parents=True, exist_ok=True)
            _write_evidence_text(
                destination / "index.md",
                "# Platform state dump\n\nDump failed: "
                + f"{error.__class__.__name__}: {error}\n",
                env=env, secrets=(admin_password,),
            )
        except OSError:
            pass
    return destination
