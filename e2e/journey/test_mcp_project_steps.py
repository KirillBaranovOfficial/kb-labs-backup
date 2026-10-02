"""Fake-transport unit coverage for S-045 and S-140 MCP journey claims."""
from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from mcp_project_steps import (apply_commit_plan, commit_tool_schema, invalid_arguments, local_auth,
                               mcp_ux_result_contract, project_isolation, secured_auth, unknown_tool)
from service_runner import _case_claims, checks


def tool_error(code: str, message: str, hint: str):
    envelope = {
        "code": code,
        "area": "project",
        "stage": "run",
        "severity": "error",
        "retryable": False,
        "message": message,
        "hint": hint,
        "correlationId": "corr-test",
    }
    machine_result = {"ok": False, "error": envelope}
    return {"result": {
        "isError": True,
        "structuredContent": machine_result,
        "content": [{"type": "text", "text": f"{code}: {message} — {hint} (correlationId: corr-test)"}],
    }}


class ProjectMcpJourneyTests(unittest.TestCase):
    def test_live_registry_ux_step_is_registered_as_s203_claim(self):
        step = "MCP UX result contract from live registry"
        self.assertIn(step, {name for name, _, _ in checks("local")})
        self.assertEqual(_case_claims(step), ["S-203.2", "S-203.6"])

    def test_ux_contract_claim_discovers_and_checks_every_live_registry_tool(self):
        tools = [
            {"name": "plugin__required", "inputSchema": {"type": "object", "required": ["name"]}},
            {"name": "plugin__no_inputs", "inputSchema": {"type": "object", "properties": {}}},
            {"name": "kb-labs__mcp_diagnostics", "inputSchema": {"type": "object", "properties": {}}},
        ]
        failure = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Invalid arguments", "Pass valid arguments.")
        calls = []

        def fake_request(path, **kwargs):
            payload = kwargs.get("payload", {})
            calls.append((payload.get("method"), payload.get("params", {}).get("name"), payload.get("params", {}).get("arguments"), kwargs.get("token")))
            if payload.get("method") == "tools/list":
                return 200, {"result": {"tools": tools}}, {}
            return 200, failure, {}

        with patch("service_runner.request", side_effect=fake_request):
            self.assertIsNone(mcp_ux_result_contract("prj_a", "token"))
        self.assertEqual([call[1] for call in calls[1:]], [tool["name"] for tool in tools])
        self.assertEqual(calls[1][2], {})
        self.assertEqual(calls[2][2], {"__ux_invalid_probe__": True})
        self.assertTrue(all(call[3] == "token" for call in calls))

    def test_ux_contract_claim_checks_secured_diagnostics_and_missing_token_envelopes(self):
        tools = [{"name": "plugin__read", "inputSchema": {"type": "object", "required": ["id"]}}]
        failure = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Invalid arguments", "Pass valid arguments.")
        unauthorized = {"ok": False, "error": {
            "code": "KB_AUTH_REQUIRED", "area": "auth", "stage": "login", "severity": "error",
            "retryable": False, "message": "Authentication is required.",
            "hint": 'Run "kb auth login --gateway-url <url> --email <email> --password <password>".', "correlationId": "corr-auth",
        }}
        calls = []

        def fake_request(path, **kwargs):
            payload = kwargs.get("payload", {})
            call = (payload.get("method"), payload.get("params", {}).get("name"), kwargs.get("token"))
            calls.append(call)
            if payload.get("method") == "tools/list":
                if kwargs.get("token"):
                    return 200, {"result": {"tools": tools}}, {}
                return 401, unauthorized, {}
            if kwargs.get("token"):
                return 200, failure, {}
            return 401, unauthorized, {}

        with patch("service_runner.request", side_effect=fake_request):
            self.assertIsNone(mcp_ux_result_contract("prj_a", "token", secured=True))
        self.assertEqual(calls[2], ("tools/call", "kb-labs__mcp_diagnostics", ""))
        self.assertEqual(calls[3], ("tools/list", None, ""))

    def test_ux_contract_claim_rejects_stack_text_and_missing_correlation_id(self):
        tools = [{"name": "plugin__read", "inputSchema": {"type": "object", "required": ["id"]}}]
        failure = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Invalid arguments", "Pass valid arguments.")
        failure["result"]["content"][0]["text"] += "\nTraceback (most recent call last):"
        with patch("service_runner.request", side_effect=[
            (200, {"result": {"tools": tools}}, {}),
            (200, failure, {}),
        ]):
            self.assertIn("stack trace", mcp_ux_result_contract("prj_a", "token"))

    def test_ux_contract_claim_rejects_error_without_correlation_id(self):
        tools = [{"name": "plugin__read", "inputSchema": {"type": "object", "required": ["id"]}}]
        failure = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Invalid arguments", "Pass valid arguments.")
        del failure["result"]["structuredContent"]["error"]["correlationId"]
        with patch("service_runner.request", side_effect=[
            (200, {"result": {"tools": tools}}, {}),
            (200, failure, {}),
        ]):
            self.assertIn("correlationId", mcp_ux_result_contract("prj_a", "token"))

    def test_unknown_tool_requires_typed_result_and_discovery_hint(self):
        response = tool_error("KB_RUNTIME_MCP_UNKNOWN_TOOL", "Unknown MCP tool", "Call tools/list to discover tools.")
        with patch("service_runner.request", return_value=(200, response, {})):
            self.assertIsNone(unknown_tool("prj_a", "token"))

    def test_unknown_tool_rejects_plain_text_error(self):
        response = {"result": {"isError": True, "content": [{"text": "Unknown tool: absent"}]}}
        with patch("service_runner.request", return_value=(200, response, {})):
            self.assertIn("structuredContent", unknown_tool("prj_a", "token"))

    def test_invalid_arguments_sends_bad_type_and_checks_field_in_envelope(self):
        response = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Argument force must be boolean", "Pass a boolean value.")
        with patch("service_runner.request", return_value=(200, response, {})) as request:
            self.assertIsNone(invalid_arguments("prj_a", "token"))
        self.assertEqual(request.call_args.kwargs["payload"]["params"]["arguments"], {"force": "yes"})

    def test_invalid_arguments_rejects_missing_argument_name(self):
        response = tool_error("KB_RUNTIME_MCP_INVALID_ARGUMENTS", "Argument has the wrong type", "Pass a valid value.")
        with patch("service_runner.request", return_value=(200, response, {})):
            self.assertIn("did not identify argument", invalid_arguments("prj_a", "token"))

    def test_secured_auth_claim_checks_missing_and_invalid_tokens(self):
        calls = []

        def fake_request(path, **kwargs):
            calls.append(kwargs.get("token"))
            return (401, {"ok": False, "error": {
                "code": "KB_AUTH_REQUIRED", "message": "Authentication is required.",
                "area": "auth", "stage": "login", "severity": "error", "retryable": False,
                "hint": 'Run "kb auth login --gateway-url <url> --email <email> --password <password>".', "correlationId": "corr-auth",
            }}, {})

        with patch("service_runner.request", side_effect=fake_request):
            self.assertIsNone(secured_auth("prj_a"))
        self.assertEqual(calls, ["", "not-a-real-token"])

    def test_secured_auth_rejects_http_401_without_the_catalog_envelope(self):
        with patch("service_runner.request", return_value=(401, {"ok": False, "error": "unauthorized"}, {})):
            self.assertIn("no KB_AUTH_REQUIRED envelope", secured_auth("prj_a"))

    def test_secured_auth_fails_if_either_request_authenticates(self):
        responses = iter([
            (401, {"ok": False, "error": {
                "code": "KB_AUTH_REQUIRED", "message": "Authentication is required.",
                "area": "auth", "stage": "login", "severity": "error", "retryable": False,
                "hint": 'Run "kb auth login --gateway-url <url> --email <email> --password <password>".', "correlationId": "corr-auth",
            }}, {}),
            (200, {"result": {"tools": []}}, {}),
        ])
        with patch("service_runner.request", side_effect=lambda *_args, **_kwargs: next(responses)):
            self.assertIn("invalid token returned HTTP 200", secured_auth("prj_a"))

    def test_local_auth_lists_tools_without_a_token(self):
        tools = [
            {"name": name}
            for name in (
                "kb-labs-commit__commit",
                "kb-labs-workflow__workflow_run",
                "kb-labs__mcp_diagnostics",
            )
        ]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})) as request:
            self.assertIsNone(local_auth("prj_a"))
        self.assertEqual(request.call_args.kwargs["token"], "")

    def test_local_auth_rejects_a_catalog_from_another_project(self):
        tools = [{"name": "kb-labs__mcp_diagnostics"}]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})):
            self.assertIn("omitted selected project tools", local_auth("prj_a"))

    def test_commit_tool_schema_checks_the_manifest_argument_type(self):
        tools = [{"name": "kb-labs-commit__commit_apply", "inputSchema": {
            "type": "object", "properties": {"force": {"type": "boolean"}, "json": {"type": "boolean"}},
        }}]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})):
            self.assertIsNone(commit_tool_schema("prj_a", "token"))

    def test_commit_tool_schema_reports_a_missing_or_wrong_schema(self):
        tools = [{"name": "kb-labs-commit__commit_apply", "inputSchema": {"type": "object", "properties": {}}}]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})):
            self.assertIn("force and json", commit_tool_schema("prj_a", "token"))

    def test_mutating_step_calls_commit_apply_without_push_and_checks_sha(self):
        response = {"result": {"isError": False, "content": [{"text": "Created commit a1b2c3d4"}]}}
        history = subprocess.CompletedProcess(args=[], returncode=0, stdout="a1b2c3d4\n", stderr="")
        with patch("service_runner.request", return_value=(200, response, {})) as request, \
             patch("mcp_project_steps.run_command", return_value=history) as git_log:
            self.assertIsNone(apply_commit_plan("prj_a", "token", "/tmp/project-a"))
        payload = request.call_args.kwargs["payload"]
        self.assertEqual(payload["params"]["name"], "kb-labs-commit__commit_apply")
        self.assertEqual(payload["params"]["arguments"], {})
        self.assertEqual(git_log.call_args.args[0][-1], "--all")

    def test_project_isolation_checks_catalog_then_typed_unknown_call(self):
        response = tool_error("KB_RUNTIME_MCP_UNKNOWN_TOOL", "Unknown MCP tool", "Call tools/list to discover tools.")
        with tempfile.TemporaryDirectory() as directory, patch("service_runner.request", side_effect=[
            (200, {"result": {"tools": [{"name": "workflow__workflow_run"}]}}, {}),
            (200, response, {}),
        ]) as request:
            self.assertIsNone(project_isolation("prj_a", "prj_b", str(Path(directory).resolve()), "token"))
        self.assertEqual(request.call_count, 2)

    def test_project_isolation_requires_the_registered_canonical_root(self):
        with tempfile.TemporaryDirectory() as directory:
            alias = str(Path(directory) / "missing")
            failure = project_isolation("prj_a", "prj_b", alias, "token")
        self.assertEqual(failure, "project B registry root must be an existing canonical directory")

    def test_sse_error_response_is_parsed(self):
        from mcp_project_steps import mcp_call
        payload = tool_error("KB_RUNTIME_MCP_UNKNOWN_TOOL", "Unknown MCP tool", "Call tools/list.")
        event = "event: message\ndata: " + json.dumps(payload) + "\n\n"
        with patch("service_runner.request", return_value=(200, event, {})):
            status, body = mcp_call("prj_a", 4, "missing", {})
        self.assertEqual(status, 200)
        self.assertEqual(body, payload)


if __name__ == "__main__":
    unittest.main()
