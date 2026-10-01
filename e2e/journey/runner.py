#!/usr/bin/env python3
"""One isolated end-user install journey; each invocation runs in a fresh container."""
from __future__ import annotations

import json
import os
import pty
import re
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

from studio_steps import create_studio_environment, create_studio_member, run_studio_browser_step
from platform_update_steps import run_platform_update_steps
from platform_channel_steps import run_platform_channel_steps
from release_index import channel_platform_version, stable_platform_version
from service_steps import Journey, record_runner_failure
from workflow_steps import merge_workflow_result, prepare_workflow_fixtures, run_workflow_journey
from steps.auth import run_bootstrap_recovery_steps, run_cli_auth_forbidden, run_cli_auth_status, run_local_auth_steps, run_secured_auth_steps
from steps.accounts import run_cli_logout_steps, run_password_and_session_steps, run_secured_account_steps
from platform_state_dump import PlatformStateDumpOnce

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
PROCESSES: list[subprocess.Popen] = []
FIRST_FAILURE = ""
CASE_JOURNEY: Journey | None = None
LAST_COMMAND_ENV: dict[str, str] = BASE_ENV.copy()
LAST_COMMAND_CWD: str | None = None
STATE_DUMP_GUARD = PlatformStateDumpOnce()


def record_case(name: str, passed: bool, message: str, *, cases: list[str]) -> None:
    if CASE_JOURNEY is None:
        return
    CASE_JOURNEY.step(name, "platform.install", lambda: None if passed else message, cases=cases)


def run(args: list[str], *, env=None, cwd=None, timeout=900, input_text=None):
    global MAX_PNPM_RSS_MIB, LAST_COMMAND_ENV, LAST_COMMAND_CWD
    LAST_COMMAND_ENV = dict(env or BASE_ENV)
    LAST_COMMAND_CWD = str(cwd) if cwd else str(Path.cwd())
    started = time.monotonic()
    proc = subprocess.Popen(args, cwd=cwd, env=env or BASE_ENV, text=True,
                            stdin=subprocess.PIPE if input_text is not None else subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if input_text is not None:
        proc.stdin.write(input_text)
        proc.stdin.close()
    deadline = started + timeout
    while proc.poll() is None:
        MAX_PNPM_RSS_MIB = max(MAX_PNPM_RSS_MIB, sample_pnpm_rss(proc.pid))
        if time.monotonic() >= deadline:
            proc.kill()
            proc.wait()
            raise subprocess.TimeoutExpired(args, timeout)
        time.sleep(0.05)
    output = proc.stdout.read()
    return subprocess.CompletedProcess(args, proc.returncode, stdout=output), time.monotonic() - started


def run_tty_confirmation(args: list[str], *, env=None, timeout=900):
    started = time.monotonic()
    master, slave = pty.openpty()
    proc = subprocess.Popen(args, env=env or BASE_ENV, stdin=slave, stdout=slave,
                            stderr=slave, close_fds=True)
    os.close(slave)
    output = bytearray()
    confirmed = False
    deadline = started + timeout
    while proc.poll() is None:
        if time.monotonic() >= deadline:
            proc.kill()
            proc.wait()
            os.close(master)
            raise subprocess.TimeoutExpired(args, timeout)
        readable, _, _ = select.select([master], [], [], 0.2)
        if not readable:
            continue
        try:
            chunk = os.read(master, 4096)
        except OSError:
            break
        output.extend(chunk)
        if not confirmed and b"Continue? [y/N]" in output:
            os.write(master, b"y\n")
            confirmed = True
    proc.wait()
    os.close(master)
    return subprocess.CompletedProcess(args, proc.returncode, stdout=output.decode(errors="replace")), time.monotonic() - started


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
        response = urllib.request.urlopen(request, timeout=30)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"MCP {method} returned HTTP {error.code}") from error
    body = response.read().decode()
    if any(line.startswith("data:") for line in body.splitlines()):
        body = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
    return json.loads(body)


def verify_project_mcp(env: dict[str, str], local: bool):
    registered, _ = run(["bash", "-c", '. "$HOME/.profile"; kb project add --init --json'],
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
    master, slave = pty.openpty()
    started = time.monotonic()
    proc = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave,
                            env=env, start_new_session=True)
    os.close(slave)
    output = bytearray()
    deadline = time.monotonic() + 900
    sent_choice = not choice
    sent_access_mode = False
    last_prompt = ""
    apply_started = None
    while proc.poll() is None and time.monotonic() < deadline:
        MAX_PNPM_RSS_MIB = max(MAX_PNPM_RSS_MIB, sample_pnpm_rss(proc.pid))
        try:
            ready, _, _ = select.select([master], [], [], 0.2)
            if not ready:
                continue
            chunk = os.read(master, 8192)
            if chunk:
                output.extend(chunk)
                if apply_started is None and re.search(rb"Applying installation", chunk, re.I):
                    apply_started = time.monotonic()
                visible = output.decode(errors="replace")[-2000:]
                if access_mode == "secured" and not sent_access_mode and "Studio access" in visible:
                    os.write(master, b"secured\n")
                    sent_access_mode = True
                    last_prompt = "Studio access (secured)"
                    continue
                prompts = re.findall(r"(?im)^.*(?:\?|\[.*(?:default|y/n|yes/no).*\]|default[^\n]*:|press enter[^\n]*|select[^\n]*:|admin email[^\n]*:|admin tenant[^\n]*:|admin password[^\n]*:|confirm admin password[^\n]*:|session signing secret[^\n]*:).*?$", visible)
                prompt = prompts[-1] if prompts else ""
                if prompt and prompt != last_prompt:
                    answer = ""
                    if access_mode == "secured" and re.search(r"Studio access", prompt, re.I):
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
                    os.write(master, (answer + "\n").encode())
                    last_prompt = prompt
        except OSError:
            break
    if proc.poll() is None:
        os.killpg(proc.pid, signal.SIGKILL)
        proc.wait()
    os.close(master)
    finished = time.monotonic()
    return proc.returncode, output.decode(errors="replace"), finished - started, apply_started - started if apply_started else None


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
        payload = json.loads(result.stdout)
    except json.JSONDecodeError:
        payload = {}
    restarted = (result.returncode == 0 and payload.get("ok") is True
                 and payload.get("operation") == "restart" and payload.get("host", {}).get("pid", 0) > 0)
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
            diagnostic = json.loads(result.stdout)
        except json.JSONDecodeError:
            diagnostic = {}
        if (result.returncode != 1 or diagnostic.get("ok") is not False
                or diagnostic.get("installed") is not False or diagnostic.get("running") is not False
                or diagnostic.get("reachable") is not False or diagnostic.get("next") != "kb-create install"):
            fail(f"kb-create doctor did not diagnose missing installation receipt with the install next step: {' '.join(result.stdout.split())[-500:]}")
        record_case("doctor reports missing receipt",
                   result.returncode == 1 and diagnostic.get("ok") is False
                   and diagnostic.get("installed") is False and diagnostic.get("running") is False
                   and diagnostic.get("reachable") is False and diagnostic.get("next") == "kb-create install",
                   "doctor did not return the missing-receipt result", cases=["PC-003.2"])
    finally:
        os.replace(hidden_receipt, receipt)
    restored, _ = run([launcher, "doctor", "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
    try:
        restored_payload = json.loads(restored.stdout)
    except json.JSONDecodeError:
        restored_payload = {}
    restored_ok = (restored.returncode == 0 and restored_payload.get("ok") is True
                   and restored_payload.get("installed") is True and restored_payload.get("running") is True
                   and restored_payload.get("reachable") is True
                   and restored_payload.get("platform", {}).get("ok") is True)
    record_case("doctor recovers after receipt restore", restored_ok,
               "doctor did not return a healthy result after restoring the receipt", cases=["PC-003.3"])


def main():
    global FIRST_FAILURE, CASE_JOURNEY
    STATE_DUMP_GUARD.reset()
    env = BASE_ENV.copy()
    env["HOME"] = str(HOME)
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
        listener = subprocess.Popen(["python3", "-u", "-c", "import http.server; s=http.server.HTTPServer(('0.0.0.0',4000),http.server.SimpleHTTPRequestHandler); print('ready', flush=True); s.serve_forever()"], stdout=subprocess.PIPE, text=True)
        PROCESSES.append(listener)
        if listener.stdout is not None and listener.stdout.readline().strip() != "ready":
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
                            and re.search(r"Plan: platform [^,]+, \d+ artifacts", output) is not None
                            and "Applying installation" in output
                            and re.search(r"(?i)next|success|installed|ready", output) is not None)
        if FRONT == "human" and not VARIANT.endswith("secured"):
            record_case("human interactive install",
                       human_install_ok, "interactive install did not show a plan and success result",
                       cases=["PC-001.2", "S-040.1", "S-040.2"])
    else:
        operation = "plan" if VARIANT in {"input", "relative-path"} else "apply"
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
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError:
            fail("kb-create apply --json: output was not one JSON document")
            append_summary(False)
            return failure_exit()
        if payload.get("ok") is not True:
            fail("kb-create apply --json: result did not report ok=true")
        if operation == "plan":
            plan_payload = payload.get("plan", {})
            plan_ok = (result.returncode == 0 and payload.get("ok") is True
                       and plan_payload.get("request", {}).get("platformRoot") == str(PLATFORM)
                       and bool(plan_payload.get("artifacts")) and bool(plan_payload.get("serviceGraph")))
            record_case("agent plan", plan_ok,
                       "plan omitted the isolated root, resolved artifacts, or service graph", cases=["PC-001.3"])
        elif operation == "apply":
            apply_ok = (result.returncode == 0 and payload.get("ok") is True
                        and isinstance(payload.get("receipt"), dict) and bool(payload.get("logPath"))
                        and payload.get("receipt", {}).get("plan", {}).get("request", {}).get("platformRoot") == str(PLATFORM))
            record_case("JSON apply", apply_ok,
                        "apply JSON omitted ok, the root-matched receipt, or logPath", cases=["PC-001.4"])
            if FRONT_DOOR == "agent":
                record_case("agent JSON apply", apply_ok,
                            "agent apply JSON omitted ok, receipt, or logPath", cases=["S-039.1"])
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
        if status.stdout.strip() != "?? note.txt" and status.stdout.strip() != "A  note.txt":
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
            with urllib.request.urlopen(update_url, timeout=30) as response:
                update_index.write_bytes(response.read())
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
            )
            update_result = platform_journey.write_json(platform_report)
            if update_result.status != "PASS":
                first = update_result.first_failure or {}
                fail(f"platform update journey: {first.get('name', 'step')}: {first.get('message', '')}")
            else:
                canary_index = HOME / "release-index-canary.json"
                canary_url = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"].rstrip("/") + "/journey-candidate/release-index-canary.json"
                try:
                    with urllib.request.urlopen(canary_url, timeout=30) as response:
                        canary_index.write_bytes(response.read())
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
                health = json.loads(result.stdout)
                passed = (result.returncode == 0 and health.get("ok") is True
                          and health.get("installed") is True and health.get("running") is True
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
                status_payload = json.loads(result.stdout)
                active_receipt = json.loads((PLATFORM / ".kb/v2/receipt.json").read_text())
                host_status = status_payload.get("host", {})
                installation = status_payload.get("installation", {})
                status_ok = (result.returncode == 0 and status_payload.get("ok") is True
                             and status_payload.get("installed") is True
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
        lint, _ = run(["bash", "-c", '. "$HOME/.profile"; kb workflow lint --json'], env=env, cwd=PROJECT, timeout=120)
        if lint.returncode != 0:
            fail(f"workflow fixture lint: `kb workflow lint --json` exited {lint.returncode}; {' '.join(lint.stdout.split())[-500:]}")
        project_add, _ = run(["bash", "-c", '. "$HOME/.profile"; kb project add "$1" --init --json', "journey", str(PROJECT)],
                             env=env, cwd=PROJECT, timeout=90)
        try:
            project_payload = json.loads(project_add.stdout)
            project_id = find_project_id(project_payload)
        except json.JSONDecodeError:
            project_payload = {}
            project_id = ""
        if not require(project_add, step="kb project add --init") or not project_id:
            fail("project registration did not return a project ID")
        service_env["KB_JOURNEY_PROJECT_ID"] = project_id
        service_env["KB_JOURNEY_PROJECT_ROOT"] = str(PROJECT)
        auth_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(f"service-auth-{mode}.json")
        auth_journey = Journey(mode, f"auth-{mode}")

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
                              cases=["S-041.2", "S-041.3", "S-041.4"])
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
        workflow_journey = Journey(mode, f"workflow-{mode}")
        def restart_for_workflow_history() -> None:
            restart_host(launcher, env)

        run_workflow_journey(
            workflow_journey,
            env=service_env,
            project=PROJECT,
            restart_host=restart_for_workflow_history,
        )
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
        project_b_root = HOME / "studio-project-b"
        (project_b_root / ".kb" / "workflows").mkdir(parents=True, exist_ok=True)
        (project_b_root / ".kb" / "crons").mkdir(parents=True, exist_ok=True)
        (project_b_root / ".kb" / "workflows" / "studio-b-workflow.yml").write_text(
            "name: studio-b-workflow\nversion: 1.0.0\non:\n  manual: true\njobs:\n  smoke:\n    runsOn: local\n    steps:\n"
            "      - name: Log B\n        id: log-b\n        uses: builtin:shell\n        with:\n          command: 'echo studio-b'\n",
            encoding="utf-8")
        (project_b_root / ".kb" / "crons" / "studio-b.yml").write_text(
            "schedule: '0 0 * * *'\nworkflow: studio-b-workflow\nenabled: true\n", encoding="utf-8")
        project_b_registered, _ = run(["bash", "-c", '. "$HOME/.profile"; kb project add --init --json'],
                                      env=service_env, cwd=project_b_root, timeout=180)
        try:
            project_b_id = str(json.loads(project_b_registered.stdout)["project"]["id"])
        except (KeyError, ValueError, TypeError):
            project_b_id = ""
            fail("Studio schedule journey could not register project B")
        studio_env.update({
            "KB_STUDIO_CRON_PROJECT_ROOT": str(PROJECT),
            "KB_STUDIO_CRON_ID": cron_id,
            "KB_STUDIO_PROJECT_A_PLUGIN_CRON_IDS": ",".join(project_a_plugin_cron_ids),
            "KB_STUDIO_PROJECT_A_USER_CRON_IDS": ",".join(project_a_user_cron_ids),
            "KB_STUDIO_PROJECT_B_ID": project_b_id,
            "KB_STUDIO_PROJECT_B_CRON_ID": "studio-b",
            "KB_STUDIO_PLAYWRIGHT_JSON": str(service_report.with_name(f"studio-playwright-{mode}.json")),
        })
        try:
            recorded_service_result = json.loads(service_report.read_text())
            studio_shell = next((step for step in recorded_service_result.get("steps", [])
                                 if step.get("capability") == "studio" and step.get("name") == "Studio shell"), None)
            studio_shell_passed = studio_shell is not None and studio_shell.get("status") == "PASS"
        except (OSError, json.JSONDecodeError):
            studio_shell_passed = False
        if mode == "secured":
            try:
                member_email, member_password = create_studio_member(
                    "http://127.0.0.1:4000", service_env["KB_JOURNEY_ADMIN_EMAIL"],
                    service_env["KB_JOURNEY_ADMIN_PASSWORD"])
                studio_env["KB_STUDIO_ADMIN_EMAIL"] = service_env["KB_JOURNEY_ADMIN_EMAIL"]
                studio_env["KB_STUDIO_ADMIN_PASSWORD"] = service_env["KB_JOURNEY_ADMIN_PASSWORD"]
                studio_env["KB_STUDIO_MEMBER_EMAIL"] = member_email
                studio_env["KB_STUDIO_MEMBER_PASSWORD"] = member_password
            except (KeyError, RuntimeError, urllib.error.URLError, ValueError) as error:
                fail(f"Studio secured member setup: {error}")
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
            try:
                output_json = next(line for line in reversed(result.stdout.splitlines())
                                   if line.lstrip().startswith("{"))
                uninstall_payload = json.loads(output_json)
            except (StopIteration, json.JSONDecodeError):
                uninstall_payload = {}
            human_uninstall_ok = (result.returncode == 0 and uninstall_payload.get("ok") is True
                                  and uninstall_payload.get("operation") == "uninstall"
                                  and bool(uninstall_payload.get("snapshot")) and bool(uninstall_payload.get("logPath")))
            record_case("human confirmed uninstall", human_uninstall_ok,
                       "confirmed human uninstall JSON omitted operation, snapshot, or logPath",
                       cases=["S-050.5"])
        else:
            try:
                uninstall_payload = json.loads(result.stdout)
            except json.JSONDecodeError:
                uninstall_payload = {}
            uninstall_ok = (result.returncode == 0 and uninstall_payload.get("ok") is True
                            and uninstall_payload.get("operation") == "uninstall"
                            and bool(uninstall_payload.get("snapshot")) and bool(uninstall_payload.get("logPath")))
            record_case("agent confirmed uninstall", uninstall_ok,
                       "agent uninstall JSON omitted operation, snapshot, or logPath", cases=["S-050.6"])
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
