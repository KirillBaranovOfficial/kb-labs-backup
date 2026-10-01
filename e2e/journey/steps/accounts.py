"""Secured API journey for tenant roles, invite lifetime, and personal tokens."""
from __future__ import annotations

import time
import json
import http.cookiejar
import urllib.error
import urllib.request
from collections.abc import Callable
import subprocess

from .auth import _login, _request


def _browser_call(base_url: str, jar: http.cookiejar.CookieJar, path: str, *, method: str = "GET",
                  body: dict | None = None, csrf: bool = False, expected: int = 200) -> dict:
    headers = {"Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if csrf:
        token = next((cookie.value for cookie in jar if cookie.name == "kb_csrf"), None)
        if not token:
            raise AssertionError("browser session did not receive the kb_csrf cookie")
        headers["x-csrf-token"] = token
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base_url.rstrip("/") + path, data=data, headers=headers, method=method)
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    try:
        with opener.open(request, timeout=15) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    if status != expected:
        raise AssertionError(f"{method} {path}: expected HTTP {expected}, got {status}: {raw[:300]!r}")
    return json.loads(raw) if raw else {}


def _browser_login(base_url: str, email: str, password: str) -> http.cookiejar.CookieJar:
    jar = http.cookiejar.CookieJar()
    _browser_call(base_url, jar, "/auth/login", method="POST",
                  body={"email": email, "password": password})
    return jar


def run_password_and_session_steps(base_url: str, email: str, password: str) -> str:
    """Exercise browser-cookie password and session routes; return the changed password."""
    first = _browser_login(base_url, email, password)
    other = _browser_login(base_url, email, password)
    change = "JourneyChanged123!"
    _browser_call(base_url, first, "/auth/password/change", method="POST",
                  body={"currentPassword": password, "newPassword": change}, csrf=True)
    _browser_call(base_url, other, "/auth/me", expected=401)
    wrong = _browser_call(base_url, first, "/auth/password/change", method="POST",
                          body={"currentPassword": password, "newPassword": "WrongChanged123!"},
                          csrf=True, expected=400)
    if wrong.get("message") != "Current password is incorrect":
        raise AssertionError(f"incorrect current password returned unexpected message: {wrong!r}")
    _browser_call(base_url, first, "/auth/me")

    updated_first = _browser_login(base_url, email, change)
    updated_other = _browser_login(base_url, email, change)
    listed = _browser_call(base_url, updated_first, "/auth/sessions").get("sessions", [])
    if len(listed) < 2 or any(not all(key in family for key in
                                     ("familyId", "createdAt", "lastUsedAt", "userAgent", "isCurrent"))
                              for family in listed):
        raise AssertionError(f"session list did not include two complete session families: {listed!r}")
    current = next((family for family in listed if family.get("isCurrent")), None)
    other_family = next((family for family in listed if not family.get("isCurrent")), None)
    if not current or not other_family:
        raise AssertionError("session list did not identify current and other families")
    _browser_call(base_url, updated_first, f"/auth/sessions/{other_family['familyId']}/revoke",
                  method="POST", csrf=True)
    _browser_call(base_url, updated_other, "/auth/me", expected=401)
    return change


def run_cli_logout_steps(cli_path: str, env: dict[str, str]) -> None:
    def invoke(args: list[str]) -> subprocess.CompletedProcess[str]:
        return subprocess.run([cli_path, *args], env=env, text=True, capture_output=True, check=False)

    logout = invoke(["auth", "logout", "--json"])
    try:
        logout_payload = json.loads(logout.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(f"kb auth logout --json did not return JSON: {logout.stdout[-400:]}") from error
    if logout.returncode != 0 or logout_payload != {"ok": True, "message": "Logged out. Credentials removed."}:
        raise AssertionError(f"kb auth logout returned unexpected JSON: {logout_payload!r}")
    status = invoke(["auth", "status", "--json"])
    try:
        status_payload = json.loads(status.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(f"kb auth status --json did not return JSON: {status.stdout[-400:]}") from error
    if status.returncode != 0 or status_payload.get("session", {}).get("authenticated") is not False:
        raise AssertionError(f"kb auth status after logout returned unexpected JSON: {status_payload!r}")


def run_secured_account_steps(base_url: str, admin_email: str, admin_password: str,
                              member_email: str, member_password: str) -> None:
    admin_access, _ = _login(base_url, admin_email, admin_password)
    member_access, _ = _login(base_url, member_email, member_password)
    admin_user_id = _request(base_url, "/auth/me", token=admin_access).get("userId")
    if not admin_user_id:
        raise AssertionError("admin profile did not include userId")

    users = _request(base_url, "/auth/users", token=admin_access).get("users", [])
    member = next((user for user in users if user.get("email") == member_email), None)
    if not member or member.get("role") != "member":
        raise AssertionError(f"admin user list did not show {member_email} as a member")

    token_result = _request(base_url, "/auth/tokens", method="POST", token=member_access,
                            body={"name": "accounts journey", "expiresInDays": 90}, expected=201)
    token = token_result.get("token")
    token_id = token_result.get("tokenRecord", {}).get("tokenId")
    if not token or not token_id or not token_result["tokenRecord"].get("expiresAt"):
        raise AssertionError("personal token create response lacks a one-time secret or expiry metadata")
    profile = _request(base_url, "/auth/me", token=token)
    if profile.get("email") != member_email or not profile.get("tenantId"):
        raise AssertionError("personal token did not identify its owner and tenant")
    member_permissions = _request(base_url, "/auth/permissions", token=token).get("permissions", [])
    if "users:read" in member_permissions:
        raise AssertionError("member API token unexpectedly has users:read")

    _request(base_url, f"/auth/users/{member['userId']}/role", method="POST", token=admin_access,
             body={"role": "admin"})
    admin_permissions = _request(base_url, "/auth/permissions", token=token).get("permissions", [])
    if "users:read" not in admin_permissions:
        raise AssertionError("API token did not inherit the user's updated admin role")
    _request(base_url, f"/auth/users/{member['userId']}/role", method="POST", token=admin_access,
             body={"role": "member"})

    token_list = _request(base_url, "/auth/tokens", token=member_access).get("tokens", [])
    listed = next((record for record in token_list if record.get("tokenId") == token_id), None)
    if not listed or "token" in listed or not listed.get("expiresAt"):
        raise AssertionError("token list did not contain secret-free expiring metadata")
    _request(base_url, f"/auth/tokens/{token_id}/revoke", method="POST", token=member_access)
    _request(base_url, "/auth/me", token=token, expected=401)

    no_expiry = _request(base_url, "/auth/tokens", method="POST", token=member_access,
                         body={"name": "accounts journey no expiry"}, expected=201)
    no_expiry_token = no_expiry.get("token")
    no_expiry_id = no_expiry.get("tokenRecord", {}).get("tokenId")
    if not no_expiry_token or not no_expiry_id or "expiresAt" in no_expiry.get("tokenRecord", {}):
        raise AssertionError("token created without an expiry option unexpectedly has expiresAt")
    no_expiry_profile = _request(base_url, "/auth/me", token=no_expiry_token)
    if no_expiry_profile.get("email") != member_email:
        raise AssertionError("non-expiring personal token could not authenticate as its owner")
    _request(base_url, f"/auth/tokens/{no_expiry_id}/revoke", method="POST", token=member_access)

    disabled_admin = _request(base_url, f"/auth/users/{admin_user_id}/disable", method="POST",
                              token=admin_access, expected=409)
    if disabled_admin.get("error") != "last_admin":
        raise AssertionError("last-admin disable did not return error=last_admin")
    demoted_admin = _request(base_url, f"/auth/users/{admin_user_id}/role", method="POST", token=admin_access,
                             body={"role": "member"}, expected=409)
    if demoted_admin.get("error") != "last_admin":
        raise AssertionError("last-admin demotion did not return error=last_admin")

    invite = _request(base_url, "/auth/invites", method="POST", token=admin_access,
                      body={"email": "accounts-expiry@example.test", "role": "member"}, expected=201)
    invite_id = invite.get("inviteId")
    invite_records = _request(base_url, "/auth/invites", token=admin_access).get("invites", [])
    invite_record = next((record for record in invite_records if record.get("inviteId") == invite_id), None)
    if not invite_record or not 6.99 * 24 * 60 * 60 * 1000 <= invite_record.get("expiresAt", 0) - invite_record.get("createdAt", 0) <= 7.01 * 24 * 60 * 60 * 1000:
        raise AssertionError("invite did not have the seven-day default lifetime")
    token_value = invite["activationUrl"].rsplit("/activate/", 1)[1]
    _request(base_url, f"/auth/invites/{invite_id}/revoke", method="POST", token=admin_access)
    rejected = _request(base_url, "/auth/activate", method="POST",
                         body={"token": token_value, "password": "JourneyMember123!"}, expected=422)
    if rejected.get("error") != "invalid_invite":
        raise AssertionError("revoked invite did not return error=invalid_invite")

    expiring = _request(base_url, "/auth/invites", method="POST", token=admin_access,
                        body={"email": "accounts-expiring@example.test", "role": "member", "ttlMs": 1000},
                        expected=201)
    expiring_id = expiring.get("inviteId")
    expiring_records = _request(base_url, "/auth/invites", token=admin_access).get("invites", [])
    expiring_record = next((record for record in expiring_records if record.get("inviteId") == expiring_id), None)
    if not expiring_record:
        raise AssertionError("short-lived invite did not appear in the admin invite list")
    while expiring_record["expiresAt"] > time.time() * 1000:
        time.sleep(min(0.05, (expiring_record["expiresAt"] - time.time() * 1000) / 1000))
    expired = _request(base_url, "/auth/activate", method="POST",
                       body={"token": expiring["activationUrl"].rsplit("/activate/", 1)[1],
                             "password": "JourneyMember123!"}, expected=422)
    if expired.get("error") != "invalid_invite":
        raise AssertionError("expired invite did not return error=invalid_invite")
