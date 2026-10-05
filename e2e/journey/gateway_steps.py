"""Gateway configuration, adapter-call and telemetry claims."""
from __future__ import annotations

import json
import os
from pathlib import Path
import shlex
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


def _cli(command: list[str], step_id: str, runner, *, cwd: str | None = None) -> subprocess.CompletedProcess[str]:
    with invocation_context(step_id=step_id):
        return runner(command, cwd=cwd or os.environ.get("KB_JOURNEY_PROJECT_ROOT"), env=os.environ.copy(),
                      timeout=180, capture_output=True, text=True, check=False)


def _json_contains(result: subprocess.CompletedProcess[str], expected: str) -> bool:
    if result.returncode != 0:
        return False
    try:
        payload = json.loads(result.stdout)
    except (TypeError, json.JSONDecodeError):
        return False
    if not isinstance(payload, dict):
        return False
    values: list[str] = []

    def collect(value: object) -> None:
        if isinstance(value, str):
            values.append(value)
        elif isinstance(value, dict):
            for child in value.values():
                collect(child)
        elif isinstance(value, list):
            for child in value:
                collect(child)

    collect(payload)
    return expected in values


def _json_string_field(result: subprocess.CompletedProcess[str] | None, field: str) -> str | None:
    if result is None or result.returncode != 0:
        return None
    try:
        payload = json.loads(result.stdout)
    except (TypeError, json.JSONDecodeError):
        return None

    def find(value: object) -> str | None:
        if isinstance(value, dict):
            candidate = value.get(field)
            if isinstance(candidate, str) and candidate:
                return candidate
            for child in value.values():
                found = find(child)
                if found:
                    return found
        elif isinstance(value, list):
            for child in value:
                found = find(child)
                if found:
                    return found
        return None

    return find(payload)


def _json_object(result: subprocess.CompletedProcess[str] | None) -> dict[str, object]:
    if result is None or result.returncode != 0:
        return {}
    try:
        payload = json.loads(result.stdout)
    except (TypeError, json.JSONDecodeError):
        return {}
    return payload if isinstance(payload, dict) else {}


def _store_studio_llm_model(model: str | None) -> None:
    fixture_file = os.environ.get("KB_JOURNEY_STUDIO_FIXTURE_FILE", "")
    if not model or not fixture_file:
        return
    path = Path(fixture_file)
    if not path.is_file():
        return
    fixture = json.loads(path.read_text(encoding="utf-8"))
    fixture["model"] = model
    path.write_text(json.dumps(fixture), encoding="utf-8")


def run_llm_provider_steps(journey: Journey, *, runner=run_command) -> None:
    """Exercise and prove both configured provider protocols with the fixture plugin."""
    mock_base = os.environ.get("KB_JOURNEY_LLM_MOCK_BASE_URL", "http://mock-llm:8317")
    adapter = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapters.llm",
                    "@kb-labs/adapters-openai", "--json"], "S-017.1", runner)
    journey.step("configure platform OpenAI adapter", "gateway", lambda: (
        None if adapter.returncode == 0 else "could not select @kb-labs/adapters-openai at platform scope"
    ), cases=["S-017.1", "S-018.1"])

    secret_ref = _cli(["kb", "config", "set", "--scope", "platform",
                       "platform.adapterOptions.llm.apiKey", "${OPENAI_API_KEY}", "--json"], "S-017.2", runner)
    project_config = Path(os.environ.get("KB_JOURNEY_PROJECT_ROOT", ".")) / ".kb" / "kb.config.jsonc"
    project_config_before = project_config.read_bytes() if project_config.is_file() else None
    openai_base = _cli(["kb", "config", "set", "--scope", "platform",
                        "platform.adapterOptions.llm.baseURL", f"{mock_base}/v1", "--json"], "S-017.2", runner)
    base_result = _json_object(openai_base)
    platform_root = Path(os.environ.get("KB_JOURNEY_PLATFORM_ROOT", str(Path.home() / "kb-platform"))).resolve()
    expected_platform_config = str(platform_root / ".kb" / "kb.config.jsonc")
    scope_ok = (base_result.get("file") == expected_platform_config
                and project_config_before == (project_config.read_bytes() if project_config.is_file() else None)
                and base_result.get("nextCommand") == f"kb-create restart --platform-root {json.dumps(str(platform_root))}")
    openai_model = _cli(["kb", "config", "set", "--scope", "platform",
                         "platform.adapterOptions.llm.model", "journey-mock-model", "--json"], "S-017.2", runner)
    inspect = _cli(["kb", "config", "get", "platform.adapterOptions.llm.apiKey", "--json"], "S-017.4", runner)
    secret_safe = (secret_ref.returncode == 0 and openai_base.returncode == 0
                   and openai_model.returncode == 0 and inspect.returncode == 0
                   and "${OPENAI_API_KEY}" in inspect.stdout
                   and os.environ.get("OPENAI_API_KEY", "") not in inspect.stdout)
    journey.step("store platform config without exposing the key or changing project config", "gateway",
                 lambda: None if secret_safe and scope_ok else "platform config wrote to the wrong file, changed project config, omitted restart guidance, or exposed the OPENAI_API_KEY reference",
                 cases=["S-017.2", "S-017.4", "S-017.5"])

    restart = _cli(shlex.split(str(base_result.get("nextCommand", "kb-create restart"))), "S-017.5", runner)
    journey.step("restart the host after changing platform LLM settings", "gateway", lambda: (
        None if restart.returncode == 0 else "the exact restart command returned by kb config set failed"
    ), cases=["S-017.5"])

    project_a = os.environ.get("KB_JOURNEY_PROJECT_ROOT", "")
    project_b = os.environ.get("KB_JOURNEY_PROJECT_B_ROOT", "")
    fixture_command = ["kb", "journey", "llm", "complete", "--json"]

    openai_a = _cli([*fixture_command, "--marker=provider-openai-project-a"], "S-017.3", runner, cwd=project_a) if project_a else None
    openai_content = openai_a is not None and _json_contains(openai_a, "OPENAI_MOCK_RESPONSE")
    openai_model = _json_string_field(openai_a, "model") if openai_content else None
    _store_studio_llm_model(openai_model)
    journey.step("call the configured OpenAI adapter from project A", "gateway", lambda: (
        None if openai_content and openai_model == "journey-mock-model"
        else "project A did not return the deterministic OpenAI mock response in its JSON result"
    ), cases=["S-017.3"])

    openai_projects = [
        _cli([*fixture_command, f"--marker=provider-openai-{label}"], "S-018.2", runner, cwd=root)
        for label, root in (("a", project_a), ("b", project_b)) if root
    ]
    project_ids = [os.environ.get("KB_JOURNEY_PROJECT_ID", ""), os.environ.get("KB_JOURNEY_PROJECT_B_ID", "")]
    journey.step("call OpenAI adapter from both registered projects", "gateway", lambda: (
        None if all(project_ids) and len(openai_projects) == 2
        and all(_json_contains(item, "OPENAI_MOCK_RESPONSE") for item in openai_projects)
        else "both registered project roots must return the deterministic OpenAI mock response"
    ), cases=["S-018.2"])

    vibe_adapter = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapters.llm",
                         "@kb-labs/adapters-vibeproxy", "--json"], "S-018.3", runner)
    vibe_url = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapterOptions.llm.baseURL",
                     mock_base, "--json"], "S-018.3", runner)
    vibe_key = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapterOptions.llm.apiKey",
                     "${VIBEPROXY_API_KEY}", "--json"], "S-018.3", runner)
    vibe_model = _cli(["kb", "config", "set", "--scope", "platform", "platform.adapterOptions.llm.model",
                       "journey-mock-model", "--json"], "S-018.3", runner)
    vibe_restart = _cli(shlex.split(str(_json_object(vibe_adapter).get("nextCommand", "kb-create restart"))),
                        "S-018.3", runner)
    vibe_projects = [
        _cli([*fixture_command, f"--marker=provider-vibeproxy-{label}"], "S-018.3", runner, cwd=root)
        for label, root in (("a", project_a), ("b", project_b)) if root
    ]
    journey.step("switch to VibeProxy messages protocol in both projects", "gateway", lambda: (
        None if all(project_ids) and len(vibe_projects) == 2
        and all(_json_contains(item, "VIBEPROXY_MOCK_RESPONSE") for item in vibe_projects)
        and all(_json_string_field(item, "stopReason") == "end_turn" for item in vibe_projects)
        and all(item.returncode == 0 for item in (vibe_adapter, vibe_url, vibe_key, vibe_model, vibe_restart))
        else "both registered projects must return the deterministic VibeProxy Messages API response with stopReason=end_turn"
    ), cases=["S-018.3"])


def run_gateway_steps(journey: Journey, *, token: str, runner=run_command,
                      opener=urllib.request.urlopen) -> None:
    """Run gateway-owned claims; credentials are always supplied by environment references."""
    run_llm_provider_steps(journey, runner=runner)

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

    with invocation_context(step_id="S-189.5"):
        error_status, error_stream = _http("/platform/v1/llm/stream", token=token, method="POST",
                                           payload={"args": ["journey-provider-401"]}, opener=opener)
        midstream_status, midstream_stream = _http("/platform/v1/llm/stream", token=token, method="POST",
                                                   payload={"args": ["journey-provider-stream-error"]}, opener=opener)
        health_status, health = _http("/health", token=token, opener=opener)
    error_envelope = error_stream.get("error", {}) if isinstance(error_stream, dict) else {}
    midstream_envelope = {}
    if isinstance(midstream_stream, str) and "event: error\n" in midstream_stream and "[DONE]" not in midstream_stream:
        for line in midstream_stream.splitlines():
            if line.startswith("data: "):
                try:
                    event_payload = json.loads(line[6:])
                except json.JSONDecodeError:
                    continue
                if isinstance(event_payload, dict) and isinstance(event_payload.get("error"), dict):
                    midstream_envelope = event_payload["error"]
                    break
    midstream_error_event = (midstream_status == 200 and midstream_envelope.get("code") == "KB_CONFIG_LLM_PROVIDER_UNAVAILABLE"
                   and "kb config get platform.adapterOptions.llm.baseURL --scope platform --json" in midstream_envelope.get("hint", "")
                   and midstream_envelope.get("message") and midstream_envelope.get("correlationId"))
    before_headers_error = (error_status == 502 and isinstance(error_envelope, dict)
                            and error_envelope.get("code") == "KB_CONFIG_LLM_KEY_INVALID"
                            and "kb config set --scope platform platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}'" in error_envelope.get("hint", "")
                            and error_envelope.get("message") and error_envelope.get("hint")
                            and error_envelope.get("correlationId"))
    health_ok = health_status == 200 and isinstance(health, dict)
    journey.step("keep gateway healthy after an LLM stream failure", "gateway", lambda: (
        None if before_headers_error and midstream_error_event and health_ok
        else f"LLM stream failure was not typed or gateway health returned HTTP {health_status}"
    ), cases=["S-189.5"])

    with invocation_context(step_id="S-189.6"):
        invalid_status, invalid_body = _http("/platform/v1/llm/stream", token=token, method="POST",
                                              payload={"args": []}, opener=opener)
    invalid_error = invalid_body.get("error", {}) if isinstance(invalid_body, dict) else {}
    journey.step("reject an LLM stream request without a prompt", "gateway", lambda: (
        None if invalid_status == 400 and invalid_error.get("code") == "KB_RUNTIME_INPUT_INVALID"
        else f"empty LLM stream args returned HTTP {invalid_status} without KB_RUNTIME_INPUT_INVALID"
    ), cases=["S-189.6"])

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
