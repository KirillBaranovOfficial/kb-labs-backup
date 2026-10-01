"""Installed workflow, history, failure, and cron journeys."""
from __future__ import annotations

import json
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Callable

from service_steps import Journey
from steps.auth import assert_cli_session_absent, assert_platform_auth_required


def run_cli(args: list[str], *, env: dict[str, str], cwd: Path, timeout: int = 120):
    cli_path = env.get("KB_JOURNEY_CLI_PATH")
    if cli_path:
        return subprocess.run(
            [cli_path, *args], env=env, cwd=cwd, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            timeout=timeout, check=False,
        )
    return subprocess.run(
        ["bash", "-c", 'if [ -f "$HOME/.profile" ]; then . "$HOME/.profile"; fi; kb "$@"', "kb", *args],
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


def run_secured_cli_auth_step(journey: Journey, *, env: dict[str, str], project: Path) -> None:
    """Check deny-by-default plugin dispatch, then restore the admin CLI session."""
    def verify_auth() -> None:
        commands = [
            ["config", "show", "--json"],
            ["workflow", "defs", "list", "--json"],
            ["marketplace", "plugins", "list", "--json"],
            ["qa", "cases", "validate", "--json"],
        ]
        with tempfile.TemporaryDirectory(prefix="kb-anonymous-cli-home-") as anonymous_home:
            anonymous_env = env.copy()
            anonymous_env["HOME"] = anonymous_home
            platform_root = env.get("KB_JOURNEY_PLATFORM_ROOT")
            cli_path = env.get("KB_JOURNEY_CLI_PATH")
            if not platform_root or not cli_path:
                raise AssertionError("anonymous CLI auth checks require explicit platform and CLI paths")
            anonymous_env["KB_PLATFORM_ROOT"] = platform_root
            anonymous_env["KB_PROJECT_ROOT"] = str(project)
            status = run_cli(["auth", "status", "--json"], env=anonymous_env, cwd=project)
            assert_cli_session_absent(status)
            allowlisted = [
                ["auth", "login", "--help"],
                ["version"],
                ["health", "--json"],
            ]
            for command in allowlisted:
                result = run_cli(command, env=anonymous_env, cwd=project)
                if result.returncode != 0:
                    raise AssertionError(f"allowlisted {' '.join(command)} failed without a session")
            for command in commands:
                result = run_cli(command, env=anonymous_env, cwd=project)
                assert_platform_auth_required(result, command)

        email = env.get("KB_JOURNEY_ADMIN_EMAIL", "")
        password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "")
        if not email or not password:
            raise AssertionError("secured workflow journey requires KB_JOURNEY_ADMIN_EMAIL and KB_JOURNEY_ADMIN_PASSWORD")
        gateway = env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000")
        login = run_cli(["auth", "login", "--gateway-url", gateway,
                         "--email", email, "--password", password], env=env, cwd=project)
        if login.returncode != 0:
            raise AssertionError(f"admin CLI login failed: {' '.join(login.stdout.split())[-400:]}")
        for command in commands:
            resumed = run_cli(command, env=env, cwd=project)
            try:
                payload = json.loads(resumed.stdout)
            except json.JSONDecodeError as error:
                raise AssertionError(f"authenticated {' '.join(command)} did not return JSON") from error
            error = payload.get("error") if isinstance(payload, dict) else None
            if isinstance(error, dict) and error.get("code") == "KB_AUTH_REQUIRED":
                raise AssertionError(f"authenticated {' '.join(command)} still returned KB_AUTH_REQUIRED")

    journey.step("secured CLI deny-by-default authentication", "workflow", verify_auth,
                 cases=["S-007.4", "S-041.7", "S-041.8", "S-041.12"])


def _logs(run_id: str, *, env: dict[str, str], cwd: Path) -> str:
    result = _json_cli(["workflow", "runs", "logs", run_id, "--json"], env=env, cwd=cwd)
    logs = result.get("logs") if isinstance(result, dict) else None
    if not isinstance(logs, list):
        raise AssertionError(f"workflow logs response did not contain logs: {result!r}")
    return "\n".join(str(item.get("message", "")) for item in logs if isinstance(item, dict))


def _write_workflows(project: Path) -> None:
    workflows = project / ".kb" / "workflows"
    workflows.mkdir(parents=True, exist_ok=True)
    (workflows / "hello.yml").write_text(
        "name: hello\nversion: 1.0.0\non:\n  manual: true\njobs:\n  hello:\n    runsOn: local\n    steps:\n"
        "      - name: Say hello\n        id: hello\n        uses: builtin:shell\n"
        "        with:\n          command: echo hello-workflow\n",
        encoding="utf-8",
    )
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
        "schedule: '* * * * *'\nworkflow: journey-cron-target\n"
        "input:\n  message: journey-cron-fired\n"
        f"enabled: {'true' if enabled else 'false'}\n"
    )


def prepare_workflow_fixtures(project: Path) -> None:
    """Create watched workflow and schedule files before the project runtime starts."""
    _write_workflows(project)
    workflows = project / ".kb" / "workflows"
    (workflows / "journey-cron-target.yml").write_text(
        "name: journey-cron-target\nversion: 1.0.0\n"
        "inputs:\n  message:\n    type: string\n    required: true\n"
        "on:\n  manual: true\njobs:\n  smoke:\n    runsOn: local\n    steps:\n"
        "      - name: Log schedule input\n        id: log-schedule-input\n        uses: builtin:shell\n"
        "        with:\n          command: 'echo ${{ inputs.message }}'\n", encoding="utf-8")
    crons = project / ".kb" / "crons"
    crons.mkdir(parents=True, exist_ok=True)
    (crons / "journey-cron.yml").write_text(_schedule_yaml(False), encoding="utf-8")


def _wait_for_workflow_fixtures(*, env: dict[str, str], cwd: Path,
                                clock: Callable[[], float], sleep: Callable[[float], None]) -> None:
    deadline = clock() + 30
    while clock() < deadline:
        workflows = _json_cli(["workflow", "defs", "list", "--json"], env=env, cwd=cwd)
        if isinstance(workflows, list):
            names = {item.get("id") for item in workflows if isinstance(item, dict)}
            if {"hello", "journey-hello", "journey-fails", "journey-cron-target"}.issubset(names):
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
        runs = _case_runs(env=env, cwd=cwd, workflow="journey-cron-target")
        scheduled = [run for run in runs if (run.get("trigger") or {}).get("type") == "schedule"]
        if scheduled:
            run = scheduled[0]
            return _terminal_run(str(run.get("id")), env=env, cwd=cwd, timeout_seconds=180,
                                 clock=clock, sleep=sleep)
        sleep(2)
    raise AssertionError("enabled cron schedule did not create a scheduled workflow run within 125s")


def _cron_case(journey: Journey, *, env: dict[str, str], cwd: Path,
               clock: Callable[[], float] = time.monotonic,
               sleep: Callable[[float], None] = time.sleep) -> None:
    cron_dir = cwd / ".kb" / "crons"
    cron_dir.mkdir(parents=True, exist_ok=True)
    schedule_file = cron_dir / "journey-cron.yml"

    def enable_schedule() -> None:
        schedule_file.write_text(_schedule_yaml(True), encoding="utf-8")
        deadline = clock() + 30
        match = None
        while clock() < deadline:
            data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
            crons = data.get("crons", []) if isinstance(data, dict) else []
            match = next((item for item in crons if isinstance(item, dict) and item.get("id") == "journey-cron"), None)
            if (match and match.get("source") == "user"
                    and match.get("target", {}).get("workflow") == "journey-cron-target"
                    and match.get("enabled") is True
                    and match.get("input") == {"message": "journey-cron-fired"}
                    and match.get("nextFireAt")):
                return
            sleep(1)
        raise AssertionError(f"runtime did not register enabled journey-cron with input and next fire time: {match!r}")

    journey.step("enable workflow cron schedule", "workflow", enable_schedule, cases=["S-044.1"])

    scheduled_run: dict = {}

    def observe_schedule() -> None:
        nonlocal scheduled_run
        scheduled_run = _wait_for_scheduled_run(env=env, cwd=cwd, clock=clock, sleep=sleep)
        if scheduled_run.get("status") != "success":
            raise AssertionError(f"scheduled workflow run ended {scheduled_run.get('status')!r}")
        if (scheduled_run.get("metadata") or {}).get("cronId") != "journey-cron":
            raise AssertionError("scheduled run metadata did not identify cron journey-cron")
        run_id = str(scheduled_run.get("id", ""))
        if "journey-cron-fired" not in _logs(run_id, env=env, cwd=cwd):
            raise AssertionError("scheduled workflow log did not contain journey-cron-fired")

    journey.step("observe scheduled workflow run and log", "workflow", observe_schedule, cases=["S-044.2"])

    def disable_and_confirm() -> None:
        disabled = run_cli(["workflow", "crons", "disable", "journey-cron"], env=env, cwd=cwd)
        if disabled.returncode != 0:
            raise AssertionError(f"`kb workflow crons disable journey-cron` failed: {' '.join(disabled.stdout.split())[-500:]}")
        deadline = clock() + 20
        while clock() < deadline:
            data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
            crons = data.get("crons", []) if isinstance(data, dict) else []
            match = next((item for item in crons if isinstance(item, dict) and item.get("id") == "journey-cron"), None)
            if match is not None and match.get("enabled") is False:
                break
            sleep(1)
        else:
            raise AssertionError("workflow runtime did not report the schedule as disabled")

        if "enabled: false" not in schedule_file.read_text(encoding="utf-8"):
            raise AssertionError("disable command did not persist enabled: false in the cron file")
        baseline_ids = {str(run.get("id")) for run in _case_runs(env=env, cwd=cwd, workflow="journey-cron-target")}
        # Observe a complete cron interval after reload to prove the disabled schedule stays quiet.
        sleep(65)
        later_ids = {str(run.get("id")) for run in _case_runs(env=env, cwd=cwd, workflow="journey-cron-target")}
        if later_ids != baseline_ids:
            raise AssertionError("workflow cron created another run after its schedule was disabled")

    journey.step("disable cron and verify no later run", "workflow", disable_and_confirm, cases=["S-044.3", "S-044.4"])

    def enable_again() -> None:
        result = run_cli(["workflow", "crons", "enable", "journey-cron"], env=env, cwd=cwd)
        if result.returncode != 0 or "enabled: true" not in schedule_file.read_text(encoding="utf-8"):
            raise AssertionError("enable command did not restore enabled: true in the cron file")
        data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
        crons = data.get("crons", []) if isinstance(data, dict) else []
        if not any(item.get("id") == "journey-cron" and item.get("enabled") is True for item in crons if isinstance(item, dict)):
            raise AssertionError("cron list did not report journey-cron enabled")

    journey.step("enable cron again", "workflow", enable_again, cases=["S-044.5"])

    def invalid_expression() -> None:
        schedule_file.write_text(_schedule_yaml(True).replace("* * * * *", "not a cron"), encoding="utf-8")
        deadline = clock() + 30
        errors = []
        while clock() < deadline:
            data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
            errors = data.get("errors", []) if isinstance(data, dict) else []
            if any(".kb/crons/journey-cron.yml" in str(error) and "cron" in str(error).lower() for error in errors):
                return
            sleep(1)
        raise AssertionError(f"invalid cron diagnostic omitted its file or reason: {errors!r}")

    journey.step("report invalid cron expression", "workflow", invalid_expression, cases=["S-044.6"])

    def unknown_workflow() -> None:
        schedule_file.write_text(_schedule_yaml(True).replace("journey-cron-target", "missing-workflow"), encoding="utf-8")
        deadline = clock() + 30
        errors = []
        while clock() < deadline:
            data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
            errors = data.get("errors", []) if isinstance(data, dict) else []
            if any(".kb/crons/journey-cron.yml" in str(error) and "missing-workflow" in str(error) for error in errors):
                return
            sleep(1)
        raise AssertionError(f"unknown workflow diagnostic omitted its file or reason: {errors!r}")

    journey.step("report unknown workflow target", "workflow", unknown_workflow, cases=["S-044.7"])

    def refuse_plugin_enable() -> None:
        listed = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
        crons = listed.get("crons", []) if isinstance(listed, dict) else []
        plugin = next((item for item in crons if isinstance(item, dict) and item.get("source") == "plugin"), None)
        if not plugin or not isinstance(plugin.get("id"), str):
            raise AssertionError("cron list did not include a plugin cron to verify read-only behavior")
        before = {key: plugin.get(key) for key in ("enabled", "schedule", "target", "input")}
        result = run_cli(["workflow", "crons", "enable", plugin["id"]], env=env, cwd=cwd)
        if result.returncode == 0 or "plugin" not in result.stdout.lower() or "read-only" not in result.stdout.lower():
            raise AssertionError(f"plugin cron enable was not refused with its reason: {result.stdout!r}")
        after_list = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=cwd)
        after_crons = after_list.get("crons", []) if isinstance(after_list, dict) else []
        after = next((item for item in after_crons if isinstance(item, dict) and item.get("id") == plugin["id"]), None)
        if after is None or {key: after.get(key) for key in before} != before:
            raise AssertionError("refused plugin cron enable changed the listed cron")

    journey.step("refuse enabling plugin cron", "workflow", refuse_plugin_enable, cases=["S-044.8"])
    schedule_file.unlink(missing_ok=True)


def run_workflow_journey(journey: Journey, *, env: dict[str, str], project: Path,
                         restart_host: Callable[[], None],
                         clock: Callable[[], float] = time.monotonic,
                         sleep: Callable[[float], None] = time.sleep) -> None:
    """Run each workflow release case in the registered project."""

    def install_plugin_cron_fixture() -> None:
        _json_cli(["marketplace", "install", "@kb-labs/steward-entry", "--json"], env=env, cwd=project)
        restart_host()
        data = _json_cli(["workflow", "crons", "list", "--json"], env=env, cwd=project)
        crons = data.get("crons", []) if isinstance(data, dict) else []
        if not any(isinstance(item, dict) and item.get("source") == "plugin" for item in crons):
            raise AssertionError("workflow cron list omitted the cron declared by installed @kb-labs/steward-entry")

    if journey.mode == "secured":
        run_secured_cli_auth_step(journey, env=env, project=project)
    journey.step("install plugin cron fixture", "workflow", install_plugin_cron_fixture)
    prepare_workflow_fixtures(project)

    def discover_fixtures() -> None:
        _wait_for_workflow_fixtures(env=env, cwd=project, clock=clock, sleep=sleep)

    discovery = journey.step("discover installed project workflow definitions", "workflow",
                             discover_fixtures, cases=["S-006.3"])
    if discovery.status != "PASS":
        return
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

    journey.step("verify terminal status and submitted input", "workflow", inspect_success,
                 cases=["S-007.2", "S-056.1"])
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
        if (not failed or failed[0].get("name") != "Deliberate failure"
                or (failed[0].get("spec") or {}).get("id") != "deliberate-failure"):
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

    def restart_and_verify_project() -> None:
        restart_host()
        workflows = _json_cli(["workflow", "defs", "list", "--json"], env=env, cwd=project)
        if not isinstance(workflows, list) or not any(
            isinstance(workflow, dict) and workflow.get("id") == "journey-hello" for workflow in workflows
        ):
            raise AssertionError("registered project workflow was unavailable after host restart")

    journey.step("restart host and verify project availability", "platform",
                 restart_and_verify_project, cases=["S-056.2"])

    def inspect_history_after_restart() -> None:
        runs = _json_cli(["workflow", "runs", "list", "--limit=100", "--json"], env=env, cwd=project)
        all_runs = runs if isinstance(runs, list) else runs.get("runs", []) if isinstance(runs, dict) else []
        if not any(isinstance(run, dict) and run.get("id") == successful_id and run.get("status") == "success"
                   for run in all_runs):
            raise AssertionError(f"workflow history after host restart omitted successful run {successful_id}")
        detail = _json_cli(["workflow", "runs", "view", successful_id, "--json=all"], env=env, cwd=project)
        if not isinstance(detail, dict) or detail.get("status") != "success":
            raise AssertionError(f"workflow detail after host restart lost terminal status for {successful_id}")
        if "hello" not in _logs(successful_id, env=env, cwd=project):
            raise AssertionError(f"workflow logs after host restart omitted known output for {successful_id}")

    journey.step("read workflow history and logs after restart", "workflow",
                 inspect_history_after_restart, cases=["S-056.3"])

    try:
        _cron_case(journey, env=env, cwd=project, clock=clock, sleep=sleep)
    finally:
        jobs_dir = project / ".kb" / "crons"
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
