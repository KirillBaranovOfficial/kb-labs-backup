"""HTTP auth journey steps for the fresh-install journey runner.

Interface: call ``run_secured_auth_steps(base_url, admin_email, admin_password,
member_email, restart_host)`` after installing and starting a secured platform.
``restart_host`` stops and starts the host, then returns its reachable base URL.
Call ``run_local_auth_steps(base_url)`` after a local install.
"""
from __future__ import annotations

import json
import urllib.error
import urllib.request
from collections.abc import Callable


def _request(base_url: str, path: str, *, method: str = "GET", token: str | None = None,
             body: dict | None = None, expected: int = 200) -> dict:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body is not None else None
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base_url.rstrip("/") + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    if status != expected:
        raise AssertionError(f"{method} {path}: expected HTTP {expected}, got {status}: {raw[:300]!r}")
    if not raw:
        return {}
    return json.loads(raw)


def _login(base_url: str, email: str, password: str) -> tuple[str, str]:
    result = _request(base_url, "/auth/login/cli", method="POST", body={"email": email, "password": password})
    return result["accessToken"], result["refreshToken"]


def run_local_auth_steps(base_url: str) -> None:
    """Prove that loopback/local mode needs no login and identifies local-admin."""
    profile = _request(base_url, "/auth/me")
    user = profile.get("user", profile)
    if user.get("userId") != "local-admin" and user.get("handle") != "local-admin":
        raise AssertionError(f"local /auth/me did not identify local-admin: {profile!r}")


def run_secured_auth_steps(base_url: str, admin_email: str, admin_password: str,
                           member_email: str, restart_host: Callable[[], str]) -> None:
    """Prove install, role, invite, reset, persistence, and logout behavior."""
    _request(base_url, "/auth/me", expected=401)
    admin_access, admin_refresh = _login(base_url, admin_email, admin_password)
    me = _request(base_url, "/auth/me", token=admin_access)
    if me.get("user", {}).get("email") != admin_email:
        raise AssertionError(f"admin /auth/me returned the wrong identity: {me!r}")
    permissions = _request(base_url, "/auth/permissions", token=admin_access)
    if not permissions.get("permissions"):
        raise AssertionError("admin permissions response was empty")

    invite = _request(base_url, "/auth/invites", method="POST", token=admin_access,
                      body={"email": member_email, "role": "member"}, expected=201)
    activation_url = invite.get("activationUrl", "")
    if "/activate/" not in activation_url:
        raise AssertionError(f"invite activation link does not match the SPA route: {activation_url!r}")
    activation_token = activation_url.rsplit("/activate/", 1)[1]
    _request(base_url, "/auth/activate", method="POST",
             body={"token": activation_token, "password": "JourneyMember123!"})
    member_access, _ = _login(base_url, member_email, "JourneyMember123!")
    _request(base_url, "/auth/users", token=member_access, expected=403)

    users = _request(base_url, "/auth/users", token=admin_access).get("users", [])
    member = next((user for user in users if user.get("email") == member_email), None)
    if not member:
        raise AssertionError(f"invited user is missing from admin user list: {users!r}")
    reset = _request(base_url, f"/auth/users/{member['userId']}/password-reset-link",
                     method="POST", token=admin_access, expected=201)
    reset_token = reset.get("resetUrl", "").rsplit("/reset-password/", 1)[-1]
    if not reset_token:
        raise AssertionError("admin reset response did not include a reset link")
    _request(base_url, "/auth/password/reset", method="POST",
             body={"token": reset_token, "newPassword": "JourneyReplacement123!"})
    _request(base_url, "/auth/me", token=member_access, expected=401)
    _request(base_url, "/auth/login/cli", method="POST",
             body={"email": member_email, "password": "JourneyMember123!"}, expected=401)
    _login(base_url, member_email, "JourneyReplacement123!")

    restarted_url = restart_host()
    _request(restarted_url, "/auth/login/cli", method="POST",
             body={"email": admin_email, "password": admin_password})
    _request(restarted_url, "/auth/login/cli", method="POST",
             body={"email": member_email, "password": "JourneyReplacement123!"})
    _request(restarted_url, "/auth/me", token=admin_access, expected=401)
    _request(restarted_url, "/auth/logout/cli", method="POST", body={"refreshToken": admin_refresh})
    _request(restarted_url, "/auth/refresh/cli", method="POST",
             body={"refreshToken": admin_refresh}, expected=401)
