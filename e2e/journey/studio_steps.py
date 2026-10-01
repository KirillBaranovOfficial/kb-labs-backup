"""Helpers for setting up Studio browser coverage in an installed journey."""
from __future__ import annotations

from collections import deque
import json
import os
import re
import signal
import subprocess
import threading
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
    output_lines: deque[str] = deque(maxlen=40)
    log_path = report.with_name(f"studio-playwright-{mode}.log")
    log_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with log_path.open("w", encoding="utf-8") as log:
            process = subprocess.Popen(command, env=env, text=True, stdout=subprocess.PIPE,
                                       stderr=subprocess.STDOUT, bufsize=1, start_new_session=True)

            def copy_output() -> None:
                assert process.stdout is not None
                with process.stdout as output_stream:
                    for line in output_stream:
                        rendered = line.rstrip("\r\n")
                        output_lines.append(rendered)
                        log.write(line)
                        log.flush()
                        print(f"[studio-playwright] {rendered}", flush=True)

            output_thread = threading.Thread(target=copy_output, daemon=True)
            output_thread.start()
            try:
                return_code = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                _signal_process_group(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    _signal_process_group(process.pid, signal.SIGKILL)
                    process.wait()
                raise
            finally:
                if process.poll() is None:
                    _signal_process_group(process.pid, signal.SIGKILL)
                    process.wait()
                output_thread.join()
        elapsed = time.monotonic() - started
        passed = return_code == 0
        message = "Studio browser journey passed"
        if not passed:
            message = f"Playwright exited {return_code}" + _output_excerpt(output_lines)
    except subprocess.TimeoutExpired:
        elapsed = time.monotonic() - started
        message = f"Playwright timed out after {timeout}s"
        message += _output_excerpt(output_lines)
        passed = False

    _record_step(report, mode, "PASS" if passed else "FAIL", elapsed, message)
    _record_playwright_case_claims(report, env.get("KB_STUDIO_PLAYWRIGHT_JSON", ""))
    return passed, elapsed, message


def _output_excerpt(lines: deque[str]) -> str:
    if not lines:
        return ""
    return "\nLast Playwright output (up to 40 lines):\n" + "\n".join(lines)


def _signal_process_group(process_id: int, signum: int) -> None:
    try:
        os.killpg(process_id, signum)
    except ProcessLookupError:
        pass


def _record_step(report: Path, mode: str, status: str, duration: float, message: str) -> None:
    try:
        result = json.loads(report.read_text())
    except (OSError, json.JSONDecodeError):
        return
    _append_step(result, mode, status, duration, message)
    report.write_text(json.dumps(result, indent=2) + "\n")


def _record_playwright_case_claims(report: Path, json_report: str) -> None:
    if not json_report:
        return
    try:
        payload = json.loads(Path(json_report).read_text(encoding="utf-8"))
        result = json.loads(report.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return
    claims: dict[str, str] = {}

    def visit(suite: dict) -> None:
        for spec in suite.get("specs", []):
            title = str(spec.get("title", ""))
            ids = re.findall(r"@(S-\d{3}\.\d+)", title)
            tests = spec.get("tests", [])
            statuses = [res.get("status") for test in tests for res in test.get("results", [])]
            status = "PASS" if statuses and all(item == "passed" for item in statuses) else "FAIL"
            for case_id in ids:
                claims[case_id] = status
        for child in suite.get("suites", []):
            visit(child)

    for suite in payload.get("suites", []):
        visit(suite)
    if not claims:
        return
    case_run = result.setdefault("case_run", {})
    existing = {item.get("claimId"): item for item in case_run.get("claims", []) if isinstance(item, dict)}
    existing.update({case_id: {"claimId": case_id, "kind": "journey", "status": status}
                     for case_id, status in claims.items()})
    case_run["claims"] = [existing[key] for key in sorted(existing)]
    report.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")


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
