"""Installed workflow, history, failure, and cron journeys."""
from __future__ import annotations

import json
import subprocess
import time
from pathlib import Path
from typing import Callable

from service_steps import Journey


def run_cli(args: list[str], *, env: dict[str, str], cwd: Path, timeout: int = 120):
    return subprocess.run(
        ["bash", "-c", '. "$HOME/.profile"; kb "$@"', "kb", *args],
        env=env, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        timeout=timeout, check=False,
    )


def _json_cli(args: list[str], *, env: dict[str, str], cwd: Path) -> object:
    result = run_cli(args, env=env, cwd=cwd)
    if result.returncode:
        raise AssertionError(f"`kb {' '.join(args)}` exited {result.returncode}: {' '.join(result.stdout.split())[-500:]}")
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(f"`kb {' '.join(args)}` did not return JSON: {' '.join(result.stdout.split())[-500:]}") from error
    if not isinstance(payload, dict) or payload.get("ok") is not True:
        raise AssertionError(f"`kb {' '.join(args)}` returned an unsuccessful result: {payload!r}")
    return payload.get("data")


def _run_id(result: object) -> str:
    if not isinstance(result, dict) or not isinstance(result.get("runId"), str):
        raise AssertionError(f"workflow run response did not contain a run ID: {result!r}")
    return result["runId"]


def _terminal_run(run_id: str, *, env: dict[str, str], cwd: Path,
                  timeout_seconds: int = 180, clock: Callable[[], float] = time.monotonic,
                  sleep: Callable[[float], None] = time.sleep) -> dict:
    deadline = clock() + timeout_seconds
    while clock() < deadline:
        detail = _json_cli(["workflow", "runs", "view", run_id, "--json=all"], env=env, cwd=cwd)
        if isinstance(detail, dict) and detail.get("status") in {"success", "failed", "cancelled"}:
            return detail
        sleep(2)
    raise AssertionError(f"workflow run {run_id} did not reach a terminal status within {timeout_seconds}s")


def _logs(run_id: str, *, env: dict[str, str], cwd: Path) -> str:
    result = _json_cli(["workflow", "runs", "logs", run_id, "--json"], env=env, cwd=cwd)
    logs = result.get("logs") if isinstance(result, dict) else None
    if not isinstance(logs, list):
        raise AssertionError(f"workflow logs response did not contain logs: {result!r}")
    return "\n".join(str(item.get("message", "")) for item in logs if isinstance(item, dict))


def _write_workflows(project: Path) -> None:
    workflows = project / ".kb" / "workflows"
    workflows.mkdir(parents=True, exist_ok=True)
    (workflows / "journey-hello.yml").write_text(
        "name: journey-hello\nversion: 1.0.0\n"
        "inputs:\n  message:\n    type: string\n    required: true\n"
        "on:\n  manual: true\njobs:\n  smoke:\n    runsOn: local\n    steps:\n"
        "      - name: Echo supplied message\n        id: echo-message\n        uses: builtin:shell\n"
        "        with:\n          command: 'echo ${{ inputs.message }}'\n",
        encoding="utf-8",
    )
    (workflows / "journey-fails.yml").write_text(
        "name: journey-fails\nversion: 1.0.0\non:\n  manual: true\n"
        "jobs:\n  smoke:\n    runsOn: local\n    steps:\n"
        "      - name: Deliberate failure\n        id: deliberate-failure\n        uses: builtin:shell\n"
        "        with:\n          command: 'echo journey-failure-diagnostic && exit 23'\n",
        encoding="utf-8",
    )


def _schedule_yaml(enabled: bool) -> str:
    return (
        "name: journey-cron\nschedule: '* * * * *'\nautoStart: true\n"
        f"enabled: {'true' if enabled else 'false'}\n"
        "jobs:\n  smoke:\n    runsOn: local\n    steps:\n"
        "      - name: Scheduled log\n        id: scheduled-log\n        uses: builtin:shell\n"
        "        with:\n          command: 'echo journey-cron-fired'\n"
    )


def prepare_workflow_fixtures(project: Path) -> None:
    """Create watched workflow and schedule files before the project runtime starts."""
    _write_workflows(project)
    jobs = project / ".kb" / "jobs"
    jobs.mkdir(parents=True, exist_ok=True)
    (jobs / "journey-cron.yml").write_text(_schedule_yaml(False), encoding="utf-8")


def _wait_for_workflow_fixtures(*, env: dict[str, str], cwd: Path,
                                clock: Callable[[], float], sleep: Callable[[float], None]) -> None:
    deadline = clock() + 30
    while clock() < deadline:
        workflows = _json_cli(["workflow", "defs", "list", "--json"], env=env, cwd=cwd)
        if isinstance(workflows, list):
            names = {item.get("id") for item in workflows if isinstance(item, dict)}
            if {"journey-hello", "journey-fails"}.issubset(names):
                return
        sleep(1)
    raise AssertionError("project workflow catalog did not discover the journey fixtures within 30s")


def _case_runs(*, env: dict[str, str], cwd: Path, workflow: str) -> list[dict]:
    result = _json_cli(["workflow", "runs", "list", f"--workflow={workflow}", "--limit=100", "--json"],
                       env=env, cwd=cwd)
    runs = result if isinstance(result, list) else result.get("runs", []) if isinstance(result, dict) else []
    if not isinstance(runs, list):
        raise AssertionError(f"workflow run history response was malformed: {result!r}")
    return [run for run in runs if isinstance(run, dict)]


def _wait_for_scheduled_run(*, env: dict[str, str], cwd: Path,
                            timeout_seconds: int = 125,
                            clock: Callable[[], float] = time.monotonic,
                            sleep: Callable[[float], None] = time.sleep) -> dict:
    deadline = clock() + timeout_seconds
    while clock() < deadline:
        runs = _case_runs(env=env, cwd=cwd, workflow="journey-cron")
        scheduled = [run for run in runs if (run.get("trigger") or {}).get("type") in {"schedule", "cron"}]
        if scheduled:
            run = scheduled[0]
            return _terminal_run(str(run.get("id")), env=env, cwd=cwd, timeout_seconds=180,
                                 clock=clock, sleep=sleep)
        sleep(2)
    raise AssertionError("enabled cron schedule did not create a scheduled workflow run within 125s")


def _cron_case(journey: Journey, *, env: dict[str, str], cwd: Path,
               clock: Callable[[], float] = time.monotonic,
               sleep: Callable[[float], None] = time.sleep) -> None:
    jobs = cwd / ".kb" / "jobs"
    jobs.mkdir(parents=True, exist_ok=True)
    schedule_file = jobs / "journey-cron.yml"

    def enable_schedule() -> None:
        schedule_file.write_text(_schedule_yaml(True), encoding="utf-8")
        data = _json_cli(["workflow", "list", "--type=cron", "--json"], env=env, cwd=cwd)
        crons = data.get("crons", []) if isinstance(data, dict) else []
        if not any(isinstance(item, dict) and item.get("id") == "user:journey-cron" for item in crons):
            raise AssertionError("runtime did not register the enabled journey-cron definition")

    journey.step("enable workflow cron schedule", "workflow", enable_schedule, cases=["S-044.1"])

    def list_schedule() -> None:
        deadline = clock() + 20
        while clock() < deadline:
            data = _json_cli(["workflow", "list", "--type=cron", "--json"], env=env, cwd=cwd)
            crons = data.get("crons", []) if isinstance(data, dict) else []
            match = next((item for item in crons if isinstance(item, dict) and item.get("id") == "user:journey-cron"), None)
            if match and match.get("enabled") is True and match.get("schedule") == "* * * * *":
                return
            sleep(1)
        raise AssertionError("`kb workflow list --type=cron` did not show the enabled journey-cron schedule")

    journey.step("inspect enabled cron schedule", "workflow", list_schedule, cases=["S-044.2"])

    scheduled_run: dict = {}

    def observe_schedule() -> None:
        nonlocal scheduled_run
        scheduled_run = _wait_for_scheduled_run(env=env, cwd=cwd, clock=clock, sleep=sleep)
        if scheduled_run.get("status") != "success":
            raise AssertionError(f"scheduled workflow run ended {scheduled_run.get('status')!r}")
        run_id = str(scheduled_run.get("id", ""))
        if "journey-cron-fired" not in _logs(run_id, env=env, cwd=cwd):
            raise AssertionError("scheduled workflow log did not contain journey-cron-fired")

    journey.step("observe scheduled workflow run and log", "workflow", observe_schedule, cases=["S-044.3"])

    def disable_and_confirm() -> None:
        schedule_file.write_text(_schedule_yaml(False), encoding="utf-8")
        deadline = clock() + 20
        while clock() < deadline:
            data = _json_cli(["workflow", "list", "--type=cron", "--json"], env=env, cwd=cwd)
            crons = data.get("crons", []) if isinstance(data, dict) else []
            match = next((item for item in crons if isinstance(item, dict) and item.get("id") == "user:journey-cron"), None)
            if match is not None and match.get("enabled") is False:
                break
            sleep(1)
        else:
            raise AssertionError("workflow runtime did not report the schedule as disabled")

        baseline_ids = {str(run.get("id")) for run in _case_runs(env=env, cwd=cwd, workflow="journey-cron")}
        # Observe a complete cron interval after reload to prove the disabled schedule stays quiet.
        sleep(65)
        later_ids = {str(run.get("id")) for run in _case_runs(env=env, cwd=cwd, workflow="journey-cron")}
        if later_ids != baseline_ids:
            raise AssertionError("workflow cron created another run after its schedule was disabled")

    journey.step("disable cron and verify no later run", "workflow", disable_and_confirm, cases=["S-044.4"])
    schedule_file.unlink(missing_ok=True)


def run_workflow_journey(journey: Journey, *, env: dict[str, str], project: Path,
                         clock: Callable[[], float] = time.monotonic,
                         sleep: Callable[[float], None] = time.sleep) -> None:
    """Run each workflow release case in the registered project."""
    prepare_workflow_fixtures(project)
    _wait_for_workflow_fixtures(env=env, cwd=project, clock=clock, sleep=sleep)
    successful_id = ""
    failed_id = ""

    def start_success() -> None:
        nonlocal successful_id
        data = _json_cli(["workflow", "run", "--workflow-id=journey-hello", '--input={"message":"hello"}', "--json"],
                         env=env, cwd=project)
        successful_id = _run_id(data)

    journey.step("start workflow with input", "workflow", start_success, cases=["S-007.1"])

    success_detail: dict = {}

    def inspect_success() -> None:
        nonlocal success_detail
        success_detail = _terminal_run(successful_id, env=env, cwd=project, clock=clock, sleep=sleep)
        if success_detail.get("status") != "success" or (success_detail.get("inputs") or {}).get("message") != "hello":
            raise AssertionError(f"run did not complete with the submitted input: {success_detail!r}")

    journey.step("verify terminal status and submitted input", "workflow", inspect_success, cases=["S-007.2"])
    journey.step("verify input appears in workflow log", "workflow",
                 lambda: None if "hello" in _logs(successful_id, env=env, cwd=project)
                 else "workflow logs did not contain the submitted value hello", cases=["S-007.3"])

    def start_failure() -> None:
        nonlocal failed_id
        data = _json_cli(["workflow", "run", "--workflow-id=journey-fails", "--json"], env=env, cwd=project)
        failed_id = _run_id(data)

    journey.step("start failing workflow", "workflow", start_failure, cases=["S-029.1"])

    failed_detail: dict = {}

    def inspect_failure() -> None:
        nonlocal failed_detail
        failed_detail = _terminal_run(failed_id, env=env, cwd=project, clock=clock, sleep=sleep)
        if failed_detail.get("status") != "failed":
            raise AssertionError(f"failing workflow ended with {failed_detail.get('status')!r}")
        jobs = failed_detail.get("jobs", [])
        steps = [step for job in jobs for step in job.get("steps", [])]
        failed = [step for step in steps if step.get("status") == "failed"]
        if not failed or failed[0].get("id") != "deliberate-failure":
            raise AssertionError(f"run detail did not identify the failed step and diagnostic: {failed_detail!r}")
        error = failed[0].get("error")
        error_message = error.get("message", "") if isinstance(error, dict) else str(error or "")
        if not error_message.strip():
            raise AssertionError("failed step detail did not include an error message")

    journey.step("inspect failed step and error", "workflow", inspect_failure,
                 cases=["S-029.2", "S-009.4"])

    def inspect_failure_logs() -> None:
        logs = _logs(failed_id, env=env, cwd=project)
        if "journey-failure-diagnostic" not in logs:
            raise AssertionError("failed workflow logs omitted the failing command diagnostic")
        if any(marker in logs.lower() for marker in ("goroutine ", "traceback", "stack trace")):
            raise AssertionError("workflow logs exposed a raw service stack trace")

    journey.step("inspect failing command logs", "workflow", inspect_failure_logs,
                 cases=["S-029.3"])

    def inspect_history() -> None:
        runs = _case_runs(env=env, cwd=project, workflow="journey-hello")
        if not any(run.get("id") == successful_id and run.get("status") == "success" and run.get("createdAt") for run in runs):
            raise AssertionError("workflow history omitted the successful run ID, status, or time")
        if not any(run.get("id") == failed_id and run.get("status") == "failed" for run in
                   _case_runs(env=env, cwd=project, workflow="journey-fails")):
            raise AssertionError("workflow history omitted the failed run")

    journey.step("list recent workflow history", "workflow", inspect_history, cases=["S-009.1"])
    journey.step("inspect selected successful run", "workflow",
                 lambda: None if (_terminal_run(successful_id, env=env, cwd=project, clock=clock, sleep=sleep)
                                  .get("status") == "success") else "selected workflow run was not successful",
                 cases=["S-009.2"])
    journey.step("inspect selected successful run logs", "workflow",
                 lambda: None if "hello" in _logs(successful_id, env=env, cwd=project)
                 else "selected workflow run logs were empty", cases=["S-009.3"])

    try:
        _cron_case(journey, env=env, cwd=project, clock=clock, sleep=sleep)
    finally:
        jobs_dir = project / ".kb" / "jobs"
        (jobs_dir / "journey-cron.yml").unlink(missing_ok=True)
        try:
            jobs_dir.rmdir()
        except OSError:
            pass


def merge_workflow_result(service_report: Path, workflow_result: dict) -> dict:
    """Add workflow journey claims to the service result consumed by release coverage."""
    try:
        service_result = json.loads(service_report.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"cannot merge workflow journey into {service_report}: {error}") from error
    service_result.setdefault("steps", []).extend(workflow_result.get("steps", []))
    claims = {
        claim["claimId"]: claim
        for claim in service_result.get("case_run", {}).get("claims", [])
        if isinstance(claim, dict) and isinstance(claim.get("claimId"), str)
    }
    for claim in workflow_result.get("case_run", {}).get("claims", []):
        if isinstance(claim, dict) and isinstance(claim.get("claimId"), str):
            claims[claim["claimId"]] = claim
    service_result.setdefault("case_run", {})["claims"] = [claims[key] for key in sorted(claims)]
    if workflow_result.get("status") != "PASS":
        service_result["status"] = "FAIL"
        service_result["first_failure"] = service_result.get("first_failure") or workflow_result.get("first_failure")
    service_report.write_text(json.dumps(service_result, indent=2) + "\n", encoding="utf-8")
    return service_result
