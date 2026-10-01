"""Installed-platform journeys for adapter defaults and project overrides."""
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


def _run(runner: CommandRunner, command: list[str], cwd: Path, env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    result = runner(command, cwd=str(cwd), env=env, capture_output=True, text=True, timeout=300)
    if result.returncode != 0:
        detail = " ".join((result.stderr or result.stdout).split())[-400:]
        raise RuntimeError(f"`{' '.join(command)}` exited {result.returncode}: {detail}")
    return result


def _json(result: subprocess.CompletedProcess[str], command: list[str]) -> dict:
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"`{' '.join(command)}` did not return JSON") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"`{' '.join(command)}` returned a non-object JSON value")
    return value


def run_adapter_default_steps(journey: Journey, project_root: str | Path,
                              env: dict[str, str] | None = None,
                              runner: CommandRunner = subprocess.run) -> None:
    """Run S-054 and S-048 using installed `kb` commands in both auth modes."""
    project_a = Path(project_root).resolve()
    base_env = dict(os.environ if env is None else env)
    base_env["KB_JOURNEY_PROJECT_ROOT"] = str(project_a)
    project_b = Path(tempfile.mkdtemp(prefix="kb-adapter-project-b-", dir=base_env["HOME"]))
    project_b_registered = False

    def cli(args: list[str], cwd: Path = project_a) -> subprocess.CompletedProcess[str]:
        return _run(runner, ["kb", *args], cwd, base_env)

    def list_adapters(cwd: Path = project_a) -> dict:
        command = ["adapter", "list", "--json"]
        return _json(cli(command, cwd), command)

    def rows_for(cwd: Path) -> dict[str, dict]:
        payload = list_adapters(cwd)
        rows = payload.get("adapters")
        if not isinstance(rows, list):
            raise RuntimeError("`kb adapter list --json` returned no adapters array")
        return {row["slot"]: row for row in rows if isinstance(row, dict) and isinstance(row.get("slot"), str)}

    def run_workflow(cwd: Path) -> tuple[str, Path, dict]:
        run_command = ["workflow", "run", "--workflow-id=e2e-hello", "--json"]
        payload = _json(cli(run_command, cwd), run_command)
        run_id = payload.get("data", {}).get("runId") if isinstance(payload.get("data"), dict) else None
        if not isinstance(run_id, str) or not run_id:
            raise RuntimeError("`kb workflow run --workflow-id=e2e-hello --json` returned no runId")
        watch_command = ["workflow", "runs", "watch", run_id]
        watched = _run(runner, ["kb", *watch_command], cwd, base_env)
        if watched.returncode != 0 or "Run SUCCESS." not in watched.stdout:
            detail = " ".join((watched.stderr or watched.stdout).split())[-300:]
            raise RuntimeError(f"workflow run {run_id} did not complete successfully: {detail}")
        row = rows_for(cwd).get("storage")
        if not row or row.get("mode") != "real" or row.get("implementation") != "@kb-labs/adapters-fs":
            raise RuntimeError("project does not report the built-in filesystem adapter for storage")
        base_dir = row.get("baseDir")
        if not isinstance(base_dir, str):
            raise RuntimeError("storage adapter list did not report its effective baseDir")
        record = Path(base_dir) / "workflows" / f"{run_id}.json"
        if not record.is_absolute():
            record = cwd / record
        if not record.is_file():
            raise RuntimeError(f"workflow run record is missing from the configured storage adapter: {record}")
        try:
            record_value = json.loads(record.read_text())
        except json.JSONDecodeError as error:
            raise RuntimeError(f"workflow run record is invalid JSON: {record}") from error
        if record_value.get("runId") != run_id or str(record_value.get("status", "")).lower() != "success":
            raise RuntimeError(f"workflow run record does not match successful run {run_id}: {record_value!r}")
        return run_id, record, record_value

    def assert_default_list() -> None:
        rows = rows_for(project_a)
        if "storage" not in rows or rows["storage"].get("baseDir") != str(project_a / ".kb" / "data"):
            raise RuntimeError("storage default is not filesystem storage at the project's .kb/data directory")
        if rows["storage"].get("networkRequired") is not False:
            raise RuntimeError("filesystem storage reports a network dependency")
        expected_slots = {
            "storage", "cache", "logger", "analytics", "vectorStore", "llm", "embeddings",
            "eventBus", "config", "invoke", "documentDatabase", "kvStore", "logs", "artifacts",
            "snapshotManager", "notifier",
        }
        if set(rows) != expected_slots:
            raise RuntimeError("adapter list omitted configured or provider-backed capability slots")
        expected_defaults = {
            "logger": "@kb-labs/adapters-pino",
            "analytics": "@kb-labs/adapters-analytics-sqlite",
            "documentDatabase": "@kb-labs/adapters-sqlite",
            "kvStore": "@kb-labs/adapters-sqlite",
            "eventBus": "InMemoryEventBus",
            "invoke": "InMemoryInvoke",
            "artifacts": "InMemoryArtifacts",
            "config": "InMemoryConfig",
        }
        for slot, implementation in expected_defaults.items():
            if rows[slot].get("implementation") != implementation:
                raise RuntimeError(f"{slot} does not use its built-in default {implementation}")
        if rows["cache"].get("implementation") != "@kb-labs/adapters-state-broker" or rows["cache"].get("networkRequired") is not False:
            raise RuntimeError("cache does not use the platform-local StateBroker default")
        if rows["logs"].get("implementation") != "HybridLogReader" or rows["logs"].get("mode") != "real":
            raise RuntimeError("logs are not backed by the installed local buffer and persistence adapters")
        if rows["notifier"].get("implementation") != "InMemoryNotifier":
            raise RuntimeError("notifier does not use the local in-process default")
        if rows["llm"].get("mode") != "unconfigured" or rows["llm"].get("requiresConfiguration") is not True:
            raise RuntimeError("provider-backed LLM slot is not identified as requiring configuration")
        if rows["vectorStore"].get("mode") != "unconfigured" or rows["vectorStore"].get("implementation") is not None:
            raise RuntimeError("vector store is not left unconfigured by the default pack")
        for slot in ("llm", "embeddings", "snapshotManager"):
            if rows[slot].get("mode") != "unconfigured":
                raise RuntimeError(f"{slot} is not reported unconfigured without an adequate local implementation")

    journey.step("list fresh-install built-in adapter defaults", "adapter", assert_default_list, cases=["S-054.1"])

    def assert_doctor() -> None:
        command = ["kb-dev", "doctor", "--json"]
        payload = _json(_run(runner, command, project_a, base_env), command)
        if payload.get("ok") is not True:
            raise RuntimeError("`kb-dev doctor --json` reports a platform health failure")
        statuses = payload.get("adapters")
        if not isinstance(statuses, list):
            raise RuntimeError("`kb-dev doctor --json` returned no adapter status report")
        storage = next((row for row in statuses if isinstance(row, dict) and row.get("slot") == "storage"), None)
        if not storage or storage.get("implementation") != "@kb-labs/adapters-fs" or storage.get("mode") != "real":
            raise RuntimeError("doctor does not report the built-in filesystem storage default as loaded")
        for slot in ("cache", "logger", "analytics", "documentDatabase", "kvStore"):
            status = next((row for row in statuses if isinstance(row, dict) and row.get("slot") == slot), None)
            if not status or status.get("mode") != "real":
                raise RuntimeError(f"doctor does not report the local {slot} default as loaded")
        logs = next((row for row in statuses if isinstance(row, dict) and row.get("slot") == "logs"), None)
        if not logs or logs.get("mode") != "real" or logs.get("implementation") != "HybridLogReader":
            raise RuntimeError("doctor does not report the local HybridLogReader as loaded")

    journey.step("doctor verifies filesystem default is loaded", "adapter", assert_doctor, cases=["S-054.2"])

    journey.step("run project workflow through default storage", "workflow",
                 lambda: _save_run(run_workflow, project_a), cases=["S-054.3"])
    journey.step("confirm defaults have no network requirement", "adapter",
                 lambda: _assert_no_network_defaults(rows_for(project_a), assert_doctor), cases=["S-054.4"])

    journey.step("missing LLM returns a typed bind hint", "adapter",
                 lambda: _assert_missing_llm(runner, base_env, project_a, cli), cases=["S-054.5"])
    journey.step("missing vector store returns a typed bind hint", "adapter",
                 lambda: _assert_missing_vector_store(runner, base_env, project_a), cases=["S-054.6"])

    try:
        add = ["project", "add", str(project_b), "--init", "--json"]
        if _json(cli(add), add).get("ok") is not True:
            raise RuntimeError("could not register project B for adapter isolation check")
        project_b_registered = True

        def assert_baseline_records() -> None:
            for project in (project_a, project_b):
                config_file = project / ".kb" / "kb.config.json"
                config = json.loads(config_file.read_text()) if config_file.exists() else {}
                if "storage" in config.get("platform", {}).get("adapters", {}):
                    raise RuntimeError(f"project {project} unexpectedly has a project-level storage override")
            before_a = run_workflow(project_a)
            before_b = run_workflow(project_b)
            _assert_records(before_a, before_b, project_a, project_b)
        journey.step("both projects write workflow records through defaults", "adapter",
                     assert_baseline_records, cases=["S-048.1"])
        if journey.steps[-1].status != "PASS":
            return

        def bind_override() -> None:
            options = ["config", "set", "platform.adapterOptions.storage", '{"baseDir":".kb/override-data"}', "--json"]
            if _json(cli(options), options).get("ok") is not True:
                raise RuntimeError("could not set project A storage override options")
            bind = ["adapter", "bind", "storage", "@kb-labs/adapters-fs", "--json"]
            if _json(cli(bind), bind).get("ok") is not True:
                raise RuntimeError("could not bind the built-in filesystem storage override")
            row = rows_for(project_a).get("storage")
            if not row or row.get("baseDir") != str(project_a / ".kb" / "override-data"):
                raise RuntimeError("storage override is not effective in project A")
            row_b = rows_for(project_b).get("storage")
            if not row_b or row_b.get("baseDir") != str(project_b / ".kb" / "data"):
                raise RuntimeError("binding project A changed project B's default")
        journey.step("bind project A storage override", "adapter", bind_override, cases=["S-048.2"])
        if journey.steps[-1].status != "PASS":
            return

        override_run: tuple[str, Path, dict] | None = None
        def write_override_record() -> None:
            nonlocal override_run
            override_run = run_workflow(project_a)
            _assert_record_under(override_run, project_a / ".kb" / "override-data")
            default_record = project_a / ".kb" / "data" / "workflows" / f"{override_run[0]}.json"
            if default_record.exists():
                raise RuntimeError(f"override run {override_run[0]} also wrote to the default storage directory")
        journey.step("workflow record lands in project A override directory", "workflow",
                     write_override_record, cases=["S-048.3"])
        if journey.steps[-1].status != "PASS":
            return
        def write_project_b_record() -> None:
            project_b_run = run_workflow(project_b)
            _assert_record_under(project_b_run, project_b / ".kb" / "data")
        journey.step("project B continues writing to its default", "workflow",
                     write_project_b_record, cases=["S-048.4"])
        if journey.steps[-1].status != "PASS":
            return

        def unbind_and_restore() -> None:
            command = ["adapter", "unbind", "storage", "--json"]
            if _json(cli(command), command).get("ok") is not True:
                raise RuntimeError("could not unbind project A storage override")
            restored = run_workflow(project_a)
            _assert_record_under(restored, project_a / ".kb" / "data")
            override_record = project_a / ".kb" / "override-data" / "workflows" / f"{restored[0]}.json"
            if override_record.exists():
                raise RuntimeError("new workflow run still uses project A's overridden storage directory")
        journey.step("unbind project A and return new writes to default", "workflow",
                     unbind_and_restore, cases=["S-048.5"])
        if journey.steps[-1].status != "PASS":
            return

        def verify_restore_and_isolation() -> None:
            rows_a, rows_b = rows_for(project_a), rows_for(project_b)
            if rows_a.get("storage", {}).get("baseDir") != str(project_a / ".kb" / "data"):
                raise RuntimeError("project A no longer reports the built-in storage default after unbind")
            if rows_b.get("storage", {}).get("baseDir") != str(project_b / ".kb" / "data"):
                raise RuntimeError("project B storage default changed during project A override")
            config = json.loads((project_a / ".kb" / "kb.config.json").read_text())
            platform = config.get("platform", {})
            if "storage" in platform.get("adapters", {}) or "storage" in platform.get("adapterOptions", {}):
                raise RuntimeError("project A storage binding or its scoped options remain after unbind")
        journey.step("inspect both projects after storage unbind", "adapter", verify_restore_and_isolation, cases=["S-048.6"])
    except Exception as error:
        journey.step("project storage override flow", "adapter", lambda: str(error))
    finally:
        if project_b_registered:
            try:
                command = ["project", "remove", str(project_b), "--yes", "--json"]
                cli(command)
            except Exception as error:
                journey.step("unregister adapter project B", "adapter", lambda: str(error))
        shutil.rmtree(project_b, ignore_errors=True)


def _save_run(run_workflow, project):
    result = run_workflow(project)
    if result[2].get("status", "").lower() != "success":
        raise RuntimeError("default workflow record is not successful")
    return None


def _assert_no_network_defaults(rows: dict[str, dict], assert_doctor) -> None:
    if any(row.get("networkRequired") is not False for row in rows.values()):
        raise RuntimeError("a built-in default reports a network dependency")
    assert_doctor()


def _assert_missing_llm(runner: CommandRunner, env: dict[str, str], project: Path, cli) -> None:
    config_path = project / ".kb" / "kb.config.json"
    original_config = config_path.read_bytes() if config_path.exists() else None
    tracked = runner(
        ["git", "ls-files", "-z"], cwd=str(project), env=env,
        capture_output=True, text=False, timeout=30,
    )
    if tracked.returncode != 0:
        raise RuntimeError("could not locate a tracked file for the LLM-dependent commit flow")
    candidates = [project / item.decode() for item in tracked.stdout.split(b"\0") if item]
    target = next((item for item in candidates if item.is_file() and item.suffix in {".md", ".txt"}), None)
    if target is None:
        raise RuntimeError("the project has no tracked text file to exercise commit generation")
    original_file = target.read_bytes()
    try:
        target.write_bytes(original_file + b"\n<!-- adapter-default-case -->\n")
        config_command = ["config", "set", "commit.llm.enabled", "true", "--json"]
        _json(cli(config_command), config_command)
        _assert_adapter_error(runner, env, project, ["commit", "generate", "--json"], "llm")
    finally:
        target.write_bytes(original_file)
        if original_config is None:
            config_path.unlink(missing_ok=True)
        else:
            config_path.write_bytes(original_config)


def _assert_missing_vector_store(runner: CommandRunner, env: dict[str, str], project: Path) -> None:
    _assert_adapter_error(
        runner,
        env,
        project,
        ["mind", "search", "--text", "adapter defaults", "--json"],
        "vectorStore",
    )


def _assert_adapter_error(
    runner: CommandRunner,
    env: dict[str, str],
    project: Path,
    args: list[str],
    slot: str,
) -> None:
    command = ["kb", *args]
    result = runner(command, cwd=str(project), env=env, capture_output=True, text=True, timeout=300)
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"`{' '.join(command)}` did not return an error envelope") from error
    envelope = payload.get("error") if isinstance(payload, dict) else None
    action = envelope.get("actions", [{}])[0] if isinstance(envelope, dict) else {}
    if result.returncode == 0 or not isinstance(envelope, dict):
        raise RuntimeError(f"`{' '.join(command)}` did not fail with the expected typed error")
    if envelope.get("code") != "KB_ADAPTER_NOT_CONFIGURED" or envelope.get("details", {}).get("service") != slot:
        raise RuntimeError(f"`{' '.join(command)}` identified the wrong missing capability: {envelope!r}")
    if not isinstance(action, dict) or not str(action.get("command", "")).startswith(f"kb adapter bind {slot} "):
        raise RuntimeError(f"`{' '.join(command)}` did not return an adapter bind action for {slot}")
    if "stack" in result.stdout.lower() or "trace" in result.stdout.lower():
        raise RuntimeError(f"`{' '.join(command)}` exposed a stack trace instead of a user error envelope")


def _assert_records(run_a, run_b, project_a: Path, project_b: Path) -> None:
    _assert_record_under(run_a, project_a / ".kb" / "data")
    _assert_record_under(run_b, project_b / ".kb" / "data")


def _assert_record_under(run, expected_root: Path) -> None:
    if not run[1].is_relative_to(expected_root):
        raise RuntimeError(f"workflow record is not under the expected storage root {expected_root}: {run[1]}")
