"""Installed-platform analytics API claims for S-191 and S-201."""
from __future__ import annotations

import json
import os
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path

from invocations import invocation_context, open_http, run_command
from service_steps import Journey
from gateway_steps import run_gateway_steps


def _request(method: str, path: str, *, token: str, payload: object = None,
             opener=urllib.request.urlopen) -> tuple[int, object]:
    base = os.environ.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(
        f"{base}{path}", data=data,
        headers={"Accept": "application/json", "Content-Type": "application/json",
                 **({"Authorization": f"Bearer {token}"} if token else {})},
        method=method,
    )
    status, raw, _ = open_http(opener, request, timeout=30, env=os.environ)
    try:
        return status, json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        return status, raw.decode(errors="replace")


def _event_list(body: object) -> list[dict]:
    data = body.get("data", body) if isinstance(body, dict) else {}
    events = data.get("events", data) if isinstance(data, dict) else data
    if not isinstance(events, list):
        raise RuntimeError("analytics event query returned no events array")
    return [event for event in events if isinstance(event, dict)]


def run_analytics_steps(journey: Journey, *, token: str, runner=run_command,
                        opener=urllib.request.urlopen) -> None:
    """Claim event persistence and project API isolation with explicit step IDs."""
    event_type = f"case.analytics.persist.{journey.run_id}"
    source = "case-fixture"
    before = datetime.now(timezone.utc) - timedelta(seconds=2)
    with invocation_context(step_id="S-191.1"):
        track_status, track_body = _request("POST", "/platform/v1/analytics/track", token=token,
                                             payload={"args": [event_type, {"source": source}]}, opener=opener)
    journey.step("track local analytics event", "analytics", lambda: (
        None if track_status == 200 and isinstance(track_body, dict) and track_body.get("ok") is True
        else f"platform analytics track returned HTTP {track_status}"
    ), cases=["S-191.1"])

    query_path = ("/api/v1/analytics/events?type=" + event_type + "&source=" + source
                  + "&from=" + before.isoformat() + "&to="
                  + (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat())
    with invocation_context(step_id="S-191.2"):
        status, body = _request("GET", query_path, token=token, opener=opener)
    events = _event_list(body) if status == 200 else []
    journey.step("query tracked analytics event", "analytics", lambda: (
        None if status == 200 and any(event.get("type") == event_type for event in events)
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
        and any(event.get("type") == event_type for event in after_events)
        else f"restart exited {restarted.returncode}; post-restart analytics query returned HTTP {after_status}"
    ), cases=["S-191.3"])

    projects = [os.environ.get("KB_JOURNEY_PROJECT_ID", ""),
                os.environ.get("KB_JOURNEY_PROJECT_B_ID", "")]
    project_events: list[list[dict]] = []
    project_runs: list[object] = []
    for project_id in projects:
        with invocation_context(step_id="S-201.3"):
            status, body = _request("GET", f"/api/v1/projects/{project_id}/analytics/events?source={source}",
                                    token=token, opener=opener)
        with invocation_context(step_id="S-201.3"):
            run_status, runs = _request("GET", f"/api/v1/projects/{project_id}/workflow/api/v1/runs",
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
