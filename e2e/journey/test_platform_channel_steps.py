from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from platform_channel_steps import decode_canary_apply_result, run_platform_channel_steps
from test_install_layout import create_test_install_container
from test_catalog_exit_status import catalogued_exit

def apply_document(record: dict) -> str:
    return json.dumps(record)


class PlatformChannelStepsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="journey-channel-test-")
        self.root = Path(self.temp.name)
        self.active_root = create_test_install_container(self.root)
        self.receipt = self.active_root / ".kb/v2/receipt.json"
        self.receipt.parent.mkdir(parents=True)
        self._write_receipt("3.0.0", "stable")
        self.request = self.root / "request.json"
        self.request.write_text(json.dumps({"platformRoot": str(self.root)}) + "\n")
        self.fresh_root = self.root / "fresh"
        self.commands: list[list[str]] = []
        self.fail_canary_preflight = False
        self.record_apply_refusal = False

    def tearDown(self):
        self.temp.cleanup()

    def _write_receipt(self, version: str, channel: str):
        self.receipt.write_text(json.dumps({"plan": {"platformChannel": channel,
                                                       "serviceGraph": {"platformVersion": version}}}))

    def fake_kb(self, args: list[str]) -> subprocess.CompletedProcess:
        self.commands.append(args)
        if args[1] == "plan":
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"plan": {
                "request": {"policy": "strict"}, "serviceGraph": {"platformVersion": "3.0.0"},
                "artifacts": [{"kind": "sdk", "version": "3.0.0"}]}}}), "")
        if args[1] in {"stop", "start", "uninstall"}:
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {}}), "")
        if args[1] == "apply":
            if self.record_apply_refusal:
                fixture = Path(__file__).parent / "testdata/gateway-index-plan-refusal.json"
                return subprocess.CompletedProcess(args, 1, fixture.read_text(), "")
            if self.fail_canary_preflight:
                return subprocess.CompletedProcess(args, 1, apply_document({"ok": False, "error": {
                    "code": "KB_INSTALL_MEMORY_INSUFFICIENT",
                    "message": "The platform install needs at least 1228.80 MiB of available memory; 1200.00 MiB is available.",
                    "hint": "Close memory-heavy applications or increase the machine memory, then run `kb-create install` again.",
                    "meta": {"minimum": "1228.80 MiB", "available": "1200.00 MiB"}, "correlationId": "c-memory",
                }}), "")
            fresh_active_root = create_test_install_container(self.fresh_root)
            (fresh_active_root / ".kb/v2").mkdir(parents=True, exist_ok=True)
            plan = {"platformChannel": "canary", "serviceGraph": {"platformVersion": "3.0.2-canary.1"},
                    "artifacts": [{"kind": "platform", "version": "3.0.2-canary.1", "sha256": "platform"},
                                  {"kind": "sdk", "version": "3.0.2-canary.1", "sha256": "sdk"}]}
            (fresh_active_root / ".kb/v2/receipt.json").write_text(json.dumps({"plan": plan}))
            result = {"ok": True, "data": {"receipt": {"plan": plan}}}
            return subprocess.CompletedProcess(args, 0, apply_document(result), "")
        if args[1] == "update" and str(self.root / "canary-index.json") in args:
            self._write_receipt("3.0.2-canary.1", "canary")
            if "--json" not in args:
                return subprocess.CompletedProcess(args, 0, "Platform update completed.\n", "")
            output = {"ok": True, "data": {"hostRestarted": True, "receipt": {"plan": {
                "platformChannel": "canary", "serviceGraph": {"platformVersion": "3.0.2-canary.1"}}}}}
            return subprocess.CompletedProcess(args, 0, json.dumps(output), "")
        if args[1] == "update":
            output = {"ok": False, "error": {"code": "KB_UPDATE_DOWNGRADE_REFUSED",
                                               "message": "The requested update is older than the installed platform.",
                                               "hint": "use `kb-create rollback`",
                                               "meta": {"installedVersion": "3.0.2-canary.1", "targetVersion": "3.0.0"}, "correlationId": "c-down"}}
            return subprocess.CompletedProcess(args, 2, json.dumps(output), "")
        if args[1] in {"status", "doctor"} and "--json" in args:
            output = {"ok": True, "data": {"platformChannel": "canary", "platformVersion": "3.0.2-canary.1"}}
            if args[1] == "status":
                output["data"]["installation"] = {"platformChannel": "canary", "platformVersion": "3.0.2-canary.1"}
            return subprocess.CompletedProcess(args, 0, json.dumps(output), "")
        if args[1] in {"status", "doctor"}:
            return subprocess.CompletedProcess(args, 0, "Platform channel: canary\nPlatform version: 3.0.2-canary.1\n", "")
        if args[1] == "rollback":
            self._write_receipt("3.0.0", "stable")
            if "--json" not in args:
                return subprocess.CompletedProcess(args, 0, "Platform rollback completed.\n", "")
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"hostRestarted": True}}), "")
        return subprocess.CompletedProcess(args, 2, "unexpected fake command", "")

    def test_canary_update_status_downgrade_refusal_and_rollback_claim_steps(self):
        result = run_platform_channel_steps(
            mode="local", launcher="kb-create", platform_root=self.root,
            stable_index=self.root / "stable-index.json", canary_index=self.root / "canary-index.json",
            request_file=self.request, stable_version="3.0.0", canary_version="3.0.2-canary.1",
            fresh_root=self.fresh_root, project_root=self.root / "project",
            run_command=self.fake_kb,
        ).result()
        self.assertEqual(result.status, "PASS")
        self.assertEqual({claim["claimId"] for claim in result.case_run["claims"]},
                         {f"S-052.{step}" for step in range(1, 7)} | {"S-053.1", "S-203.10"})
        self.assertEqual([command[1] for command in self.commands], [
            "plan", "stop", "apply", "status", "doctor", "status", "doctor", "uninstall", "start",
            "update", "update", "status", "doctor", "status", "doctor", "update", "rollback", "rollback",
        ])
        self.assertIn("--platform-channel", self.commands[2])
        self.assertIn("--yes", self.commands[-1])
        self.assertNotIn("--json", self.commands[9])
        self.assertIn("--json", self.commands[10])
        self.assertNotIn("--json", self.commands[-2])
        self.assertIn("--json", self.commands[-1])

    def test_insufficient_canary_preflight_blocks_dependent_claims_with_resource_reason(self):
        self.fail_canary_preflight = True
        result = run_platform_channel_steps(
            mode="local", launcher="kb-create", platform_root=self.root,
            stable_index=self.root / "stable-index.json", canary_index=self.root / "canary-index.json",
            request_file=self.request, stable_version="3.0.0", canary_version="3.0.2-canary.1",
            fresh_root=self.fresh_root, project_root=self.root / "project",
            run_command=self.fake_kb,
        ).result()
        steps = {step["name"]: step for step in result.steps}
        self.assertEqual(result.status, "BLOCKED")
        self.assertIsNone(result.first_failure)
        self.assertEqual(steps["apply a fresh installation from the sealed canary channel"]["status"], "BLOCKED")
        self.assertIn("1200.00 MiB is available", steps["apply a fresh installation from the sealed canary channel"]["message"])
        self.assertIn("1228.80 MiB available memory", steps["apply a fresh installation from the sealed canary channel"]["message"])
        self.assertEqual(steps["verify canary channel and version in receipt, status, and doctor"]["status"], "BLOCKED")
        self.assertIn("canary install preflight requires", steps["verify canary channel and version in receipt, status, and doctor"]["message"])
        claims = {claim["claimId"]: claim["status"] for claim in result.case_run["claims"]}
        self.assertNotIn("S-052.2", claims)
        self.assertEqual([command[1] for command in self.commands], ["plan", "stop", "apply", "start"])

    def test_s053_canary_apply_refusal_reads_recorded_json_document(self):
        fixture = Path(__file__).parent / "testdata/gateway-index-plan-refusal.json"
        command = subprocess.CompletedProcess(["kb-create", "apply", "--json"], 1, fixture.read_text(), "")
        error = decode_canary_apply_result(command)
        self.assertEqual(error["code"], "KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE")
        self.assertEqual(command.returncode, catalogued_exit(error["code"]))
        self.assertIn("rm", error["hint"])
        self.assertIn("doctor", error["hint"])

    def test_s053_canary_step_decodes_recorded_document_refusal(self):
        self.record_apply_refusal = True
        result = run_platform_channel_steps(
            mode="local", launcher="kb-create", platform_root=self.root,
            stable_index=self.root / "stable-index.json", canary_index=self.root / "canary-index.json",
            request_file=self.request, stable_version="3.0.0", canary_version="3.0.2-canary.1",
            fresh_root=self.fresh_root, project_root=self.root / "project",
            run_command=self.fake_kb,
        ).result()
        apply_step = next(step for step in result.steps
                          if step["name"] == "apply a fresh installation from the sealed canary channel")
        self.assertEqual(apply_step["status"], "FAIL")
        self.assertIn("KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE", apply_step["message"])
        self.assertNotIn("CLI response is not JSON", apply_step["message"])


if __name__ == "__main__":
    unittest.main()
