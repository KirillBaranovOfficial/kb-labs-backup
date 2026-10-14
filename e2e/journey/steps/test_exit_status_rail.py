from __future__ import annotations

import json
import subprocess
import unittest

from invocations import read_kb_create_cli_result, read_ux_cli_result, run_command


class InvocationExitStatusRailTests(unittest.TestCase):
    def test_fake_runner_cannot_pass_wrong_catalogued_status(self):
        stdout = json.dumps({"ok": False, "error": {
            "code": "KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE",
            "message": "Gateway index unavailable",
        }})
        result = subprocess.CompletedProcess(["kb-create", "plan", "--json"], 2, stdout, "")
        with self.assertRaisesRegex(ValueError, r"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE \(operation-failure\).*process status 2"):
            run_command(["kb-create", "plan", "--json"], runner=lambda *_args, **_kwargs: result)

    def test_plain_mapping_or_custom_result_cannot_enter_the_decoder(self):
        values = ({"stdout": '{"ok":true,"data":{}}', "returncode": 0},
                  type("ResultLike", (), {"stdout": '{"ok":true,"data":{}}', "returncode": 0})())
        for value in values:
            with self.subTest(type=type(value).__name__), self.assertRaisesRegex(ValueError, "requires a subprocess.CompletedProcess"):
                read_ux_cli_result(value)

    def test_process_and_terminal_status_mismatch_names_the_catalogued_error(self):
        stdout = (json.dumps({"ok": False, "error": {
            "code": "KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE",
            "message": "Gateway index unavailable",
        }}) + "\n" + json.dumps({"ok": True, "data": None, "meta": {"stream": {
            "terminal": True, "exitCode": 2,
        }}}) + "\n")
        result = subprocess.CompletedProcess(["kb-create", "apply", "--json"], 1, stdout, "")
        with self.assertRaisesRegex(ValueError, r"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE \(operation-failure\).*process status 1.*terminal status 2"):
            read_kb_create_cli_result("apply", result)


if __name__ == "__main__":
    unittest.main()
