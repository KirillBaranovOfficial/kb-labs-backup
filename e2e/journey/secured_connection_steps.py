"""Installed secured journey coverage for connection and session cases."""
from __future__ import annotations

import json
import os
from pathlib import Path
import tempfile
from datetime import datetime, timezone
from urllib.parse import urlparse
import urllib.request

from invocations import (decode_ux_error_result, decode_ux_success_result, invocation_context,
                         open_http, read_ux_cli_result, read_ux_http_result, run_command)
from service_steps import Journey
from steps.accounts import _browser_call, _browser_login
from steps.auth import _login, _request


def _expect(value: bool, message: str) -> None:
    if not value:
        raise AssertionError(message)


def _cli(cli: str, env: dict[str, str], *args: str):
    return run_command([cli, *args], env=env, timeout=90)


def _journey_home(env: dict[str, str]) -> Path:
    """Return the isolated HOME passed to child commands, not the runner's HOME."""
    return Path(env["HOME"])


def _json(result, description: str) -> dict:
    try:
        value = read_ux_cli_result(result.stdout, result.returncode)
    except json.JSONDecodeError as error:
        raise AssertionError(f"{description} did not return JSON") from error
    _expect(isinstance(value, dict), f"{description} did not return a JSON object")
    envelope = value
    return envelope


def _response(base: str, path: str, *, method: str = "GET", token: str = "",
              body: dict | None = None) -> tuple[int, dict]:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body is not None else None
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    status, raw, _ = open_http(urllib.request.urlopen, request, timeout=15, env=os.environ)
    try:
        envelope = read_ux_http_result(raw, status)
    except json.JSONDecodeError as error:
        raise AssertionError(f"{method} {path} did not return JSON") from error
    payload = (decode_ux_success_result(envelope) if 200 <= status < 300
               else decode_ux_error_result(envelope))
    _expect(isinstance(payload, dict), f"{method} {path} did not return a JSON object")
    return status, payload


def validate_case_response(case_id: str, status: int, value: dict, *, expected: str = "") -> None:
    """Validate observable response contracts; split by claim for fake-failure tests."""
    contracts = {
        "S-042.1": lambda: status == 201 and bool(value.get("inviteId"))
        and urlparse(str(value.get("activationUrl", ""))).netloc == expected,
        "S-042.2": lambda: status == 200 and value.get("ok") is True,
        "S-042.3": lambda: status == 422 and value.get("error") == "invalid_invite"
        and value.get("message") == "Invalid or expired invite",
        "S-042.5": lambda: status == 200 and value.get("ok") is True,
        "S-042.6": lambda: status == 400 and value.get("error") == "invalid_password_reset"
        and value.get("message") == "This reset link is invalid, expired, or already used",
        "S-005.4": lambda: status == 200 and "projects:read" in value.get("permissions", [])
        and "users:read" not in value.get("permissions", []),
        "S-005.5": lambda: status == 403 and isinstance(value.get("error"), dict)
        and value["error"].get("code") == "KB_AUTH_FORBIDDEN"
        and value["error"].get("message") == "Permission denied: users:read"
        and bool(value["error"].get("hint")),
    }
    if case_id not in contracts:
        raise ValueError(f"no HTTP response contract for {case_id}")
    _expect(contracts[case_id](), f"{case_id} response did not match the case contract")


def validate_cli_case(case_id: str, returncode: int, output: str, payload: dict | None = None,
                      *, email: str = "", gateway: str = "", root: str = "") -> None:
    """Validate CLI observables independently so fake failures exercise each claim."""
    payload = payload or {}
    session = payload.get("session", {})
    checks = {
        "S-005.1": returncode == 0 and payload.get("ok") is True and root == expected_root(payload),
        "S-005.2": returncode == 0,
        "S-005.3": returncode == 0 and session.get("authenticated") is True
        and session.get("email") == email and session.get("gatewayUrl") == gateway,
        "S-031.1": returncode == 0 and session.get("authenticated") is True
        and session.get("tokenExpired") is True and session.get("gatewayUrl") == gateway,
        "S-031.2": returncode == 0 and bool(output.strip()),
        "S-031.3": returncode != 0 and "kb auth login" in output.lower()
        and "stack" not in output.lower() and "at " not in output,
        "S-031.4": returncode != 0 and "kb auth login --gateway-url" in output,
        "S-042.4": returncode == 0 and payload.get("ok") is True
        and bool(payload.get("resetUrl")) and isinstance(payload.get("expiresAt"), (int, float)),
    }
    if case_id not in checks:
        raise ValueError(f"no CLI response contract for {case_id}")
    _expect(checks[case_id], f"{case_id} CLI result did not match the case contract")


def expected_root(payload: dict) -> str:
    receipt = payload.get("receipt", {})
    return str(receipt.get("plan", {}).get("request", {}).get("platformRoot", ""))


def _record(journey: Journey, case_id: str, name: str, check) -> None:
    journey.step(name, "auth.secured", check, cases=[case_id])


def register_secured_connection_steps(journey: Journey, env: dict[str, str]) -> None:
    """Register exact S-005, S-031, and S-042 journey claims.

    The flow intentionally provisions the invited account before connecting it,
    then exercises the real CLI session file and gateway refresh-token routes.
    """
    base = env.get("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
    cli = env.get("KB_JOURNEY_CLI_PATH", str(Path.home() / ".local/bin/kb"))
    admin_email = env.get("KB_JOURNEY_ADMIN_EMAIL", "journey-admin@example.test")
    admin_password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "")
    member_email = "coverage-member@example.test"
    member_password = "MemberPass123!"

    state: dict[str, str] = {}

    def invite():
        with invocation_context(step_id="journey.setup"):
            access, _ = _login(base, admin_email, admin_password)
            jar = _browser_login(base, admin_email, admin_password)
        response = _browser_call(base, jar, "/auth/invites", method="POST",
                                 body={"email": member_email, "role": "member"}, csrf=True,
                                 expected=201)
        validate_case_response("S-042.1", 201, response, expected=urlparse(base).netloc)
        _expect(bool(response.get("inviteId")), "invite response omitted inviteId")
        url = str(response.get("activationUrl", ""))
        _expect(urlparse(url).netloc == urlparse(base).netloc, "activation URL was not on the gateway host")
        token = url.rstrip("/").rsplit("/", 1)[-1]
        _expect(bool(token), "activation URL omitted its token")
        state.update(admin_access=access, invite_token=token)
        return None

    def activate():
        with invocation_context(step_id="journey.setup"):
            jar = _browser_login(base, admin_email, admin_password)
        body = _browser_call(base, jar,
                             "/auth/activate", method="POST",
                             body={"token": state["invite_token"], "password": member_password},
                             expected=200)
        validate_case_response("S-042.2", 200, body)
        _expect(body.get("ok") is True, "activation did not return ok=true")
        profile = _browser_call(base, jar, "/auth/me")
        user = profile.get("user", profile)
        _expect(user.get("email") == member_email and bool(user.get("tenantId")),
                "activation session did not identify the member and tenant")
        with invocation_context(step_id="journey.setup"):
            access, _ = _login(base, member_email, member_password)
        state["member_access"] = access
        return None

    def replay_invite():
        status, value = _response(base, "/auth/activate", method="POST",
                                  body={"token": state["invite_token"], "password": member_password})
        validate_case_response("S-042.3", status, value)
        return None

    _record(journey, "S-042.1", "invite member with admin session", invite)
    _record(journey, "S-042.2", "activate invitation and resolve member session", activate)
    _record(journey, "S-042.3", "reject consumed activation token", replay_invite)

    def reset_link():
        started = datetime.now(timezone.utc)
        with invocation_context(step_id="journey.setup"):
            users = _request(base, "/auth/users", token=state["admin_access"]).get("users", [])
        member = next((user for user in users if user.get("email") == member_email), None)
        _expect(isinstance(member, dict) and bool(member.get("userId")), "admin could not find invited user")
        result = _cli(cli, env, "auth", "reset-link", "--user-id", str(member["userId"]), "--json")
        value = _json(result, "kb auth reset-link")
        reset_url = str(value.get("resetUrl", ""))
        expires = value.get("expiresAt")
        if reset_url:
            state["reset_token"] = reset_url.rstrip("/").rsplit("/", 1)[-1]
        validate_cli_case("S-042.4", result.returncode, result.stdout, value)
        _expect(bool(reset_url) and isinstance(expires, (int, float)),
                "reset-link JSON omitted expiresAt required by S-042.4")
        expires_at = (datetime.fromtimestamp(float(expires) / 1000, timezone.utc)
                      if float(expires) > 10_000_000_000
                      else datetime.fromtimestamp(float(expires), timezone.utc))
        remaining = (expires_at - started).total_seconds()
        _expect(24 * 3600 - 60 <= remaining <= 24 * 3600 + 60,
                "reset link did not expire 24 hours after creation")
        token = reset_url.rstrip("/").rsplit("/", 1)[-1]
        _expect(bool(token), "reset URL omitted its token")
        state.update(reset_token=token, reset_expires=str(expires))
        return None

    def perform_reset():
        with invocation_context(step_id="S-042.5"):
            old_access, old_refresh = _login(base, member_email, member_password)
            reset = _request(base, "/auth/password/reset", method="POST",
                             body={"token": state["reset_token"], "newPassword": "NewMemberPass123!"})
            validate_case_response("S-042.5", 200, reset)
            old_status, _ = _response(base, "/auth/me", token=old_access)
            _expect(old_status == 401, "password reset did not revoke the member access session")
            status, _ = _response(base, "/auth/refresh/cli", method="POST",
                                  body={"refreshToken": old_refresh})
            _expect(status == 401, "password reset did not revoke the member refresh session")
            _login(base, member_email, "NewMemberPass123!")
        return None

    def replay_reset():
        status, value = _response(base, "/auth/password/reset", method="POST",
                                  body={"token": state["reset_token"], "newPassword": "NewMemberPass123!"})
        validate_case_response("S-042.6", status, value)
        return None

    _record(journey, "S-042.4", "issue CLI password reset link", reset_link)
    _record(journey, "S-042.5", "consume reset link and verify revoked sessions", perform_reset)
    _record(journey, "S-042.6", "reject consumed password reset token", replay_reset)

    # Keep the secured CLI profile independent from the reset-link flow.
    def apply_isolated_platform():
        home = _journey_home(env)
        receipt = json.loads((Path(env["KB_JOURNEY_PLATFORM_ROOT"]) / ".kb/v2/receipt.json").read_text())
        request_data = json.loads(json.dumps(receipt["plan"]["request"]))
        with tempfile.TemporaryDirectory(prefix="s005-platform-", dir=home) as temp:
            root = Path(temp) / "platform"
            request_file = Path(temp) / "request.json"
            request_file.write_text(json.dumps(request_data) + "\n")
            index = home / ".local/share/kb-create/release-index.json"
            result = run_command([env.get("KB_JOURNEY_KB_CREATE_PATH", str(home / ".local/bin/kb-create")),
                                  "apply", "--index", str(index), "--input", str(request_file),
                                  "--request-platform-root", str(root), "--json"],
                                 env=env, cwd=home, timeout=600)
            value = _json(result, "kb-create apply")
            actual = value.get("receipt", {}).get("plan", {}).get("request", {}).get("platformRoot")
            validate_cli_case("S-005.1", result.returncode, result.stdout, value, root=str(actual or ""))
            _expect(actual == str(root), "kb-create apply did not report the requested isolated platform root")
        return None

    _record(journey, "S-005.1", "apply isolated client platform", apply_isolated_platform)

    def member_login():
        result = _cli(cli, env, "auth", "login", "--gateway-url", base,
                      "--email", member_email, "--password", "NewMemberPass123!")
        validate_cli_case("S-005.2", result.returncode, result.stdout)
        return None

    def member_status():
        result = _cli(cli, env, "auth", "status", "--json")
        value = _json(result, "kb auth status")
        validate_cli_case("S-005.3", result.returncode, result.stdout, value,
                          email=member_email, gateway=base)
        return None

    def member_permissions():
        with invocation_context(step_id="journey.setup"):
            access = _login(base, member_email, "NewMemberPass123!")[0]
        response = _request(base, "/auth/permissions", token=access)
        validate_case_response("S-005.4", 200, response)
        return None

    def member_users_denied():
        with invocation_context(step_id="journey.setup"):
            access, _ = _login(base, member_email, "NewMemberPass123!")
        status, value = _response(base, "/auth/users", token=access)
        validate_case_response("S-005.5", status, value)
        return None

    _record(journey, "S-005.2", "member signs in to secured gateway", member_login)
    _record(journey, "S-005.3", "member auth status identifies gateway", member_status)
    _record(journey, "S-005.4", "member reads authorized permissions", member_permissions)
    _record(journey, "S-005.5", "member cannot read tenant users", member_users_denied)

    def expired_status():
        with invocation_context(step_id="journey.setup"):
            _cli(cli, env, "auth", "login", "--gateway-url", base, "--email", admin_email,
                 "--password", admin_password)
        session_path = Path.home() / ".kb/session.json"
        session = json.loads(session_path.read_text())
        session["expiresAt"] = 0
        session_path.write_text(json.dumps(session) + "\n")
        result = _cli(cli, env, "auth", "status", "--json")
        value = _json(result, "kb auth status")
        validate_cli_case("S-031.1", result.returncode, result.stdout, value, gateway=base)
        return None

    def refresh_session():
        result = _cli(cli, env, "auth", "tokens")
        validate_cli_case("S-031.2", result.returncode, result.stdout)
        return None

    def revoked_session():
        session_path = Path.home() / ".kb/session.json"
        session = json.loads(session_path.read_text())
        with invocation_context(step_id="journey.setup"):
            _request(base, "/auth/logout/cli", method="POST",
                     body={"refreshToken": session["refreshToken"]})
        session["expiresAt"] = 0
        session_path.write_text(json.dumps(session) + "\n")
        with invocation_context(step_id="S-031.3"):
            result = _cli(cli, env, "auth", "tokens")
        output = result.stdout + result.stderr
        validate_cli_case("S-031.3", result.returncode, output)
        return None

    def removed_session():
        (Path.home() / ".kb/session.json").unlink(missing_ok=True)
        result = _cli(cli, env, "auth", "tokens")
        output = result.stdout + result.stderr
        validate_cli_case("S-031.4", result.returncode, output)
        return None

    _record(journey, "S-031.1", "report expired saved CLI session", expired_status)
    _record(journey, "S-031.2", "refresh expired CLI session", refresh_session)
    _record(journey, "S-031.3", "report revoked CLI session", revoked_session)
    _record(journey, "S-031.4", "report removed CLI session", removed_session)
