#!/usr/bin/env python3
"""One isolated end-user install journey; each invocation runs in a fresh container."""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

from studio_steps import (create_studio_access_tokens, create_studio_environment,
                          create_studio_member, run_studio_browser_step)
from platform_update_steps import run_platform_update_steps
from platform_channel_steps import run_platform_channel_steps
from release_index import channel_platform_version, stable_platform_version
from service_steps import Journey, record_runner_failure
from workflow_steps import merge_workflow_result, prepare_workflow_fixtures, run_workflow_journey
from steps.auth import run_bootstrap_recovery_steps, run_cli_auth_forbidden, run_cli_auth_status, run_local_auth_steps, run_secured_auth_steps
from steps.accounts import run_cli_logout_steps, run_password_and_session_steps, run_secured_account_steps
from platform_state_dump import PlatformStateDumpOnce
from scoreboard import write_scoreboards
from invocations import (invocation_context, open_http, run_command, run_pty_command,
                         start_background_command)

INSTALLER = "/usr/local/share/kb-create/install.sh"
FRONT_DOOR = os.getenv("KB_JOURNEY_FRONT_DOOR", "agent")
SCENARIO = os.getenv("KB_JOURNEY_SCENARIO", "commit")
VARIANT = os.getenv("KB_JOURNEY_VARIANT", "default")
REGISTRY = os.getenv("KB_JOURNEY_REGISTRY", "http://verdaccio:4873")
BASE_ENV = os.environ.copy()
HOME = Path(tempfile.mkdtemp(prefix="kb-journey-home-"))
BIN = HOME / ".local" / "bin"
PLATFORM = HOME / "kb-platform"
PROJECT = HOME / "fresh-project"
TIMES: dict[str, float] = {}
MAX_PNPM_RSS_MIB = 0.0
PLATFORM_SIZE_MIB: float | None = None
PROCESSES = []
FIRST_FAILURE = ""
CASE_JOURNEY: Journey | None = None
LAST_COMMAND_ENV: dict[str, str] = BASE_ENV.copy()
LAST_COMMAND_CWD: str | None = None
STATE_DUMP_GUARD = PlatformStateDumpOnce()


def result_payload(value: object) -> dict:
    if not isinstance(value, dict):
        return {}
    if value.get("ok") is True:
        data = value.get("data")
        return data if isinstance(data, dict) else {}
    return value


def record_case(name: str, passed: bool, message: str, *, cases: list[str]) -> None:
    if CASE_JOURNEY is None:
        return
    CASE_JOURNEY.step(name, "platform.install", lambda: None if passed else message, cases=cases)


def run(args: list[str], *, env=None, cwd=None, timeout=900, input_text=None):
    global MAX_PNPM_RSS_MIB, LAST_COMMAND_ENV, LAST_COMMAND_CWD
    LAST_COMMAND_ENV = dict(env or BASE_ENV)
    LAST_COMMAND_CWD = str(cwd) if cwd else str(Path.cwd())
    child_env = env or BASE_ENV
    started = time.monotonic()
    result = run_command(args, cwd=cwd or Path.cwd(), env=child_env, timeout=timeout,
                         input=input_text, stderr=subprocess.STDOUT,
                         monitor_process=lambda pid: _sample_pnpm_rss_for_run(pid))
    duration = time.monotonic() - started
    return subprocess.CompletedProcess(args, result.returncode,
                                       stdout=(result.stdout or "") + (result.stderr or ""),
                                       stderr=result.stderr or ""), duration


def _sample_pnpm_rss_for_run(pid: int) -> None:
    global MAX_PNPM_RSS_MIB
    MAX_PNPM_RSS_MIB = max(MAX_PNPM_RSS_MIB, sample_pnpm_rss(pid))


def run_tty_confirmation(args: list[str], *, env=None, timeout=900):
    confirmed = False
    def respond(transcript: str):
        nonlocal confirmed
        if not confirmed and "Continue? [y/N]" in transcript:
            confirmed = True
            return "y\n"
        return None
    return run_pty_command(args, env=env or BASE_ENV, timeout=timeout, on_output=respond)


def sample_pnpm_rss(_root_pid: int) -> float:
    peak_kib = 0
    for proc_root in Path("/proc").iterdir():
        if not proc_root.name.isdigit():
            continue
        try:
            command = (proc_root / "cmdline").read_bytes().replace(b"\0", b" ").lower()
            if b"pnpm" not in command:
                continue
            for line in (proc_root / "status").read_text().splitlines():
                if line.startswith("VmRSS:"):
                    peak_kib = max(peak_kib, int(line.split()[1]))
                    break
        except (FileNotFoundError, PermissionError, ProcessLookupError, ValueError, OSError):
            continue
    return peak_kib / 1024


def fail(message: str):
    global FIRST_FAILURE
    if not FIRST_FAILURE:
        FIRST_FAILURE = message


def dump_failure_state() -> None:
    report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md"))
    mode = "secured" if VARIANT.endswith("secured") else "local"
    STATE_DUMP_GUARD.dump(
        PLATFORM, report.parent, mode=mode, front=FRONT_DOOR, scenario=SCENARIO,
        variant=VARIANT, first_failure=FIRST_FAILURE or "journey reported FAIL",
        env=LAST_COMMAND_ENV, cwd=LAST_COMMAND_CWD, admin_password="JourneyPassw0rd!" if mode == "secured" else "",
    )


def failure_exit() -> int:
    dump_failure_state()
    return 1


def capture_case_failure() -> None:
    if CASE_JOURNEY is None or CASE_JOURNEY.result().status != "FAIL" or FIRST_FAILURE:
        return
    first = CASE_JOURNEY.result().first_failure or {}
    fail(f"{first.get('name', 'journey step')}: {first.get('message', 'reported FAIL')}")


def require(result, expected=0, step="command"):
    if result.returncode != expected:
        excerpt = " ".join(result.stdout.split())[-700:]
        fail(f"{step}: exit {result.returncode}; {excerpt or 'no output'}")
        return False
    return True


def mcp_rpc(url: str, method: str, request_id: int, params=None, token=None):
    payload = {"jsonrpc": "2.0", "id": request_id, "method": method}
    if params is not None:
        payload["params"] = params
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
                 **({"Authorization": f"Bearer {token}"} if token else {})},
        method="POST",
    )
    try:
        status, raw, _ = open_http(urllib.request.urlopen, request, timeout=30, env=os.environ)
        if status >= 400:
            raise RuntimeError(f"MCP {method} returned HTTP {status}")
        body = raw.decode()
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"MCP {method} returned HTTP {error.code}") from error
    if any(line.startswith("data:") for line in body.splitlines()):
        body = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
    return json.loads(body)


def verify_project_mcp(env: dict[str, str], local: bool):
    registered, _ = run(["bash", "-c", '. "$HOME/.profile"; kb project add --json'],
                        env=env, cwd=PROJECT, timeout=180)
    if not require(registered, step="register project for MCP"):
        return
    try:
        project = json.loads(registered.stdout)["project"]
        project_id = project["id"]
        url = f"http://127.0.0.1:4000/api/v1/projects/{project_id}/mcp"
        token = None
        if not local:
            try:
                mcp_rpc(url, "tools/list", 0)
            except RuntimeError as error:
                if "HTTP 401" not in str(error):
                    raise
            else:
                raise RuntimeError("secured MCP accepted a request without a token")
            token = env.get("KB_MCP_TOKEN")
            if not token:
                raise RuntimeError("secured MCP journey needs KB_MCP_TOKEN from gateway login")
        initialized = mcp_rpc(url, "initialize", 1, {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "kb-labs-journey", "version": "1.0.0"},
        }, token)
        if "result" not in initialized:
            raise RuntimeError("initialize returned no result")
        listed = mcp_rpc(url, "tools/list", 2, token=token)
        tools = listed.get("result", {}).get("tools", [])
        names = {tool.get("name") for tool in tools}
        expected = {"kb-labs-commit__commit", "workflow__workflow_run", "kb-labs__mcp_diagnostics"}
        if not expected.issubset(names):
            raise RuntimeError(f"tools/list missing expected tools: {sorted(expected - names)}")
        called = mcp_rpc(url, "tools/call", 3, {"name": "kb-labs__mcp_diagnostics", "arguments": {}}, token)
        content = called.get("result", {}).get("content", [])
        text = "\n".join(item.get("text", "") for item in content)
        if called.get("result", {}).get("isError") or "loaded cleanly" not in text:
            raise RuntimeError(f"diagnostics call had unexpected result: {text}")
    except (KeyError, ValueError, RuntimeError, urllib.error.URLError) as error:
        fail(f"project MCP journey: {error}")


def pty_install(command: list[str], env: dict[str, str], choice: str = "", access_mode: str = "local"):
    global MAX_PNPM_RSS_MIB
    sent_choice = not choice
    sent_access_mode = False
    last_prompt = ""
    apply_started = None
    started = time.monotonic()
    seen_length = 0
    def respond(transcript: str):
        nonlocal sent_choice, sent_access_mode, last_prompt, apply_started, seen_length
        global MAX_PNPM_RSS_MIB
        if apply_started is None and re.search(r"Applying installation", transcript[seen_length:], re.I):
            apply_started = time.monotonic()
        seen_length = len(transcript)
        visible = transcript[-2000:]
        if access_mode == "secured" and not sent_access_mode and "Studio access" in visible:
            sent_access_mode = True
            last_prompt = "Studio access (secured)"
            return "secured\n"
        prompts = re.findall(r"(?im)^.*(?:\?|\[.*(?:default|y/n|yes/no).*\]|default[^\n]*:|press enter[^\n]*|select[^\n]*:|project directory[^\n]*:|admin email[^\n]*:|admin tenant[^\n]*:|admin password[^\n]*:|confirm admin password[^\n]*:|session signing secret[^\n]*:).*?$", visible)
        prompt = prompts[-1] if prompts else ""
        if not prompt or prompt == last_prompt:
            return None
        answer = ""
        if re.search(r"Project directory", prompt, re.I):
            answer = str(PROJECT)
        elif access_mode == "secured" and re.search(r"Studio access", prompt, re.I):
            answer = "secured"
        elif access_mode == "secured" and re.search(r"Admin email", prompt, re.I):
            answer = "journey-admin@example.test"
        elif access_mode == "secured" and re.search(r"Admin tenant", prompt, re.I):
            answer = "journey"
        elif access_mode == "secured" and re.search(r"Admin password", prompt, re.I) and not re.search(r"Confirm", prompt, re.I):
            answer = "JourneyPassw0rd!"
        elif access_mode == "secured" and re.search(r"Confirm admin password", prompt, re.I):
            answer = "JourneyPassw0rd!"
        elif choice and not sent_choice and re.search(r"scenario|journey|setup", prompt, re.I):
            answer = choice
        if answer:
            sent_choice = True
        last_prompt = prompt
        return answer + "\n"
    result, duration = run_pty_command(command, env=env, timeout=900, on_output=respond,
                                       monitor_process=_sample_pnpm_rss_for_run,
                                       start_new_session=True, raise_on_timeout=False)
    return result.returncode, result.stdout, duration, apply_started - started if apply_started else None


def human_text_is_clean(output: str):
    if re.search(r'\{\s*"ok"\s*:|"(?:code|error)"\s*:', output):
        fail("human output contains a raw JSON object")
    if "goroutine " in output or re.search(r"(?m)^panic:", output) or "stack trace" in output.lower():
        fail("human output contains a stack trace")


def append_summary(passed: bool):
    root_size = PLATFORM_SIZE_MIB
    if root_size is None:
        root_size = 0.0
        if PLATFORM.exists():
            root_size = sum(p.stat().st_size for p in PLATFORM.rglob("*") if p.is_file()) / (1024 * 1024)
    title = f"{FRONT_DOOR}/{SCENARIO}" if VARIANT == "default" else VARIANT
    lines = [f"| {title} | {'PASS' if passed else 'FAIL'} | {FIRST_FAILURE or 'all assertions passed'} | "
             f"{TIMES.get('install.sh', 0):.1f}s | {TIMES.get('questions', 0):.1f}s | "
             f"{TIMES.get('apply', 0):.1f}s | {TIMES.get('start', 0):.1f}s | "
             f"{TIMES.get('first command', 0):.1f}s | "
             f"{sum(TIMES.values()):.1f}s | {root_size:.1f} MiB | {MAX_PNPM_RSS_MIB:.1f} MiB |"]
    report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md"))
    report.parent.mkdir(parents=True, exist_ok=True)
    if CASE_JOURNEY is not None:
        CASE_JOURNEY.write_json(report.with_name(
            f"installer-case-{CASE_JOURNEY.mode}-{FRONT_DOOR}-{SCENARIO}-{VARIANT}.json"))
    if not report.exists():
        report.write_text("| Journey | Result | First broken step | install.sh | questions | apply | start | first command | total | platform root | peak pnpm RSS |\n"
                          "|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
    with report.open("a") as stream:
        stream.write("\n".join(lines) + "\n")
    if (not passed and BASE_ENV.get("KB_JOURNEY_SERVICE_PROFILE") == "nightly"
            and (VARIANT.endswith("secured") or SCENARIO == "explore")):
        mode = "secured" if VARIANT.endswith("secured") else "local"
        service_report = report.with_name(f"service-{mode}.json")
        if not service_report.exists():
            record_runner_failure(service_report, mode, FIRST_FAILURE)
    if not passed:
        diagnosis = report.with_name("journey-diagnostics.log")
        candidates = sorted((PLATFORM / ".kb" / "diagnostics").glob("*.json"), key=lambda path: path.stat().st_mtime, reverse=True)
        logs = sorted((PLATFORM / ".kb" / "logs").glob("*.log"), key=lambda path: path.stat().st_mtime, reverse=True)
        with diagnosis.open("a") as stream:
            for path in [*candidates[:1], *logs[:1]]:
                stream.write(f"\n== {title}: {path.name} ==\n{path.read_text(errors='replace')[-5000:]}\n")
            for path in [PLATFORM / "node_modules/.bin/kb", PLATFORM / "node_modules/@kb-labs/cli-bin", PLATFORM / "node_modules/@kb-labs/cli-bin/dist/bin.js", BIN / "kb"]:
                try:
                    target = os.readlink(path)
                except OSError:
                    target = ""
                stream.write(f"tree {path}: exists={path.exists()} symlink={path.is_symlink()} target={target!r} resolved={path.resolve(strict=False)}\n")
                if path.is_file() and path.name in {"kb", "bin.js"}:
                    stream.write(f"file {path}: {path.read_text(errors='replace')[:3000]}\n")
    copied_catalog = Path(__file__).with_name("scenarios")
    catalog = copied_catalog if copied_catalog.is_dir() else Path(__file__).resolve().parents[2] / "docs" / "qa" / "scenarios"
    write_scoreboards(report.parent, catalog)
    print("\n".join(lines))


def extract_request(payload):
    if isinstance(payload, dict):
        if payload.get("schema") == "kb.create/v2" and "platformRoot" in payload:
            return payload
        for value in payload.values():
            found = extract_request(value)
            if found is not None:
                return found
    elif isinstance(payload, list):
        for value in payload:
            found = extract_request(value)
            if found is not None:
                return found
    return None


def find_project_id(payload):
    if isinstance(payload, dict):
        for key in ("projectId", "project_id", "id"):
            value = payload.get(key)
            if isinstance(value, str) and value.startswith("prj_"):
                return value
        for value in payload.values():
            found = find_project_id(value)
            if found:
                return found
    elif isinstance(payload, list):
        for value in payload:
            found = find_project_id(value)
            if found:
                return found
    return ""


def verify_listener_ownership():
    result, _ = run(["ss", "-H", "-ltnp"], timeout=20)
    if not require(result, step="list platform listeners"):
        return
    pids = set(re.findall(r"pid=(\d+)", result.stdout))
    if not pids:
        fail("platform is running but exposes no owned listening process")
        return
    for pid in pids:
        cmdline = Path(f"/proc/{pid}/cmdline")
        try:
            command = cmdline.read_bytes().replace(b"\0", b" ").decode(errors="replace")
        except OSError:
            fail(f"listener pid {pid} disappeared before ownership could be checked")
            continue
        try:
            cwd = os.readlink(f"/proc/{pid}/cwd")
        except OSError:
            cwd = ""
        if str(PLATFORM) not in command and not cwd.startswith(str(PLATFORM)):
            fail(f"listener pid {pid} is not running from this platform root: {command[:240]}")


def uninstall_removed_platform_root():
    receipt = PLATFORM / ".kb" / "v2" / "receipt.json"
    snapshots = PLATFORM / ".kb" / "v2" / "snapshots"
    return not PLATFORM.exists() and not receipt.exists() and not snapshots.exists()


def restart_host(launcher: str, env: dict[str, str]) -> str:
    result, _ = run([launcher, "restart", "--platform-root", str(PLATFORM), "--json"], env=env, timeout=180)
    try:
        payload = result_payload(json.loads(result.stdout))
    except json.JSONDecodeError:
        payload = {}
    restarted = (result.returncode == 0 and payload.get("operation") == "restart"
                 and payload.get("host", {}).get("pid", 0) > 0)
    record_case("restart host", restarted,
               "restart JSON did not report a started host process", cases=["PC-004.1"])
    if not restarted:
        raise RuntimeError("host restart failed during secured auth journey")
    return "http://127.0.0.1:4000"


def verify_broken_install_doctor(launcher: str, env: dict[str, str]) -> None:
    receipt = PLATFORM / ".kb" / "v2" / "receipt.json"
    hidden_receipt = receipt.with_name("receipt.journey-hidden.json")
    if not receipt.is_file() or hidden_receipt.exists():
        fail("broken-install doctor fixture requires one active receipt")
        return
    os.replace(receipt, hidden_receipt)
    try:
        result, _ = run([launcher, "doctor", "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
        try:
            diagnostic = json.loads(result.stdout).get("error", {})
        except (json.JSONDecodeError, AttributeError):
            diagnostic = {}
        human, _ = run([launcher, "doctor", "--platform-root", str(PLATFORM)], env=env, timeout=120)
        typed_ok = (result.returncode == 1 and diagnostic.get("code") == "KB_INSTALL_RECEIPT_UNAVAILABLE"
                    and diagnostic.get("hint") and diagnostic.get("correlationId")
                    and "KB_INSTALL_RECEIPT_UNAVAILABLE" in human.stdout
                    and "correlationId" in human.stdout)
        record_case("doctor reports missing receipt", typed_ok,
                    "doctor did not return the typed missing-receipt diagnostic in both modes",
                    cases=["PC-003.2", "S-203.11"])
    finally:
        os.replace(hidden_receipt, receipt)
    restored, _ = run([launcher, "doctor", "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
    try:
        restored_payload = result_payload(json.loads(restored.stdout))
    except json.JSONDecodeError:
        restored_payload = {}
    restored_ok = (restored.returncode == 0 and restored_payload.get("installed") is True and restored_payload.get("running") is True
                   and restored_payload.get("reachable") is True
                   and restored_payload.get("platform", {}).get("ok") is True)
    record_case("doctor recovers after receipt restore", restored_ok,
               "doctor did not return a healthy result after restoring the receipt", cases=["PC-003.3"])


def main():
    global FIRST_FAILURE, CASE_JOURNEY
    STATE_DUMP_GUARD.reset()
    env = BASE_ENV.copy()
    env["HOME"] = str(HOME)
    env["KB_JOURNEY_HOME_LABEL"] = "main"
    env["KB_JOURNEY_INVOCATIONS"] = str(Path(os.getenv(
        "KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).resolve().parent)
    os.environ["KB_JOURNEY_INVOCATIONS"] = env["KB_JOURNEY_INVOCATIONS"]
    env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
    (HOME / ".profile").write_text("if [ -f ~/.bashrc ]; then . ~/.bashrc; fi\n")
    (HOME / ".bash_profile").write_text("if [ -f ~/.profile ]; then . ~/.profile; fi\n")
    (HOME / ".npmrc").write_text(f"registry={REGISTRY}\n")
    env["KB_CREATE_DOWNLOAD_BASE_URL"] = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"]
    if VARIANT.endswith("secured"):
        env["KB_JOURNEY_ADMIN_PASSWORD"] = "JourneyPassw0rd!"
    mode = "secured" if VARIANT.endswith("secured") else "local"
    CASE_JOURNEY = Journey(mode, f"installer-{mode}-{FRONT_DOOR}-{SCENARIO}-{VARIANT}")
    # Agent flows pass --registry explicitly. Human `install` uses the package
    # manager's normal registry setting, so point that setting at the isolated
    # journey registry as well.
    env["NPM_CONFIG_REGISTRY"] = REGISTRY
    env.pop("KB_PLATFORM_ROOT", None)
    env.pop("KB_HOME", None)
    env.pop("KB_NET_OFFSET", None)
    env["NO_COLOR"] = "1"

    if VARIANT == "wrong-node":
        fake_bin = HOME / "fake-bin"
        fake_bin.mkdir()
        fake_node = fake_bin / "node"
        fake_node.write_text("#!/bin/sh\necho v22.11.0\n")
        fake_node.chmod(0o755)
        env["PATH"] = f"{fake_bin}:{env['PATH']}"
        result, duration = run([INSTALLER, "--version", "journey-candidate"], env=env, timeout=90)
        TIMES["install.sh"] = duration
        # The prerequisite is evaluated by kb-create install; install.sh itself only downloads.
        (BIN / "kb-create").exists() and None
        launcher, _ = run([str(BIN / "kb-create"), "install"], env=env, timeout=90) if (BIN / "kb-create").exists() else (result, 0)
        passed = "KB_INSTALL_NODE_VERSION" in launcher.stdout and "Node.js 24" in launcher.stdout
        if not passed:
            fail(f"wrong Node major: expected KB_INSTALL_NODE_VERSION and Node 24 hint; {launcher.stdout.strip()[:300]}")
        append_summary(passed)
        return 0 if passed else failure_exit()

    if VARIANT == "occupied-port":
        listener_command = ["python3", "-u", "-c", "import http.server; s=http.server.HTTPServer(('0.0.0.0',4000),http.server.SimpleHTTPRequestHandler); print('ready', flush=True); s.serve_forever()"]
        listener = start_background_command(listener_command, env=env)
        PROCESSES.append(listener)
        if not listener.wait_for_output("ready", 5):
            fail("port collision fixture failed to bind port 4000")
    if VARIANT == "relative-path":
        FRONT = "agent"
    else:
        FRONT = FRONT_DOOR

    started = time.monotonic()
    result, elapsed = run([INSTALLER, "--version", "journey-candidate"], env=env, timeout=90)
    TIMES["install.sh"] = elapsed
    if not require(result, step="install.sh"):
        append_summary(False)
        return failure_exit()
    if not (BIN / "kb-create").exists():
        fail("install.sh: kb-create binary was not installed")
        append_summary(False)
        return failure_exit()

    launcher = str(BIN / "kb-create")
    version, _ = run([launcher, "--version"], env=env, timeout=30)
    version_ok = version.returncode == 0 and re.fullmatch(r"kb-create [^\s]+\s*", version.stdout) is not None
    record_case("launcher version", version_ok,
               "kb-create --version did not print the launcher version", cases=["PC-001.1"])
    if VARIANT == "occupied-port":
        args = [launcher, "apply", "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                "--request-platform-root", str(PLATFORM), "--scenario", "commit", "--registry", REGISTRY]
        result, elapsed = run(args, env=env, timeout=90)
        TIMES["apply"] = elapsed
        passed = result.returncode != 0 and "KB_INSTALL_PORT_IN_USE" in result.stdout and "4000" in result.stdout
        if not passed:
            fail(f"port conflict: expected KB_INSTALL_PORT_IN_USE for port 4000; {' '.join(result.stdout.split())[-500:]}")
        append_summary(passed)
        return 0 if passed else failure_exit()
    if FRONT == "human" and not VARIANT.endswith("secured"):
        PROJECT.mkdir(parents=True, exist_ok=True)
        choice = "commit" if VARIANT == "human-nondefault" else ""
        access_mode = "secured" if VARIANT.endswith("secured") else "local"
        code, output, duration, apply_offset = pty_install(
            [launcher, "install", "--registry", REGISTRY], env, choice, access_mode
        )
        TIMES["questions"] = apply_offset if apply_offset is not None else duration
        if apply_offset is not None:
            TIMES["apply"] = duration - apply_offset
        if VARIANT.startswith("human-"):
            diagnostic = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log")
            with diagnostic.open("a", encoding="utf-8") as stream:
                stream.write(f"\n== {FRONT}/{SCENARIO}: install-pty (access={access_mode}, exit {code}) ==\n{output[-12000:]}\n")
        human_text_is_clean(output)
        if code != 0:
            fail(f"kb-create install: exit {code}; {' '.join(output.split())[-2000:]}")
        if not re.search(r"(?i)install|download|configure|verify|starting", output):
            fail("kb-create install: terminal showed no progress")
        if not re.search(r"(?i)next|success|installed|ready", output):
            fail("kb-create install: terminal showed no success and next steps")
        human_install_ok = (code == 0 and "Set up KB Labs" in output
                            and re.search(r"Plan: platform [^,]+ at [^,]+, \d+ artifacts", output) is not None
                            and "Applying installation" in output
                            and f"; project {PROJECT.resolve()}" in output
                            and str(PROJECT.resolve()) in output
                            and not (PROJECT / ".kb").exists()
                            and "Verification: platform installed" in output
                            and re.search(r"(?i)next|success|installed|ready", output) is not None)
        if FRONT == "human" and not VARIANT.endswith("secured"):
            record_case("human interactive install",
                       human_install_ok, "interactive install did not show a plan and success result",
                       cases=["PC-001.2", "S-040.1", "S-040.2", "S-187.1", "S-187.2", "S-187.3", "S-203.10"])
    else:
        operation = "plan" if VARIANT in {"input", "relative-path"} else "install"
        install_scenario = "custom" if VARIANT.endswith("secured") else SCENARIO
        args = [launcher, operation, "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                "--request-platform-root", str(PLATFORM), "--service-profile", "default", "--scenario", install_scenario,
                "--registry", REGISTRY]
        if VARIANT.endswith("secured"):
            answers = json.dumps({
                "access.mode": "secured",
                "admin.email": "journey-admin@example.test",
                "admin.tenant": "journey",
                # Declares the secret input in the plan; the real value comes
                # from the isolated runner environment via --secret-env.
                "admin.password": "journey-secret-placeholder",
            })
            args.extend(["--scenario-answers", answers,
                         "--secret-env", "gateway.bootstrap.password=KB_JOURNEY_ADMIN_PASSWORD"])
        if VARIANT == "relative-path":
            args[args.index(str(PLATFORM))] = os.path.relpath(PLATFORM, Path.cwd())
        result, elapsed = run(args, env=env, timeout=1200)
        TIMES["apply"] = elapsed
        if VARIANT == "relative-path":
            try:
                payload = json.loads(result.stdout)
                root = payload.get("plan", {}).get("request", {}).get("platformRoot", "")
            except json.JSONDecodeError:
                root = ""
            passed = result.returncode == 0 and Path(root).is_absolute() and root == str(PLATFORM.resolve())
            if not passed:
                fail(f"relative platform root: expected a successful plan with an absolute normalized root; {' '.join(result.stdout.split())[-500:]}")
            append_summary(passed)
            return 0 if passed else failure_exit()
        if not require(result, step=f"kb-create {operation} --json"):
            append_summary(False)
            return failure_exit()
        if operation == "install":
            try:
                records = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
            except json.JSONDecodeError:
                records = []
            terminal = records[-1] if records else {}
            stream_meta = terminal.get("meta", {}).get("stream", {}) if isinstance(terminal, dict) else {}
            install_ok = (result.returncode == 0 and bool(records)
                          and terminal.get("ok") is True and terminal.get("data", "not-null") is None
                          and stream_meta.get("terminal") is True and stream_meta.get("exitCode") == 0
                          and any(record.get("ok") is True and isinstance(record.get("data"), dict)
                                  for record in records[:-1] if isinstance(record, dict)))
            record_case("JSON install progress", install_ok,
                       "install stream omitted progress or its terminal success record",
                       cases=["S-203.10", "S-203.13"])
            if not install_ok:
                fail("kb-create install --json: stream did not finish successfully")
            payload = {}
        else:
            try:
                envelope = json.loads(result.stdout)
            except json.JSONDecodeError:
                fail(f"kb-create {operation} --json: output was not valid JSON")
                append_summary(False)
                return failure_exit()
            payload = envelope.get("data") if isinstance(envelope, dict) else None
            if envelope.get("ok") is not True or not isinstance(payload, dict):
                fail(f"kb-create {operation} --json: result did not contain a success data object")
                append_summary(False)
                return failure_exit()
        if operation == "plan":
            plan_payload = payload.get("plan", {})
            plan_ok = (result.returncode == 0
                       and plan_payload.get("request", {}).get("platformRoot") == str(PLATFORM)
                       and bool(plan_payload.get("artifacts")) and bool(plan_payload.get("serviceGraph")))
            record_case("agent plan", plan_ok,
                       "plan omitted the isolated root, resolved artifacts, or service graph", cases=["PC-001.3"])
        elif operation == "install":
            if FRONT_DOOR == "agent":
                record_case("agent JSON install", install_ok,
                            "agent install stream omitted progress or terminal success", cases=["S-039.1", "S-203.10"])
        if VARIANT.endswith("secured"):
            env["KB_JOURNEY_ADMIN_EMAIL"] = "journey-admin@example.test"
        if VARIANT == "input":
            request = extract_request(payload)
            if request is None:
                fail("kb-create apply --json: launcher output contained no kb.create/v2 request")
            else:
                request_file = HOME / "launcher-request.json"
                request_file.write_text(json.dumps(request) + "\n")
                args = [launcher, "apply", "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                        "--input", str(request_file), "--registry", REGISTRY]
                result, elapsed = run(args, env=env, timeout=1200)
                TIMES["apply"] += elapsed
                if not require(result, step="kb-create apply --input launcher request"):
                    append_summary(False)
                    return failure_exit()
                try:
                    if json.loads(result.stdout).get("ok") is not True:
                        fail("kb-create apply --input: result did not report ok=true")
                except json.JSONDecodeError:
                    fail("kb-create apply --input: output was not one JSON document")

    capture_case_failure()
    if FIRST_FAILURE:
        append_summary(False)
        return failure_exit()

    if VARIANT.endswith("secured"):
        provider_config = json.dumps({
            "email-password": {"type": "email-password"},
            "corp": {
                "type": "oidc",
                "issuer": "https://idp-stub:9443",
                "clientId": "kb-gateway",
                "clientSecretEnv": "OIDC_CORP_CLIENT_SECRET",
                "scopes": ["openid", "email"],
                "pkce": True,
            },
        })
        for key, value in (("gateway.auth.providers", provider_config),
                           ("gateway.auth.cookieSecure", "false")):
            configured, _ = run(
                ["bash", "-c", '. "$HOME/.profile"; kb config set --scope platform "$1" "$2" --json',
                 "journey", key, value], env=env, cwd=PROJECT, timeout=90)
            if not require(configured, step=f"kb config set --scope platform {key}"):
                append_summary(False)
                return failure_exit()

    result, elapsed = run([launcher, "start", "--platform-root", str(PLATFORM)], env=env, timeout=300)
    TIMES["start"] = elapsed
    if not require(result, step="kb-create start"):
        append_summary(False)
        return failure_exit()
    record_case("start host", result.returncode == 0,
               "kb-create start did not start the configured host", cases=["PC-002.1"])

    verify_listener_ownership()

    # New shell with the installed profile proves user-facing PATH and runtime isolation.
    PROJECT.mkdir(exist_ok=True)
    run(["git", "init", "-q"], env=env, cwd=PROJECT)
    (PROJECT / "note.txt").write_text("journey first value\n")
    run(["git", "add", "note.txt"], env=env, cwd=PROJECT)
    help_command = 'kb --help'
    if FRONT == "agent" and SCENARIO == "commit":
        help_command += ' && kb commit --help'
    result, elapsed = run(["bash", "-c", f'. "$HOME/.profile"; {help_command}'], env=env, cwd=PROJECT, timeout=180)
    TIMES["first command"] = elapsed
    if not require(result, step="new login shell kb --help"):
        with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
            stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: first-command-output ==\n{result.stdout[-5000:]}\n")
        append_summary(False)
        return failure_exit()
    else:
        if FRONT == "agent" and SCENARIO == "commit":
            result, elapsed = run(["bash", "-c", '. "$HOME/.profile"; kb commit --dry-run --scope root'],
                                  env=env, cwd=PROJECT, timeout=300)
            TIMES["first command"] += elapsed
            if not require(result, step="kb commit first value"):
                with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                    stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: first-value-output ==\n{result.stdout[-5000:]}\n")
            elif 'scope "root"' not in result.stdout:
                with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                    stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: first-value-output ==\n{result.stdout[-5000:]}\n")
                fail("kb commit first value: output did not include the supplied scope")
        status, _ = run(["git", "status", "--porcelain"], env=env, cwd=PROJECT)
        clean_repo = not (PROJECT / ".kb").exists() and status.stdout.strip() in {"?? note.txt", "A  note.txt"}
        record_case("first CLI commands leave fresh repository untouched", clean_repo,
                    f"project gained KB Labs files: {status.stdout.strip()}", cases=["S-054.17"])
        if not clean_repo:
            fail(f"project gained KB Labs files: {status.stdout.strip()}")

    if FRONT == "agent" and SCENARIO == "commit":
        verify_project_mcp(env, env.get("KB_ACCESS_MODE", "local") == "local")

    update_journey = (SCENARIO == "commit" and VARIANT in {"default", "input"}) or VARIANT.endswith("secured")
    if update_journey:
        try:
            receipt_path = PLATFORM / ".kb" / "v2" / "receipt.json"
            active = json.loads(receipt_path.read_text())
            request = active["plan"]["request"]
            stable_request = json.loads(json.dumps(request))
            base_version = active["plan"]["serviceGraph"]["platformVersion"]
            request_file = HOME / "platform-update-request.json"
            request_file.write_text(json.dumps(request) + "\n")
            update_index = HOME / "release-index-update.json"
            update_url = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"].rstrip("/") + "/journey-candidate/release-index-update.json"
            update_status, update_body, _ = open_http(
                urllib.request.urlopen, urllib.request.Request(update_url), timeout=30, env=env)
            if update_status != 200:
                raise RuntimeError(f"GET {update_url} returned HTTP {update_status}")
            update_index.write_bytes(update_body)
            updated_version = stable_platform_version(json.loads(update_index.read_text()))
            request.setdefault("platform", {})["version"] = updated_version
            request["platform"].pop("channel", None)
            request["sdk"] = {"version": updated_version}
            request_file.write_text(json.dumps(request) + "\n")
        except (KeyError, OSError, ValueError, urllib.error.URLError) as error:
            fail(f"platform update candidate: could not load installed V2 request and second sealed index: {error}")
        else:
            access_mode = "secured" if VARIANT.endswith("secured") else "local"

            def update_command(args):
                result, _ = run(args, env=env, cwd=PROJECT, timeout=1200)
                return result

            verify_admin = None
            if access_mode == "secured":
                admin_email = env.get("KB_JOURNEY_ADMIN_EMAIL", "journey-admin@example.test")
                admin_password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "JourneyPassw0rd!")

                def verify_admin():
                    result, _ = run(["bash", "-c", '. "$HOME/.profile"; kb auth login --gateway-url http://127.0.0.1:4000 --email "$1" --password "$2"',
                                     "journey", admin_email, admin_password],
                                    env=env, cwd=PROJECT, timeout=90)
                    return result

                def verify_contract_transition():
                    result, _ = run(["bash", "-c", '. "$HOME/.profile"; kb config show --json'],
                                    env=env, cwd=PROJECT, timeout=90)
                    try:
                        payload = json.loads(result.stdout)
                    except json.JSONDecodeError as error:
                        raise RuntimeError("saved CLI session check did not return JSON") from error
                    failure = payload.get("error") if isinstance(payload, dict) else None
                    if (result.returncode == 0 or not isinstance(failure, dict)
                            or failure.get("code") != "KB_AUTH_REQUIRED"
                            or failure.get("message") != "Authentication is required."
                            or failure.get("hint") != 'Run "kb auth login".'):
                        raise RuntimeError("saved CLI session did not return KB_AUTH_REQUIRED with the login hint")

            platform_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(
                f"platform-update-{access_mode}.json")
            platform_journey = run_platform_update_steps(
                mode=access_mode,
                launcher=launcher,
                kb_binary=str(BIN / "kb"),
                platform_root=PLATFORM,
                update_index=update_index,
                request_file=request_file,
                base_version=str(base_version),
                updated_version=str(updated_version),
                run_command=update_command,
                verify_user=verify_admin,
                verify_contract_transition=verify_contract_transition if access_mode == "secured" else None,
            )
            update_result = platform_journey.write_json(platform_report)
            if update_result.status != "PASS":
                first = update_result.first_failure or {}
                fail(f"platform update journey: {first.get('name', 'step')}: {first.get('message', '')}")
            else:
                canary_index = HOME / "release-index-canary.json"
                canary_url = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"].rstrip("/") + "/journey-candidate/release-index-canary.json"
                try:
                    canary_status, canary_body, _ = open_http(
                        urllib.request.urlopen, urllib.request.Request(canary_url), timeout=30, env=env)
                    if canary_status != 200:
                        raise RuntimeError(f"GET {canary_url} returned HTTP {canary_status}")
                    canary_index.write_bytes(canary_body)
                    canary_version = channel_platform_version(json.loads(canary_index.read_text()), "canary")
                    channel_request = HOME / "platform-channel-request.json"
                    channel_request.write_text(json.dumps(stable_request) + "\n")
                    channel_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(
                        f"platform-channels-{access_mode}.json")
                    channel_journey = run_platform_channel_steps(
                        mode=access_mode, launcher=launcher, platform_root=PLATFORM,
                        stable_index=HOME / ".local/share/kb-create/release-index.json",
                        canary_index=canary_index, request_file=channel_request,
                        stable_version=str(base_version), canary_version=str(canary_version),
                        fresh_root=HOME / "canary-fresh", project_root=PROJECT,
                        run_command=update_command,
                    )
                    channel_result = channel_journey.write_json(channel_report)
                    if channel_result.status != "PASS":
                        first = channel_result.first_failure or {}
                        fail(f"platform channel journey: {first.get('name', 'step')}: {first.get('message', '')}")
                except (KeyError, OSError, ValueError, urllib.error.URLError) as error:
                    fail(f"platform channel candidate: could not load sealed canary index: {error}")

    for command, key in (("status", ""), ("doctor", "")):
        result, _ = run([launcher, command, "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
        if not require(result, step=f"kb-create {command}"):
            with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: {command}-output ==\n{result.stdout[-5000:]}\n")
            if command == "doctor":
                diagnostic, _ = run([launcher, command, "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
                with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                    stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: doctor-json (exit {diagnostic.returncode}) ==\n{(diagnostic.stdout or '')[-12000:]}\n{(diagnostic.stderr or '')[-4000:]}\n")
        elif command == "doctor":
            try:
                health = result_payload(json.loads(result.stdout))
                passed = (result.returncode == 0 and health.get("installed") is True and health.get("running") is True
                          and health.get("reachable") is True and health.get("platform", {}).get("ok") is True)
            except json.JSONDecodeError:
                passed = False
            if not passed:
                fail(f"kb-create doctor: expected healthy JSON report; {' '.join(result.stdout.split())[-1000:]}")
            record_case("healthy doctor", passed,
                       "doctor JSON did not report a healthy installation and platform",
                       cases=["PC-001.6", "PC-003.1", "S-039.3"])
            if FRONT_DOOR == "human" and not VARIANT.endswith("secured"):
                record_case("human install doctor", passed,
                           "doctor JSON did not report a healthy installation", cases=["S-040.4"])
            if mode == "secured":
                record_case("post-restart doctor", passed,
                           "doctor JSON did not report a healthy platform after restart", cases=["PC-004.3"])
        elif key and not re.search(key, result.stdout, re.I | re.S):
            with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: {command}-output ==\n{result.stdout[-5000:]}\n")
            fail(f"kb-create {command}: expected human summary '{key}'")
        if command == "status":
            try:
                status_payload = result_payload(json.loads(result.stdout))
                active_receipt = json.loads((PLATFORM / ".kb/v2/receipt.json").read_text())
                host_status = status_payload.get("host", {})
                installation = status_payload.get("installation", {})
                status_ok = (result.returncode == 0 and status_payload.get("installed") is True
                             and status_payload.get("running") is True and status_payload.get("reachable") is True
                             and host_status.get("state") == "running"
                             and installation.get("receiptId") == active_receipt.get("id"))
            except (json.JSONDecodeError, AttributeError, OSError):
                status_ok = False
            if not status_ok:
                fail("kb-create status: expected running/reachable JSON and active receipt ID")
            record_case("running status", status_ok,
                       "status JSON did not report a running host and active receipt",
                       cases=["PC-001.7", "PC-002.2"])
            if FRONT_DOOR == "agent":
                record_case("agent status", status_ok,
                           "status JSON did not match the apply receipt", cases=["S-039.2"])
            if FRONT_DOOR == "human" and not VARIANT.endswith("secured"):
                record_case("human install status", status_ok,
                           "status JSON did not report a running installation", cases=["S-040.3"])
            if mode == "secured":
                record_case("post-restart status", status_ok,
                           "status JSON did not report a running host after restart", cases=["PC-004.2"])

    verify_broken_install_doctor(launcher, env)

    local_service_journey = FRONT == "agent" and SCENARIO == "explore" and VARIANT == "default"
    secured_service_journey = FRONT == "human" and VARIANT.endswith("secured")
    if BASE_ENV.get("KB_JOURNEY_SERVICE_PROFILE") == "nightly" and (local_service_journey or secured_service_journey):
        mode = "secured" if secured_service_journey else "local"
        service_env = env.copy()
        service_env["KB_JOURNEY_CLI_PATH"] = str(BIN / "kb")
        service_env["KB_JOURNEY_PLATFORM_ROOT"] = str(PLATFORM)
        service_env["KB_JOURNEY_PROJECT_ROOT"] = str(PROJECT)
        if mode == "secured":
            service_env["KB_JOURNEY_ADMIN_EMAIL"] = "journey-admin@example.test"
            service_env["KB_JOURNEY_ADMIN_PASSWORD"] = "JourneyPassw0rd!"
            login, _ = run(["bash", "-c", '. "$HOME/.profile"; kb auth login --gateway-url http://127.0.0.1:4000 --email "$1" --password "$2"',
                            "journey", service_env["KB_JOURNEY_ADMIN_EMAIL"], service_env["KB_JOURNEY_ADMIN_PASSWORD"]],
                           env=service_env, cwd=PROJECT, timeout=90)
            if login.returncode != 0:
                fail(f"secured service journey admin CLI login: `kb auth login` exited {login.returncode}; {' '.join(login.stdout.split())[-400:]}")
        workflow_dir = PROJECT / ".kb" / "workflows"
        workflow_dir.mkdir(parents=True, exist_ok=True)
        (workflow_dir / "e2e-hello.yml").write_text(
            "name: e2e-hello\nversion: 1.0.0\ndescription: Installed service journey smoke workflow\n"
            "on:\n  manual: true\njobs:\n  smoke:\n    runsOn: local\n    steps:\n"
            "      - name: Say hello\n        id: hello\n        uses: builtin:shell\n"
            "        with:\n          command: echo hello-workflow\n",
            encoding="utf-8",
        )
        project_add, _ = run(["bash", "-c", '. "$HOME/.profile"; kb project add "$1" --json', "journey", str(PROJECT)],
                             env=env, cwd=PROJECT, timeout=90)
        try:
            project_payload = json.loads(project_add.stdout)
            project_id = find_project_id(project_payload)
        except json.JSONDecodeError:
            project_payload = {}
            project_id = ""
        if not require(project_add, step="kb project add") or not project_id:
            fail("project registration did not return a project ID")
        service_env["KB_JOURNEY_PROJECT_ID"] = project_id
        service_env["KB_JOURNEY_PROJECT_ROOT"] = str(PROJECT)
        lint, _ = run(["bash", "-c", '. "$HOME/.profile"; kb workflow lint --json'], env=service_env, cwd=PROJECT, timeout=120)
        if lint.returncode != 0:
            fail(f"workflow fixture lint: `kb workflow lint --json` exited {lint.returncode}; {' '.join(lint.stdout.split())[-500:]}")
        auth_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(f"service-auth-{mode}.json")
        auth_journey = Journey(mode, f"auth-{mode}")
        if mode == "secured":
            from secured_connection_steps import register_secured_connection_steps
            register_secured_connection_steps(auth_journey, service_env)

        def run_auth_flow():
            if mode == "local":
                run_local_auth_steps("http://127.0.0.1:4000")
            else:
                run_secured_auth_steps(
                    "http://127.0.0.1:4000",
                    service_env["KB_JOURNEY_ADMIN_EMAIL"],
                    service_env["KB_JOURNEY_ADMIN_PASSWORD"],
                    "auth-reset-member@example.test",
                    lambda: restart_host(launcher, env),
                )
            return None

        auth_capability = "auth.local" if mode == "local" else "auth.invite-reset"
        if mode == "local":
            auth_journey.step("local identity", auth_capability, run_auth_flow, cases=["S-041.1"])
            auth_journey.step("local CLI status", "auth.local",
                              lambda: run_cli_auth_status(str(BIN / "kb"), env, authenticated=False),
                              cases=["S-041.5"])
        else:
            auth_journey.step("admin invite, reset, and restart persistence", auth_capability, run_auth_flow,
                              cases=["S-041.2", "S-041.3"])
            auth_journey.step(
                "bootstrap administrator CLI status", "auth.accounts",
                lambda: run_cli_auth_status(str(BIN / "kb"), service_env, authenticated=True,
                                            email=service_env["KB_JOURNEY_ADMIN_EMAIL"],
                                            password=service_env["KB_JOURNEY_ADMIN_PASSWORD"],
                                            base_url="http://127.0.0.1:4000"),
                cases=["S-100.1"],
            )
            recovered_password = "JourneyRecovered123!"
            auth_journey.step(
                "bootstrap administrator recovery", "auth.accounts",
                lambda: run_bootstrap_recovery_steps(
                    launcher, str(PLATFORM), service_env["KB_JOURNEY_ADMIN_EMAIL"],
                    service_env["KB_JOURNEY_ADMIN_PASSWORD"], recovered_password, env, run,
                    on_valid_password=lambda password: service_env.__setitem__("KB_JOURNEY_ADMIN_PASSWORD", password),
                ),
                cases=["S-100.2", "S-100.3", "S-100.4"],
            )
            auth_journey.step(
                "tenant roles, API token lifecycle, last-admin guard, and invite revocation",
                "auth.accounts",
                lambda: run_secured_account_steps(
                    "http://127.0.0.1:4000",
                    service_env["KB_JOURNEY_ADMIN_EMAIL"],
                    service_env["KB_JOURNEY_ADMIN_PASSWORD"],
                    "auth-reset-member@example.test",
                    "JourneyReplacement123!",
                ),
                cases=["S-103.1", "S-103.2", "S-103.3", "S-103.4", "S-103.5",
                       "S-104.7", "S-105.1", "S-105.2", "S-105.3", "S-105.4"],
            )
            auth_journey.step(
                "member CLI login and token listing", "auth.accounts",
                lambda: run_cli_auth_status(str(BIN / "kb"), service_env, authenticated=True,
                                            email="auth-reset-member@example.test",
                                            password="JourneyReplacement123!",
                                            base_url="http://127.0.0.1:4000"),
                cases=["S-041.6"],
            )
            auth_journey.step(
                "member reset-link command returns the shared permission envelope", "auth.accounts",
                lambda: run_cli_auth_forbidden(str(BIN / "kb"), service_env),
                cases=["S-041.9"],
            )
            auth_journey.step(
                "change password and manage browser sessions", "auth.accounts",
                lambda: run_password_and_session_steps(
                    "http://127.0.0.1:4000", "auth-reset-member@example.test", "JourneyReplacement123!"),
                cases=["S-101.1", "S-101.2", "S-101.3", "S-102.1", "S-102.2"],
            )
            auth_journey.step(
                "CLI logout clears saved session", "auth.accounts",
                lambda: run_cli_logout_steps(str(BIN / "kb"), service_env),
                cases=["S-102.3"],
            )
        auth_result = auth_journey.write_json(auth_report)
        if auth_result.status != "PASS":
            fail(f"{mode} auth journey: {auth_result.first_failure}")
        prepare_workflow_fixtures(PROJECT)
        service_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(f"service-{mode}.json")
        studio_fixture_file = service_report.with_name("studio-analytics-fixtures.json")
        project_fixture_file = service_report.with_name(f"project-fixtures-{mode}.json")
        studio_fixture_file.unlink(missing_ok=True)
        project_fixture_file.unlink(missing_ok=True)
        service_env["KB_JOURNEY_STUDIO_FIXTURE_FILE"] = str(studio_fixture_file)
        service_env["KB_JOURNEY_PROJECT_FIXTURES_FILE"] = str(project_fixture_file)
        service_env.pop("KB_JOURNEY_PROJECT_B_ID", None)
        service_env.pop("KB_JOURNEY_PROJECT_B_ROOT", None)
        result, elapsed = run(["python3", "/opt/kb-journey/service_runner.py", "--mode", mode, "--output", str(service_report)],
                             env=service_env, timeout=900)
        TIMES[f"{mode} service journeys"] = elapsed
        if result.returncode != 0:
            try:
                service_result = json.loads(service_report.read_text())
                first = service_result.get("first_failure") or {}
                detail = f"{first.get('name', 'journey step')}: {first.get('message', '')}"
            except (OSError, json.JSONDecodeError):
                excerpt = " ".join(result.stdout.split())[-1500:]
                detail = f"service_runner exited {result.returncode} without a result JSON; {excerpt or 'no output'}"
            fail(f"{mode} service journey: {detail}")

        if not service_report.exists():
            # Nothing to merge workflow claims into; the failure above names the cause.
            append_summary(False)
            return failure_exit()
        try:
            project_fixtures = json.loads(project_fixture_file.read_text(encoding="utf-8"))
            project_b_id = project_fixtures.get("projectBId")
            project_b_root_value = project_fixtures.get("projectBRoot")
            if (not isinstance(project_b_id, str) or not project_b_id
                    or not isinstance(project_b_root_value, str)
                    or not Path(project_b_root_value).is_absolute()
                    or str(Path(project_b_root_value).resolve()) != project_b_root_value):
                raise ValueError("registry result omitted a valid project B ID or canonical root")
        except (OSError, json.JSONDecodeError, AttributeError, ValueError) as error:
            fail(f"project B fixture handoff failed: {error}; rerun the installed journey to register its isolation project")
        service_env["KB_JOURNEY_PROJECT_B_ID"] = project_b_id
        service_env["KB_JOURNEY_PROJECT_B_ROOT"] = project_b_root_value
        workflow_journey = Journey(mode, f"workflow-{mode}")
        def restart_for_workflow_history() -> None:
            restart_host(launcher, env)

        run_workflow_journey(
            workflow_journey,
            env=service_env,
            project=PROJECT,
            restart_host=restart_for_workflow_history,
        )
        project_b_root_value = service_env["KB_JOURNEY_PROJECT_B_ROOT"]
        project_b_id = service_env["KB_JOURNEY_PROJECT_B_ID"]
        if project_b_root_value and project_b_id:
            project_b_fixture_root = Path(project_b_root_value)
            prepare_workflow_fixtures(project_b_fixture_root)
            definitions_deadline = time.monotonic() + 30
            healthy_workflow_ready = False
            while time.monotonic() < definitions_deadline:
                with invocation_context(step_id="journey.setup"):
                    definitions_result, _ = run(["bash", "-c", '. "$HOME/.profile"; kb workflow defs list --json'],
                                                env=service_env, cwd=project_b_fixture_root, timeout=90)
                try:
                    definitions_payload = json.loads(definitions_result.stdout)
                    definitions_data = definitions_payload.get("data", definitions_payload)
                    healthy_workflow_ready = isinstance(definitions_data, list) and any(
                        isinstance(item, dict) and item.get("id") == "journey-hello" for item in definitions_data)
                except json.JSONDecodeError:
                    healthy_workflow_ready = False
                if healthy_workflow_ready:
                    break
                time.sleep(1)
            healthy_result = None
            if healthy_workflow_ready:
                with invocation_context(step_id="journey.setup"):
                    healthy_result, _ = run(
                        ["bash", "-c", '. "$HOME/.profile"; kb workflow run --workflow-id=journey-hello --input=\'{"message":"studio-healthy-project"}\' --json'],
                        env=service_env, cwd=project_b_fixture_root, timeout=120)
            try:
                healthy_payload = json.loads(healthy_result.stdout) if healthy_result else {}
                healthy_data = healthy_payload.get("data", healthy_payload)
                healthy_run_id = healthy_data.get("runId", "") if isinstance(healthy_data, dict) else ""
            except json.JSONDecodeError:
                healthy_run_id = ""
            if healthy_result is not None and healthy_result.returncode == 0 and isinstance(healthy_run_id, str) and healthy_run_id:
                healthy_deadline = time.monotonic() + 180
                while time.monotonic() < healthy_deadline:
                    with invocation_context(step_id="journey.setup"):
                        viewed, _ = run(["bash", "-c", '. "$HOME/.profile"; kb workflow runs view "$1" --json=all',
                                         "journey", healthy_run_id], env=service_env,
                                        cwd=project_b_fixture_root, timeout=90)
                    try:
                        detail_payload = json.loads(viewed.stdout)
                        detail = detail_payload.get("data", detail_payload)
                    except json.JSONDecodeError:
                        detail = {}
                    if isinstance(detail, dict) and detail.get("status") in {"success", "failed", "cancelled"}:
                        if detail.get("status") == "success":
                            service_env["KB_STUDIO_HEALTHY_PROJECT_RUN_ID"] = healthy_run_id
                        break
                    time.sleep(2)
        workflow_result = workflow_journey.result()
        merge_workflow_result(service_report, workflow_result.__dict__)
        if workflow_result.status != "PASS":
            fail(f"{mode} workflow journey: {workflow_result.first_failure}")

        project = project_payload.get("project", project_payload) if isinstance(project_payload, dict) else {}
        project_name = str(project.get("name") or "") if isinstance(project, dict) else ""
        studio_env = create_studio_environment(
            service_env,
            mode=mode,
            project_id=project_id,
            project_name=project_name,
            project_root=str(PROJECT),
            kb_bin=str(BIN / "kb"),
        )
        cron_id = "studio-a"
        (PROJECT / ".kb" / "crons").mkdir(parents=True, exist_ok=True)
        (PROJECT / ".kb" / "crons" / f"{cron_id}.yml").write_text(
            "schedule: '0 0 * * *'\nworkflow: journey-hello\ninput:\n  message: studio-a\nenabled: true\n",
            encoding="utf-8")
        project_a_plugin_cron_ids = []
        project_a_user_cron_ids = []
        project_a_cron_error = "project A cron list did not contain the fixture user and plugin crons"
        cron_list_deadline = time.monotonic() + 30
        while time.monotonic() < cron_list_deadline:
            project_a_cron_list, _ = run(
                ["bash", "-c", '. "$HOME/.profile"; kb workflow crons list --json'],
                env=studio_env, cwd=PROJECT, timeout=120)
            try:
                project_a_crons = json.loads(project_a_cron_list.stdout)["data"]["crons"]
                project_a_plugin_cron_ids = [str(cron["id"]) for cron in project_a_crons
                                             if isinstance(cron, dict) and cron.get("source") == "plugin"]
                project_a_user_cron_ids = [str(cron["id"]) for cron in project_a_crons
                                           if isinstance(cron, dict) and cron.get("source") == "user"]
                if (project_a_cron_list.returncode == 0 and project_a_plugin_cron_ids
                        and cron_id in project_a_user_cron_ids):
                    break
            except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
                project_a_cron_error = str(error)
            time.sleep(1)
        else:
            fail(f"Studio schedule journey could not list project A crons: {project_a_cron_error}")
        if not project_b_root_value or not project_b_id:
            fail("installed Studio fixture requires registered KB_JOURNEY_PROJECT_B_ROOT and KB_JOURNEY_PROJECT_B_ID")
        project_b_root = Path(project_b_root_value)
        (project_b_root / ".kb" / "workflows").mkdir(parents=True, exist_ok=True)
        (project_b_root / ".kb" / "crons").mkdir(parents=True, exist_ok=True)
        (project_b_root / ".kb" / "workflows" / "studio-b-workflow.yml").write_text(
            "name: studio-b-workflow\nversion: 1.0.0\non:\n  manual: true\njobs:\n  smoke:\n    runsOn: local\n    steps:\n"
            "      - name: Log B\n        id: log-b\n        uses: builtin:shell\n        with:\n          command: 'echo studio-b'\n",
            encoding="utf-8")
        (project_b_root / ".kb" / "crons" / "studio-b.yml").write_text(
            "schedule: '0 0 * * *'\nworkflow: studio-b-workflow\nenabled: true\n", encoding="utf-8")
        studio_env.update({
            "KB_STUDIO_CRON_PROJECT_ROOT": str(PROJECT),
            "KB_STUDIO_CRON_ID": cron_id,
            "KB_STUDIO_PROJECT_A_PLUGIN_CRON_IDS": ",".join(project_a_plugin_cron_ids),
            "KB_STUDIO_PROJECT_A_USER_CRON_IDS": ",".join(project_a_user_cron_ids),
            "KB_STUDIO_PROJECT_B_ID": project_b_id,
            "KB_STUDIO_PROJECT_B_CRON_ID": "studio-b",
            "KB_STUDIO_PLAYWRIGHT_JSON": str(service_report.with_name(f"studio-playwright-{mode}.json")),
            "KB_STUDIO_PROJECT_A_NAME": project_name,
            "KB_STUDIO_PROJECT_B_NAME": project_b_root.name,
        })
        if service_env.get("KB_STUDIO_DIAGNOSTIC_RUN_ID") and service_env.get("KB_STUDIO_HEALTHY_PROJECT_RUN_ID"):
            studio_env["KB_STUDIO_DIAGNOSTIC_PROJECT"] = project_name
            studio_env["KB_STUDIO_DIAGNOSTIC_RUN_ID"] = service_env["KB_STUDIO_DIAGNOSTIC_RUN_ID"]
            studio_env["KB_STUDIO_HEALTHY_PROJECT_RUN_ID"] = service_env["KB_STUDIO_HEALTHY_PROJECT_RUN_ID"]
        if studio_fixture_file.is_file():
            analytics_fixture = json.loads(studio_fixture_file.read_text())
            analytics_fields = {
                "eventType": "KB_STUDIO_ANALYTICS_EVENT_TYPE",
                "source": "KB_STUDIO_ANALYTICS_SOURCE",
                "model": "KB_STUDIO_ANALYTICS_MODEL",
                "eventId": "KB_STUDIO_ANALYTICS_EVENT_ID",
            }
            if all(isinstance(analytics_fixture.get(key), str) and analytics_fixture[key]
                   for key in analytics_fields):
                studio_env.update({target: analytics_fixture[key]
                                   for key, target in analytics_fields.items()})
            project_event_fields = {
                "projectAEvent": "KB_STUDIO_PROJECT_A_EVENT",
                "projectBEvent": "KB_STUDIO_PROJECT_B_EVENT",
            }
            if all(isinstance(analytics_fixture.get(key), str) and analytics_fixture[key]
                   for key in project_event_fields):
                studio_env.update({target: analytics_fixture[key]
                                   for key, target in project_event_fields.items()})
        try:
            recorded_service_result = json.loads(service_report.read_text())
            studio_shell = next((step for step in recorded_service_result.get("steps", [])
                                 if step.get("capability") == "studio" and step.get("name") == "Studio shell"), None)
            studio_shell_passed = studio_shell is not None and studio_shell.get("status") == "PASS"
        except (OSError, json.JSONDecodeError):
            studio_shell_passed = False
        if mode == "secured":
            try:
                with invocation_context(step_id="S-106.2"):
                    member_email, member_password = create_studio_member(
                        "http://127.0.0.1:4000", service_env["KB_JOURNEY_ADMIN_EMAIL"],
                        service_env["KB_JOURNEY_ADMIN_PASSWORD"])
                studio_env["KB_STUDIO_MEMBER_EMAIL"] = member_email
                studio_env["KB_STUDIO_MEMBER_PASSWORD"] = member_password
                studio_env["KB_STUDIO_OAUTH_JOURNEY"] = "1"
                studio_env["KB_STUDIO_OIDC_PROVIDER_ID"] = "corp"
                studio_env["KB_STUDIO_OIDC_PROVIDER_NAME"] = "corp"
                studio_env["KB_STUDIO_OIDC_EMAIL"] = service_env["KB_JOURNEY_ADMIN_EMAIL"]
                with invocation_context(step_id="journey.setup"):
                    admin_token, member_token = create_studio_access_tokens(
                        "http://127.0.0.1:4000", service_env["KB_JOURNEY_ADMIN_EMAIL"],
                        service_env["KB_JOURNEY_ADMIN_PASSWORD"], member_email, member_password,
                        env=service_env)
                studio_env["KB_STUDIO_ADMIN_TOKEN"] = admin_token
                studio_env["KB_STUDIO_MEMBER_TOKEN"] = member_token
                required_studio_credentials = (
                    "KB_STUDIO_ADMIN_EMAIL", "KB_STUDIO_ADMIN_PASSWORD",
                    "KB_STUDIO_MEMBER_EMAIL", "KB_STUDIO_MEMBER_PASSWORD",
                )
                missing_studio_credentials = [key for key in required_studio_credentials
                                              if not studio_env.get(key)]
                if missing_studio_credentials:
                    raise RuntimeError(
                        "secured Studio test setup did not export "
                        + ", ".join(missing_studio_credentials)
                        + "; provide journey admin credentials and rerun the secured journey"
                    )
            except (KeyError, RuntimeError, urllib.error.URLError, ValueError) as error:
                fail(f"Studio secured member setup: {error}")
        studio_claims = "S-106.1,S-106.2,S-106.3,S-106.4,S-106.5,S-106.6,S-107.1,S-107.2,S-107.3"
        if mode == "secured":
            studio_claims += ",S-109.1,S-109.2,S-109.3,S-109.4"
        with invocation_context(step_id=studio_claims):
            studio_passed, studio_elapsed, studio_message = run_studio_browser_step([
                "/opt/kb-studio-playwright/node_modules/.bin/playwright", "test",
                "--config=/opt/kb-studio-playwright/playwright.config.mjs",
            ], studio_env, service_report, mode, studio_shell_passed, timeout=660)
        TIMES["Studio browser journey"] = studio_elapsed
        if not studio_passed:
            fail(f"Studio browser journey: {studio_message}")

    global PLATFORM_SIZE_MIB
    if PLATFORM.exists():
        PLATFORM_SIZE_MIB = sum(p.stat().st_size for p in PLATFORM.rglob("*") if p.is_file()) / (1024 * 1024)
    uninstall_args = [launcher, "uninstall", "--platform-root", str(PLATFORM), "--registry", REGISTRY]
    scope_is_separate = PLATFORM.resolve() != PROJECT.resolve() and PLATFORM.exists() and PROJECT.is_dir()
    record_case("inspect uninstall scope", scope_is_separate,
                "platform root is not separate from the project directory", cases=["S-050.1"])
    capture_case_failure()
    if FIRST_FAILURE:
        dump_failure_state()
    refused, _ = run([*uninstall_args, "--json"], env=env, timeout=120)
    try:
        refusal_payload = json.loads(refused.stdout)
    except json.JSONDecodeError:
        refusal_payload = {}
    refusal_error = refusal_payload.get("error", {})
    expected_hint = (f'rerun `kb-create uninstall --platform-root "{PLATFORM}" '
                     f"--registry {REGISTRY} --json --yes`")
    refusal_ok = (refused.returncode == 2 and refusal_error.get("code") == "KB_INSTALL_CONFIRMATION_REQUIRED"
                  and refusal_error.get("hint") == expected_hint
                  and (PLATFORM / ".kb/v2/receipt.json").is_file())
    record_case("non-TTY uninstall requires confirmation", refusal_ok,
                "non-TTY uninstall did not require --yes or left the receipt unchanged", cases=["S-050.4"])
    capture_case_failure()
    if FIRST_FAILURE:
        dump_failure_state()
    if FRONT_DOOR == "human":
        result, _ = run_tty_confirmation(uninstall_args, env=env, timeout=180)
    else:
        result, _ = run([*uninstall_args, "--yes", "--json"], env=env, timeout=180)
    if require(result, step="kb-create uninstall"):
        if FRONT_DOOR == "human":
            prompt_ok = all(fragment in result.stdout for fragment in
                            (str(PLATFORM), "services", "receipt", "snapshots", "Project files are kept"))
            record_case("human uninstall confirmation prompt", prompt_ok,
                       "uninstall prompt omitted its root, artifacts, receipt, snapshots, or project preservation",
                       cases=["S-050.2"])
            human_uninstall_ok = (result.returncode == 0 and "uninstall" in result.stdout.lower())
            record_case("human confirmed uninstall", human_uninstall_ok,
                       "confirmed human uninstall did not show its completion summary",
                       cases=["S-050.5"])
        else:
            try:
                uninstall_payload = result_payload(json.loads(result.stdout))
            except json.JSONDecodeError:
                uninstall_payload = {}
            uninstall_ok = (result.returncode == 0 and uninstall_payload.get("operation") == "uninstall"
                            and bool(uninstall_payload.get("snapshot")) and bool(uninstall_payload.get("logPath")))
            record_case("agent confirmed uninstall", uninstall_ok,
                       "agent uninstall result omitted operation, snapshot, or logPath", cases=["S-050.6"])
        alive = [p for p in PROCESSES if p.poll() is None]
        listeners, _ = run(["ss", "-H", "-ltnp"], timeout=20)
        platform_pids = set()
        for pid in re.findall(r"pid=(\d+)", listeners.stdout):
            try:
                command = Path(f"/proc/{pid}/cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace")
                cwd = os.readlink(f"/proc/{pid}/cwd")
                if str(PLATFORM) in command or cwd.startswith(str(PLATFORM)):
                    platform_pids.add(pid)
            except OSError:
                continue
        if platform_pids:
            fail(f"uninstall left platform listeners: {', '.join(sorted(platform_pids))}")
        if alive or platform_pids or not uninstall_removed_platform_root() or (BIN / "kb").exists():
            fail("uninstall left a platform process, listening port, shim, platform root, receipt, or snapshots")
        project_preserved = (PROJECT.is_dir() and (PROJECT / "note.txt").is_file()
                             and (PROJECT / "note.txt").read_text() == "journey first value\n")
        record_case("project files survive uninstall", project_preserved,
                    "platform uninstall removed or changed the separate project fixture", cases=["S-050.7"])
    capture_case_failure()
    passed = not FIRST_FAILURE
    append_summary(passed)
    return 0 if passed else failure_exit()


def cleanup() -> None:
    for process in PROCESSES:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=10)
    shutil.rmtree(HOME, ignore_errors=True)


if __name__ == "__main__":
    try:
        sys.exit(main())
    finally:
        cleanup()
