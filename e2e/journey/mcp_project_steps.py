"""Project MCP journey steps for typed errors, mutations, auth and isolation."""
from __future__ import annotations

import json
import os
import re
from pathlib import Path

from invocations import run_command



def _request(*args, **kwargs):
    # Import lazily because service_runner also registers these step functions.
    from service_runner import request
    return request(*args, **kwargs)


def _ux_contracts() -> tuple[dict, dict]:
    installed = Path("/opt/kb-contracts/ux-result")
    root = installed if installed.is_dir() else Path(__file__).resolve().parents[2] / "contracts" / "ux-result"
    error_schema_path = (
        root.parent / "error-envelope.schema.json"
        if installed.is_dir()
        else Path(__file__).resolve().parents[2] / "core" / "platform" / "src" / "error-envelope" / "error-envelope.schema.json"
    )
    return (
        json.loads((root / "machine-result.schema.json").read_text(encoding="utf-8")),
        json.loads(error_schema_path.read_text(encoding="utf-8")),
    )


def _schema_errors(value: object, schema: dict, error_schema: dict, path: str = "$", root_schema: dict | None = None) -> list[str]:
    root_schema = root_schema or schema
    ref = schema.get("$ref")
    if isinstance(ref, str):
        if ref.startswith("#/"):
            target = root_schema
            for part in ref[2:].split("/"):
                target = target[part]
        elif ref.endswith("error-envelope.schema.json"):
            target = error_schema
        else:
            return [f"{path}: unsupported schema reference {ref}"]
        return _schema_errors(value, target, error_schema, path, root_schema)
    if "oneOf" in schema:
        matches = [not _schema_errors(value, option, error_schema, path, root_schema) for option in schema["oneOf"]]
        return [] if sum(matches) == 1 else [f"{path}: did not match exactly one schema alternative"]
    errors = []
    expected_type = schema.get("type")
    valid_type = {
        "object": lambda item: isinstance(item, dict),
        "array": lambda item: isinstance(item, list),
        "string": lambda item: isinstance(item, str),
        "boolean": lambda item: isinstance(item, bool),
    }
    if expected_type in valid_type and not valid_type[expected_type](value):
        return [f"{path}: expected {expected_type}"]
    if "const" in schema and value != schema["const"]:
        errors.append(f"{path}: expected constant {schema['const']!r}")
    if "enum" in schema and value not in schema["enum"]:
        errors.append(f"{path}: value is outside enum")
    if isinstance(value, str):
        if len(value) < schema.get("minLength", 0):
            errors.append(f"{path}: string is too short")
        if "pattern" in schema and re.search(schema["pattern"], value) is None:
            errors.append(f"{path}: string does not match pattern")
        if schema.get("format") == "uri" and not re.match(r"^[A-Za-z][A-Za-z0-9+.-]*:", value):
            errors.append(f"{path}: expected URI")
    if isinstance(value, dict):
        missing = set(schema.get("required", [])) - value.keys()
        errors.extend(f"{path}: missing required property {key}" for key in sorted(missing))
        properties = schema.get("properties", {})
        for key, item in value.items():
            if key in properties:
                errors.extend(_schema_errors(item, properties[key], error_schema, f"{path}.{key}", root_schema))
            elif schema.get("additionalProperties") is False:
                errors.append(f"{path}: unexpected property {key}")
            elif isinstance(schema.get("additionalProperties"), dict):
                errors.extend(_schema_errors(item, schema["additionalProperties"], error_schema, f"{path}.{key}", root_schema))
    if isinstance(value, list) and isinstance(schema.get("items"), dict):
        for index, item in enumerate(value):
            errors.extend(_schema_errors(item, schema["items"], error_schema, f"{path}[{index}]", root_schema))
    return errors


def _matches_machine_result(value: object) -> bool:
    machine_schema, error_schema = _ux_contracts()
    return not _schema_errors(value, machine_schema, error_schema)


def _valid_error_result(body: object) -> str | None:
    result = body.get("result") if isinstance(body, dict) else None
    envelope = result.get("structuredContent") if isinstance(result, dict) else None
    if not isinstance(result, dict) or result.get("isError") is not True:
        return "MCP invalid-argument call did not return isError: true"
    if not _matches_machine_result(envelope):
        return "MCP structuredContent does not match contracts/ux-result/machine-result.schema.json"
    error = envelope["error"]
    if not isinstance(error.get("hint"), str) or not error["hint"].strip():
        return "MCP error envelope omitted its hint"
    if not isinstance(error.get("correlationId"), str) or not error["correlationId"].strip():
        return "MCP error envelope omitted its correlationId"
    texts = [item.get("text", "") for item in result.get("content", []) if isinstance(item, dict)]
    text = "\n".join(texts)
    if re.search(r"Traceback|\bat\s+[^\n]+\([^\n]*:\d+:\d+\)|(?:^|\n)(?:Error|TypeError|ReferenceError):|panic:", text):
        return "MCP error text exposed a stack trace"
    if any(value not in text for value in (error["code"], error["message"], error["hint"], error["correlationId"])):
        return "MCP error text omitted its code, message, hint, or correlationId"
    return None


def mcp_ux_result_contract(project_id: str, token: str, *, secured: bool = False) -> str | None:
    status, body, _ = _request(
        f"/api/v1/projects/{project_id}/mcp", method="POST", token=token,
        accept="application/json, text/event-stream",
        payload={"jsonrpc": "2.0", "id": 2030, "method": "tools/list", "params": {}},
    )
    if status != 200:
        return f"MCP tools/list returned HTTP {status}, expected HTTP 200"
    result = body.get("result") if isinstance(body, dict) else None
    tools = result.get("tools") if isinstance(result, dict) else None
    if not isinstance(tools, list) or not tools:
        return "MCP tools/list did not return the live tool registry"
    for index, tool in enumerate(tools):
        if not isinstance(tool, dict) or not isinstance(tool.get("name"), str):
            return f"MCP registry entry {index} has no tool name"
        input_schema = tool.get("inputSchema")
        required = input_schema.get("required", []) if isinstance(input_schema, dict) else []
        arguments = {} if required else {"__ux_invalid_probe__": True}
        status, response = mcp_call(project_id, 2031 + index, tool["name"], arguments, token)
        if status != 200:
            return f"MCP invalid-argument call for {tool['name']} returned HTTP {status}, expected HTTP 200"
        failure = _valid_error_result(response)
        if failure:
            return f"MCP tool {tool['name']}: {failure}"

    if secured:
        diagnostics_status, diagnostics_body = mcp_call(
            project_id, 2090, "kb-labs__mcp_diagnostics", {}, "",
        )
        if diagnostics_status != 401:
            return f"secured diagnostics call without authorization returned HTTP {diagnostics_status}, expected 401"
        if not _matches_machine_result(diagnostics_body):
            return "secured diagnostics call did not return the shared ux-result error envelope"
        diagnostics_error = diagnostics_body["error"]
        if diagnostics_error.get("code") != "KB_AUTH_REQUIRED" or not diagnostics_error.get("hint") or not diagnostics_error.get("correlationId"):
            return "secured diagnostics call returned an incomplete KB_AUTH_REQUIRED envelope"

        no_token_status, no_token_body, _ = _request(
            f"/api/v1/projects/{project_id}/mcp", method="POST", token="",
            accept="application/json, text/event-stream",
            payload={"jsonrpc": "2.0", "id": 2091, "method": "tools/list", "params": {}},
        )
        if no_token_status != 401:
            return f"secured MCP request without a token returned HTTP {no_token_status}, expected 401"
        if not _matches_machine_result(no_token_body):
            return "secured MCP request without a token did not return the shared ux-result error envelope"
        no_token_error = no_token_body["error"]
        if no_token_error.get("code") != "KB_AUTH_REQUIRED" or not no_token_error.get("hint") or not no_token_error.get("correlationId"):
            return "secured MCP request without a token returned an incomplete KB_AUTH_REQUIRED envelope"
    return None


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
    machine_result = result.get("structuredContent") if isinstance(result, dict) else None
    if not isinstance(result, dict) or result.get("isError") is not True:
        return "MCP call did not return an error tool result"
    if not _matches_machine_result(machine_result):
        return "MCP structuredContent does not match the shared machine-result contract"
    envelope = machine_result["error"]
    required = {"code", "area", "stage", "severity", "retryable", "message", "hint", "correlationId"}
    if not required <= envelope.keys():
        return "MCP structuredContent does not contain the full shared error envelope"
    if envelope["code"] != expected_code:
        return f"MCP tool error code was {envelope['code']!r}, expected {expected_code}"
    text_lines = [item.get("text", "") for item in result.get("content", []) if isinstance(item, dict)]
    expected_text = f"{envelope['code']}: {envelope['message']} — {envelope['hint']} (correlationId: {envelope['correlationId']})"
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
    envelope = result["structuredContent"]["error"]
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
        machine_result = body if isinstance(body, dict) else None
        envelope = machine_result.get("error") if isinstance(machine_result, dict) else None
        if (not isinstance(envelope, dict)
                or machine_result.get("ok") is not False
                or envelope.get("code") != "KB_AUTH_REQUIRED"
                or envelope.get("hint") != 'Run "kb auth login".'
                or not isinstance(envelope.get("correlationId"), str)
                or not envelope["correlationId"].strip()):
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
