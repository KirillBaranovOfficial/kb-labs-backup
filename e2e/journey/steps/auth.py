"""HTTP auth journey steps for the fresh-install journey runner.

Interface: call ``run_secured_auth_steps(base_url, admin_email, admin_password,
member_email, restart_host)`` after installing and starting a secured platform.
``restart_host`` stops and starts the host, then returns its reachable base URL.
Call ``run_local_auth_steps(base_url)`` after a local install.
"""
from __future__ import annotations

import json
import subprocess
import urllib.error
import urllib.request
from collections.abc import Callable
from pathlib import Path


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


def run_cli_auth_status(cli_path: str, env: dict[str, str], *, authenticated: bool,
                        email: str | None = None, base_url: str | None = None,
                        password: str | None = None) -> None:
    """Prove the CLI status surface in local and secured mode."""
    def invoke(args: list[str]) -> subprocess.CompletedProcess[str]:
        return subprocess.run([cli_path, *args], env=env, text=True, capture_output=True, check=False)

    if authenticated:
        if not base_url or not email or not password:
            raise ValueError("secured CLI auth status requires gateway URL, email, and password")
        login = invoke(["auth", "login", "--gateway-url", base_url, "--email", email, "--password", password])
        if login.returncode != 0:
            raise AssertionError(f"kb auth login failed: {(login.stdout + login.stderr)[-400:]}")
    status = invoke(["auth", "status", "--json"])
    try:
        status_payload = json.loads(status.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(f"kb auth status --json did not return JSON: {status.stdout[-400:]}") from error
    session = status_payload.get("session", {})
    if status.returncode != 0 or status_payload.get("ok") is not True or session.get("authenticated") is not authenticated:
        raise AssertionError(f"kb auth status returned unexpected JSON: {status_payload!r}")
    if email and session.get("email") != email:
        raise AssertionError(f"kb auth status returned the wrong email: {session!r}")
    if authenticated:
        tokens = invoke(["auth", "tokens", "--json"])
        try:
            tokens_payload = json.loads(tokens.stdout)
        except json.JSONDecodeError as error:
            raise AssertionError(f"kb auth tokens --json did not return JSON: {tokens.stdout[-400:]}") from error
        if tokens.returncode != 0 or tokens_payload.get("ok") is not True:
            raise AssertionError(f"kb auth tokens returned unexpected JSON: {tokens_payload!r}")


def run_cli_auth_forbidden(cli_path: str, env: dict[str, str], *, user_id: str = "member-1") -> None:
    """A logged-in member must get the gateway's typed permission denial."""
    args = [cli_path, "auth", "reset-link", "--user-id", user_id, "--json"]
    result = subprocess.run(args, env=env, text=True, capture_output=True, check=False)
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError("member auth reset-link did not return JSON") from error
    error = payload.get("error") if isinstance(payload, dict) else None
    if (result.returncode == 0 or not isinstance(error, dict)
            or error.get("code") != "KB_AUTH_FORBIDDEN"
            or error.get("details", {}).get("permission") != "users:write"):
        raise AssertionError(f"member auth reset-link returned an unexpected result: {payload!r}")


def _combined(result: subprocess.CompletedProcess[str]) -> str:
    """Output of a command whose stderr may already be merged into stdout (stderr is None)."""
    return (result.stdout or "") + (result.stderr or "")


def assert_platform_auth_required(result: subprocess.CompletedProcess[str], command: list[str]) -> None:
    """Require the exact anonymous CLI auth envelope promised by S-041.7."""
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError(f"unauthenticated {' '.join(command)} did not return JSON") from error
    failure = payload.get("error") if isinstance(payload, dict) else None
    if (result.returncode == 0 or not isinstance(failure, dict)
            or failure.get("code") != "KB_AUTH_REQUIRED"
            or failure.get("message") != "Authentication is required."
            or failure.get("hint") != 'Run "kb auth login".'):
        raise AssertionError(f"unauthenticated {' '.join(command)} returned an unexpected result: {payload!r}")


def assert_cli_session_absent(result: subprocess.CompletedProcess[str]) -> None:
    """Confirm the CLI product reports no authenticated human session."""
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise AssertionError("kb auth status --json did not return JSON") from error
    session = payload.get("session") if isinstance(payload, dict) else None
    if result.returncode != 0 or not isinstance(payload, dict) or payload.get("ok") is not True:
        raise AssertionError(f"kb auth status --json did not report an unauthenticated session: {payload!r}")
    if not isinstance(session, dict) or session.get("authenticated") is not False:
        raise AssertionError("kb auth status --json reported that the CLI session is authenticated")


def run_bootstrap_recovery_steps(launcher: str, platform_root: str, email: str,
                                 old_password: str, new_password: str, env: dict[str, str],
                                 invoke: Callable[..., tuple[subprocess.CompletedProcess[str], float]],
                                 *, on_valid_password: Callable[[str], None]) -> None:
    """Exercise stopped-host recovery, recovered login, and the running-host guard."""
    login = _request("http://127.0.0.1:4000", "/auth/login/cli", method="POST",
                     body={"email": email, "password": old_password})
    if not login.get("accessToken"):
        raise AssertionError("bootstrap admin login did not return an access token")
    initial_profile = _request("http://127.0.0.1:4000", "/auth/me", token=login["accessToken"])
    tenant = initial_profile.get("tenantId")
    if not tenant:
        raise AssertionError("bootstrap administrator profile did not include tenantId")
    stopped, _ = invoke([launcher, "stop", "--platform-root", platform_root], env=env)
    if stopped.returncode != 0:
        raise AssertionError(f"kb-create stop failed: {_combined(stopped)[-400:]}")
    recover_args = [launcher, "doctor", "--platform-root", platform_root, "--recover", "--email", email,
                    "--tenant", tenant, "--password-stdin", "--yes"]
    recovered: subprocess.CompletedProcess[str] | None = None
    recovery_error: Exception | None = None
    try:
        recovered, _ = invoke(recover_args, env=env, input_text=new_password + "\n")
    except Exception as error:
        recovery_error = error
    finally:
        started, _ = invoke([launcher, "start", "--platform-root", platform_root], env=env)
    if started.returncode != 0:
        raise AssertionError(f"kb-create start failed after recovery: {_combined(started)[-400:]}")

    valid_password = ""
    for password in (new_password, old_password):
        try:
            access, _ = _login("http://127.0.0.1:4000", email, password)
            profile = _request("http://127.0.0.1:4000", "/auth/me", token=access)
        except Exception:
            continue
        if profile.get("email") == email and profile.get("tenantId") == tenant:
            valid_password = password
            break
    if not valid_password:
        raise AssertionError("neither the recovered nor the original bootstrap password authenticates")
    on_valid_password(valid_password)

    if recovery_error:
        raise AssertionError("kb-create doctor --recover could not be run") from recovery_error
    if recovered is None or recovered.returncode != 0:
        output = "" if recovered is None else _combined(recovered)
        for secret in (old_password, new_password):
            output = output.replace(secret, "[redacted]")
        raise AssertionError(f"kb-create doctor --recover failed: {output[-400:]}")
    if valid_password != new_password:
        raise AssertionError("kb-create doctor --recover exited successfully but the new password did not authenticate")
    if new_password in _combined(recovered):
        raise AssertionError("kb-create doctor --recover echoed the supplied password")
    running_recovery, _ = invoke(recover_args, env=env, input_text=new_password + "\n")
    output = _combined(running_recovery).lower()
    if running_recovery.returncode == 0 or "stopped" not in output or "kb-create stop" not in output or "kb-create start" not in output:
        raise AssertionError("running-host recovery did not explain that the host must be stopped and restarted")


def run_secured_auth_steps(base_url: str, admin_email: str, admin_password: str,
                           member_email: str, restart_host: Callable[[], str]) -> None:
    """Prove install, role, invite, reset, persistence, and logout behavior."""
    _request(base_url, "/auth/me", expected=401)
    admin_access, admin_refresh = _login(base_url, admin_email, admin_password)
    me = _request(base_url, "/auth/me", token=admin_access)
    if me.get("email") != admin_email:
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
    restarted_admin = _request(restarted_url, "/auth/me", token=admin_access)
    if restarted_admin.get("email") != admin_email:
        raise AssertionError(f"admin session did not survive host restart: {restarted_admin!r}")
    _request(restarted_url, "/auth/login/cli", method="POST",
             body={"email": member_email, "password": "JourneyReplacement123!"})
    logout = _request(restarted_url, "/auth/logout/cli", method="POST", body={"refreshToken": admin_refresh})
    if logout.get("ok") is not True:
        raise AssertionError(f"CLI logout did not confirm session revocation: {logout!r}")
    _request(restarted_url, "/auth/me", token=admin_access, expected=401)
    _request(restarted_url, "/auth/refresh/cli", method="POST",
             body={"refreshToken": admin_refresh}, expected=401)
    fresh_admin_access, _ = _login(restarted_url, admin_email, admin_password)
    fresh_admin = _request(restarted_url, "/auth/me", token=fresh_admin_access)
    if fresh_admin.get("email") != admin_email:
        raise AssertionError(f"fresh admin login after logout returned the wrong identity: {fresh_admin!r}")
