from __future__ import annotations

from types import SimpleNamespace
import unittest

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


if __name__ == "__main__":
    unittest.main()
