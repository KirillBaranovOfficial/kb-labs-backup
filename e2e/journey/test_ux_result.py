from __future__ import annotations

import unittest

from invocations import decode_ux_success_result


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


if __name__ == "__main__":
    unittest.main()
