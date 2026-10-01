#!/usr/bin/env python3
"""Probe fresh-install service capabilities and emit machine-readable results."""
from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import urllib.error
import urllib.request
import uuid
from urllib.request import build_opener, HTTPCookieProcessor

from service_steps import Journey
from plugin_author_steps import run_plugin_author_flow
from marketplace_steps import run_marketplace_steps
from adapter_defaults_steps import run_adapter_default_steps
from mcp_project_steps import (
    apply_commit_plan,
    commit_tool_schema,
    invalid_arguments,
    local_auth,
    project_isolation,
    secured_auth,
    unknown_tool,
)


BASE_URL = os.getenv("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
PROJECT_ID = os.getenv("KB_JOURNEY_PROJECT_ID", "")
ADMIN_EMAIL = os.getenv("KB_JOURNEY_ADMIN_EMAIL", "admin@example.com")
ADMIN_PASSWORD = os.getenv("KB_JOURNEY_ADMIN_PASSWORD", "")
HTTP = build_opener(HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path: str, *, method="GET", payload=None, token="", accept="application/json") -> tuple[int, object, dict[str, str]]:
    body = json.dumps(payload).encode() if payload is not None else None
    headers = {"Accept": accept}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(BASE_URL + path, data=body, headers=headers, method=method)
    try:
        with HTTP.open(req, timeout=15) as response:
            raw = response.read()
            status = response.status
            response_headers = dict(response.headers.items())
    except urllib.error.HTTPError as error:
        raw, status, response_headers = error.read(), error.code, dict(error.headers.items())
    except (urllib.error.URLError, TimeoutError) as error:
        raise RuntimeError(f"gateway is unreachable at {BASE_URL}: {error}; start it with `kb-create start`") from error
    try:
        parsed = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        parsed = raw.decode(errors="replace")
    return status, parsed, response_headers


def expect(path: str, expected: set[int], capability: str, *, method="GET", payload=None, token=""):
    status, body, _ = request(path, method=method, payload=payload, token=token)
    if status not in expected:
        detail = body.get("message", body.get("error", "")) if isinstance(body, dict) else ""
        reason = f"{capability} unavailable: {method} {path} returned HTTP {status}"
        if detail:
            reason += f" ({detail})"
        reason += "; build and start the capability, then rerun `python3 /opt/kb-journey/service_runner.py`"
        return reason
    return None


def expect_json(path: str, capability: str, *, token="") -> str | None:
    status, body, _ = request(path, token=token)
    if status != 200:
        detail = body.get("message", body.get("error", "")) if isinstance(body, dict) else ""
        suffix = f" ({detail})" if detail else ""
        return f"{capability} unavailable: GET {path} returned HTTP {status}{suffix}"
    if not isinstance(body, dict):
        return f"{capability} returned HTTP 200 without a JSON object"
    return None


def checks(mode: str):
    yield ("gateway readiness", "gateway", lambda: expect_json("/ready", "gateway readiness"))
    if mode == "secured":
        def admin_login():
            if not ADMIN_PASSWORD:
                return "BLOCKED: secured profile needs KB_JOURNEY_ADMIN_PASSWORD; provide test credentials in the isolated journey container"
            status, body, _ = request("/auth/login/cli", method="POST",
                                      payload={"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
            if status not in {200, 201}:
                return f"secured admin CLI login unavailable: POST /auth/login/cli returned HTTP {status}; check the isolated secured install credentials"
            if not isinstance(body, dict):
                return "secured admin CLI login returned a non-JSON response"
            token = body.get("accessToken") or body.get("token")
            if not token:
                return "secured admin CLI login returned no access token from /auth/login/cli"
            os.environ["KB_JOURNEY_SERVICE_TOKEN"] = str(token)
            me_status, _, _ = request("/auth/me", token=str(token))
            if me_status != 200:
                return f"secured admin CLI token did not authenticate /auth/me (HTTP {me_status})"
            return None
        yield ("secured admin CLI login", "auth.secured", admin_login)

    token = os.getenv("KB_JOURNEY_SERVICE_TOKEN", "")
    if not PROJECT_ID:
        project = lambda: "BLOCKED: no project is registered; run `kb project add <path> --init --json` and set KB_JOURNEY_PROJECT_ID"
    else:
        project = lambda: expect(f"/api/v1/projects/{PROJECT_ID}", {200}, "project registry lookup", token=os.getenv("KB_JOURNEY_SERVICE_TOKEN", token))
    yield ("project registration", "projects", project)
    pid = PROJECT_ID or "{projectId}"
    auth_token = lambda: os.getenv("KB_JOURNEY_SERVICE_TOKEN", token)
    yield ("workflow health", "workflow", lambda: expect_json(
        f"/api/v1/projects/{pid}/workflow/health", "project-aware workflow health", token=auth_token()))
    yield ("workflow discovery", "workflow", lambda: discover_workflow(pid, auth_token()))
    yield ("workflow execution endpoint", "workflow", lambda: expect(
        f"/api/v1/projects/{pid}/workflow/api/v1/workflows/e2e-hello/runs", {200, 201, 202},
        "project-aware workflow run endpoint", method="POST", payload={"trigger": {"type": "manual"}}, token=auth_token()))
    yield ("project-scoped MCP initialize", "mcp", lambda: mcp_initialize(pid, auth_token()))
    yield ("project-scoped MCP tools/list", "mcp", lambda: mcp_probe(pid, auth_token()))
    yield ("project-scoped MCP diagnostics", "mcp", lambda: mcp_diagnostics(pid, auth_token()))
    if mode == "local":
        yield ("project MCP tokenless local access", "mcp", lambda: local_auth(pid))
    else:
        yield ("project MCP secured missing and invalid token rejection", "mcp", lambda: secured_auth(pid))
    yield ("project MCP commit schema", "mcp", lambda: commit_tool_schema(pid, auth_token()))
    project_root = os.getenv("KB_JOURNEY_PROJECT_ROOT", "")
    if project_root:
        yield ("project MCP commit mutation", "mcp", lambda: apply_commit_plan(pid, auth_token(), project_root))
    else:
        yield ("project MCP commit mutation", "mcp", lambda: "BLOCKED: provide KB_JOURNEY_PROJECT_ROOT for the disposable commit fixture")
    yield ("project MCP unknown tool error", "mcp", lambda: unknown_tool(pid, auth_token()))
    yield ("project MCP invalid argument error", "mcp", lambda: invalid_arguments(pid, auth_token()))
    project_b_id = os.getenv("KB_JOURNEY_PROJECT_B_ID", "")
    if project_b_id:
        yield ("project MCP cross-project isolation", "mcp", lambda: project_isolation(pid, project_b_id, auth_token()))
    else:
        yield ("project MCP cross-project isolation", "mcp", lambda: "BLOCKED: provide KB_JOURNEY_PROJECT_B_ID for the isolated second project")
    yield ("Studio shell", "studio", lambda: expect("/", {200}, "Studio shell"))
    yield ("Studio project API", "studio", lambda: expect("/api/v1/projects", {200}, "Studio project API", token=auth_token()))
    yield ("marketplace registry", "marketplace", lambda: expect(f"/api/v1/projects/{pid}/rest/api/v1/plugins/registry", {200}, "marketplace registry", token=auth_token()))
def declared_capabilities(mode: str) -> set[str]:
    """Return every capability the selected service profile promises to check."""
    capabilities = {capability for _, capability, _ in checks(mode)}
    capabilities.add("auth.local" if mode == "local" else "auth.invite-reset")
    capabilities.add("plugin-author")
    return capabilities


def discover_workflow(project_id: str, token: str) -> str | None:
    path = f"/api/v1/projects/{project_id}/workflow/api/v1/workflows"
    status, body, _ = request(path, token=token)
    if status != 200:
        return f"project-aware workflow discovery unavailable: GET {path} returned HTTP {status}"
    data = body.get("data", body) if isinstance(body, dict) else body
    workflows = data.get("workflows", []) if isinstance(data, dict) else data
    if not isinstance(workflows, list) or not any(
        isinstance(item, dict) and (item.get("name") == "e2e-hello" or item.get("id") == "e2e-hello")
        for item in workflows
    ):
        return "project-aware workflow catalog does not include the e2e-hello fixture from .kb/workflows/e2e-hello.yml"
    return None


def _mcp_response(project_id: str, token: str, payload: dict) -> tuple[int, object]:
    status, body, _ = request(f"/api/v1/projects/{project_id}/mcp", method="POST", token=token,
                              accept="application/json, text/event-stream", payload=payload)
    if isinstance(body, str) and any(line.startswith("data:") for line in body.splitlines()):
        data = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
        try:
            body = json.loads(data)
        except json.JSONDecodeError:
            return status, None
    return status, body


def mcp_initialize(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-03-26", "capabilities": {},
                   "clientInfo": {"name": "kb-labs-journey", "version": "1.0.0"}},
    })
    if status != 200:
        return f"project-scoped MCP initialize unavailable: POST /api/v1/projects/{project_id}/mcp returned HTTP {status}; enable the project MCP runtime and rerun the service journey"
    result = body.get("result") if isinstance(body, dict) else None
    if not isinstance(result, dict) or result.get("protocolVersion") != "2025-03-26":
        return "project-scoped MCP initialize returned no result for protocol version 2025-03-26"
    return None


def mcp_probe(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {},
    })
    if status != 200:
        return f"project-scoped MCP tools/list unavailable: POST /api/v1/projects/{project_id}/mcp returned HTTP {status}; enable the project MCP runtime and rerun the service journey"
    if not isinstance(body, dict) or not isinstance(body.get("result"), dict):
        return "project-scoped MCP tools/list returned no JSON-RPC result"
    tools = body["result"].get("tools")
    if not isinstance(tools, list) or not tools:
        return "project-scoped MCP tools/list returned no project tools"
    names = {tool.get("name") for tool in tools if isinstance(tool, dict)}
    expected = {"kb-labs-commit__commit", "workflow__workflow_run", "kb-labs__mcp_diagnostics"}
    if not expected <= names:
        return f"project-scoped MCP tools/list omitted expected tools: {sorted(expected - names)}"
    if any(not isinstance(tool, dict) or not isinstance(tool.get("name"), str)
           or not isinstance(tool.get("inputSchema"), dict) for tool in tools):
        return "project-scoped MCP tools/list returned a tool without its name or input schema"
    return None


def mcp_diagnostics(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": {"name": "kb-labs__mcp_diagnostics", "arguments": {}},
    })
    if status != 200:
        return f"project-scoped MCP diagnostics unavailable: HTTP {status}"
    result = body.get("result") if isinstance(body, dict) else None
    content = result.get("content", []) if isinstance(result, dict) else []
    text = "\n".join(item.get("text", "") for item in content if isinstance(item, dict))
    if not isinstance(result, dict) or result.get("isError") or "loaded cleanly" not in text:
        return "project-scoped MCP diagnostics did not report that the project loaded cleanly"
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["local", "secured"], required=True)
    parser.add_argument("--output", default="service-journey.json")
    parser.add_argument("--run-id")
    args = parser.parse_args()
    journey = Journey(args.mode, args.run_id or str(uuid.uuid4()))
    for name, capability, check in checks(args.mode):
        output = check()
        if output and output.startswith("BLOCKED:"):
            journey.blocked(name, capability, output.removeprefix("BLOCKED: "))
        elif name in {"gateway readiness", "workflow health"}:
            journey.step(name, capability, lambda value=output: value, cases=["PC-002.3"])
        elif name == "project-scoped MCP initialize":
            journey.step(name, capability, lambda value=output: value, cases=["S-045.1"])
        elif name == "project-scoped MCP tools/list":
            journey.step(name, capability, lambda value=output: value, cases=["S-045.2"])
        elif name == "project-scoped MCP diagnostics":
            journey.step(name, capability, lambda value=output: value, cases=["S-045.3"])
        elif name == "project MCP tokenless local access":
            journey.step(name, capability, lambda value=output: value, cases=["S-045.4"])
        elif name == "project MCP secured missing and invalid token rejection":
            journey.step(name, capability, lambda value=output: value, cases=["S-045.5", "S-140.6"])
        elif name == "project MCP commit schema":
            journey.step(name, capability, lambda value=output: value, cases=["S-140.1"])
        elif name == "project MCP commit mutation":
            journey.step(name, capability, lambda value=output: value, cases=["S-140.2"])
        elif name == "project MCP unknown tool error":
            journey.step(name, capability, lambda value=output: value, cases=["S-140.3"])
        elif name == "project MCP invalid argument error":
            journey.step(name, capability, lambda value=output: value, cases=["S-140.4"])
        elif name == "project MCP cross-project isolation":
            if output and output.startswith("BLOCKED:"):
                journey.blocked(name, capability, output.removeprefix("BLOCKED: "), cases=["S-140.5"])
            else:
                journey.step(name, capability, lambda value=output: value, cases=["S-140.5"])
        else:
            journey.step(name, capability, lambda value=output: value)
    project_root = os.getenv("KB_JOURNEY_PROJECT_ROOT")
    registry = os.getenv("KB_JOURNEY_REGISTRY", "http://verdaccio:4873")
    if project_root:
        run_marketplace_steps(journey, project_root, registry, os.environ.copy())
        run_adapter_default_steps(journey, project_root, os.environ.copy())
        run_plugin_author_flow(journey, Path(project_root), Path.home() / ".local/bin/kb", registry)
    else:
        reason = "journey runner did not receive KB_JOURNEY_PROJECT_ROOT from the installed project setup"
        journey.blocked("marketplace and adapter user flows", "marketplace", reason)
        journey.blocked("plugin author flow", "plugin-author", reason)
    result = journey.write_json(args.output)
    print(json.dumps(result.__dict__, indent=2))
    return 0 if result.status == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
