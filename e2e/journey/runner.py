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
from pathlib import Path

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
PROCESSES: list[subprocess.Popen] = []
FIRST_FAILURE = ""


def run(args: list[str], *, env=None, cwd=None, timeout=900, input_text=None):
    started = time.monotonic()
    result = subprocess.run(args, cwd=cwd, env=env or BASE_ENV, text=True,
                            input=input_text, stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT, timeout=timeout)
    return result, time.monotonic() - started


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


def pty_install(command: list[str], env: dict[str, str], choice: str = ""):
    master, slave = pty.openpty()
    started = time.monotonic()
    proc = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave,
                            env=env, start_new_session=True)
    os.close(slave)
    output = bytearray()
    deadline = time.monotonic() + 900
    sent_choice = not choice
    last_prompt = ""
    while proc.poll() is None and time.monotonic() < deadline:
        try:
            ready, _, _ = select.select([master], [], [], 0.2)
            if not ready:
                continue
            chunk = os.read(master, 8192)
            if chunk:
                output.extend(chunk)
                visible = output.decode(errors="replace")[-2000:]
                prompts = re.findall(r"(?im)^.*(?:\?|\[.*(?:default|y/n|yes/no).*\]|default[^\n]*:|press enter[^\n]*|select[^\n]*:).*?$", visible)
                prompt = prompts[-1] if prompts else ""
                if prompt and prompt != last_prompt:
                    answer = choice if choice and not sent_choice and re.search(r"scenario|journey|setup", prompt, re.I) else ""
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
    return proc.returncode, output.decode(errors="replace"), time.monotonic() - started


def human_text_is_clean(output: str):
    if re.search(r'\{\s*"ok"\s*:|"(?:code|error)"\s*:', output):
        fail("human output contains a raw JSON object")
    if "goroutine " in output or re.search(r"(?m)^panic:", output) or "stack trace" in output.lower():
        fail("human output contains a stack trace")


def append_summary(passed: bool):
    root_size = 0
    if PLATFORM.exists():
        root_size = sum(p.stat().st_size for p in PLATFORM.rglob("*") if p.is_file())
    title = f"{FRONT_DOOR}/{SCENARIO}" if VARIANT == "default" else VARIANT
    lines = [f"| {title} | {'PASS' if passed else 'FAIL'} | {FIRST_FAILURE or 'all assertions passed'} | "
             f"{TIMES.get('install.sh', 0):.1f}s | {TIMES.get('questions', 0):.1f}s | "
             f"{TIMES.get('apply', 0):.1f}s | {TIMES.get('first command', 0):.1f}s | "
             f"{sum(TIMES.values()):.1f}s | {root_size / (1024 * 1024):.1f} MiB |"]
    report = Path(os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md"))
    report.parent.mkdir(parents=True, exist_ok=True)
    if not report.exists():
        report.write_text("| Journey | Result | First broken step | install.sh | questions | apply | first command | total | platform root |\n"
                          "|---|---|---|---:|---:|---:|---:|---:|---:|\n")
    with report.open("a") as stream:
        stream.write("\n".join(lines) + "\n")
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


def main():
    global FIRST_FAILURE
    env = BASE_ENV.copy()
    env["HOME"] = str(HOME)
    env["PATH"] = f"{BIN}:/usr/local/bin:/usr/bin:/bin"
    (HOME / ".profile").write_text("if [ -f ~/.bashrc ]; then . ~/.bashrc; fi\n")
    env["KB_CREATE_DOWNLOAD_BASE_URL"] = BASE_ENV["KB_CREATE_DOWNLOAD_BASE_URL"]
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
        passed = "KB_INSTALL_NODE_VERSION" in launcher.stdout and "Node 24" in launcher.stdout
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
    if FRONT == "human":
        choice = "commit" if VARIANT == "human-nondefault" else ""
        code, output, duration = pty_install([launcher, "install"], env, choice)
        TIMES["questions"] = duration
        human_text_is_clean(output)
        if code != 0:
            fail(f"kb-create install: exit {code}; {' '.join(output.split())[-700:]}")
        if not re.search(r"(?i)install|download|configure|verify|starting", output):
            fail("kb-create install: terminal showed no progress")
        if not re.search(r"(?i)next|success|installed|ready", output):
            fail("kb-create install: terminal showed no success and next steps")
    else:
        args = [launcher, "apply", "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                "--request-platform-root", str(PLATFORM), "--scenario", SCENARIO,
                "--registry", REGISTRY, "--yes"]
        if VARIANT == "relative-path":
            args[args.index(str(PLATFORM))] = "./relative-kb-platform"
        result, elapsed = run(args, env=env, timeout=1200)
        TIMES["apply"] = elapsed
        if VARIANT == "relative-path":
            passed = result.returncode != 0 and re.search(r"KB_INSTALL_[A-Z0-9_]+", result.stdout) is not None
            passed = passed and re.search(r"(?i)(absolute|relative).*(path|platform root)|(--request-platform-root|kb-create apply)", result.stdout) is not None
            if not passed:
                fail(f"relative platform root: expected a precise KB_INSTALL_* error and path hint; {' '.join(result.stdout.split())[-500:]}")
            append_summary(passed)
            return 0 if passed else 1
        if not require(result, step="kb-create apply --json"):
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
        if VARIANT == "input":
            request = extract_request(payload)
            if request is None:
                fail("kb-create apply --json: launcher output contained no kb.create/v2 request")
            else:
                request_file = HOME / "launcher-request.json"
                request_file.write_text(json.dumps(request) + "\n")
                removed, _ = run([launcher, "uninstall", "--yes"], env=env, timeout=180)
                if require(removed, step="remove direct-flags install before --input"):
                    args = [launcher, "apply", "--json", "--index", str(HOME / ".local/share/kb-create/release-index.json"),
                            "--input", str(request_file), "--registry", REGISTRY, "--yes"]
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

    verify_listener_ownership()

    # New login shell and fresh project directory prove user-facing PATH and runtime isolation.
    PROJECT.mkdir(exist_ok=True)
    run(["git", "init", "-q"], env=env, cwd=PROJECT)
    (PROJECT / "note.txt").write_text("journey first value\n")
    run(["git", "add", "note.txt"], env=env, cwd=PROJECT)
    result, elapsed = run(["bash", "-lc", "kb --help && kb commit --help"], env=env, cwd=PROJECT, timeout=180)
    TIMES["first command"] = elapsed
    if require(result, step="new login shell kb --help"):
        if FRONT == "agent" and SCENARIO == "commit":
            result, elapsed = run(["bash", "-lc", "kb commit --dry-run --note 'Prepare the staged change'"],
                                  env=env, cwd=PROJECT, timeout=300)
            TIMES["first command"] += elapsed
            if not require(result, step="kb commit first value"):
                pass
            elif "Prepare the staged change" not in result.stdout:
                fail("kb commit first value: output did not include the supplied human note")
        status, _ = run(["git", "status", "--porcelain"], env=env, cwd=PROJECT)
        if status.stdout.strip() != "?? note.txt" and status.stdout.strip() != "A  note.txt":
            fail(f"project gained KB Labs files: {status.stdout.strip()}")

    for command, key in (("status", "installed and running"), ("doctor", "healthy")):
        result, _ = run([launcher, command], env=env, timeout=120)
        if require(result, step=f"kb-create {command}") and not re.search(key, result.stdout, re.I):
            fail(f"kb-create {command}: expected human summary '{key}'")

    result, _ = run([launcher, "uninstall", "--yes"], env=env, timeout=180)
    if require(result, step="kb-create uninstall"):
        alive = [p for p in PROCESSES if p.poll() is None]
        listeners, _ = run(["ss", "-H", "-ltnp"], timeout=20)
        if listeners.stdout.strip():
            fail(f"uninstall left listening TCP ports: {' '.join(listeners.stdout.split())[:400]}")
        platform_pids = set()
        for pid in re.findall(r"pid=(\d+)", listeners.stdout):
            try:
                command = Path(f"/proc/{pid}/cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace")
                cwd = os.readlink(f"/proc/{pid}/cwd")
                if str(PLATFORM) in command or cwd.startswith(str(PLATFORM)):
                    platform_pids.add(pid)
            except OSError:
                continue
        if alive or platform_pids or PLATFORM.exists() or (BIN / "kb" ).exists():
            fail("uninstall left a platform process, listening port, shim, or platform root")
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
