from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
from steps.accounts import run_cli_logout_steps
from steps.auth import (assert_platform_auth_required, run_bootstrap_recovery_steps, run_cli_auth_forbidden,
                        run_cli_auth_status, assert_cli_session_absent)


class CliAuthJourneyTests(unittest.TestCase):
    # case: S-041.7
    def test_cli_gate_requires_the_complete_case_error_envelope(self):
        result = subprocess.CompletedProcess(
            ["kb", "config", "show", "--json"], 1,
            json.dumps({"ok": False, "error": {
                "code": "KB_AUTH_REQUIRED",
                "message": "Authentication is required.",
                "hint": 'Run "kb auth login".',
            }}), "",
        )
        assert_platform_auth_required(result, ["config", "show", "--json"])

        for error in (
            {"code": "KB_AUTH_REQUIRED", "message": "Different message.", "hint": 'Run "kb auth login".'},
            {"code": "KB_AUTH_REQUIRED", "message": "Authentication is required.", "hint": "Different hint."},
            {"code": "OTHER_ERROR", "message": "Authentication is required.", "hint": 'Run "kb auth login".'},
        ):
            malformed = subprocess.CompletedProcess(
                ["kb", "config", "show", "--json"], 1,
                json.dumps({"ok": False, "error": error}), "",
            )
            with self.subTest(error=error), self.assertRaises(AssertionError):
                assert_platform_auth_required(malformed, ["config", "show", "--json"])

    # case: S-041.7, S-041.8
    def test_anonymous_auth_status_is_verified_through_the_cli_product(self):
        result = subprocess.CompletedProcess(
            ["kb", "auth", "status", "--json"], 0,
            json.dumps({"ok": True, "authenticated": False, "session": {"authenticated": False}}), "",
        )
        assert_cli_session_absent(result)

        authenticated = subprocess.CompletedProcess(
            ["kb", "auth", "status", "--json"], 0,
            json.dumps({"ok": True, "authenticated": True, "session": {"authenticated": True}}), "",
        )
        with self.assertRaisesRegex(AssertionError, "session is authenticated"):
            assert_cli_session_absent(authenticated)

    # case: S-041.5, S-041.6, S-100.1
    def test_local_status_and_secured_login_status_tokens_commands(self):
        calls: list[list[str]] = []

        def invoke(args, **_kwargs):
            calls.append(args)
            if args[1:3] == ["auth", "status"]:
                payload = {"ok": True, "session": {"authenticated": bool(state["email"]),
                                                       **({"email": state["email"]} if state["email"] else {})}}
            elif args[1:3] == ["auth", "login"]:
                state["email"] = "member@example.test" if "member@example.test" in args else "admin@example.test"
                payload = {"ok": True}
            elif args[1:3] == ["auth", "tokens"]:
                payload = {"ok": True, "tokens": []}
            else:
                raise AssertionError(args)
            return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

        state = {"email": None}
        with patch("steps.auth.subprocess.run", side_effect=invoke):
            run_cli_auth_status("/isolated/bin/kb", {"HOME": "/isolated"}, authenticated=False)
            state["email"] = None
            run_cli_auth_status("/isolated/bin/kb", {"HOME": "/isolated"}, authenticated=True,
                                email="member@example.test", password="MemberPass123!",
                                base_url="http://gateway.test")

        self.assertEqual(calls[0], ["/isolated/bin/kb", "auth", "status", "--json"])
        self.assertIn(["/isolated/bin/kb", "auth", "login", "--gateway-url", "http://gateway.test",
                       "--email", "member@example.test", "--password", "MemberPass123!"], calls)
        self.assertIn(["/isolated/bin/kb", "auth", "tokens", "--json"], calls)

    # case: S-041.9
    def test_member_reset_link_requires_admin_permission(self):
        args = ["/isolated/bin/kb", "auth", "reset-link", "--user-id", "member-1", "--json"]
        response = {"ok": False, "error": {
            "code": "KB_AUTH_FORBIDDEN", "details": {"permission": "users:write"},
        }}
        with patch("steps.auth.subprocess.run", return_value=subprocess.CompletedProcess(
                args, 1, json.dumps(response), "")) as invoke:
            run_cli_auth_forbidden("/isolated/bin/kb", {"HOME": "/isolated"})
        self.assertEqual(invoke.call_args.args[0], args)

    # case: S-100.2, S-100.3, S-100.4
    def test_bootstrap_recovery_uses_stopped_host_command_and_checks_running_guard(self):
        commands = []
        doctor_calls = 0
        recovered_passwords = []

        def invoke(args, *, env, input_text=None):
            nonlocal doctor_calls
            commands.append((args, input_text))
            if args[1] == "doctor":
                self.assertNotIn("KB_PROJECT_ID", env)
                self.assertNotIn("KB_PROJECT_ROOT", env)
                self.assertEqual(args[args.index("--platform-root") + 1], "/isolated/platform")
            if args[1] == "doctor":
                doctor_calls += 1
            if args[1] == "doctor" and doctor_calls == 2:
                return subprocess.CompletedProcess(args, 1,
                    'KB_INSTALL_RECOVERY_HOST_RUNNING: Recovery needs the host to be stopped. Next: Run "kb-create stop", recover, then "kb-create start".', ""), 0.0
            return subprocess.CompletedProcess(args, 0, "done", ""), 0.0

        with patch("steps.auth._login", return_value=("new", "refresh")), \
             patch("steps.auth._request", side_effect=[{"accessToken": "old"}, {"tenantId": "tenant-1"},
                 {"email": "admin@example.test", "tenantId": "tenant-1"}]):
            run_bootstrap_recovery_steps("/bin/kb-create", "/isolated/platform", "admin@example.test",
                                         "OldPass123!", "NewPass123!", {"HOME": "/isolated"}, invoke,
                                         on_valid_password=recovered_passwords.append)

        recover = [args for args, _ in commands if "--recover" in args]
        self.assertEqual(len(recover), 2)
        self.assertIn("--platform-root", recover[0])
        self.assertIn("--password-stdin", recover[0])
        self.assertIn("--yes", recover[0])
        self.assertIn((recover[0], "NewPass123!\n"), commands)
        self.assertEqual(recovered_passwords, ["NewPass123!"])

    # case: S-100.2, S-100.3
    def test_failed_bootstrap_recovery_restarts_host_without_adopting_password(self):
        commands = []
        recovered_passwords = []

        def invoke(args, *, env, input_text=None):
            commands.append(args)
            if args[1] == "doctor":
                return subprocess.CompletedProcess(args, 1, "recovery failed", ""), 0.0
            return subprocess.CompletedProcess(args, 0, "done", ""), 0.0

        with patch("steps.auth._login", side_effect=[AssertionError("invalid credentials"),
                                                      ("old-access", "old-refresh")]), \
             patch("steps.auth._request", side_effect=[{"accessToken": "old"}, {"tenantId": "tenant-1"},
                                                        {"email": "admin@example.test", "tenantId": "tenant-1"}]), \
             self.assertRaisesRegex(AssertionError, "recovery failed"):
            run_bootstrap_recovery_steps("/bin/kb-create", "/isolated/platform", "admin@example.test",
                                         "OldPass123!", "NewPass123!", {"HOME": "/isolated"}, invoke,
                                         on_valid_password=recovered_passwords.append)

        self.assertIn(["/bin/kb-create", "start", "--platform-root", "/isolated/platform"], commands)
        self.assertEqual(recovered_passwords, ["OldPass123!"])

    # case: S-100.2, S-100.3
    def test_echoed_recovery_password_fails_step_but_keeps_verified_credentials(self):
        commands = []
        recovered_passwords = []

        def invoke(args, *, env, input_text=None):
            commands.append(args)
            output = "NewPass123!" if args[1] == "doctor" else "done"
            return subprocess.CompletedProcess(args, 0, output, ""), 0.0

        with patch("steps.auth._login", return_value=("new-access", "new-refresh")), \
             patch("steps.auth._request", side_effect=[{"accessToken": "old"}, {"tenantId": "tenant-1"},
                                                        {"email": "admin@example.test", "tenantId": "tenant-1"}]), \
             self.assertRaisesRegex(AssertionError, "echoed the supplied password"):
            run_bootstrap_recovery_steps("/bin/kb-create", "/isolated/platform", "admin@example.test",
                                         "OldPass123!", "NewPass123!", {"HOME": "/isolated"}, invoke,
                                         on_valid_password=recovered_passwords.append)

        self.assertEqual(recovered_passwords, ["NewPass123!"])
        self.assertIn(["/bin/kb-create", "start", "--platform-root", "/isolated/platform"], commands)

    # case: S-102.3
    def test_cli_logout_removes_session_and_status_reports_unauthenticated(self):
        commands = []

        def invoke(args, **_kwargs):
            commands.append(args)
            payload = {"ok": True, "message": "Logged out. Credentials removed."} if args[2] == "logout" else {
                "ok": True, "session": {"authenticated": False}}
            return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

        with patch("steps.accounts.subprocess.run", side_effect=invoke):
            run_cli_logout_steps("/isolated/bin/kb", {"HOME": "/isolated"})
        self.assertEqual(commands, [["/isolated/bin/kb", "auth", "logout", "--json"],
                                    ["/isolated/bin/kb", "auth", "status", "--json"]])


if __name__ == "__main__":
    unittest.main()
