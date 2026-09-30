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

from studio_steps import create_studio_member, record_studio_browser_step
from service_steps import record_runner_failure

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


def run(args: list[str], *, env=None, cwd=None, timeout=900, input_text=None):
    global MAX_PNPM_RSS_MIB
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
    if body.lstrip().startswith("data:"):
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
        expected = {"commit__commit", "workflow__workflow_run", "kb-labs__mcp_diagnostics"}
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


def main():
    global FIRST_FAILURE
    env = BASE_ENV.copy()
    env["HOME"] = str(HOME)
    env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
    (HOME / ".profile").write_text("if [ -f ~/.bashrc ]; then . ~/.bashrc; fi\n")
    (HOME / ".bash_profile").write_text("if [ -f ~/.profile ]; then . ~/.profile; fi\n")
    (HOME / ".npmrc").write_text(f"registry={REGISTRY}\n")
    env["KB_CREATE_DOWNLOAD_BASE_URL"] = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"]
    if VARIANT.endswith("secured"):
        env["KB_JOURNEY_ADMIN_PASSWORD"] = "JourneyPassw0rd!"
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
        return 0 if passed else 1

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
        return 1
    if not (BIN / "kb-create").exists():
        fail("install.sh: kb-create binary was not installed")
        append_summary(False)
        return 1

    launcher = str(BIN / "kb-create")
    if VARIANT == "occupied-port":
        args = [launcher, "apply", "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                "--request-platform-root", str(PLATFORM), "--scenario", "commit", "--registry", REGISTRY]
        result, elapsed = run(args, env=env, timeout=90)
        TIMES["apply"] = elapsed
        passed = result.returncode != 0 and "KB_INSTALL_PORT_IN_USE" in result.stdout and "4000" in result.stdout
        if not passed:
            fail(f"port conflict: expected KB_INSTALL_PORT_IN_USE for port 4000; {' '.join(result.stdout.split())[-500:]}")
        append_summary(passed)
        return 0 if passed else 1
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
            return 0 if passed else 1
        if not require(result, step=f"kb-create {operation} --json"):
            append_summary(False)
            return 1
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError:
            fail("kb-create apply --json: output was not one JSON document")
            append_summary(False)
            return 1
        if payload.get("ok") is not True:
            fail("kb-create apply --json: result did not report ok=true")
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
                    return 1
                try:
                    if json.loads(result.stdout).get("ok") is not True:
                        fail("kb-create apply --input: result did not report ok=true")
                except json.JSONDecodeError:
                    fail("kb-create apply --input: output was not one JSON document")

    if FIRST_FAILURE:
        append_summary(False)
        return 1

    result, elapsed = run([launcher, "start", "--platform-root", str(PLATFORM)], env=env, timeout=300)
    TIMES["start"] = elapsed
    if not require(result, step="kb-create start"):
        append_summary(False)
        return 1

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
        return 1
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

    for command, key in (("status", r"Installed:\s+yes.*Running:\s+yes"), ("doctor", "")):
        result, _ = run([launcher, command, "--platform-root", str(PLATFORM)], env=env, timeout=120)
        if not require(result, step=f"kb-create {command}"):
            with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: {command}-output ==\n{result.stdout[-5000:]}\n")
            if command == "doctor":
                diagnostic, _ = run([launcher, command, "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
                with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                    stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: doctor-json (exit {diagnostic.returncode}) ==\n{(diagnostic.stdout or '')[-12000:]}\n{(diagnostic.stderr or '')[-4000:]}\n")
        elif command == "doctor":
            diagnostic, _ = run([launcher, command, "--platform-root", str(PLATFORM), "--json"], env=env, timeout=120)
            try:
                health = json.loads(diagnostic.stdout)
                passed = diagnostic.returncode == 0 and health.get("ok") is True and health.get("platform", {}).get("ok") is True
            except json.JSONDecodeError:
                passed = False
            if not passed:
                fail(f"kb-create doctor: expected healthy JSON report; {' '.join(diagnostic.stdout.split())[-1000:]}")
        elif key and not re.search(key, result.stdout, re.I | re.S):
            with Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name("journey-diagnostics.log").open("a") as stream:
                stream.write(f"\n== {FRONT_DOOR}/{SCENARIO}: {command}-output ==\n{result.stdout[-5000:]}\n")
            fail(f"kb-create {command}: expected human summary '{key}'")

    local_service_journey = FRONT == "agent" and SCENARIO == "explore" and VARIANT == "default"
    secured_service_journey = FRONT == "human" and VARIANT.endswith("secured")
    if BASE_ENV.get("KB_JOURNEY_SERVICE_PROFILE") == "nightly" and (local_service_journey or secured_service_journey):
        mode = "secured" if secured_service_journey else "local"
        service_env = env.copy()
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
        service_env["KB_JOURNEY_PROJECT_ID"] = project_id
        service_report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")).with_name(f"service-{mode}.json")
        result, elapsed = run(["python3", "/opt/kb-journey/service_runner.py", "--mode", mode, "--output", str(service_report)],
                             env=service_env, timeout=300)
        TIMES[f"{mode} service journeys"] = elapsed
        if result.returncode != 0:
            try:
                service_result = json.loads(service_report.read_text())
                first = service_result.get("first_failure") or {}
                detail = f"{first.get('name', 'journey step')}: {first.get('message', '')}"
            except (OSError, json.JSONDecodeError):
                detail = "service journey result JSON could not be read"
            fail(f"{mode} service journey: {detail}")

        project = project_payload.get("project", project_payload) if isinstance(project_payload, dict) else {}
        project_name = str(project.get("name") or "") if isinstance(project, dict) else ""
        studio_env = service_env.copy()
        studio_env.update({
            "GATEWAY_URL": "http://127.0.0.1:4000",
            "KB_STUDIO_PROJECT_ID": project_id,
            "KB_STUDIO_PROJECT_NAME": project_name,
            "KB_ACCESS_MODE": mode,
        })
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
        studio_result, studio_elapsed = run([
            "/opt/kb-studio-playwright/node_modules/.bin/playwright", "test",
            "--config=/opt/kb-studio-playwright/playwright.config.mjs",
        ], env=studio_env, timeout=180)
        TIMES["Studio browser journey"] = studio_elapsed
        studio_passed = studio_result.returncode == 0
        studio_message = "Studio browser journey passed"
        if not studio_passed:
            studio_message = " ".join(studio_result.stdout.split())[-700:] or f"Playwright exited {studio_result.returncode}"
            fail(f"Studio browser journey: {studio_message}")
        record_studio_browser_step(service_report, mode, studio_passed, studio_elapsed, studio_message)

    global PLATFORM_SIZE_MIB
    if PLATFORM.exists():
        PLATFORM_SIZE_MIB = sum(p.stat().st_size for p in PLATFORM.rglob("*") if p.is_file()) / (1024 * 1024)
    result, _ = run([launcher, "uninstall", "--platform-root", str(PLATFORM), "--registry", REGISTRY], env=env, timeout=180)
    if require(result, step="kb-create uninstall"):
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
    passed = not FIRST_FAILURE
    append_summary(passed)
    return 0 if passed else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    finally:
        for process in PROCESSES:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
        shutil.rmtree(HOME, ignore_errors=True)
