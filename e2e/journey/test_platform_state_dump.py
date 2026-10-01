from __future__ import annotations

import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from platform_state_dump import MAX_DUMP_BYTES, MAX_FILE_BYTES, PlatformStateDumpOnce, dump_platform_state


class PlatformStateDumpTests(unittest.TestCase):
    def test_dump_once_then_continue_to_uninstall(self):
        dump_guard = PlatformStateDumpOnce()
        events = []
        with patch("platform_state_dump.dump_platform_state") as write_dump:
            write_dump.side_effect = lambda *args, **kwargs: events.append("dump")
            dump_guard.dump("platform", "reports", mode="local", front="agent",
                            scenario="commit", variant="default", first_failure="failed step")
            events.append("continue to uninstall")
            dump_guard.dump("platform", "reports", mode="local", front="agent",
                            scenario="commit", variant="default", first_failure="failed step")

        self.assertEqual(events, ["dump", "continue to uninstall"])
        write_dump.assert_called_once()

    def test_dump_is_bounded_redacted_and_records_command_failures(self):
        with tempfile.TemporaryDirectory(prefix="journey-dump-test-") as temporary:
            root = Path(temporary)
            home = root / "home"
            launcher_logs = home / ".kb" / "launcher"
            launcher_logs.mkdir(parents=True)
            (launcher_logs / "host.log").write_text(
                "\n".join(f"host-line-{number}" for number in range(500)) + "\napiKey: launcher-secret\n"
            )
            platform = root / "platform"
            reports = root / "reports"
            binaries = root / "bin"
            binaries.mkdir()
            (platform / ".kb/v2").mkdir(parents=True)
            (platform / ".kb/logs").mkdir(parents=True)
            (platform / ".kb/diagnostics").mkdir(parents=True)
            (platform / "node_modules/@kb-labs/sample").mkdir(parents=True)
            (platform / ".kb/marketplace.lock").write_text('{"commit":"abc","token":"lock-secret"}')
            (platform / ".kb/kb.config.jsonc").write_text('{\n  "apiKey": "config-secret",\n  // retained comment\n}\n')
            (platform / ".kb/v2/receipt.json").write_text(json.dumps({
                "password": "receipt-secret",
                "plan": {"artifacts": [
                    {"id": "sample", "package": "@kb-labs/sample", "version": "1.2.3"},
                    {"id": "missing", "package": "@kb-labs/missing", "version": "4.5.6"},
                ]},
            }))
            (platform / ".kb/v2/snapshots").mkdir()
            (platform / ".kb/v2/snapshots/snapshot-good.json").write_text(
                '{"id":"snapshot-good","token":"snapshot-secret"}'
            )
            (platform / ".kb/v2/snapshots/snapshot-too-large.json").write_text("x" * (MAX_FILE_BYTES + 1))
            (platform / "node_modules/@kb-labs/sample/package.json").write_text(
                '{"name":"@kb-labs/sample","version":"1.2.3"}'
            )
            (platform / "node_modules/@kb-labs/sample/kb-create.manifest.json").write_text('{}\n')
            (platform / ".kb/logs/oversized.log").write_text("skip-this-" + "x" * (MAX_DUMP_BYTES // 2))
            (platform / ".kb/logs/recent.log").write_text(
                "\n".join(f"line-{number}" for number in range(500)) + "\nJourneyPassw0rd!\n"
            )
            (platform / ".kb/diagnostics/recent.json").write_text(
                json.dumps({"message": "JourneyPassw0rd!", "secret": "diag-secret"})
            )

            kb = binaries / "kb"
            kb.write_text(
                "#!/bin/sh\n"
                "if [ \"$1\" = marketplace ] && [ \"$2\" = plugins ]; then echo 'marketplace plugins command failed'; exit 7; fi\n"
                "echo 'diag command failed'; exit 8\n"
            )
            kb.chmod(0o755)
            launcher = binaries / "kb-create"
            launcher.write_text("#!/bin/sh\necho '{\"token\":\"launcher-secret\"}'\nexit 9\n")
            launcher.chmod(0o755)
            env = {**os.environ, "HOME": str(home), "PATH": f"{binaries}:{os.environ.get('PATH', '')}",
                   "KB_JOURNEY_GIT_REVISION": "abc123def"}

            destination = dump_platform_state(
                platform, reports, mode="secured", front="agent", scenario="commit", variant="default",
                first_failure="kb command failed: Unknown command: workflow run",
                env=env, cwd=root, admin_password="JourneyPassw0rd!",
            )

            self.assertTrue((destination / ".kb/marketplace.lock").is_file())
            self.assertTrue((destination / ".kb/v2/receipt.json").is_file())
            snapshot = destination / ".kb/v2/snapshots/snapshot-good.json"
            self.assertTrue(snapshot.is_file())
            self.assertNotIn("snapshot-secret", snapshot.read_text())
            self.assertFalse((destination / ".kb/v2/snapshots/snapshot-too-large.json").exists())
            manifests = json.loads((destination / "receipt-artifact-manifests.json").read_text())
            self.assertEqual(manifests, [
                {"id": "sample", "package": "@kb-labs/sample", "version": "1.2.3", "manifestExists": True},
                {"id": "missing", "package": "@kb-labs/missing", "version": "4.5.6", "manifestExists": False},
            ])
            self.assertIn("sample@1.2.3", (destination / "kb-labs-packages.txt").read_text())
            tail = (destination / ".kb/logs/recent.log").read_text()
            self.assertIn("line-499", tail)
            self.assertNotIn("line-99\n", tail)
            self.assertTrue((destination / ".kb/logs/oversized.log").is_file())
            self.assertLessEqual((destination / ".kb/logs/oversized.log").stat().st_size, 1024 * 1024)
            self.assertNotIn("JourneyPassw0rd!", "".join(
                path.read_text(errors="replace") for path in destination.rglob("*") if path.is_file()
            ))
            self.assertNotIn("config-secret", (destination / ".kb/kb.config.jsonc").read_text())
            self.assertNotIn("receipt-secret", (destination / ".kb/v2/receipt.json").read_text())
            self.assertNotIn("diag-secret", (destination / ".kb/diagnostics/recent.json").read_text())
            self.assertIn("exit_code: 7", (destination / "kb-marketplace-plugins-platform.txt").read_text())
            self.assertIn("exit_code: 7", (destination / "kb-marketplace-plugins-project.txt").read_text())
            self.assertIn("exit_code: 8", (destination / "kb-command-diagnostic.txt").read_text())
            self.assertIn("exit_code: 9", (destination / "kb-create-status.txt").read_text())
            launcher_tail = (destination / "launcher-logs/host.log").read_text()
            self.assertIn("host-line-499", launcher_tail)
            self.assertNotIn("host-line-99\n", launcher_tail)
            self.assertNotIn("launcher-secret", launcher_tail)
            self.assertIn("First failure:", (destination / "index.md").read_text())
            self.assertIn("Journey git revision: abc123def", (destination / "index.md").read_text())
            size = sum(path.stat().st_size for path in destination.rglob("*") if path.is_file())
            self.assertLessEqual(size, MAX_DUMP_BYTES)

    def test_dump_includes_project_workflow_records_and_daemon_logs(self):
        with tempfile.TemporaryDirectory(prefix="journey-workflow-dump-test-") as temporary:
            home = Path(temporary)
            platform = home / "kb-platform"
            project = home / "fresh-project"
            reports = home / "reports"
            workflow_dir = project / ".kb/data/workflows"
            log_dir = project / ".kb/logs"
            workflow_dir.mkdir(parents=True)
            log_dir.mkdir(parents=True)
            (workflow_dir / "run-success.json").write_text(
                '{"id":"run-success","status":"success","input":{"message":"hello"},"token":"hidden"}'
            )
            (log_dir / "workflow-daemon.log").write_text("run-success completed successfully\n")

            destination = dump_platform_state(
                platform, reports, mode="local", front="agent", scenario="explore", variant="default",
                first_failure="workflow did not reach a terminal status",
                env={**os.environ, "PATH": ""}, cwd=home,
            )

            evidence = destination / "workflow-evidence/fresh-project/.kb/data/workflows/run-success.json"
            daemon_log = destination / "workflow-evidence/fresh-project/.kb/logs/workflow-daemon.log"
            self.assertTrue(evidence.is_file())
            self.assertIn('"status": "success"', evidence.read_text())
            self.assertIn('"token": "[REDACTED]"', evidence.read_text())
            self.assertTrue(daemon_log.is_file())
            self.assertIn("run-success completed successfully", daemon_log.read_text())
            size = sum(path.stat().st_size for path in destination.rglob("*") if path.is_file())
            self.assertLessEqual(size, MAX_DUMP_BYTES)


if __name__ == "__main__":
    unittest.main()
