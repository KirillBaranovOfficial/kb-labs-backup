import os
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from gateway_steps import run_gateway_steps, run_llm_provider_steps
from service_steps import Journey
from test_install_layout import create_test_install_container


class FakeResponse:
    status = 404
    headers = {}

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return b'{"ok":false,"error":{"code":"KB_RUNTIME_UNEXPECTED","message":"fixture route unavailable"}}'


class JsonResponse:
    headers = {}

    def __init__(self, status, body):
        self.status = status
        self.body = body if isinstance(body, bytes) else body.encode()

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return self.body


class GatewayStepTests(unittest.TestCase):
    def setUp(self):
        self.platform_temp = tempfile.TemporaryDirectory(prefix="journey-gateway-container-")
        self.platform = Path(self.platform_temp.name) / "platform"
        create_test_install_container(self.platform)
        self.env_patcher = patch.dict(os.environ, {"KB_JOURNEY_PLATFORM_ROOT": str(self.platform)}, clear=False)
        self.env_patcher.start()
        self.addCleanup(self.env_patcher.stop)
        self.addCleanup(self.platform_temp.cleanup)

    def test_stream_failure_claim_requires_typed_error_and_healthy_gateway(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_JOURNEY_PROJECT_ROOT": directory,
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_PLATFORM_ROOT": str(self.platform),
            "OPENAI_API_KEY": "fake-secret",
        }, clear=False):
            def failed(command, **_kwargs):
                return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {"code": "KB_RUNTIME_UNEXPECTED", "message": "fixture failed"}}), "")

            def opener(request, **_kwargs):
                path = request.full_url.split("gateway.test", 1)[-1]
                args = json.loads(request.data).get("args") if path == "/platform/v1/llm/stream" else None
                if path == "/platform/v1/llm/stream" and args == ["journey-provider-401"]:
                    return JsonResponse(502, json.dumps({"ok": False, "error": {
                        "code": "KB_CONFIG_LLM_KEY_INVALID", "message": "bad key",
                        "hint": "kb config set --scope platform platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}'",
                        "correlationId": "req-test",
                    }}))
                if path == "/platform/v1/llm/stream" and args == ["journey-provider-stream-error"]:
                    return JsonResponse(200, 'data: partial\n\nevent: error\ndata: {"ok":false,"error":{"code":"KB_CONFIG_LLM_PROVIDER_UNAVAILABLE","message":"provider failed","hint":"kb config get platform.adapterOptions.llm.baseURL --scope platform --json","correlationId":"req-test"}}\n\n')
                if path == "/platform/v1/llm/stream" and args == []:
                    return JsonResponse(400, json.dumps({"ok": False, "error": {
                        "code": "KB_RUNTIME_INPUT_INVALID", "message": "prompt required",
                        "hint": "Correct input", "correlationId": "req-test",
                    }}))
                if path == "/health":
                    return JsonResponse(200, '{"ok":true,"data":{}}')
                if path == "/platform/v1/llm/stream":
                    return JsonResponse(200, "data: [DONE]\n\n")
                return JsonResponse(200, '{"ok":true,"data":{}}')

            journey = Journey("local", "stream-error-healthy")
            run_gateway_steps(journey, token="fixture-token", runner=failed, opener=opener)

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses["S-189.5"], "PASS")

    def test_provider_steps_require_deterministic_content_in_the_json_result(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_LLM_MOCK_BASE_URL": "http://mock-llm:8317",
            "KB_JOURNEY_PLATFORM_ROOT": str(self.platform),
            "OPENAI_API_KEY": "fake-openai-key",
        }, clear=False):
            calls = []
            def successful(command, **_kwargs):
                calls.append(command)
                if command[:3] == ["kb", "marketplace", "install"]:
                    output = '{"ok":true,"data":{"package":"@kb-labs/adapters-vibeproxy"}}'
                elif command[1:3] == ["config", "get"]:
                    output = '{"ok":true,"data":{"value":"${OPENAI_API_KEY}"}}'
                elif command[1:3] == ["config", "set"] and any("baseURL" in arg for arg in command):
                    platform_root = str(self.platform.resolve())
                    output = json.dumps({
                        "ok": True,
                        "data": {
                            "file": str((self.platform / "current").resolve() / ".kb/kb.config.jsonc"),
                            "nextCommand": f"kb-create restart --platform-root {json.dumps(platform_root)}",
                        },
                    })
                elif command[:2] == ["kb-create", "restart"]:
                    output = '{"ok":true,"data":{}}'
                elif command[1:4] == ["journey", "llm", "complete"]:
                    marker = "VIBEPROXY_MOCK_RESPONSE" if any("vibeproxy" in arg for arg in command) else "OPENAI_MOCK_RESPONSE"
                    stop_reason = "end_turn" if marker == "VIBEPROXY_MOCK_RESPONSE" else None
                    output = json.dumps({"ok": True, "data": {"result": {
                        "content": marker, "model": "journey-mock-model", "stopReason": stop_reason,
                    }}})
                else:
                    output = '{"ok":true,"data":{}}'
                return subprocess.CompletedProcess(command, 0, output, "")

            journey = Journey("local", "provider-green")
            run_llm_provider_steps(journey, runner=successful)

        claims = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(claims, {
            "S-017.1": "PASS", "S-017.2": "PASS", "S-017.3": "PASS", "S-017.4": "PASS",
            "S-017.5": "PASS",
            "S-018.1": "PASS", "S-018.2": "PASS", "S-018.3": "PASS",
        })
        install_index = next(i for i, call in enumerate(calls) if call[:3] == ["kb", "marketplace", "install"])
        bind_index = next(i for i, call in enumerate(calls) if "@kb-labs/adapters-vibeproxy" in call
                          and "platform.adapters.llm" in call)
        self.assertLess(install_index, bind_index)

    def test_failed_vibeproxy_install_blocks_s018_3_without_cascade(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_LLM_MOCK_BASE_URL": "http://mock-llm:8317",
            "OPENAI_API_KEY": "fake-openai-key",
        }, clear=False):
            calls = []

            def install_fails(command, **_kwargs):
                calls.append(command)
                if command[:3] == ["kb", "marketplace", "install"]:
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_MARKETPLACE_PACKAGE_NOT_FOUND", "message": "VibeProxy package unavailable",
                    }}), "")
                return subprocess.CompletedProcess(command, 0, '{"ok":true,"data":{}}', "")

            journey = Journey("local", "provider-install-fails")
            run_llm_provider_steps(journey, runner=install_fails)

        claims = {item["claimId"]: item for item in journey.result().case_run["claims"]}
        self.assertEqual(claims["S-018.3"]["status"], "FAIL")
        step = next(item for item in journey.result().steps
                    if item["name"] == "switch to VibeProxy messages protocol in both projects")
        self.assertIn("prerequisite not met: VibeProxy package unavailable", step["message"])
        self.assertFalse(any("@kb-labs/adapters-vibeproxy" in call
                             and call[:3] != ["kb", "marketplace", "install"] for call in calls))

    def test_provider_steps_reject_success_exit_with_wrong_provider_content(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_LLM_MOCK_BASE_URL": "http://mock-llm:8317",
            "OPENAI_API_KEY": "fake-openai-key",
        }, clear=False):
            def wrong_provider(command, **_kwargs):
                output = ('{"ok":true,"value":"${OPENAI_API_KEY}"}' if command[1:3] == ["config", "get"]
                          else '{"ok":true,"data":{"result":{"content":"cached response from another provider"}}}'
                          if command[1:4] == ["journey", "llm", "complete"] else '{"ok":true,"data":{}}')
                return subprocess.CompletedProcess(command, 0, output, "")

            journey = Journey("local", "provider-red")
            run_llm_provider_steps(journey, runner=wrong_provider)

        claims = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(claims["S-017.3"], "FAIL")
        self.assertEqual(claims["S-018.2"], "FAIL")
        self.assertEqual(claims["S-018.3"], "FAIL")

    def test_vibeproxy_claim_requires_messages_api_stop_reason_in_json_result(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_LLM_MOCK_BASE_URL": "http://mock-llm:8317",
            "OPENAI_API_KEY": "fake-openai-key",
        }, clear=False):
            def missing_message_shape(command, **_kwargs):
                if command[1:3] == ["config", "get"]:
                    output = '{"ok":true,"data":{"value":"${OPENAI_API_KEY}"}}'
                elif command[1:4] == ["journey", "llm", "complete"]:
                    marker = "VIBEPROXY_MOCK_RESPONSE" if any("vibeproxy" in arg for arg in command) else "OPENAI_MOCK_RESPONSE"
                    output = json.dumps({"ok": True, "data": {"result": {
                        "content": marker, "model": "journey-mock-model", "stopReason": None,
                    }}})
                else:
                    output = '{"ok":true,"data":{}}'
                return subprocess.CompletedProcess(command, 0, output, "")

            journey = Journey("local", "provider-wrong-shape")
            run_llm_provider_steps(journey, runner=missing_message_shape)

        claims = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(claims["S-018.3"], "FAIL")

    def test_fake_cli_and_http_failures_make_every_gateway_claim_red(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_BASE_URL": "http://gateway.test",
            "KB_JOURNEY_PROJECT_ROOT": directory,
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "OPENAI_API_KEY": "fake-secret",
        }, clear=False):
            def failed(command, **_kwargs):
                return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {"code": "KB_RUNTIME_UNEXPECTED", "message": "fixture failed"}}), "")

            journey = Journey("local", "fake-gateway")
            run_gateway_steps(journey, token="fixture-token", runner=failed,
                              opener=lambda *_args, **_kwargs: FakeResponse())

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses, {
            "S-017.1": "FAIL", "S-017.2": "FAIL", "S-017.3": "FAIL", "S-017.4": "FAIL",
            "S-018.1": "FAIL", "S-018.2": "FAIL", "S-018.3": "FAIL",
            "S-189.1": "FAIL", "S-189.2": "FAIL", "S-189.3": "FAIL", "S-189.4": "FAIL", "S-189.5": "FAIL", "S-189.6": "FAIL",
            "S-017.5": "FAIL",
            "S-190.1": "FAIL", "S-190.2": "FAIL",
        })


if __name__ == "__main__":
    unittest.main()
