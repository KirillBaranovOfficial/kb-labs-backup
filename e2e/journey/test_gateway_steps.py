import os
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from gateway_steps import run_gateway_steps, run_llm_provider_steps
from service_steps import Journey


class FakeResponse:
    status = 404
    headers = {}

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return b'{"error":"fixture route unavailable"}'


class GatewayStepTests(unittest.TestCase):
    def test_provider_steps_require_deterministic_content_in_the_json_result(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
            "KB_JOURNEY_INVOCATIONS": directory,
            "KB_JOURNEY_PROJECT_ROOT": "/fixture/project-a",
            "KB_JOURNEY_PROJECT_B_ROOT": "/fixture/project-b",
            "KB_JOURNEY_PROJECT_ID": "project-a",
            "KB_JOURNEY_PROJECT_B_ID": "project-b",
            "KB_JOURNEY_LLM_MOCK_BASE_URL": "http://mock-llm:8317",
            "OPENAI_API_KEY": "fake-openai-key",
        }, clear=False):
            def successful(command, **_kwargs):
                if command[1:3] == ["config", "get"]:
                    output = '{"ok":true,"value":"${OPENAI_API_KEY}"}'
                elif command[1:4] == ["journey", "llm", "complete"]:
                    marker = "VIBEPROXY_MOCK_RESPONSE" if any("vibeproxy" in arg for arg in command) else "OPENAI_MOCK_RESPONSE"
                    stop_reason = "end_turn" if marker == "VIBEPROXY_MOCK_RESPONSE" else None
                    output = json.dumps({"ok": True, "result": {
                        "content": marker, "model": "journey-mock-model", "stopReason": stop_reason,
                    }})
                else:
                    output = '{"ok":true}'
                return subprocess.CompletedProcess(command, 0, output, "")

            journey = Journey("local", "provider-green")
            run_llm_provider_steps(journey, runner=successful)

        claims = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(claims, {
            "S-017.1": "PASS", "S-017.2": "PASS", "S-017.3": "PASS", "S-017.4": "PASS",
            "S-018.1": "PASS", "S-018.2": "PASS", "S-018.3": "PASS",
        })

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
                          else '{"ok":true,"result":{"content":"cached response from another provider"}}'
                          if command[1:4] == ["journey", "llm", "complete"] else '{"ok":true}')
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
                    output = '{"ok":true,"value":"${OPENAI_API_KEY}"}'
                elif command[1:4] == ["journey", "llm", "complete"]:
                    marker = "VIBEPROXY_MOCK_RESPONSE" if any("vibeproxy" in arg for arg in command) else "OPENAI_MOCK_RESPONSE"
                    output = json.dumps({"ok": True, "result": {
                        "content": marker, "model": "journey-mock-model", "stopReason": None,
                    }})
                else:
                    output = '{"ok":true}'
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
                return subprocess.CompletedProcess(command, 1, "", "fixture failed")

            journey = Journey("local", "fake-gateway")
            run_gateway_steps(journey, token="fixture-token", runner=failed,
                              opener=lambda *_args, **_kwargs: FakeResponse())

        statuses = {item["claimId"]: item["status"] for item in journey.result().case_run["claims"]}
        self.assertEqual(statuses, {
            "S-017.1": "FAIL", "S-017.2": "FAIL", "S-017.3": "FAIL", "S-017.4": "FAIL",
            "S-018.1": "FAIL", "S-018.2": "FAIL", "S-018.3": "FAIL",
            "S-189.1": "FAIL", "S-189.2": "FAIL", "S-189.3": "FAIL", "S-189.4": "FAIL",
            "S-190.1": "FAIL", "S-190.2": "FAIL",
        })


if __name__ == "__main__":
    unittest.main()
