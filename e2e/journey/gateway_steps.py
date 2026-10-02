"""Gateway configuration, adapter-call and telemetry claims."""
from __future__ import annotations

import json
import os
import subprocess
import urllib.request

from invocations import invocation_context, open_http, run_command
from service_steps import Journey


def _http(path: str, *, token: str, method: str = "GET", payload: object = None,
          opener=urllib.request.urlopen) -> tuple[int, object]:
    base = os.environ.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
    request = urllib.request.Request(
        base + path,
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={"Accept": "application/json, text/event-stream", "Content-Type": "application/json",
                 **({"Authorization": f"Bearer {token}"} if token else {})}, method=method,
    )
    status, raw, _ = open_http(opener, request, timeout=60, env=os.environ)
    try:
        return status, json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        return status, raw.decode(errors="replace")


def _cli(command: list[str], step_id: str, runner) -> subprocess.CompletedProcess[str]:
    with invocation_context(step_id=step_id):
        return runner(command, cwd=os.environ.get("KB_JOURNEY_PROJECT_ROOT"), env=os.environ.copy(),
                      timeout=180, capture_output=True, text=True, check=False)


def run_gateway_steps(journey: Journey, *, token: str, runner=run_command,
                      opener=urllib.request.urlopen) -> None:
    """Run gateway-owned claims; credentials are always supplied by environment references."""
    adapter = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapters.llm",
                    "@kb-labs/adapters-openai", "--json"], "S-017.1", runner)
    journey.step("configure platform LLM adapter", "gateway", lambda: (
        None if adapter.returncode == 0 else f"platform adapter config exited {adapter.returncode}"
    ), cases=["S-017.1", "S-018.1"])

    secret_ref = _cli(["kb", "config", "set", "--scope", "platform",
                       "platform.adapterOptions.llm.apiKey", "${OPENAI_API_KEY}", "--json"], "S-017.2", runner)
    inspect = _cli(["kb", "config", "get", "platform.adapterOptions.llm.apiKey", "--json"], "S-017.4", runner)
    secret_safe = secret_ref.returncode == 0 and inspect.returncode == 0 \
        and "${OPENAI_API_KEY}" in inspect.stdout \
        and os.environ.get("OPENAI_API_KEY", "") not in inspect.stdout
    journey.step("store and inspect provider secret reference", "gateway",
                 lambda: None if secret_safe else "platform configuration did not retain a literal environment reference without exposing the resolved key",
                 cases=["S-017.2", "S-017.4"])

    command = os.environ.get("KB_JOURNEY_LLM_COMMAND", "")
    result = _cli([command] if command else ["kb", "platform", "call", "llm", "complete", "--json"],
                  "S-017.3,S-018.2,S-018.3", runner)
    journey.step("exercise configured platform LLM adapter", "gateway", lambda: (
        None if result.returncode == 0 else f"LLM-backed command exited {result.returncode}: {(result.stderr or result.stdout).strip()}"
    ), cases=["S-017.3", "S-018.2", "S-018.3"])

    # The case requires two project calls under each provider; assert both projects are supplied.
    projects = [os.environ.get("KB_JOURNEY_PROJECT_ID", ""), os.environ.get("KB_JOURNEY_PROJECT_B_ID", "")]
    journey.step("verify provider configuration spans both registered projects", "gateway",
                 lambda: None if all(projects) and os.environ.get("KB_JOURNEY_VIBEPROXY_READY") == "1"
                 else "S-018 requires two registered projects and both provider credentials; seed KB_JOURNEY_PROJECT_B_ID and KB_JOURNEY_VIBEPROXY_READY",
                 cases=["S-018.2", "S-018.3"])

    # The documented catalog endpoint is exercised as specified; this currently exposes its absence.
    with invocation_context(step_id="S-189.1"):
        catalog_status, catalog = _http("/platform/v1/adapters", token=token, opener=opener)
    journey.step("discover platform adapter method catalog", "gateway", lambda: (
        None if catalog_status == 200 and isinstance(catalog, dict) and isinstance(catalog.get("adapters"), dict)
        else f"documented platform adapter catalog returned HTTP {catalog_status} or no adapter catalog"
    ), cases=["S-189.1"])

    with invocation_context(step_id="S-189.2"):
        call_status, call_body = _http("/platform/v1/analytics/track", token=token, method="POST",
                                       payload={"args": ["case.gateway.call", {"source": "case-fixture"}]}, opener=opener)
    journey.step("call analytics through platform API", "gateway", lambda: (
        None if call_status == 200 and isinstance(call_body, dict) and call_body.get("ok") is True
        else f"platform analytics call returned HTTP {call_status} without the success envelope"
    ), cases=["S-189.2"])

    for path in ("/platform/v1/unknownAdapter/method", "/platform/v1/analytics/unknownMethod"):
        with invocation_context(step_id="S-189.3"):
            status, body = _http(path, token=token, method="POST", payload={"args": []}, opener=opener)
        error = body.get("error", {}) if isinstance(body, dict) else {}
        if status >= 400 and isinstance(error, dict) and error.get("code") and error.get("hint") and "stack" not in body:
            continue
        journey.step("reject unsupported platform adapter call", "gateway",
                     lambda: f"{path} did not return a typed client-safe error with corrective hint (HTTP {status})",
                     cases=["S-189.3"])
        break
    else:
        journey.step("reject unsupported platform adapter calls", "gateway", lambda: None, cases=["S-189.3"])

    with invocation_context(step_id="S-189.4"):
        stream_status, stream = _http("/platform/v1/llm/stream", token=token, method="POST",
                                      payload={"args": [{"prompt": "case stream"}]}, opener=opener)
    journey.step("stream platform LLM results", "gateway", lambda: (
        None if stream_status == 200 and isinstance(stream, str) and "[DONE]" in stream
        else f"LLM stream returned HTTP {stream_status} without SSE chunks and [DONE]"
    ), cases=["S-189.4"])

    telemetry_source = f"case-fixture-{journey.run_id}"
    telemetry_from = os.environ.get("KB_JOURNEY_STARTED_AT", "")
    events500 = [{"source": telemetry_source, "type": f"case.telemetry.{index}"} for index in range(500)]
    with invocation_context(step_id="S-190.1"):
        accepted_status, accepted = _http("/telemetry/v1/ingest", token=token, method="POST",
                                          payload={"events": events500}, opener=opener)
    journey.step("ingest telemetry batch at limit", "gateway", lambda: (
        None if accepted_status == 200 and isinstance(accepted, dict)
        and accepted.get("accepted") == 500 and accepted.get("rejected") == 0
        else f"500-event telemetry batch returned HTTP {accepted_status} or incorrect counts"
    ), cases=["S-190.1"])

    events501 = [{"source": telemetry_source, "type": f"case.telemetry.over.{index}"} for index in range(501)]
    with invocation_context(step_id="S-190.2"):
        rejected_status, rejected = _http("/telemetry/v1/ingest", token=token, method="POST",
                                          payload={"events": events501}, opener=opener)
    telemetry_query = f"/api/v1/analytics/events?source={telemetry_source}"
    if telemetry_from:
        telemetry_query += f"&from={telemetry_from}"
    with invocation_context(step_id="S-190.2"):
        after_rejection_status, after_rejection = _http(telemetry_query, token=token, opener=opener)
    issues = rejected.get("issues", []) if isinstance(rejected, dict) else []
    stored = after_rejection.get("events", []) if isinstance(after_rejection, dict) else []
    if isinstance(after_rejection, dict) and isinstance(after_rejection.get("data"), dict):
        stored = after_rejection["data"].get("events", stored)
    mutated = isinstance(stored, list) and any(
        isinstance(event, dict) and str(event.get("type", "")).startswith("case.telemetry.over.")
        for event in stored
    )
    journey.step("reject telemetry batch above limit", "gateway", lambda: (
        None if rejected_status == 400 and isinstance(rejected, dict)
        and rejected.get("error") == "Bad Request" and any("500" in json.dumps(issue) for issue in issues)
        and after_rejection_status == 200 and not mutated
        else f"501-event telemetry batch returned HTTP {rejected_status} or was written / could not be verified after rejection"
    ), cases=["S-190.2"])
