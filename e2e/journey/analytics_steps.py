"""Installed-platform analytics API claims for S-191 and S-201."""
from __future__ import annotations
from journey_values import journey_value

from project_service_path import project_service_path

import json
import os
import subprocess
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path

from invocations import (decode_ux_error_result, decode_ux_success_result, invocation_context,
                         open_http, read_ux_cli_result, read_ux_http_result, run_command, run_with_runner)
from service_steps import Journey
from gateway_steps import run_gateway_steps


def _request(method: str, path: str, *, token: str, payload: object = None,
             opener=urllib.request.urlopen) -> tuple[int, object]:
    base = journey_value("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(
        f"{base}{path}", data=data,
        headers={"Accept": "application/json", "Content-Type": "application/json",
                 **({"Authorization": f"Bearer {token}"} if token else {})},
        method=method,
    )
    status, raw, _ = open_http(opener, request, timeout=30, env=os.environ)
    try:
        envelope = read_ux_http_result(raw, status)
        return status, (decode_ux_success_result(envelope) if 200 <= status < 300
                        else decode_ux_error_result(envelope))
    except json.JSONDecodeError:
        return status, raw.decode(errors="replace")


def _event_list(body: object) -> list[dict]:
    data = body.get("data", body) if isinstance(body, dict) else {}
    events = data.get("events", data) if isinstance(data, dict) else data
    if not isinstance(events, list):
        raise RuntimeError("analytics event query returned no events array")
    return [event for event in events if isinstance(event, dict)]


def _run_fixture_command(command: list[str], *, step_id: str, runner, cwd: str) -> subprocess.CompletedProcess[str]:
    with invocation_context(step_id=step_id):
        return run_with_runner(runner, command, cwd=cwd, env=os.environ.copy(), timeout=180,
                               capture_output=True, text=True, check=False)


def _marker_event(events: list[dict], marker: str) -> dict | None:
    for event in events:
        payload = event.get("payload")
        if isinstance(payload, dict) and payload.get("marker") == marker:
            return event
    return None


def _emission_succeeded(result: subprocess.CompletedProcess[str] | None, marker: str) -> bool:
    if result is None or result.returncode != 0:
        return False
    payload = read_ux_cli_result(result.stdout, result.returncode)
    if result.returncode != 0:
        return False
    data = decode_ux_success_result(payload)
    data = data.get("result", data) if isinstance(data, dict) else data
    return (isinstance(data, dict) and data.get("type") == "case.analytics.persist"
            and data.get("marker") == marker)


def run_analytics_steps(journey: Journey, *, token: str, runner=run_command,
                        opener=urllib.request.urlopen) -> None:
    """Claim event persistence and project API isolation with explicit step IDs."""
    event_type = "case.analytics.persist"
    source = "case-fixture"
    before = datetime.now(timezone.utc) - timedelta(seconds=2)
    project_a = journey_value("KB_JOURNEY_PROJECT_ROOT", "")
    project_b = journey_value("KB_JOURNEY_PROJECT_B_ROOT", "")
    fixture_install = _run_fixture_command(
        ["kb", "marketplace", "install", "@kb-labs/e2e-analytics-emitter", "--scope", "platform", "--json"],
        step_id="S-191.1", runner=runner, cwd=project_a,
    ) if project_a else None
    marker_a = f"{journey.run_id}:project-a"
    emit_a = _run_fixture_command(["kb", "journey", "analytics", "emit", f"--marker={marker_a}", "--json"],
                                  step_id="S-191.1", runner=runner, cwd=project_a) if project_a and fixture_install and fixture_install.returncode == 0 else None
    marker_b = f"{journey.run_id}:project-b"
    emit_b = _run_fixture_command(["kb", "journey", "analytics", "emit", f"--marker={marker_b}", "--json"],
                                  step_id="S-191.1", runner=runner, cwd=project_b) if project_b and fixture_install and fixture_install.returncode == 0 else None
    journey.step("track local analytics event", "analytics", lambda: (
        None if fixture_install is not None and fixture_install.returncode == 0
        and _emission_succeeded(emit_a, marker_a) and _emission_succeeded(emit_b, marker_b)
        else "fixture analytics plugin install and both project invocations must succeed; provide registered project roots"
    ), cases=["S-191.1"])

    project_id = journey_value("KB_JOURNEY_PROJECT_ID", "")
    query_path = project_service_path(project_id, "rest", "analytics/events?type=" + event_type + "&source=" + source
                  + "&from=" + before.isoformat() + "&to="
                  + (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat())
    with invocation_context(step_id="S-191.2"):
        status, body = _request("GET", query_path, token=token, opener=opener)
    events = _event_list(body) if status == 200 else []
    event_a = _marker_event(events, marker_a)
    event_b = _marker_event(events, marker_b)
    journey.step("query tracked analytics event", "analytics", lambda: (
        None if project_id and status == 200 and event_a is not None and event_a.get("type") == event_type
        and event_a.get("id") and event_b is None
        else f"analytics query returned HTTP {status} or omitted {event_type}"
    ), cases=["S-191.2"])

    with invocation_context(step_id="S-191.3"):
        restarted = runner(["kb-create", "restart", "--platform-root",
                            os.environ.get("KB_PLATFORM_ROOT", str(Path.home() / "kb-platform")), "--json"],
                           cwd=os.environ.get("HOME"), env=os.environ.copy(), timeout=300,
                           capture_output=True, text=True, check=False)
    with invocation_context(step_id="S-191.3"):
        after_status, after_body = _request("GET", query_path, token=token, opener=opener)
    after_events = _event_list(after_body) if after_status == 200 else []
    journey.step("analytics event survives restart", "analytics", lambda: (
        None if restarted.returncode == 0 and after_status == 200
        and _marker_event(after_events, marker_a) is not None
        else f"restart exited {restarted.returncode}; post-restart analytics query returned HTTP {after_status}"
    ), cases=["S-191.3"])

    fixture_file = journey_value("KB_JOURNEY_STUDIO_FIXTURE_FILE", "")
    if fixture_file and event_a:
        fixture_path = Path(fixture_file)
        fixture_path.parent.mkdir(parents=True, exist_ok=True)
        fixture_path.write_text(json.dumps({
            "eventType": event_type,
            "source": source,
            "eventId": str(event_a["id"]),
            "projectAEvent": marker_a,
            "projectBEvent": marker_b,
        }), encoding="utf-8")

    projects = [journey_value("KB_JOURNEY_PROJECT_ID", ""),
                journey_value("KB_JOURNEY_PROJECT_B_ID", "")]
    project_events: list[list[dict]] = []
    project_runs: list[object] = []
    for project_id in projects:
        with invocation_context(step_id="S-201.3"):
            status, body = _request("GET", project_service_path(project_id, "rest", f"analytics/events?source={source}"),
                                    token=token, opener=opener)
        with invocation_context(step_id="S-201.3"):
            run_status, runs = _request("GET", project_service_path(project_id, "workflow", "runs"),
                                         token=token, opener=opener)
        project_events.append(_event_list(body) if status == 200 else [])
        project_runs.append(runs if run_status == 200 else None)
    journey.step("project analytics API isolation", "analytics", lambda: (
        None if all(projects) and len(project_events) == 2 and all(runs is not None for runs in project_runs)
        and all(any(event.get("projectId") == project_id for event in events)
                for project_id, events in zip(projects, project_events))
        and not any(event.get("projectId") == projects[1] for event in project_events[0])
        and not any(event.get("projectId") == projects[0] for event in project_events[1])
        else "project analytics responses did not contain only their requested project's events"
    ), cases=["S-201.3"])


def run_analytics_and_gateway_steps(journey: Journey, *, token: str, runner=run_command,
                                    opener=urllib.request.urlopen) -> None:
    """Single service-runner hook for this stream's API and gateway checks."""
    run_analytics_steps(journey, token=token, runner=runner, opener=opener)
    run_gateway_steps(journey, token=token, runner=runner, opener=opener)
