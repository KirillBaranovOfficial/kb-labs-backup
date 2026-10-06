from __future__ import annotations

import subprocess
import tempfile
import unittest
import json
from pathlib import Path

from installer_case_steps import (_seed_doctor_fixture, run_doctor_root_isolation_step,
                                  run_analytics_invalid_retention_step, run_installer_case_steps,
                                  run_uninstall_decline_step)


class InstallerCaseStepsTest(unittest.TestCase):
    def test_invalid_analytics_retention_claim_requires_catalogued_start_error(self):
        with tempfile.TemporaryDirectory(prefix="journey-invalid-analytics-retention-") as temp:
            root = Path(temp)
            config_path = root / ".kb/kb.config.jsonc"
            config_path.parent.mkdir(parents=True)
            config_path.write_text('{"platform":{"adapterOptions":{"analytics":{"retentionDays":90}}}}\n')
            calls = []

            def run_command(args):
                calls.append(args)
                self.assertEqual(json.loads(config_path.read_text())["platform"]["adapterOptions"]["analytics"]["retentionDays"], 0)
                payload = {"ok": False, "error": {
                    "code": "KB_CONFIG_INVALID",
                    "message": "Invalid platform.adapterOptions.analytics.retentionDays",
                    "cause": "must be a positive integer",
                    "hint": "Run kb config set --scope platform platform.adapterOptions.analytics.retentionDays 90",
                }}
                return subprocess.CompletedProcess(args, 1, json.dumps(payload), "")

            journey = run_analytics_invalid_retention_step(mode="local", launcher="kb-create",
                                                           platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["S-191.7"])
        self.assertEqual(journey.steps[0].status, "PASS")
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][1], "start")

    def test_doctor_root_isolation_claim_checks_repair_and_main_status(self):
        with tempfile.TemporaryDirectory(prefix="journey-doctor-isolation-") as temp:
            root = Path(temp)
            source = root / "source"
            receipt = source / ".kb/v2/receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text(json.dumps({"plan": {"artifacts": [
                {"kind": "platform", "id": "platform", "package": "@kb/platform", "version": "1.0.0"},
            ]}}))
            calls = []

            def run_command(args):
                calls.append(args)
                if args[1:3] == ["doctor", "--fix"]:
                    output = {"ok": True, "data": {"operation": "doctor-fix", "hostRestarted": False}}
                elif args[1] == "start":
                    output = {"ok": False, "error": {"code": "KB_HOST_ALREADY_RUNNING",
                               "message": f"The KB Labs host for platform root {source.resolve()} is already running (pid 42).",
                               "hint": f"Stop that platform first with `kb-create stop --platform-root {source}`.",
                               "meta": {"pid": "42"}}}
                    return subprocess.CompletedProcess(args, 1, json.dumps(output), "")
                elif args[1] == "status" and str(source) not in " ".join(args):
                    output = {"ok": True, "data": {"host": {"state": "stopped"}}}
                else:
                    output = {"ok": True, "data": {"host": {"state": "running"}}}
                return subprocess.CompletedProcess(args, 0, json.dumps(output), "")

            journey = run_doctor_root_isolation_step(mode="local", launcher="kb-create",
                                                     platform_root=source, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["S-025.7"])
        self.assertEqual(journey.steps[0].status, "PASS")
        self.assertEqual(journey.steps[1].cases, ["S-025.8"])
        self.assertEqual(journey.steps[1].status, "PASS")
        self.assertEqual(len(calls), 5)

    def test_doctor_fixture_requirement_matches_receipt_and_manifest_projection(self):
        with tempfile.TemporaryDirectory(prefix="journey-doctor-fixture-") as temp:
            root = Path(temp)
            source = root / "source"
            source_receipt = source / ".kb/v2/receipt.json"
            source_receipt.parent.mkdir(parents=True)
            source_receipt.write_text(json.dumps({"plan": {"artifacts": [
                {"kind": "platform", "id": "platform", "package": "@kb/platform", "version": "1.0.0"},
            ]}}))
            target = root / "fixture"
            requirement = {"id": "fixture.required", "path": "/fixture/required", "required": True,
                           "secret": False, "hint": "configure fixture"}

            _seed_doctor_fixture(source, target, requirement=requirement)

            receipt = json.loads((target / ".kb/v2/receipt.json").read_text())
            manifest = json.loads((target / "node_modules/@kb-labs/journey-doctor-fixture/kb-create.manifest.json").read_text())
            fixture_artifact = next(item for item in receipt["plan"]["artifacts"]
                                    if item["id"] == "journey-doctor-fixture")
            self.assertEqual(fixture_artifact["config"], [requirement])
            self.assertEqual(manifest["requirements"], [requirement])

    def test_uninstall_decline_claim_fails_for_a_failed_fake_response(self):
        with tempfile.TemporaryDirectory(prefix="journey-uninstall-decline-") as temp:
            receipt = Path(temp) / "receipt.json"
            receipt.write_text('{"id":"unchanged"}')

            def fake_pty(args, *, on_output):
                self.assertEqual(on_output("Continue? [y/N] "), "n\n")
                return subprocess.CompletedProcess(args, 2, "KB_INSTALL_CONFIRMATION_REQUIRED", ""), 0.0

            journey = run_uninstall_decline_step(mode="local", args=["kb-create", "uninstall"],
                                                 receipt_path=receipt, run_pty=fake_pty)
            self.assertEqual(journey.steps[0].cases, ["S-050.3"])
            self.assertEqual(journey.steps[0].status, "FAIL")

    def test_each_claim_fails_when_the_cli_returns_a_failed_envelope(self):
        with tempfile.TemporaryDirectory(prefix="journey-installer-cases-") as temp:
            root = Path(temp)
            stable = root / "stable.json"
            canary = root / "canary.json"
            stable.write_text('{"platforms": []}')
            canary.write_text('{"platforms": []}')
            installed = root / "installed"
            installed.mkdir()

            def fail_every_command(args):
                return subprocess.CompletedProcess(args, 2, '{"ok":false,"error":{"code":"FAKE_FAILURE"}}', "")

            journey = run_installer_case_steps(mode="local", launcher="kb-create", stable_index=stable,
                                               canary_index=canary, platform_root=installed,
                                               secret_value="journey-test-secret",
                                               run_command=fail_every_command)
            claims = {claim: status for step in journey.steps for claim in step.cases
                      for status in [step.status]}
            expected = {"PC-001.4", "PC-001.5", "PC-001.8", "PC-001.9", "S-181.1", "S-181.2",
                        "S-181.3", "S-181.4", "S-181.5", "S-025.1", "S-025.2",
                        "S-025.3", "S-025.4", "S-025.5", "S-025.6", "S-191.7",
                        "S-053.2", "S-053.3", "S-053.4", "S-053.5", "S-053.6", "S-053.7",
                        "S-187.4", "S-187.5"}
            self.assertEqual(set(claims), expected)
            self.assertTrue(all(claims[claim] == "FAIL" for claim in expected))


if __name__ == "__main__":
    unittest.main()
