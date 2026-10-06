from __future__ import annotations

import json
import hashlib
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
        self.config = self.root / ".kb/kb.config.jsonc"
        self.receipt = self.root / ".kb/v2/receipt.json"
        self._write_receipt("3.0.0")
        self.commands: list[list[str]] = []
        self.user_checks = 0

    def tearDown(self):
        self.temp.cleanup()

    def _write_receipt(self, version: str):
        self.receipt.write_text(json.dumps({
            "plan": {"serviceGraph": {"platformVersion": version}},
            "verification": {"configSha256": hashlib.sha256(self.config.read_bytes()).hexdigest()},
        }))

    def fake_kb(self, args: list[str]) -> subprocess.CompletedProcess:
        self.commands.append(args)
        stdout = ""
        code = 0
        if args[1] == "plan":
            stdout = json.dumps({"ok": True, "plan": {
                "serviceGraph": {"platformVersion": "3.0.1"}, "planHash": "resolved-plan-hash",
            }})
        elif args[1] == "update":
            update_count = sum(command[1] == "update" for command in self.commands)
            self.config.write_text('{"stable":true,"platform":"3.0.1"}\n')
            self._write_receipt("3.0.1")
            if "--json" not in args:
                stdout = "Platform update completed.\n"
                return subprocess.CompletedProcess(args, 0, stdout, "")
            stdout = json.dumps({"ok": True, "receipt": {"plan": {"serviceGraph": {"platformVersion": "3.0.1"}, "planHash": "resolved-plan-hash"}},
                                 "snapshot": {"id": f"snapshot-{update_count}"},
                                 "hostRestarted": True, "logPath": "/logs/update.log"})
        elif args[1] == "rollback":
            if "missing-journey-snapshot" in args:
                stdout = json.dumps({"ok": False, "error": {"code": "KB_UPDATE_NO_SNAPSHOT", "message": "Snapshot is unknown.", "hint": "restore a named snapshot", "correlationId": "c-missing"}})
                code = 1
            else:
                self.config.write_text('{"stable":true}\n')
                self._write_receipt("3.0.0")
                if "--json" not in args:
                    return subprocess.CompletedProcess(args, 0, "Platform rollback completed.\n", "")
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
        if stdout:
            decoded = json.loads(stdout) if stdout.startswith("{") else None
            if isinstance(decoded, dict) and decoded.get("ok") is True and "data" not in decoded:
                decoded = {"ok": True, "data": {key: value for key, value in decoded.items() if key != "ok"}}
                stdout = json.dumps(decoded)
        return subprocess.CompletedProcess(args, code, stdout, "")

    def test_update_rollback_preserves_the_saved_user_session(self):
        def verify_user():
            self.user_checks += 1
            return subprocess.CompletedProcess(["kb", "auth", "login"], 0, "logged in", "")

        result = run_platform_update_steps(
            mode="secured", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json",
            request_file=self.root / "request.json", base_version="3.0.0", updated_version="3.0.1",
            run_command=self.fake_kb, verify_user=verify_user,
        )
        report = result.result()
        self.assertEqual(report.status, "PASS")
        steps = {step["name"]: step for step in report.steps}
        self.assertEqual(steps["existing admin can log in after update"]["status"], "PASS")
        self.assertEqual(steps["existing admin can log in after rollback"]["status"], "PASS")
        self.assertEqual(
            steps["save installed receipt, config, and host status"]["details"]["configSha256"]["beforeUpdate"],
            hashlib.sha256(b'{"stable":true}\n').hexdigest(),
        )
        self.assertEqual(
            set(steps["verify restored receipt, config, host, and project command"]["details"]["configSha256"]),
            {"beforeUpdate", "afterUpdateHostRestart", "afterRollbackHostRestart", "afterMissingSnapshotRecovery"},
        )
        checkpoints = steps["verify restored receipt, config, host, and project command"]["details"]["configSha256"]
        self.assertNotEqual(checkpoints["beforeUpdate"], checkpoints["afterUpdateHostRestart"])
        self.assertEqual(checkpoints["beforeUpdate"], checkpoints["afterRollbackHostRestart"])
        self.assertEqual(checkpoints["afterRollbackHostRestart"], checkpoints["afterMissingSnapshotRecovery"])
        claims = {claim["claimId"] for claim in report.case_run["claims"]}
        self.assertEqual(claims, {f"S-023.{step}" for step in range(1, 8)} |
                         {f"S-024.{step}" for step in (1, 2, 3, 4, 5)} | {"S-041.4", "S-203.10", "S-203.11"})
        self.assertEqual([command[1] for command in self.commands], [
            "status", "plan", "update", "update", "status", "project", "plan", "update",
            "rollback", "rollback", "rollback", "status", "project", "doctor",
        ])
        for operation in ("plan", "update"):
            command = next(args for args in self.commands if args[1] == operation)
            self.assertTrue({"--json", "--index", "--input"}.issubset(command))
        rollback = next(args for args in self.commands if args[1] == "rollback" and "missing-journey-snapshot" not in args)
        self.assertIn("--yes", rollback)
        self.assertIn("snapshot-1", rollback)
        self.assertNotIn("--json", self.commands[3])
        self.assertNotIn("--json", self.commands[8])
        self.assertIn("--json", self.commands[9])
        self.assertEqual(self.user_checks, 3)
        self.assertNotIn("S-041.4", steps["verify updated receipt, config, and host status"]["cases"])
        self.assertIn("S-041.4", steps["verify restored receipt, config, host, and project command"]["cases"])

    def test_failed_initial_login_does_not_claim_saved_session_survives_update(self):
        login_attempts = 0

        def failed_login():
            nonlocal login_attempts
            login_attempts += 1
            code = 1 if login_attempts == 1 else 0
            return subprocess.CompletedProcess(["kb", "auth", "login"], code,
                                               "authentication failed" if code else "logged in", "")

        result = run_platform_update_steps(
            mode="secured", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json", request_file=self.root / "request.json",
            base_version="3.0.0", updated_version="3.0.1", run_command=self.fake_kb, verify_user=failed_login,
        )
        steps = {step["name"]: step for step in result.result().steps}
        self.assertEqual(steps["save installed receipt, config, and host status"]["status"], "FAIL")
        self.assertEqual(steps["verify restored receipt, config, host, and project command"]["status"], "FAIL")
        self.assertIn("saved user session was not established", steps["verify restored receipt, config, host, and project command"]["message"])
        self.assertEqual(
            steps["save installed receipt, config, and host status"]["details"]["configSha256"]["beforeUpdate"],
            hashlib.sha256(b'{"stable":true}\n').hexdigest(),
        )

    def test_missing_baseline_blocks_s024_3_with_reason(self):
        status_calls = 0

        def missing_initial_status(args: list[str]) -> subprocess.CompletedProcess:
            nonlocal status_calls
            if args[1] == "status":
                status_calls += 1
                if status_calls == 1:
                    return subprocess.CompletedProcess(args, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_HOST_UNHEALTHY", "message": "The host is stopped.", "hint": "Start the host."}}), "")
            return self.fake_kb(args)

        result = run_platform_update_steps(
            mode="local", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json", request_file=self.root / "request.json",
            base_version="3.0.0", updated_version="3.0.1", run_command=missing_initial_status,
        )
        steps = {step["name"]: step for step in result.result().steps}
        verification = steps["verify restored receipt, config, host, and project command"]
        self.assertEqual(verification["status"], "BLOCKED")
        self.assertIn("before-update config SHA-256 is unavailable", verification["message"])
        self.assertNotIn("beforeUpdate", verification["details"]["configSha256"])

    def test_s024_3_names_first_unexpected_checkpoint_change_and_both_hashes(self):
        def mutate_after_rollback(args: list[str]) -> subprocess.CompletedProcess:
            result = self.fake_kb(args)
            if args[1] == "rollback" and "missing-journey-snapshot" not in args and result.returncode == 0:
                self.config.write_text('{"stable":true,"rewritten":true}\n')
            return result

        result = run_platform_update_steps(
            mode="local", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json", request_file=self.root / "request.json",
            base_version="3.0.0", updated_version="3.0.1", run_command=mutate_after_rollback,
        )
        verification = next(step for step in result.result().steps if step["cases"] == ["S-024.3"])
        self.assertEqual(verification["status"], "FAIL")
        self.assertIn("after successful rollback plus host restart", verification["message"])
        self.assertIn("beforeUpdate=", verification["message"])
        self.assertIn("afterRollbackHostRestart=", verification["message"])

    def test_s024_3_names_change_during_missing_snapshot_recovery(self):
        def mutate_during_recovery(args: list[str]) -> subprocess.CompletedProcess:
            result = self.fake_kb(args)
            if args[1] == "rollback" and "missing-journey-snapshot" in args:
                self.config.write_text('{"stable":true,"recoveryRewrite":true}\n')
            return result

        result = run_platform_update_steps(
            mode="local", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json", request_file=self.root / "request.json",
            base_version="3.0.0", updated_version="3.0.1", run_command=mutate_during_recovery,
        )
        verification = next(step for step in result.result().steps if step["cases"] == ["S-024.3"])
        self.assertEqual(verification["status"], "FAIL")
        self.assertIn("after missing-snapshot recovery", verification["message"])
        self.assertIn("afterRollbackHostRestart=", verification["message"])
        self.assertIn("afterMissingSnapshotRecovery=", verification["message"])

    def test_failed_update_reports_the_first_failed_case_step(self):
        def broken(args: list[str]) -> subprocess.CompletedProcess:
            if args[1] == "plan":
                return subprocess.CompletedProcess(args, 2, json.dumps({"ok": False, "error": {
                    "code": "KB_TEST_PLAN_REJECTED", "message": "Invalid update request.",
                    "hint": "Correct the request and retry."}}), "")
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
        self.assertIn("KB_TEST_PLAN_REJECTED", report.first_failure["message"])

    def test_repeat_plan_drift_names_changed_plan_fields_before_mutation(self):
        def drifting(args: list[str]) -> subprocess.CompletedProcess:
            result = self.fake_kb(args)
            if args[1] == "plan" and sum(command[1] == "update" for command in self.commands) > 0:
                payload = json.loads(result.stdout)
                payload["data"]["plan"]["planHash"] = "drifted-plan-hash"
                payload["data"]["plan"]["scenarioStateDigest"] = "changed-state"
                result = subprocess.CompletedProcess(args, 0, json.dumps(payload), "")
            return result

        result = run_platform_update_steps(
            mode="secured", launcher="kb-create", kb_binary="kb", platform_root=self.root,
            update_index=self.root / "update-index.json", request_file=self.root / "request.json",
            base_version="3.0.0", updated_version="3.0.1", run_command=drifting,
        )
        failure = result.result().first_failure
        self.assertEqual(failure["cases"], ["S-023.6"])
        self.assertIn("plan.planHash", failure["message"])
        self.assertIn("plan.scenarioStateDigest", failure["message"])
        self.assertEqual(sum(command[1] == "update" for command in self.commands), 2)


if __name__ == "__main__":
    unittest.main()
