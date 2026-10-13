from __future__ import annotations

import unittest
from pathlib import Path
import re

from invocations import (decode_ux_error_result, decode_ux_success_result, describe_cli_failure,
                         KB_CREATE_JSON_STREAM_COMMANDS, read_kb_create_cli_result,
                         read_ux_cli_result, read_ux_cli_stream)


class UxResultDecodeTests(unittest.TestCase):
    # case: S-203.1
    def test_decodes_success_payload_from_valid_envelope(self):
        self.assertEqual(
            decode_ux_success_result({"ok": True, "data": {"status": "healthy"}}),
            {"status": "healthy"},
        )

    def test_rejects_raw_and_failure_bodies(self):
        for body in ({"status": "healthy"}, {"ok": False, "error": {}}):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, "valid UX success envelope"):
                decode_ux_success_result(body)

    def test_decodes_error_payload_from_valid_envelope(self):
        self.assertEqual(
            decode_ux_error_result({"ok": False, "error": {"code": "KB_AUTH_REQUIRED", "message": "Sign in."}}),
            {"code": "KB_AUTH_REQUIRED", "message": "Sign in."},
        )

    def test_rejects_non_envelope_and_success_body_as_error(self):
        for body in ({"code": "KB_AUTH_REQUIRED"}, {"ok": True, "data": {}}):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, "valid UX error envelope"):
                decode_ux_error_result(body)

    def test_cli_exit_code_must_match_success_envelope(self):
        with self.assertRaisesRegex(ValueError, "exit code does not match"):
            read_ux_cli_result('{"ok":true,"data":{}}', 1)

    def test_cli_exit_code_must_match_error_envelope(self):
        with self.assertRaisesRegex(ValueError, "exit code does not match"):
            read_ux_cli_result('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', 0)

    def test_cli_stream_validates_terminal_exit_and_preserves_result_events(self):
        stream = ('{"ok":true,"data":{"phase":"Apply","state":"completed"}}\n'
                  '{"ok":true,"data":{"operation":"apply","receipt":{}},"meta":{"stream":{"result":true}}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":0}}}\n')
        records = read_ux_cli_stream(stream, 0)
        self.assertEqual(records[-2]["data"]["operation"], "apply")

    def test_cli_stream_rejects_terminal_exit_code_mismatch(self):
        stream = ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":1}}}\n')
        with self.assertRaisesRegex(ValueError, "terminal record does not match"):
            read_ux_cli_stream(stream, 0)

    def test_setup_failure_description_preserves_typed_error_and_hint(self):
        output = '{"error":{"code":"KB_DOCTOR_INVALID","message":"Manifest scan failed.","hint":"Run `kb-create doctor --fix` and retry."}}'
        detail = describe_cli_failure("kb-create doctor", output, "", 2)
        self.assertEqual(detail, "kb-create doctor exited 2: KB_DOCTOR_INVALID; Manifest scan failed.; hint: Run `kb-create doctor --fix` and retry.")

    def test_kb_create_output_forms_match_registered_command_dispatch(self):
        root = Path(__file__).resolve().parents[2]
        registry = (root / "tools/kb-create/v2/cmd/kb-create-v2/root.go").read_text()
        descriptors = re.findall(
            r'(?ms)^\s*\{Name: "([^"]+)"(.*?)(?=^\s*\{Name: "|^\s*\})', registry
        )
        stream_commands = {
            name for name, body in descriptors
            if re.search(r"if jsonMode\(cmd\)\s*\{\s*return execute\w+Stream\(", body)
        }
        self.assertTrue(descriptors, "could not read kb-create operation descriptors from root.go")
        self.assertEqual(stream_commands, KB_CREATE_JSON_STREAM_COMMANDS)

    def test_command_reader_enforces_document_and_stream_forms(self):
        fixtures = Path(__file__).parent / "testdata"
        document = (fixtures / "gateway-index-plan-refusal.json").read_text()
        stream = (fixtures / "gateway-index-apply-refusal.jsonl").read_text()
        self.assertEqual(read_kb_create_cli_result("plan", document, 2)["ok"], False)
        self.assertEqual(read_kb_create_cli_result("apply", stream, 2)["ok"], False)
        with self.assertRaisesRegex(ValueError, "not JSON"):
            read_kb_create_cli_result("plan", stream, 2)
        with self.assertRaisesRegex(ValueError, "non-JSON record"):
            read_kb_create_cli_result("apply", document, 2)


if __name__ == "__main__":
    unittest.main()
