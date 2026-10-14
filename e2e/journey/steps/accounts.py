"""Secured API journey for tenant roles, invite lifetime, and personal tokens."""
from __future__ import annotations

from kb_invocation import kb_command

import time
import json
import http.cookiejar
import urllib.error
import urllib.request
from collections.abc import Callable
import os

from .auth import _login, _request
from invocations import (
    CompletedProcess,
    decode_ux_error_result,
    decode_ux_success_result,
    open_http,
    read_ux_cli_result,
    read_ux_http_result,
    run_command,
    store_report_secret,
)


def _browser_call(base_url: str, jar: http.cookiejar.CookieJar, path: str, *, method: str = "GET",
                  body: dict | None = None, csrf: bool = False, expected: int = 200) -> dict:
    headers = {"Accept": "application/json"}
    # The journey talks to the gateway over its internal HTTP listener. Its
    # production Secure cookies must still be forwarded as a browser would
    # send them through the HTTPS ingress.
    cookie_header = "; ".join(f"{cookie.name}={cookie.value}" for cookie in jar)
    if cookie_header:
        headers["Cookie"] = cookie_header
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
    status, raw, _ = open_http(opener.open, request, timeout=15, env=os.environ)
    if status != expected:
        raise AssertionError(f"{method} {path}: expected HTTP {expected}, got {status}: {raw[:300]!r}")
    envelope = read_ux_http_result(raw, status)
    return (decode_ux_success_result(envelope) if 200 <= status < 300
            else decode_ux_error_result(envelope))


def _browser_login(base_url: str, email: str, password: str) -> http.cookiejar.CookieJar:
    jar = http.cookiejar.CookieJar()
    _browser_call(base_url, jar, "/auth/login", method="POST",
                  body={"email": email, "password": password})
    return jar


def run_password_and_session_steps(base_url: str, email: str, password: str) -> None:
    """Exercise browser-cookie password and session routes, including the new password."""
    first = _browser_login(base_url, email, password)
    other = _browser_login(base_url, email, password)
    change = "JourneyChanged123!"
    _browser_call(base_url, first, "/auth/password/change", method="POST",
                  body={"currentPassword": password, "newPassword": change}, csrf=True)
    _browser_call(base_url, other, "/auth/me", expected=401)
    wrong = _browser_call(base_url, first, "/auth/password/change", method="POST",
                          body={"currentPassword": password, "newPassword": "WrongChanged123!"},
                          csrf=True, expected=400)
    if (wrong.get("code") != "KB_AUTH_INVALID_CURRENT_PASSWORD"
            or wrong.get("message") != "Current password is incorrect"
            or wrong.get("hint") != "Check the current password and retry the password change."):
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


def run_cli_logout_steps(cli_path: str, env: dict[str, str]) -> None:
    def invoke(args: list[str]) -> CompletedProcess[str]:
        return run_command([cli_path, *args], env=env)


    email = env["KB_JOURNEY_ADMIN_EMAIL"]
    password = env["KB_JOURNEY_ADMIN_PASSWORD"]
    gateway_url = env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000")
    login = run_command(kb_command([ "auth", "login", "--gateway-url", gateway_url,
                         "--email", email, "--password-stdin", "--json"], executable=cli_path),
                        env=env, input=password + "\n")
    try:
        login_payload = decode_ux_success_result(read_ux_cli_result(login))
    except ValueError as error:
        raise AssertionError(f"kb auth login --json did not return JSON: {login.stdout[-400:]}") from error
    if login.returncode != 0 or login_payload.get("email") != email or login_payload.get("gatewayUrl") != gateway_url:
        raise AssertionError(f"kb auth login did not establish the session for S-102.3: {login_payload!r}")

    logout = invoke(["auth", "logout", "--json"])
    try:
        logout_payload = decode_ux_success_result(read_ux_cli_result(logout))
    except ValueError as error:
        raise AssertionError(f"kb auth logout --json did not return JSON: {logout.stdout[-400:]}") from error
    if logout.returncode != 0 or logout_payload != {"message": "Logged out. Credentials removed."}:
        raise AssertionError(f"kb auth logout returned unexpected JSON: {logout_payload!r}")
    status = invoke(["auth", "status", "--json"])
    try:
        status_payload = decode_ux_success_result(read_ux_cli_result(status))
    except ValueError as error:
        raise AssertionError(f"kb auth status --json did not return JSON: {status.stdout[-400:]}") from error
    if status.returncode != 0 or status_payload.get("session", {}).get("authenticated") is not False:
        raise AssertionError(f"kb auth status after logout returned unexpected JSON: {status_payload!r}")


def run_secured_account_steps(base_url: str, admin_email: str, admin_password: str,
                              member_email: str, member_password: str) -> None:
    """Exercise the account API journeys as separate behavior groups."""
    run_api_token_lifecycle_steps(base_url, admin_email, admin_password, member_email, member_password)
    run_last_admin_guard_steps(base_url, admin_email, admin_password)
    run_invite_lifecycle_steps(base_url, admin_email, admin_password)


def run_last_admin_guard_steps(base_url: str, admin_email: str, admin_password: str) -> None:
    admin_access, _ = _login(base_url, admin_email, admin_password)
    admin = _request(base_url, "/auth/me", token=admin_access)
    admin_user_id = admin.get("userId")
    if not admin_user_id:
        raise AssertionError("admin profile did not include userId")
    disabled = _request(base_url, f"/auth/users/{admin_user_id}/disable", method="POST",
                        token=admin_access, expected=409)
    demoted = _request(base_url, f"/auth/users/{admin_user_id}/role", method="POST",
                       token=admin_access, body={"role": "member"}, expected=409)
    if disabled.get("code") != "KB_AUTH_LAST_ADMIN" or demoted.get("code") != "KB_AUTH_LAST_ADMIN":
        raise AssertionError("last-admin guard did not return KB_AUTH_LAST_ADMIN for both operations")


def run_api_token_lifecycle_steps(base_url: str, admin_email: str, admin_password: str,
                                 member_email: str, member_password: str) -> None:
    admin_access, _ = _login(base_url, admin_email, admin_password)
    member_access, _ = _login(base_url, member_email, member_password)
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
    store_report_secret(os.environ, "KB_JOURNEY_PERSONAL_API_TOKEN", token, "personal API token")
    profile = _request(base_url, "/auth/me", token=token)
    if profile.get("email") != member_email or not profile.get("tenantId"):
        raise AssertionError("personal token did not identify its owner and tenant")
    permissions = _request(base_url, "/auth/permissions", token=token).get("permissions", [])
    if "users:read" in permissions:
        raise AssertionError("member API token unexpectedly has users:read")

    _request(base_url, f"/auth/users/{member['userId']}/role", method="POST", token=admin_access,
             body={"role": "admin"})
    permissions = _request(base_url, "/auth/permissions", token=token).get("permissions", [])
    if "users:read" not in permissions:
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
    store_report_secret(os.environ, "KB_JOURNEY_NON_EXPIRING_API_TOKEN", no_expiry_token,
                        "non-expiring personal API token")
    if _request(base_url, "/auth/me", token=no_expiry_token).get("email") != member_email:
        raise AssertionError("non-expiring personal token could not authenticate as its owner")
    _request(base_url, f"/auth/tokens/{no_expiry_id}/revoke", method="POST", token=member_access)


def run_invite_lifecycle_steps(base_url: str, admin_email: str, admin_password: str) -> None:
    admin_access, _ = _login(base_url, admin_email, admin_password)
    invite = _request(base_url, "/auth/invites", method="POST", token=admin_access,
                      body={"email": "accounts-expiry@example.test", "role": "member"}, expected=201)
    invite_id = invite.get("inviteId")
    records = _request(base_url, "/auth/invites", token=admin_access).get("invites", [])
    invite_record = next((record for record in records if record.get("inviteId") == invite_id), None)
    if not invite_record or not 6.99 * 24 * 60 * 60 * 1000 <= invite_record.get("expiresAt", 0) - invite_record.get("createdAt", 0) <= 7.01 * 24 * 60 * 60 * 1000:
        raise AssertionError("invite did not have the seven-day default lifetime")
    token = invite["activationUrl"].rsplit("/activate/", 1)[1]
    store_report_secret(os.environ, "KB_JOURNEY_INVITE_TOKEN", token, "invite activation token")
    _request(base_url, f"/auth/invites/{invite_id}/revoke", method="POST", token=admin_access)
    rejected = _request(base_url, "/auth/activate", method="POST",
                        body={"token": token, "password": "JourneyMember123!"}, expected=422)
    if rejected.get("code") != "KB_AUTH_INVALID_INVITE":
        raise AssertionError("revoked invite did not return KB_AUTH_INVALID_INVITE")

    expiring = _request(base_url, "/auth/invites", method="POST", token=admin_access,
                        body={"email": "accounts-expiring@example.test", "role": "member", "ttlMs": 1000},
                        expected=201)
    records = _request(base_url, "/auth/invites", token=admin_access).get("invites", [])
    expiring_record = next((record for record in records if record.get("inviteId") == expiring.get("inviteId")), None)
    if not expiring_record:
        raise AssertionError("short-lived invite did not appear in the admin invite list")
    expiring_token = expiring["activationUrl"].rsplit("/activate/", 1)[1]
    store_report_secret(os.environ, "KB_JOURNEY_EXPIRING_INVITE_TOKEN", expiring_token,
                        "expiring invite activation token")
    while expiring_record["expiresAt"] > time.time() * 1000:
        time.sleep(min(0.05, (expiring_record["expiresAt"] - time.time() * 1000) / 1000))
    expired = _request(base_url, "/auth/activate", method="POST",
                       body={"token": expiring_token,
                             "password": "JourneyMember123!"}, expected=422)
    if expired.get("code") != "KB_AUTH_INVALID_INVITE":
        raise AssertionError("expired invite did not return KB_AUTH_INVALID_INVITE")
