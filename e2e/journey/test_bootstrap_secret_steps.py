from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from bootstrap_secret_steps import (bootstrap_password_file,
                                    register_supplied_password_absence_step)
from invocations import store_report_secret, write_report_json


class BootstrapSecretStepsTest(unittest.TestCase):
    def test_finds_password_in_nested_backup_temp_and_binary_files_without_reporting_it(self):
        password = "journey-bootstrap-secret-sentinel"
        env = {}
        store_report_secret(env, "KB_JOURNEY_ADMIN_PASSWORD", password,
                            "installed first-admin password")
        with tempfile.TemporaryDirectory(prefix="journey-bootstrap-secret-") as temporary:
            root = Path(temporary) / "install"
            nested = root / "versions" / "version-a" / ".kb" / "v2"
            nested.mkdir(parents=True)
            outside = Path(temporary) / "outside"
            outside.mkdir()
            for relative, content in (
                    ("versions/version-a/.kb/v2/secrets.json", password.encode()),
                    (".atomic-write.tmp", password.encode()),
                    ("versions/version-a/.kb/v2/receipt.json.bak", password.encode()),
                    ("versions/version-a/.kb/v2/binary", b"\x00\x01" + password.encode() + b"\xff")):
                path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(content)
                location = bootstrap_password_file(root, (("outside", outside),), password)
                self.assertEqual(location, f"install/{relative}")
                report_path = Path(temporary) / "report.json"
                report = write_report_json(report_path, {"message": f"credential remains at {location}"}, env=env)
                rendered = json.dumps(report)
                self.assertIn(relative, rendered)
                self.assertNotIn(password, rendered)
                path.unlink()

            outside_file = outside / "declared-file"
            outside_file.write_bytes(password.encode())
            self.assertEqual(bootstrap_password_file(root, (("outside", outside),), password),
                             "outside/declared-file")

    def test_supplied_password_step_scans_the_install_home_and_keeps_report_redacted(self):
        password = "supplied-bootstrap-secret-sentinel"
        env = {}
        store_report_secret(env, "KB_JOURNEY_ADMIN_PASSWORD", password,
                            "supplied first-admin password")
        with tempfile.TemporaryDirectory(prefix="journey-supplied-bootstrap-") as temporary:
            scan_root = Path(temporary) / "home"
            nested = scan_root / "platform" / "versions" / "version-a" / ".kb" / "v2"
            nested.mkdir(parents=True)
            secret_file = nested / "secrets.env.bak"
            secret_file.write_bytes(password.encode())

            class CapturingJourney:
                runner_selector = "agent-secured:commit"
                step_args = None

                def step(self, *args, **kwargs):
                    self.step_args = (args, kwargs)

            journey = CapturingJourney()
            register_supplied_password_absence_step(journey, scan_root, password)
            args, kwargs = journey.step_args
            self.assertEqual(args[0], "verify supplied bootstrap credential absent from install files")
            self.assertEqual(kwargs["cases"], ["S-181.3"])
            self.assertEqual(kwargs["runners"], ("agent-secured:commit", "agent:commit"))
            failure = args[2]()
            self.assertEqual(failure, "bootstrap credential remains at install/platform/versions/version-a/.kb/v2/secrets.env.bak")
            report_path = Path(temporary) / "report.json"
            report = write_report_json(report_path, {"message": failure}, env=env)
            self.assertNotIn(password, json.dumps(report))

    def test_supplied_password_step_is_not_registered_for_an_unowned_runner(self):
        class CapturingJourney:
            runner_selector = "service-local:explore"
            step_args = None

            def step(self, *args, **kwargs):
                self.step_args = (args, kwargs)

        journey = CapturingJourney()
        register_supplied_password_absence_step(journey, Path("/unused"), "sentinel")
        self.assertIsNone(journey.step_args)


if __name__ == "__main__":
    unittest.main()
