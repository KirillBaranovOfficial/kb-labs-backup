from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from platform_update_steps import run_platform_update_steps


class PlatformUpdateStepsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="journey-update-test-")
        self.root = Path(self.temp.name)
        (self.root / ".kb/v2").mkdir(parents=True)
        (self.root / ".kb/kb.config.jsonc").write_text('{"stable":true}\n')
        self.receipt = self.root / ".kb/v2/receipt.json"
        self._write_receipt("3.0.0")
        self.commands: list[list[str]] = []
        self.user_checks = 0

    def tearDown(self):
        self.temp.cleanup()

    def _write_receipt(self, version: str):
        self.receipt.write_text(json.dumps({
            "plan": {"serviceGraph": {"platformVersion": version}},
        }))

    def fake_kb(self, args: list[str]) -> subprocess.CompletedProcess:
        self.commands.append(args)
        stdout = ""
        code = 0
        if args[1] == "plan":
            stdout = json.dumps({"ok": True, "plan": {"serviceGraph": {"platformVersion": "3.0.1"}}})
        elif args[1] == "update":
            update_count = sum(command[1] == "update" for command in self.commands)
            self._write_receipt("3.0.1")
            stdout = json.dumps({"ok": True, "receipt": {"plan": {"serviceGraph": {"platformVersion": "3.0.1"}, "planHash": "resolved-plan-hash"}},
                                 "snapshot": {"id": f"snapshot-{update_count}"},
                                 "hostRestarted": True, "logPath": "/logs/update.log"})
        elif args[1] == "rollback":
            if "missing-journey-snapshot" in args:
                stdout = json.dumps({"ok": False, "error": {"code": "KB_UPDATE_NO_SNAPSHOT", "hint": "restore a named snapshot"}})
                code = 1
            else:
                self._write_receipt("3.0.0")
                stdout = json.dumps({"ok": True, "snapshot": {"id": "snapshot-1"},
                                     "hostRestarted": True, "logPath": "/logs/rollback.log"})
        elif args[1] == "status":
            stdout = json.dumps({"ok": True, "running": True, "reachable": True})
        elif args[1] == "doctor":
            stdout = json.dumps({"ok": True, "platform": {"ok": True}})
        elif args[1] == "project":
            stdout = json.dumps({"ok": True, "projects": [], "total": 0})
        else:
            code = 2
            stdout = "unexpected fake command"
        return subprocess.CompletedProcess(args, code, stdout, "")

    def test_update_rollback_claims_all_steps_and_confirms_non_tty_rollback(self):
        def verify_user():
            self.user_checks += 1
            return subprocess.CompletedProcess(["kb", "auth", "login"], 0, "logged in", "")

        result = run_platform_update_steps(
            mode="local", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json",
            request_file=self.root / "request.json", base_version="3.0.0", updated_version="3.0.1",
            run_command=self.fake_kb, verify_user=verify_user,
        )
        report = result.result()
        self.assertEqual(report.status, "PASS")
        claims = {claim["claimId"] for claim in report.case_run["claims"]}
        self.assertEqual(claims, {f"S-023.{step}" for step in range(1, 8)} | {f"S-024.{step}" for step in (1, 2, 3, 4, 5)})
        self.assertEqual([command[1] for command in self.commands], [
            "status", "plan", "update", "status", "project", "update", "rollback", "rollback", "status", "project", "doctor",
        ])
        for operation in ("plan", "update"):
            command = next(args for args in self.commands if args[1] == operation)
            self.assertTrue({"--json", "--index", "--input"}.issubset(command))
        rollback = next(args for args in self.commands if args[1] == "rollback" and "missing-journey-snapshot" not in args)
        self.assertIn("--yes", rollback)
        self.assertIn("snapshot-1", rollback)
        self.assertEqual(self.user_checks, 3)

    def test_failed_update_reports_the_first_failed_case_step(self):
        def broken(args: list[str]) -> subprocess.CompletedProcess:
            if args[1] == "plan":
                return subprocess.CompletedProcess(args, 2, "invalid request", "")
            return self.fake_kb(args)

        result = run_platform_update_steps(
            mode="secured", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json",
            request_file=self.root / "request.json", base_version="3.0.0", updated_version="3.0.1",
            run_command=broken,
        )
        report = result.result()
        self.assertEqual(report.status, "FAIL")
        self.assertEqual(report.first_failure["name"], "plan the newer sealed release")
        self.assertEqual(report.first_failure["cases"], ["S-023.2"])
        self.assertIn("exit 2", report.first_failure["message"])


if __name__ == "__main__":
    unittest.main()
