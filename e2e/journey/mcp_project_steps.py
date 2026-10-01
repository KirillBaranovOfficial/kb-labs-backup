"""Project MCP journey steps for typed errors, mutations, auth and isolation."""
from __future__ import annotations

import json
import os
import re

from invocations import run_command



def _request(*args, **kwargs):
    # Import lazily because service_runner also registers these step functions.
    from service_runner import request
    return request(*args, **kwargs)


def mcp_call(project_id: str, request_id: int, name: str, arguments: dict, token: str = ""):
    status, body, _ = _request(
        f"/api/v1/projects/{project_id}/mcp",
        method="POST",
        token=token,
        accept="application/json, text/event-stream",
        payload={
            "jsonrpc": "2.0",
            "id": request_id,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        },
    )
    if isinstance(body, str) and any(line.startswith("data:") for line in body.splitlines()):
        data = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
        try:
            body = json.loads(data)
        except json.JSONDecodeError:
            return status, None
    return status, body


def _check_error_result(body: object, expected_code: str, *, argument: str | None = None) -> str | None:
    result = body.get("result") if isinstance(body, dict) else None
    envelope = result.get("structuredContent") if isinstance(result, dict) else None
    if not isinstance(result, dict) or result.get("isError") is not True:
        return "MCP call did not return an error tool result"
    if not isinstance(envelope, dict):
        return "MCP tool error omitted structuredContent error envelope"
    required = {"code", "area", "stage", "severity", "retryable", "message", "hint"}
    if not required <= envelope.keys():
        return "MCP structuredContent does not contain the full ADR-0049 error envelope"
    if envelope["code"] != expected_code:
        return f"MCP tool error code was {envelope['code']!r}, expected {expected_code}"
    text_lines = [item.get("text", "") for item in result.get("content", []) if isinstance(item, dict)]
    expected_text = f"{envelope['code']}: {envelope['message']} — {envelope['hint']}"
    if expected_text not in text_lines:
        return "MCP tool error text does not match its structured envelope"
    if argument and argument not in envelope.get("message", "") and argument not in str(envelope.get("details", {})):
        return f"MCP validation error did not identify argument {argument!r}"
    return None


def unknown_tool(project_id: str, token: str = "") -> str | None:
    status, body = mcp_call(project_id, 1403, "tool-that-does-not-exist", {}, token)
    if status != 200:
        return f"unknown tool returned HTTP {status}, expected HTTP 200 tool error result"
    failure = _check_error_result(body, "KB_RUNTIME_MCP_UNKNOWN_TOOL")
    if failure:
        return failure
    result = body["result"]
    envelope = result["structuredContent"]
    if "tools/list" not in envelope["hint"]:
        return "unknown tool error hint did not name tools/list"
    return None


def invalid_arguments(project_id: str, token: str = "") -> str | None:
    status, body = mcp_call(project_id, 1404, "kb-labs-commit__commit_apply", {"force": "yes"}, token)
    if status != 200:
        return f"invalid arguments returned HTTP {status}, expected HTTP 200 tool error result"
    return _check_error_result(body, "KB_RUNTIME_MCP_INVALID_ARGUMENTS", argument="force")


def secured_auth(project_id: str) -> str | None:
    for token in ("", "not-a-real-token"):
        status, body, _ = _request(
            f"/api/v1/projects/{project_id}/mcp",
            method="POST",
            payload={"jsonrpc": "2.0", "id": 145, "method": "tools/list", "params": {}},
            token=token,
            accept="application/json, text/event-stream",
        )
        if status != 401:
            return f"secured tools/list with {'missing' if not token else 'invalid'} token returned HTTP {status}, expected 401"
        envelope = body.get("error") if isinstance(body, dict) else None
        if (not isinstance(envelope, dict)
                or envelope.get("code") != "KB_AUTH_REQUIRED"
                or envelope.get("hint") != 'Run "kb auth login".'):
            return f"secured tools/list with {'missing' if not token else 'invalid'} token returned no KB_AUTH_REQUIRED envelope"
        if isinstance(body, dict) and isinstance(body.get("result"), dict) and body["result"].get("tools"):
            return "secured tools/list returned a tool catalog without valid authentication"
    return None


def local_auth(project_id: str) -> str | None:
    status, body, _ = _request(
        f"/api/v1/projects/{project_id}/mcp",
        method="POST",
        payload={"jsonrpc": "2.0", "id": 454, "method": "tools/list", "params": {}},
        token="",
        accept="application/json, text/event-stream",
    )
    if status != 200:
        return f"local tokenless tools/list returned HTTP {status}, expected 200"
    result = body.get("result") if isinstance(body, dict) else None
    tools = result.get("tools") if isinstance(result, dict) else None
    if not isinstance(tools, list) or not tools:
        return "local tokenless tools/list did not return the selected project catalog"
    names = {tool.get("name") for tool in tools if isinstance(tool, dict)}
    expected = {
        "kb-labs-commit__commit",
        "kb-labs-workflow__workflow_run",
        "kb-labs__mcp_diagnostics",
    }
    if not expected <= names:
        return f"local tokenless tools/list omitted selected project tools: {sorted(expected - names)}"
    return None


def commit_tool_schema(project_id: str, token: str) -> str | None:
    status, body, _ = _request(
        f"/api/v1/projects/{project_id}/mcp",
        method="POST",
        token=token,
        accept="application/json, text/event-stream",
        payload={"jsonrpc": "2.0", "id": 1401, "method": "tools/list", "params": {}},
    )
    if status != 200:
        return f"project A tools/list returned HTTP {status}"
    result = body.get("result") if isinstance(body, dict) else None
    tools = result.get("tools", []) if isinstance(result, dict) else []
    tool = next((item for item in tools if isinstance(item, dict) and item.get("name") == "kb-labs-commit__commit_apply"), None)
    if tool is None:
        return "project A catalog omitted kb-labs-commit__commit_apply"
    schema = tool.get("inputSchema")
    if not isinstance(schema, dict) or schema.get("type") != "object" or not isinstance(schema.get("properties"), dict):
        return "commit apply tool did not expose its manifest-derived object input schema"
    properties = schema["properties"]
    if any(not isinstance(properties.get(name), dict) or properties[name].get("type") != "boolean"
           for name in ("force", "json")):
        return "commit apply schema did not describe its force and json flags as booleans"
    required = schema.get("required", [])
    if not isinstance(required, list) or any(name in required for name in ("force", "json")):
        return "commit apply schema did not preserve the flags' optional status"
    return None


def apply_commit_plan(project_id: str, token: str, project_root: str) -> str | None:
    status, body = mcp_call(project_id, 1402, "kb-labs-commit__commit_apply", {}, token)
    if status != 200:
        return f"commit apply returned HTTP {status}"
    result = body.get("result") if isinstance(body, dict) else None
    content = result.get("content", []) if isinstance(result, dict) else []
    text = "\n".join(item.get("text", "") for item in content if isinstance(item, dict))
    if not isinstance(result, dict) or result.get("isError") or not text:
        return "commit apply did not return a successful result containing the created commit SHA"
    commits = re.findall(r"(?<![0-9a-f])[0-9a-f]{7,40}(?![0-9a-f])", text, flags=re.IGNORECASE)
    if not commits:
        return "commit apply output did not contain a commit SHA"
    history = run_command(["git", "-C", project_root, "log", "--format=%h", "--all"],
                          env=os.environ, timeout=30)
    if history.returncode != 0:
        return "could not inspect project A Git history after the commit tool call"
    if not any(commit.lower() in {line.lower() for line in history.stdout.splitlines()} for commit in commits):
        return "commit apply output SHA was not found in project A Git history"
    return None


def project_isolation(project_a: str, project_b: str, token: str) -> str | None:
    if project_a == project_b:
        return "project A and project B must be distinct registered projects"
    status, body, _ = _request(
        f"/api/v1/projects/{project_b}/mcp",
        method="POST",
        token=token,
        accept="application/json, text/event-stream",
        payload={"jsonrpc": "2.0", "id": 1405, "method": "tools/list", "params": {}},
    )
    if status != 200:
        return f"project B tools/list returned HTTP {status}"
    result = body.get("result") if isinstance(body, dict) else None
    tools = result.get("tools", []) if isinstance(result, dict) else []
    if any(isinstance(tool, dict) and tool.get("name") == "kb-labs-commit__commit_apply" for tool in tools):
        return "project B listed project A's project-scoped commit tool"
    return unknown_tool(project_b, token)
