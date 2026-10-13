from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from bootstrap_secret_steps import bootstrap_password_file
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


if __name__ == "__main__":
    unittest.main()
