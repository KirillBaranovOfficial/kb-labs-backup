from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from secured_connection_steps import validate_case_response, validate_cli_case


class SecuredConnectionContractTests(unittest.TestCase):
    # The fake gateway/CLI failure is deliberately supplied to every exact claim
    # validator; each documented step must turn that failure into a red claim.
    def test_fake_gateway_failures_make_each_http_claim_red(self):
        for case_id in ("S-042.1", "S-042.2", "S-042.3", "S-042.5", "S-042.6",
                        "S-005.4", "S-005.5"):
            with self.subTest(case_id=case_id), self.assertRaisesRegex(AssertionError, case_id):
                validate_case_response(case_id, 503, {}, expected="gateway.test")

    def test_fake_cli_failures_make_each_cli_claim_red(self):
        for case_id in ("S-005.2", "S-005.3", "S-031.1", "S-031.2",
                        "S-031.3", "S-031.4", "S-042.4"):
            with self.subTest(case_id=case_id), self.assertRaisesRegex(AssertionError, case_id):
                validate_cli_case(case_id, 1, "simulated command failure with stack trace", {})

    def test_s031_session_status_requires_the_canonical_path(self):
        payload = {"ok": True, "data": {"session": {
            "authenticated": True, "tokenExpired": True, "gatewayUrl": "https://gateway.test",
            "path": "/tmp/cli-home/.kb/session.json",
        }}}
        validate_cli_case("S-031.1", 0, "", payload, gateway="https://gateway.test")
        payload["data"]["session"].pop("path")
        with self.assertRaisesRegex(AssertionError, "S-031.1"):
            validate_cli_case("S-031.1", 0, "", payload, gateway="https://gateway.test")

    def test_s042_reset_link_requires_the_enveloped_reset_url_and_expiry(self):
        payload = {"ok": True, "data": {"resetUrl": "https://gateway.test/reset/secret", "expiresAt": 123456}}
        validate_cli_case("S-042.4", 0, "", payload)
        with self.assertRaisesRegex(AssertionError, "S-042.4"):
            validate_cli_case("S-042.4", 0, "", {"ok": True, "reset_token": "secret"})

    def test_case_specific_denial_and_consumption_responses_pass(self):
        validate_case_response("S-042.3", 422,
                               {"code": "KB_AUTH_INVALID_INVITE", "hint": "Request a new invite"})
        validate_case_response("S-042.6", 400, {
            "code": "KB_AUTH_INVALID_PASSWORD_RESET", "hint": "Request a new link",
        })
        validate_case_response("S-005.5", 403, {"error": {
            "code": "KB_AUTH_FORBIDDEN", "message": "Permission denied: users:read", "hint": "Ask an administrator",
        }})


if __name__ == "__main__":
    unittest.main()
