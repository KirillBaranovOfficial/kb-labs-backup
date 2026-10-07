from __future__ import annotations

import unittest

from invocations import decode_ux_error_result, decode_ux_success_result, describe_cli_failure, read_ux_cli_result


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

    def test_setup_failure_description_preserves_typed_error_and_hint(self):
        output = '{"error":{"code":"KB_DOCTOR_INVALID","message":"Manifest scan failed.","hint":"Run `kb-create doctor --fix` and retry."}}'
        detail = describe_cli_failure("kb-create doctor", output, "", 2)
        self.assertEqual(detail, "kb-create doctor exited 2: KB_DOCTOR_INVALID; Manifest scan failed.; hint: Run `kb-create doctor --fix` and retry.")


if __name__ == "__main__":
    unittest.main()
