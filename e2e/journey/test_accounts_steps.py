from __future__ import annotations

import sys
import unittest
from http.cookiejar import Cookie, CookieJar
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
from steps.accounts import _browser_call, run_password_and_session_steps, run_secured_account_steps


class SecuredAccountsJourneyTests(unittest.TestCase):
    def test_browser_call_forwards_secure_cookies_to_internal_http_gateway(self):
        jar = CookieJar()
        for name, value in (("kb_access", "access"), ("kb_csrf", "csrf")):
            jar.set_cookie(Cookie(
                version=0, name=name, value=value, port=None, port_specified=False,
                domain="gateway.test", domain_specified=False, domain_initial_dot=False,
                path="/", path_specified=True, secure=True, expires=None, discard=True,
                comment=None, comment_url=None, rest={}, rfc2109=False,
            ))

        class Response:
            status = 200

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            @staticmethod
            def read():
                return b"{}"

        class Opener:
            def open(self, request, timeout):
                self.request = request
                self.timeout = timeout
                return Response()

        opener = Opener()
        with patch("steps.accounts.urllib.request.build_opener", return_value=opener):
            _browser_call("http://gateway.test", jar, "/auth/me")

        self.assertIn("kb_access=access", opener.request.get_header("Cookie"))
        self.assertIn("kb_csrf=csrf", opener.request.get_header("Cookie"))

    # case: S-103.1, S-103.2, S-103.3, S-103.4, S-103.5, S-104.7, S-105.1, S-105.2, S-105.3, S-105.4
    def test_roles_tokens_last_admin_and_invite_revocation(self):
        calls: list[tuple[str, str, dict | None, int]] = []
        state = {"role": "member", "revoked": False}

        def request(_base: str, path: str, *, method: str = "GET", token: str | None = None,
                    body: dict | None = None, expected: int = 200):
            calls.append((method, path, body, expected))
            if path == "/auth/me":
                if token == "expiring-token":
                    return {} if state["revoked"] else {"email": "auth-reset-member@example.test", "tenantId": "tenant-1"}
                if token == "unexpired-token":
                    return {"email": "auth-reset-member@example.test", "tenantId": "tenant-1"}
                return {"userId": "root"}
            if path == "/auth/users" and method == "GET":
                return {"users": [{"userId": "member-1", "email": "auth-reset-member@example.test", "role": "member"}]}
            if path == "/auth/tokens" and method == "POST":
                if body and body.get("expiresInDays"):
                    return {"token": "expiring-token", "tokenRecord": {"tokenId": "token-1", "expiresAt": 9_000_000}}
                return {"token": "unexpired-token", "tokenRecord": {"tokenId": "token-2"}}
            if path == "/auth/permissions":
                return {"permissions": ["users:read"] if state["role"] == "admin" else ["projects:read"]}
            if path == "/auth/users/member-1/role" and method == "POST":
                state["role"] = body["role"] if body else "member"
                return {"ok": True}
            if path == "/auth/tokens" and method == "GET":
                return {"tokens": [{"tokenId": "token-1", "expiresAt": 9_000_000}]}
            if path.endswith("/token-1/revoke"):
                state["revoked"] = True
                return {"ok": True}
            if path.endswith("/token-2/revoke"):
                return {"ok": True}
            if path == "/auth/users/root/disable" or path == "/auth/users/root/role":
                return {"error": "last_admin"}
            if path == "/auth/invites" and method == "POST":
                if body and body.get("ttlMs"):
                    return {"inviteId": "expiry-invite", "activationUrl": "https://gateway.test/activate/expired-token"}
                return {"inviteId": "invite-1", "activationUrl": "https://gateway.test/activate/invite-token"}
            if path == "/auth/invites" and method == "GET":
                return {"invites": [
                    {"inviteId": "invite-1", "createdAt": 1_000, "expiresAt": 1_000 + 7 * 24 * 60 * 60 * 1000},
                    {"inviteId": "expiry-invite", "createdAt": 0, "expiresAt": 0},
                ]}
            if path.endswith("/invite-1/revoke"):
                return {"ok": True}
            if path == "/auth/activate":
                return {"error": "invalid_invite"}
            raise AssertionError(f"unexpected request: {method} {path} {body!r}")

        with patch("steps.accounts._login", return_value=("session-token", "refresh-token")), \
             patch("steps.accounts._request", side_effect=request):
            run_secured_account_steps("https://gateway.test", "admin@example.test", "admin-pass",
                                      "auth-reset-member@example.test", "member-pass")

        self.assertIn(("POST", "/auth/users/member-1/role", {"role": "admin"}, 200), calls)
        self.assertIn(("POST", "/auth/users/member-1/role", {"role": "member"}, 200), calls)
        self.assertIn(("POST", "/auth/users/root/disable", None, 409), calls)
        self.assertIn(("POST", "/auth/users/root/role", {"role": "member"}, 409), calls)
        self.assertIn(("POST", "/auth/activate", {"token": "invite-token", "password": "JourneyMember123!"}, 422), calls)

    # case: S-101.1, S-101.2, S-101.3, S-102.1, S-102.2
    def test_password_change_revokes_other_family_and_session_revoke_is_owner_scoped(self):
        calls: list[tuple[str, str, int]] = []
        passwords = []
        login_count = 0
        revoked = {"other": False}

        def login(_base, _email, password):
            nonlocal login_count
            login_count += 1
            passwords.append(password)
            return login_count

        def browser_call(_base, jar, path, *, method="GET", body=None, csrf=False, expected=200):
            calls.append((method, path, expected))
            if path == "/auth/password/change" and expected == 400:
                return {"message": "Current password is incorrect"}
            if path == "/auth/me" and jar == 2:
                return {"error": "unauthorized"}
            if path == "/auth/sessions":
                return {"sessions": [
                    {"familyId": "current", "createdAt": 1, "lastUsedAt": 2, "userAgent": "journey", "isCurrent": True},
                    {"familyId": "other", "createdAt": 1, "lastUsedAt": 2, "userAgent": "journey", "isCurrent": False},
                ]}
            if path == "/auth/sessions/other/revoke":
                revoked["other"] = True
                return {"ok": True}
            if path == "/auth/me" and jar == 4 and revoked["other"]:
                return {"error": "unauthorized"}
            return {"ok": True}

        with patch("steps.accounts._browser_login", side_effect=login), \
             patch("steps.accounts._browser_call", side_effect=browser_call):
            changed = run_password_and_session_steps("https://gateway.test", "member@example.test", "MemberPass123!")

        self.assertIsNone(changed)
        self.assertEqual(passwords, ["MemberPass123!", "MemberPass123!",
                                     "JourneyChanged123!", "JourneyChanged123!"])
        self.assertIn(("POST", "/auth/password/change", 200), calls)
        self.assertIn(("GET", "/auth/me", 401), calls)
        self.assertIn(("POST", "/auth/sessions/other/revoke", 200), calls)


if __name__ == "__main__":
    unittest.main()
