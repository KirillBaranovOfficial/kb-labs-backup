"""Helpers for setting up Studio browser coverage in an installed journey."""
from __future__ import annotations

import json
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
    try:
        result = json.loads(report.read_text())
    except (OSError, json.JSONDecodeError):
        return
    status = "PASS" if passed else "FAIL"
    result.setdefault("steps", []).append({
        "name": "Studio fresh-host browser journey",
        "capability": "studio",
        "mode": mode,
        "status": status,
        "duration_seconds": round(duration, 3),
        "message": message,
    })
    if not passed:
        result["status"] = "FAIL"
        if not result.get("first_failure"):
            result["first_failure"] = result["steps"][-1]
    report.write_text(json.dumps(result, indent=2) + "\n")
