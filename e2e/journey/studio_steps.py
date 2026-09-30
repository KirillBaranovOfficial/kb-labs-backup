"""Helpers for setting up Studio browser coverage in an installed journey."""
from __future__ import annotations

import json
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def create_studio_member(base_url: str, admin_email: str, admin_password: str) -> tuple[str, str]:
    def post(path: str, payload: dict, token: str = "") -> dict:
        headers = {"Content-Type": "application/json", "Accept": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        request = urllib.request.Request(
            f"{base_url}{path}", data=json.dumps(payload).encode(), headers=headers, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                status, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
        if status not in {200, 201}:
            raise RuntimeError(f"POST {path} returned HTTP {status}")
        return json.loads(raw) if raw else {}

    login = post("/auth/login/cli", {"email": admin_email, "password": admin_password})
    token = str(login.get("accessToken") or login.get("token") or "")
    if not token:
        raise RuntimeError("secured admin login returned no access token")
    invite = post("/auth/invites", {"email": "journey-member@example.test", "role": "member"}, token)
    activation_path = urllib.parse.urlparse(str(invite.get("activationUrl") or "")).path
    if "/activate/" not in activation_path:
        raise RuntimeError("member invite returned no Studio activation link")
    activation_token = activation_path.rsplit("/activate/", 1)[1]
    member_password = "JourneyMember123!"
    post("/auth/activate", {"token": activation_token, "password": member_password})
    return "journey-member@example.test", member_password


def record_studio_browser_step(report: Path, mode: str, passed: bool, duration: float, message: str) -> None:
    status = "PASS" if passed else "FAIL"
    _record_step(report, mode, status, duration, message)


def run_studio_browser_step(command: list[str], env: dict[str, str], report: Path,
                            mode: str, shell_passed: bool, timeout: int = 180) -> tuple[bool, float, str]:
    if not shell_passed:
        message = "Playwright was not run because the Studio shell check failed"
        _record_step(report, mode, "BLOCKED", 0.0, message)
        return False, 0.0, message

    started = time.monotonic()
    try:
        result = subprocess.run(command, env=env, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=timeout, check=False)
        elapsed = time.monotonic() - started
        passed = result.returncode == 0
        message = "Studio browser journey passed"
        if not passed:
            message = " ".join((result.stdout or "").split())[-700:] or f"Playwright exited {result.returncode}"
    except subprocess.TimeoutExpired as error:
        elapsed = time.monotonic() - started
        output = error.stdout or ""
        if isinstance(output, bytes):
            output = output.decode(errors="replace")
        excerpt = " ".join(output.split())[-700:]
        message = f"Playwright timed out after {timeout}s"
        if excerpt:
            message += f"; last output: {excerpt}"
        passed = False

    _record_step(report, mode, "PASS" if passed else "FAIL", elapsed, message)
    return passed, elapsed, message


def _record_step(report: Path, mode: str, status: str, duration: float, message: str) -> None:
    try:
        result = json.loads(report.read_text())
    except (OSError, json.JSONDecodeError):
        return
    _append_step(result, mode, status, duration, message)
    report.write_text(json.dumps(result, indent=2) + "\n")


def _append_step(result: dict, mode: str, status: str, duration: float, message: str) -> None:
    step = {
        "name": "Studio fresh-host browser journey",
        "capability": "studio",
        "mode": mode,
        "status": status,
        "duration_seconds": round(duration, 3),
        "message": message,
    }
    result.setdefault("steps", []).append(step)
    if status == "FAIL":
        result["status"] = "FAIL"
        result["first_failure"] = result.get("first_failure") or step
    elif status == "BLOCKED" and result.get("status") == "PASS":
        result["status"] = "BLOCKED"
