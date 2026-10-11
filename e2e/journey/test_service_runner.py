from __future__ import annotations

import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import service_runner
from service_runner import restore_secured_cli_login


class SecuredCliLoginTests(unittest.TestCase):
    def test_restores_cli_login_after_auth_journey_logs_out(self):
        calls = []

        def runner(command, **kwargs):
            calls.append((command, kwargs))
            return SimpleNamespace(returncode=0, stdout="signed in", stderr="")

        env = {
            "HOME": "/isolated-home",
            "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
            "KB_JOURNEY_ADMIN_PASSWORD": "JourneyPassw0rd!",
            "KB_JOURNEY_BASE_URL": "http://127.0.0.1:4000",
        }

        restore_secured_cli_login("/isolated-home/.local/bin/kb", env, runner=runner)

        self.assertEqual(calls[0][0], [
            "/isolated-home/.local/bin/kb", "auth", "login",
            "--gateway-url", "http://127.0.0.1:4000",
            "--email", "admin@example.test",
            "--password-stdin",
        ])
        self.assertEqual(calls[0][1]["cwd"], "/isolated-home")
        self.assertEqual(calls[0][1]["env"], env)

    def test_login_failure_names_the_recovery_command(self):
        def runner(_command, **_kwargs):
            return SimpleNamespace(returncode=1, stdout="", stderr="credentials rejected")

        with self.assertRaisesRegex(RuntimeError, "kb auth login.*credentials rejected"):
            restore_secured_cli_login(
                "/isolated-home/.local/bin/kb",
                {"HOME": "/isolated-home", "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                 "KB_JOURNEY_ADMIN_PASSWORD": "bad"},
                runner=runner,
            )


class SecuredServiceTokenTests(unittest.TestCase):
    def _admin_login_check(self):
        return next(check[2] for check in service_runner._checks("secured")
                    if check[0] == "secured admin CLI login")

    def test_short_login_token_fails_at_acquisition_before_storage(self):
        with patch.dict(os.environ, {}, clear=True), \
                patch.object(service_runner, "ADMIN_PASSWORD", "synthetic-admin-password"), \
                patch.object(service_runner, "request", return_value=(200, {"accessToken": "weak"}, None)):
            with self.assertRaisesRegex(AssertionError, "at least 12 characters"):
                self._admin_login_check()()

            self.assertNotIn("KB_JOURNEY_SERVICE_TOKEN", os.environ)

    def test_valid_login_token_is_stored_and_redacted_from_report(self):
        secret = "synthetic-service-access-token-for-tests"
        with tempfile.TemporaryDirectory(prefix="journey-service-token-") as temp, \
                patch.dict(os.environ, {}, clear=True), \
                patch.object(service_runner, "ADMIN_PASSWORD", "synthetic-admin-password"), \
                patch.object(service_runner, "request", side_effect=[
                    (200, {"accessToken": secret}, None), (200, {}, None),
                ]):
            self.assertIsNone(self._admin_login_check()())
            self.assertEqual(os.environ["KB_JOURNEY_SERVICE_TOKEN"], secret)

            import invocations
            report = Path(temp) / "service-token.json"
            invocations.write_report_json(report, {"diagnostic": secret}, env={})
            self.assertNotIn(secret, report.read_text())
            self.assertEqual(json.loads(report.read_text())["diagnostic"], "[REDACTED]")


if __name__ == "__main__":
    unittest.main()
