"""Fake-transport unit coverage for S-045 and S-140 MCP journey claims."""
from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from mcp_project_steps import (apply_commit_plan, commit_tool_schema, invalid_arguments, local_auth,
                               project_isolation, secured_auth, unknown_tool)


def tool_error(code: str, message: str, hint: str):
    envelope = {
        "code": code,
        "area": "project",
        "stage": "run",
        "severity": "error",
        "retryable": False,
        "message": message,
        "hint": hint,
    }
    return {"result": {
        "isError": True,
        "structuredContent": envelope,
        "content": [{"type": "text", "text": f"{code}: {message} — {hint}"}],
    }}


class ProjectMcpJourneyTests(unittest.TestCase):
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
            return (401, {"error": "unauthorized"}, {})

        with patch("service_runner.request", side_effect=fake_request):
            self.assertIsNone(secured_auth("prj_a"))
        self.assertEqual(calls, ["", "not-a-real-token"])

    def test_secured_auth_fails_if_either_request_authenticates(self):
        responses = iter([(401, {}, {}), (200, {"result": {"tools": []}}, {})])
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
             patch("mcp_project_steps.subprocess.run", return_value=history) as git_log:
            self.assertIsNone(apply_commit_plan("prj_a", "token", "/tmp/project-a"))
        payload = request.call_args.kwargs["payload"]
        self.assertEqual(payload["params"]["name"], "kb-labs-commit__commit_apply")
        self.assertEqual(payload["params"]["arguments"], {})
        self.assertEqual(git_log.call_args.args[0][-1], "--all")

    def test_project_isolation_checks_catalog_then_typed_unknown_call(self):
        response = tool_error("KB_RUNTIME_MCP_UNKNOWN_TOOL", "Unknown MCP tool", "Call tools/list to discover tools.")
        with patch("service_runner.request", side_effect=[
            (200, {"result": {"tools": [{"name": "workflow__workflow_run"}]}}, {}),
            (200, response, {}),
        ]) as request:
            self.assertIsNone(project_isolation("prj_a", "prj_b", "token"))
        self.assertEqual(request.call_count, 2)

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
