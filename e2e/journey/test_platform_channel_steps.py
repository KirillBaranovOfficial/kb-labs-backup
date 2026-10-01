from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from platform_channel_steps import run_platform_channel_steps


class PlatformChannelStepsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="journey-channel-test-")
        self.root = Path(self.temp.name)
        (self.root / ".kb/v2").mkdir(parents=True)
        self.receipt = self.root / ".kb/v2/receipt.json"
        self._write_receipt("3.0.0", "stable")
        self.request = self.root / "request.json"
        self.request.write_text(json.dumps({"platformRoot": str(self.root)}) + "\n")
        self.fresh_root = self.root / "fresh"
        self.commands: list[list[str]] = []

    def tearDown(self):
        self.temp.cleanup()

    def _write_receipt(self, version: str, channel: str):
        self.receipt.write_text(json.dumps({"plan": {"platformChannel": channel,
                                                       "serviceGraph": {"platformVersion": version}}}))

    def fake_kb(self, args: list[str]) -> subprocess.CompletedProcess:
        self.commands.append(args)
        if args[1] == "plan":
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "plan": {
                "request": {"policy": "strict"}, "serviceGraph": {"platformVersion": "3.0.0"},
                "artifacts": [{"kind": "sdk", "version": "3.0.0"}]}}), "")
        if args[1] in {"stop", "start", "uninstall"}:
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True}), "")
        if args[1] == "apply":
            self.fresh_root.joinpath(".kb/v2").mkdir(parents=True, exist_ok=True)
            plan = {"platformChannel": "canary", "serviceGraph": {"platformVersion": "3.0.2-canary.1"},
                    "artifacts": [{"kind": "platform", "version": "3.0.2-canary.1", "sha256": "platform"},
                                  {"kind": "sdk", "version": "3.0.2-canary.1", "sha256": "sdk"}]}
            self.fresh_root.joinpath(".kb/v2/receipt.json").write_text(json.dumps({"plan": plan}))
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "receipt": {"plan": plan}}), "")
        if args[1] == "update" and str(self.root / "canary-index.json") in args:
            self._write_receipt("3.0.2-canary.1", "canary")
            output = {"ok": True, "hostRestarted": True, "receipt": {"plan": {
                "platformChannel": "canary", "serviceGraph": {"platformVersion": "3.0.2-canary.1"}}}}
            return subprocess.CompletedProcess(args, 0, json.dumps(output), "")
        if args[1] == "update":
            output = {"ok": False, "error": {"code": "KB_UPDATE_DOWNGRADE_REFUSED",
                                               "hint": "use `kb-create rollback`",
                                               "details": {"installedVersion": "3.0.2-canary.1", "targetVersion": "3.0.0"}}}
            return subprocess.CompletedProcess(args, 2, json.dumps(output), "")
        if args[1] in {"status", "doctor"} and "--json" in args:
            output = {"ok": True, "platformChannel": "canary", "platformVersion": "3.0.2-canary.1"}
            if args[1] == "status":
                output["installation"] = {"platformChannel": "canary", "platformVersion": "3.0.2-canary.1"}
            return subprocess.CompletedProcess(args, 0, json.dumps(output), "")
        if args[1] in {"status", "doctor"}:
            return subprocess.CompletedProcess(args, 0, "Platform channel: canary\nPlatform version: 3.0.2-canary.1\n", "")
        if args[1] == "rollback":
            self._write_receipt("3.0.0", "stable")
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "hostRestarted": True}), "")
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
                         {f"S-052.{step}" for step in range(1, 6)} | {"S-053.1"})
        self.assertEqual([command[1] for command in self.commands], [
            "plan", "stop", "apply", "status", "doctor", "status", "doctor", "uninstall", "start",
            "update", "status", "doctor", "status", "doctor", "update", "rollback",
        ])
        self.assertIn("--platform-channel", self.commands[2])
        self.assertIn("--yes", self.commands[-1])


if __name__ == "__main__":
    unittest.main()
