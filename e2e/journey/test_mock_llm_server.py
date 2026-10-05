import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from mock_llm_server import commit_tool_completion, valid_openai_request


class CommitFixtureResponseTests(unittest.TestCase):
    def request(self, messages):
        return {
            "model": "journey-test-model",
            "messages": [{"role": "user", "content": messages}],
            "tools": [{"type": "function", "function": {"name": "generate_commit_plan"}}],
        }

    def plan(self, response):
        self.assertIsNotNone(response)
        call = response["choices"][0]["message"]["tool_calls"][0]
        self.assertEqual(call["function"]["name"], "generate_commit_plan")
        return json.loads(call["function"]["arguments"])

    def test_first_response_requests_diff_for_staged_file(self):
        request = self.request("Staged changes:\n- src/example.ts (IsNewFile: false)")
        request["messages"].insert(0, {"role": "system", "content": "Generate a plan."})

        self.assertTrue(valid_openai_request(request))
        plan = self.plan(commit_tool_completion(request))

        self.assertTrue(plan["needsMoreContext"])
        self.assertEqual(plan["requestedFiles"], ["src/example.ts"])
        self.assertEqual(plan["commits"][0]["message"], "verify deterministic commit fixture response")

    def test_followup_response_returns_deterministic_conventional_commit(self):
        request = self.request("Diff content for requested files:\nsrc/example.ts\n+export const value = 1")

        plan = self.plan(commit_tool_completion(request))

        self.assertFalse(plan["needsMoreContext"])
        self.assertEqual(plan["requestedFiles"], [])
        self.assertEqual(plan["commits"][0]["type"], "test")
        self.assertEqual(plan["commits"][0]["scope"], "journey")
        self.assertEqual(plan["commits"][0]["message"], "verify deterministic commit fixture response")
        self.assertEqual(plan["commits"][0]["files"], ["journey-fixture.ts"])


if __name__ == "__main__":
    unittest.main()
