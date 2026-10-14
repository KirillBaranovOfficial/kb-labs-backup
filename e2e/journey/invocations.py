"""Shared, redacted invocation evidence for journey commands and HTTP calls."""
from __future__ import annotations
from journey_values import journey_value

from contextvars import ContextVar
import json
import inspect
import errno
import fcntl
import os
import pty
import platform
from pathlib import Path
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
from typing import Any, Callable, Mapping, MutableMapping, NoReturn
from urllib.error import HTTPError, URLError

from platform_state_dump import _redact_json, _redact_text

class PtyReadinessUnsupported(RuntimeError):
    """The operating system cannot report whether a PTY child is reading its terminal."""


_LINUX_READ_SYSCALLS = {"x86_64": 0, "amd64": 0, "aarch64": 63, "arm64": 63}


def _parse_proc_stat(stat: str) -> tuple[int, int, int, str] | None:
    fields = stat.rpartition(")")[2].split()
    if len(fields) < 5:
        return None
    return int(fields[1]), int(fields[3]), int(fields[4]), fields[0]  # parent, session, tty, state


def _proc_stat(pid: int) -> tuple[int, int, int, str] | None:
    try:
        stat = Path(f"/proc/{pid}/stat").read_text(encoding="ascii")
    except FileNotFoundError:
        return None
    except PermissionError as error:
        raise PtyReadinessUnsupported(
            "Linux procfs process state is not readable with this user's permissions"
        ) from error
    return _parse_proc_stat(stat)


def _pty_reader_pids(root_pid: int) -> list[int]:
    proc = Path("/proc")
    root = _proc_stat(root_pid)
    if root is None:
        return []
    pids = {root_pid}
    parent_by_pid: dict[int, int] = {}
    for entry in proc.iterdir():
        if not entry.name.isdecimal():
            continue
        pid = int(entry.name)
        try:
            details = _proc_stat(pid)
        except PtyReadinessUnsupported:
            continue
        if details is not None:
            parent_by_pid[pid] = details[0]
    changed = True
    while changed:
        changed = False
        for pid, parent in parent_by_pid.items():
            if parent in pids and pid not in pids:
                pids.add(pid)
                changed = True
    return sorted(pids)


def _decode_linux_device_number(encoded: int) -> tuple[int, int]:
    major = ((encoded >> 8) & 0xFFF) | ((encoded >> 32) & ~0xFFF)
    minor = (encoded & 0xFF) | ((encoded >> 12) & ~0xFF)
    return major, minor


def _linux_terminal_reader(pid: int, terminal_path: str) -> tuple[int, int, str] | None:
    """Return the exact task blocked reading fd 0 from this controlling PTY."""
    if not sys.platform.startswith("linux"):
        raise PtyReadinessUnsupported(
            f"PTY read readiness is not supported on {platform.system()}; the journey requires Linux procfs"
        )
    machine = platform.machine().lower()
    read_syscall = _LINUX_READ_SYSCALLS.get(machine)
    if read_syscall is None:
        raise PtyReadinessUnsupported(
            f"PTY read readiness is not supported for Linux architecture {machine}"
        )
    try:
        pids = _pty_reader_pids(pid)
        terminal_stat = os.stat(terminal_path)
    except OSError as error:
        raise PtyReadinessUnsupported(f"Linux procfs process state is unavailable: {error}") from error
    terminal_device = (os.major(terminal_stat.st_rdev), os.minor(terminal_stat.st_rdev))
    for child_pid in pids:
        details = _proc_stat(child_pid)
        if details is None:
            continue
        _, session_id, controlling_tty, _ = details
        if (session_id != pid
                or _decode_linux_device_number(controlling_tty) != terminal_device):
            continue
        try:
            if os.readlink(f"/proc/{child_pid}/fd/0") != terminal_path:
                continue
            tasks = Path(f"/proc/{child_pid}/task").iterdir()
            for task in tasks:
                task_state = _parse_proc_stat(Path(task, "stat").read_text(encoding="ascii"))
                if task_state is None or task_state[3] not in {"S", "D"}:
                    continue
                syscall = Path(task, "syscall").read_text(encoding="ascii").split()
                if len(syscall) >= 2:
                    try:
                        is_terminal_read = (int(syscall[0], 10) == read_syscall
                                            and int(syscall[1], 16) == 0)
                    except ValueError:
                        continue
                    if is_terminal_read:
                        return child_pid, int(task.name), " ".join(syscall)
        except FileNotFoundError:
            continue
        except PermissionError as error:
            raise PtyReadinessUnsupported(
                "Linux procfs syscall state is not readable with this user's permissions"
            ) from error
        except OSError:
            continue
    return None


def pty_terminal_read_ready(pid: int, terminal_path: str) -> bool:
    """Report a child task blocked in read(0) on this controlling PTY."""
    return _linux_terminal_reader(pid, terminal_path) is not None


def _terminal_pending_input(slave_fd: int) -> int:
    """Return bytes queued for the child to read, including no-echo input."""
    value = bytearray(struct.pack("i", 0))
    fcntl.ioctl(slave_fd, termios.TIOCINQ, value, True)
    return struct.unpack("i", value)[0]


def _drain_pty(master: int, transcript: bytearray) -> bool:
    """Drain all output currently queued on the PTY master; report new bytes."""
    changed = False
    while True:
        try:
            chunk = os.read(master, 8192)
        except BlockingIOError:
            return changed
        except OSError as error:
            if error.errno in (errno.EIO, errno.EBADF):
                return changed
            raise
        if not chunk:
            return changed
        transcript.extend(chunk)
        changed = True


def _acquire_controlling_terminal(slave_fd: int) -> None:
    fcntl.ioctl(slave_fd, termios.TIOCSCTTY, 0)

_STEP_ID: ContextVar[str] = ContextVar("journey_step_id", default="journey.setup")
_REPORT_ROOT: ContextVar[Path | None] = ContextVar("journey_report_root", default=None)
# Only long values are safe to replace globally: shorter secrets can be ordinary
# identifiers, exit codes, URL fragments, or path components in useful evidence.
MIN_SECRET_VALUE_LENGTH = 12
_SECRET_ARGUMENT_NAME = r"(?:password|token|secret|api[-_]?key|private[-_]?key)"
_SECRET_ARGUMENT = re.compile(rf"(?i)^--{_SECRET_ARGUMENT_NAME}$")
_SECRET_ARGUMENT_TEXT = re.compile(
    rf"(?i)(--{_SECRET_ARGUMENT_NAME}(?:=\s*|\s+))([^\s,;]+)"
)


def is_secret_key(key: str) -> bool:
    """Return whether an environment key is covered by the report redactor."""
    return any(part in key.upper() for part in ("PASSWORD", "TOKEN", "SECRET", "API_KEY"))


def store_report_secret(env: MutableMapping[str, str], key: str, value: str, source: str) -> str:
    """Validate and store a harness credential where subprocesses and reports can redact it."""
    if not is_secret_key(key):
        raise AssertionError(f"{source} must use a secret-like environment key")
    if len(value) < MIN_SECRET_VALUE_LENGTH:
        raise AssertionError(f"{source} must contain at least {MIN_SECRET_VALUE_LENGTH} characters for safe journey reporting")
    env[key] = value
    return value


def export_project_identity_to_child_environment(
    env: MutableMapping[str, str], *, project_id: str | None = None, project_root: str | None = None,
    project_b_id: str | None = None, project_b_root: str | None = None,
) -> None:
    """Expose state-backed project identities only to a child process."""
    if project_id:
        env["KB_JOURNEY_PROJECT_ID"] = project_id
    elif project_id == "":
        env.pop("KB_JOURNEY_PROJECT_ID", None)
    if project_root:
        env["KB_JOURNEY_PROJECT_ROOT"] = project_root
    elif project_root == "":
        env.pop("KB_JOURNEY_PROJECT_ROOT", None)
    if project_b_id:
        env["KB_JOURNEY_PROJECT_B_ID"] = project_b_id
    elif project_b_id == "":
        env.pop("KB_JOURNEY_PROJECT_B_ID", None)
    if project_b_root:
        env["KB_JOURNEY_PROJECT_B_ROOT"] = project_b_root
    elif project_b_root == "":
        env.pop("KB_JOURNEY_PROJECT_B_ROOT", None)


def _invalid_ux_result(message: str) -> NoReturn:
    evidence = invocation_link(_STEP_ID.get())
    suffix = f" ({evidence})" if evidence else ""
    raise ValueError(f"{message}{suffix}")


def decode_ux_success_result(value: Any) -> Any:
    """Validate a shared success envelope and return its payload."""
    if (not isinstance(value, dict) or value.get("ok") is not True
            or "data" not in value or set(value) - {"ok", "data", "meta"}
            or ("meta" in value and not isinstance(value["meta"], dict))):
        _invalid_ux_result("response is not a valid UX success envelope")
    return value["data"]


def decode_ux_error_result(value: Any) -> Any:
    """Validate a shared failure envelope and return its error payload."""
    error = value.get("error") if isinstance(value, dict) else None
    if (not isinstance(value, dict) or value.get("ok") is not False
            or set(value) != {"ok", "error"} or not isinstance(error, dict)
            or not isinstance(error.get("code"), str) or not isinstance(error.get("message"), str)):
        _invalid_ux_result("response is not a valid UX error envelope")
    return error


def read_ux_cli_result(stdout: str, returncode: int) -> dict[str, Any]:
    """Parse a CLI JSON result and validate its UX success or error envelope."""
    try:
        value = json.loads(stdout)
    except json.JSONDecodeError as error:
        try:
            _invalid_ux_result("CLI response is not JSON")
        except ValueError as failure:
            raise failure from error
    if not isinstance(value, dict):
        _invalid_ux_result("CLI response is not an object")
    if value.get("ok") is True:
        decode_ux_success_result(value)
    else:
        decode_ux_error_result(value)
    if (returncode == 0) != (value["ok"] is True):
        _invalid_ux_result("CLI exit code does not match its UX result envelope")
    return value


def read_ux_cli_stream(stdout: str, returncode: int) -> list[dict[str, Any]]:
    """Validate JSONL UX records and the terminal record's process status."""
    records = []
    for line in stdout.splitlines():
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as error:
            try:
                _invalid_ux_result("CLI stream contains a non-JSON record")
            except ValueError as failure:
                raise failure from error
        if not isinstance(value, dict):
            _invalid_ux_result("CLI stream record is not an object")
        if value.get("ok") is True:
            decode_ux_success_result(value)
        else:
            decode_ux_error_result(value)
        records.append(value)
    if not records:
        _invalid_ux_result("CLI stream is empty")
    result_records = []
    for record in records[:-1]:
        meta = record.get("meta", {})
        stream_meta = meta.get("stream", {}) if isinstance(meta, dict) else {}
        if isinstance(stream_meta, dict) and stream_meta.get("result") is True:
            result_records.append(record)
    terminal = records[-1]
    terminal_meta = terminal.get("meta", {})
    stream = terminal_meta.get("stream", {}) if isinstance(terminal_meta, dict) else {}
    exit_code = stream.get("exitCode") if isinstance(stream, dict) else None
    if (terminal.get("ok") is not True or terminal.get("data", "not-null") is not None
            or not isinstance(stream, dict) or stream.get("terminal") is not True
            or exit_code not in {0, 1, 2}
            or (returncode == 0) != (exit_code == 0)):
        _invalid_ux_result("CLI stream terminal record does not match its process exit code")
    if len(result_records) > 1:
        _invalid_ux_result("CLI stream contains multiple result records")
    if exit_code == 0 and any(record.get("ok") is not True for record in records[:-1]):
        _invalid_ux_result("successful CLI stream contains an error record")
    if exit_code != 0 and not any(record.get("ok") is False for record in records[:-1]):
        _invalid_ux_result("failed CLI stream omits its error record")
    return records


def describe_cli_failure(command: str, stdout: str, stderr: str, returncode: int) -> str:
    """Describe a failed CLI invocation using its typed error when available."""
    detail = " ".join(f"{stdout} {stderr}".split())
    try:
        value = json.loads(stdout)
    except json.JSONDecodeError:
        value = None
    if isinstance(value, dict) and isinstance(value.get("error"), dict):
        error = value["error"]
        detail = "; ".join(part for part in (
            str(error.get("code") or ""),
            str(error.get("message") or ""),
            f"hint: {error['hint']}" if error.get("hint") else "",
        ) if part)
    return f"{command} exited {returncode}" + (f": {detail}" if detail else ": no output")


def read_ux_http_result(body: bytes, status: int) -> dict[str, Any]:
    """Parse an HTTP JSON result and require its status to carry a UX envelope."""
    try:
        value = json.loads(body)
    except (json.JSONDecodeError, TypeError) as error:
        try:
            _invalid_ux_result("HTTP response is not JSON")
        except ValueError as failure:
            raise failure from error
    if not isinstance(value, dict):
        _invalid_ux_result("HTTP response is not an object")
    if 200 <= status < 300:
        decode_ux_success_result(value)
    else:
        decode_ux_error_result(value)
    return value


def invocation_context(*, step_id: str | None = None, report_root: str | Path | None = None):
    """Set the current case step and invocation destination for a scope."""
    class Context:
        def __enter__(self):
            self.step_token = _STEP_ID.set(step_id or _STEP_ID.get())
            self.report_token = _REPORT_ROOT.set(Path(report_root) if report_root else _REPORT_ROOT.get())
            return self

        def __exit__(self, *_exc):
            _STEP_ID.reset(self.step_token)
            _REPORT_ROOT.reset(self.report_token)
    return Context()


def _secret_values(env: Mapping[str, str] | None, extra: tuple[str, ...] = ()) -> set[str]:
    environments = (os.environ, env) if env is not None else (os.environ,)
    secrets = {value for values in environments for key, value in values.items()
               if len(value) >= MIN_SECRET_VALUE_LENGTH and is_secret_key(key)}
    return secrets | {secret for secret in extra if len(secret) >= MIN_SECRET_VALUE_LENGTH}


def _redact(value: str, env: Mapping[str, str] | None = None,
            extra_secrets: tuple[str, ...] = ()) -> str:
    for secret in _secret_values(env, extra_secrets):
        value = value.replace(secret, "[REDACTED]")
    value = _redact_text(value, "")
    return _SECRET_ARGUMENT_TEXT.sub(r"\1[REDACTED]", value)


def redact_report_value(value: Any, env: Mapping[str, str] | None = None,
                        secrets: tuple[str, ...] = ()) -> Any:
    """Redact strings and secret-keyed fields before journey evidence leaves memory."""
    if isinstance(value, str):
        return _redact(value, env, secrets)
    if isinstance(value, dict):
        redacted = {}
        for key, item in value.items():
            if str(key).casefold() == "argv" and isinstance(item, list):
                redacted[key] = _redact_argv(item, env, secrets)
            else:
                redacted[key] = redact_report_value(item, env, secrets)
        return _redact_json(redacted)
    if isinstance(value, list):
        return [redact_report_value(item, env, secrets) for item in value]
    if isinstance(value, tuple):
        return tuple(redact_report_value(item, env, secrets) for item in value)
    return value


def _redact_argv(argv: list[Any], env: Mapping[str, str] | None,
                 secrets: tuple[str, ...]) -> list[Any]:
    result: list[Any] = []
    redact_next = False
    for raw in argv:
        argument = raw if isinstance(raw, str) else str(raw)
        if redact_next:
            result.append("[REDACTED]")
            redact_next = False
            continue
        flag, separator, _inline_value = argument.partition("=")
        if argument.startswith("--") and _SECRET_ARGUMENT.search(flag):
            if separator:
                result.append(f"{flag}=[REDACTED]")
            else:
                result.append(argument)
                redact_next = True
            continue
        result.append(_redact(argument, env, secrets))
    return result


def write_report_json(path: str | Path, value: Any, *, env: Mapping[str, str] | None = None,
                      append: bool = False, indent: int | None = 2,
                      ensure_ascii: bool = False, secrets: tuple[str, ...] = ()) -> Any:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    redacted = redact_report_value(value, env, secrets)
    rendered = json.dumps(redacted, indent=indent, ensure_ascii=ensure_ascii) + "\n"
    with target.open("a" if append else "w", encoding="utf-8") as stream:
        stream.write(rendered)
    return redacted


def write_report_text(path: str | Path, value: str, *, env: Mapping[str, str] | None = None,
                      append: bool = False, secrets: tuple[str, ...] = ()) -> str:
    rendered = _redact(value, env, secrets)
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a" if append else "w", encoding="utf-8") as stream:
        stream.write(rendered)
    return rendered


def print_report_text(value: str, *, env: Mapping[str, str] | None = None,
                      stream=None, end: str = "\n", flush: bool = False) -> str:
    rendered = _redact(value, env)
    output = stream or sys.stdout
    output.write(rendered + end)
    if flush:
        output.flush()
    return rendered


def record_invocation(*, kind: str, argv: list[str], cwd: str | Path | None,
                      env: Mapping[str, str] | None, exit_code: int | None,
                      stdout: str = "", stderr: str = "", duration_seconds: float,
                      url: str | None = None, request_body: str | None = None,
                      invocation_evidence: list[str] | None = None,
                      error: str | None = None) -> dict[str, Any] | None:
    root = _REPORT_ROOT.get()
    if root is None:
        path = journey_value("KB_JOURNEY_INVOCATIONS")
        root = Path(path) if path else None
    if root is None:
        return None
    effective_env = env if env is not None else os.environ
    record = {
        "step_id": _STEP_ID.get(),
        "kind": kind,
        "argv": argv,
        "url": url,
        "request_body": request_body,
        "cwd": str(cwd or Path.cwd()),
        "home_label": effective_env.get("KB_JOURNEY_HOME_LABEL", "main"),
        "exit_code": exit_code,
        "stdout": stdout,
        "stderr": stderr,
        "error": error,
        "duration_seconds": round(duration_seconds, 3),
    }
    if invocation_evidence is None and kind == "command" and exit_code not in (None, 0):
        invocation_evidence = _capture_typed_error_files(stdout, stderr, effective_env)
    if invocation_evidence:
        record["invocation_evidence"] = invocation_evidence
    target = root / "invocations" / "calls.jsonl"
    return write_report_json(target, record, env=effective_env, append=True, indent=None, ensure_ascii=False)


def _capture_typed_error_files(stdout: str, stderr: str, env: Mapping[str, str] | None) -> list[str]:
    """Capture bounded tails for existing file paths named by a typed error."""
    root = _REPORT_ROOT.get()
    if root is None:
        configured = journey_value("KB_JOURNEY_INVOCATIONS")
        root = Path(configured) if configured else None
    if root is None:
        return []
    try:
        result = json.loads(stdout or stderr)
    except json.JSONDecodeError:
        return []
    error = result.get("error") if isinstance(result, dict) and result.get("ok") is False else None
    details = error.get("details") if isinstance(error, dict) else None
    if not isinstance(details, dict):
        return []
    paths = []
    for value in details.values():
        if not isinstance(value, str):
            continue
        candidate = Path(value)
        try:
            if candidate.is_file() and candidate not in paths:
                paths.append(candidate)
        except OSError:
            continue
    if not paths:
        return []
    calls = root / "invocations" / "calls.jsonl"
    try:
        invocation_number = len(calls.read_text(encoding="utf-8").splitlines()) + 1
    except OSError:
        invocation_number = 1
    references = []
    for ordinal, source in enumerate(paths, 1):
        try:
            lines = source.read_text(errors="replace").splitlines()
        except OSError:
            continue
        tail = "\n".join(lines[-200:]) + "\n"
        tail = tail[-256 * 1024:]
        target = root / "invocations" / "evidence" / f"{invocation_number:04d}-{ordinal:02d}-{source.name}"
        write_report_text(target, tail, env=env)
        references.append(str(target.relative_to(root)))
    return references


class PtyCommandTimeout(subprocess.TimeoutExpired):
    """PTY timeout retaining the terminal output received before the deadline."""

    def __init__(self, cmd: list[str], timeout: float, transcript: str):
        super().__init__(cmd, timeout, output=transcript)

    def __str__(self) -> str:
        details = [f"PTY command timed out after {self.timeout}s"]
        if self.output:
            details.append(f"transcript received:\n{self.output}")
        else:
            details.append("no output received before the timeout")
        return "\n".join(details)


def invocation_link(step_id: str) -> str | None:
    root = _REPORT_ROOT.get()
    if root is None:
        path = journey_value("KB_JOURNEY_INVOCATIONS")
        root = Path(path) if path else None
    if root is None:
        return None
    target = root / "invocations" / "calls.jsonl"
    try:
        matching = [number for number, line in enumerate(target.read_text(encoding="utf-8").splitlines(), 1)
                    if json.loads(line).get("step_id") == step_id]
    except (OSError, json.JSONDecodeError):
        return None
    return f"invocations/calls.jsonl:{matching[-1]}" if matching else None


def run_command(argv: list[str], *, cwd: str | Path | None = None,
                env: Mapping[str, str] | None = None, timeout: float = 300,
                runner: Callable[..., subprocess.CompletedProcess[str]] | None = None,
                monitor_process: Callable[[int], None] | None = None,
                **kwargs: Any) -> subprocess.CompletedProcess[str]:
    started = time.monotonic()
    if runner is None and monitor_process is not None:
        return _run_monitored_command(argv, cwd=cwd, env=env, timeout=timeout,
                                      monitor_process=monitor_process, started=started, **kwargs)
    runner = runner or subprocess.run
    options = {
        "cwd": str(cwd) if cwd else None,
        "env": dict(env) if env is not None else None,
        "capture_output": True,
        "text": True,
        "timeout": timeout,
        **kwargs,
    }
    try:
        signature = inspect.signature(runner)
        accepts_kwargs = any(parameter.kind is inspect.Parameter.VAR_KEYWORD
                             for parameter in signature.parameters.values())
        if not accepts_kwargs:
            options = {key: value for key, value in options.items() if key in signature.parameters}
    except (TypeError, ValueError):
        pass
    try:
        result = runner(argv, **options)
    except (OSError, subprocess.TimeoutExpired) as error:
        record_invocation(kind="command", argv=argv, cwd=cwd, env=env, exit_code=None,
                          duration_seconds=time.monotonic() - started, error=str(error))
        raise
    raw_stdout = result.stdout or ""
    raw_stderr = result.stderr or ""
    stdout = raw_stdout.decode(errors="replace") if isinstance(raw_stdout, bytes) else raw_stdout
    stderr = raw_stderr.decode(errors="replace") if isinstance(raw_stderr, bytes) else raw_stderr
    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                      exit_code=result.returncode, stdout=stdout, stderr=stderr,
                      duration_seconds=time.monotonic() - started)
    return result


def run_with_runner(runner: Callable[..., subprocess.CompletedProcess[str]], argv: list[str],
                    **kwargs: Any) -> subprocess.CompletedProcess[str]:
    """Use the evidence helper for real or injected command runners without double-recording."""
    if runner is run_command:
        return run_command(argv, **kwargs)
    return run_command(argv, runner=runner, **kwargs)


def _run_monitored_command(argv: list[str], *, cwd: str | Path | None,
                           env: Mapping[str, str] | None, timeout: float,
                           monitor_process: Callable[[int], None], started: float,
                           **kwargs: Any) -> subprocess.CompletedProcess[str]:
    options = dict(kwargs)
    options.pop("capture_output", None)
    text_mode = options.pop("text", True)
    merge_stderr = options.pop("stderr", None) is subprocess.STDOUT
    input_value = options.pop("input", None)
    stdin_value = options.pop("stdin", subprocess.PIPE if input_value is not None else subprocess.DEVNULL)
    with tempfile.TemporaryFile() as stdout_file, tempfile.TemporaryFile() as stderr_file:
        try:
            process = subprocess.Popen(argv, cwd=str(cwd) if cwd else None,
                                       env=dict(env) if env is not None else None,
                                       stdin=stdin_value, stdout=stdout_file,
                                       stderr=subprocess.STDOUT if merge_stderr else stderr_file,
                                       **options)
            if input_value is not None:
                raw_input = input_value.encode() if isinstance(input_value, str) else input_value
                assert process.stdin is not None
                process.stdin.write(raw_input)
                process.stdin.close()
            deadline = started + timeout
            while process.poll() is None:
                monitor_process(process.pid)
                if time.monotonic() >= deadline:
                    process.kill()
                    process.wait()
                    stdout_file.seek(0)
                    stderr_file.seek(0)
                    stdout, stderr = stdout_file.read(), stderr_file.read()
                    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                                      exit_code=process.returncode,
                    stdout=stdout.decode(errors="replace"),
                    stderr="" if merge_stderr else stderr.decode(errors="replace"),
                                      duration_seconds=time.monotonic() - started,
                                      error=f"timed out after {timeout}s")
                    raise subprocess.TimeoutExpired(argv, timeout, output=stdout, stderr=stderr)
                time.sleep(0.05)
            stdout_file.seek(0)
            stderr_file.seek(0)
            stdout, stderr = stdout_file.read(), stderr_file.read()
        except OSError as error:
            record_invocation(kind="command", argv=argv, cwd=cwd, env=env, exit_code=None,
                              duration_seconds=time.monotonic() - started, error=str(error))
            raise
    rendered_stdout = stdout.decode(errors="replace") if text_mode else stdout
    rendered_stderr = stderr.decode(errors="replace") if text_mode else stderr
    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                      exit_code=process.returncode,
                      stdout=rendered_stdout if isinstance(rendered_stdout, str) else rendered_stdout.decode(errors="replace"),
                      stderr=("" if merge_stderr else
                              rendered_stderr if isinstance(rendered_stderr, str) else rendered_stderr.decode(errors="replace")),
                      duration_seconds=time.monotonic() - started)
    return subprocess.CompletedProcess(argv, process.returncode,
                                        stdout=rendered_stdout, stderr=rendered_stderr)


def run_pty_command(argv: list[str], *, cwd: str | Path | None = None,
                    env: Mapping[str, str] | None = None, timeout: float = 900,
                    on_output: Callable[[str], str | bytes | None] | None = None,
                    on_read_ready: Callable[[str], str | bytes | None] | None = None,
                    monitor_process: Callable[[int], None] | None = None,
                    start_new_session: bool = False,
                    raise_on_timeout: bool = True) -> tuple[subprocess.CompletedProcess[str], float]:
    """Run a command on a PTY and record its complete transcript and case step."""
    if on_read_ready and not sys.platform.startswith("linux"):
        raise PtyReadinessUnsupported(
            f"PTY read readiness is not supported on {platform.system()}; the journey requires Linux procfs"
        )
    if on_read_ready and not start_new_session:
        raise ValueError("PTY read readiness requires a new session with a controlling terminal")
    started = time.monotonic()
    master, slave = pty.openpty()
    if on_read_ready:
        os.set_blocking(master, False)
    terminal_path = os.ttyname(slave)
    process = None
    transcript = bytearray()
    timed_out = False
    try:
        process = subprocess.Popen(argv, cwd=str(cwd) if cwd else None,
                                   env=dict(env) if env is not None else None,
                                   stdin=slave, stdout=slave, stderr=slave,
                                   close_fds=True, start_new_session=start_new_session,
                                   preexec_fn=(lambda: _acquire_controlling_terminal(slave))
                                   if on_read_ready else None)
        if not on_read_ready:
            os.close(slave)
            slave = -1
        deadline = started + timeout
        while process.poll() is None and time.monotonic() < deadline:
            if monitor_process:
                monitor_process(process.pid)
            readable, _, _ = select.select([master], [], [], 0.05)
            if readable:
                if on_read_ready:
                    output_changed = _drain_pty(master, transcript)
                else:
                    try:
                        chunk = os.read(master, 8192)
                    except OSError:
                        remaining = max(0, deadline - time.monotonic())
                        try:
                            process.wait(timeout=remaining)
                        except subprocess.TimeoutExpired:
                            timed_out = True
                        break
                    output_changed = bool(chunk)
                    transcript.extend(chunk)
                if output_changed and on_output:
                    answer = on_output(transcript.decode(errors="replace"))
                    if answer:
                        os.write(master, answer.encode() if isinstance(answer, str) else answer)
            if on_read_ready:
                # Readiness is one invariant: the same child task is still
                # blocked in terminal read(0), all output queued before that
                # read has been drained into the transcript, and the PTY has
                # no input bytes left from an answer already sent by this owner.
                observed_reader = _linux_terminal_reader(process.pid, terminal_path)
                if observed_reader is not None:
                    drained = _drain_pty(master, transcript)
                    if drained and on_output:
                        answer = on_output(transcript.decode(errors="replace"))
                        if answer:
                            os.write(master, answer.encode() if isinstance(answer, str) else answer)
                    confirmed_reader = _linux_terminal_reader(process.pid, terminal_path)
                    if (confirmed_reader == observed_reader
                            and _terminal_pending_input(slave) == 0):
                        answer = on_read_ready(transcript.decode(errors="replace"))
                        if answer:
                            os.write(master, answer.encode() if isinstance(answer, str) else answer)
        if process.poll() is None and time.monotonic() >= deadline:
            timed_out = True
        if timed_out:
            if start_new_session:
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
            process.wait()
        else:
            process.wait()
            while True:
                readable, _, _ = select.select([master], [], [], 0)
                if not readable:
                    break
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                transcript.extend(chunk)
                if on_output:
                    answer = on_output(transcript.decode(errors="replace"))
                    if answer:
                        os.write(master, answer.encode() if isinstance(answer, str) else answer)
    except OSError as error:
        record_invocation(kind="command", argv=argv, cwd=cwd, env=env, exit_code=None,
                          stdout=transcript.decode(errors="replace"),
                          duration_seconds=time.monotonic() - started,
                          error=f"{error.__class__.__name__}: {error}")
        raise
    except BaseException:
        if process is not None and process.poll() is None:
            try:
                if start_new_session:
                    os.killpg(process.pid, signal.SIGKILL)
                else:
                    process.kill()
            except ProcessLookupError:
                pass
            process.wait()
        raise
    finally:
        if slave >= 0:
            try:
                os.close(slave)
            except OSError:
                pass
        os.close(master)
    rendered = transcript.decode(errors="replace")
    duration = time.monotonic() - started
    error = (f"timed out after {timeout}s" if rendered else
             f"no output received before the timeout ({timeout}s)") if timed_out else None
    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                      exit_code=process.returncode, stdout=rendered,
                      duration_seconds=duration, error=error)
    if timed_out and raise_on_timeout:
        raise PtyCommandTimeout(argv, timeout, rendered)
    return subprocess.CompletedProcess(argv, process.returncode, stdout=rendered, stderr=""), duration


def run_streaming_command(argv: list[str], *, cwd: str | Path | None = None,
                          env: Mapping[str, str] | None = None, timeout: float = 300,
                          on_output_line: Callable[[str], None] | None = None,
                          start_new_session: bool = False) -> tuple[subprocess.CompletedProcess[str], float]:
    """Stream combined subprocess output while retaining the full transcript as evidence."""
    started = time.monotonic()
    transcript_parts: list[str] = []
    output_thread: threading.Thread | None = None
    try:
        process = subprocess.Popen(argv, cwd=str(cwd) if cwd else None,
                                   env=dict(env) if env is not None else None,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   bufsize=1, start_new_session=start_new_session)

        def copy_output() -> None:
            assert process.stdout is not None
            with process.stdout as stream:
                for line in stream:
                    transcript_parts.append(line)
                    if on_output_line:
                        on_output_line(line.rstrip("\r\n"))

        output_thread = threading.Thread(target=copy_output, daemon=True)
        output_thread.start()
        try:
            return_code = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            if start_new_session:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
            else:
                process.kill()
                process.wait()
            return_code = process.returncode
            duration = time.monotonic() - started
            transcript = "".join(transcript_parts)
            record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                              exit_code=return_code, stdout=transcript,
                              duration_seconds=duration, error=f"timed out after {timeout}s")
            raise subprocess.TimeoutExpired(argv, timeout, output=transcript)
        finally:
            output_thread.join()
    except OSError as error:
        duration = time.monotonic() - started
        record_invocation(kind="command", argv=argv, cwd=cwd, env=env, exit_code=None,
                          stdout="".join(transcript_parts), duration_seconds=duration,
                          error=f"{error.__class__.__name__}: {error}")
        raise
    duration = time.monotonic() - started
    transcript = "".join(transcript_parts)
    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                      exit_code=return_code, stdout=transcript,
                      duration_seconds=duration)
    return subprocess.CompletedProcess(argv, return_code, stdout=transcript, stderr=""), duration


class BackgroundCommand:
    """A recorded long-running process whose complete output is drained in the helper."""

    def __init__(self, process: subprocess.Popen[str], argv: list[str], cwd: str | Path | None,
                 env: Mapping[str, str] | None, started: float):
        self.process = process
        self.argv = argv
        self.cwd = cwd
        self.env = env
        self.started = started
        self.lines: list[str] = []
        self.changed = threading.Condition()
        self.recorded = False
        self.reader = threading.Thread(target=self._read_output, daemon=True)
        self.reader.start()

    def _read_output(self) -> None:
        assert self.process.stdout is not None
        with self.process.stdout as stream:
            for line in stream:
                with self.changed:
                    self.lines.append(line)
                    self.changed.notify_all()
        with self.changed:
            self.changed.notify_all()

    def poll(self) -> int | None:
        return self.process.poll()

    def wait_for_output(self, expected: str, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        with self.changed:
            while expected not in "".join(self.lines) and self.process.poll() is None:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    return False
                self.changed.wait(remaining)
            return expected in "".join(self.lines)

    def terminate(self) -> None:
        self.process.terminate()

    def wait(self, timeout: float | None = None) -> int:
        try:
            result = self.process.wait(timeout=timeout)
        finally:
            if self.process.poll() is not None:
                self.reader.join()
                self._record()
        return result

    def _record(self) -> None:
        if self.recorded:
            return
        self.recorded = True
        record_invocation(kind="command", argv=self.argv, cwd=self.cwd, env=self.env,
                          exit_code=self.process.returncode, stdout="".join(self.lines),
                          duration_seconds=time.monotonic() - self.started)


def start_background_command(argv: list[str], *, cwd: str | Path | None = None,
                             env: Mapping[str, str] | None = None) -> BackgroundCommand:
    """Start a background command and capture its full output until it exits."""
    started = time.monotonic()
    try:
        process = subprocess.Popen(argv, cwd=str(cwd) if cwd else None,
                                   env=dict(env) if env is not None else None,
                                   text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   bufsize=1)
    except OSError as error:
        record_invocation(kind="command", argv=argv, cwd=cwd, env=env, exit_code=None,
                          duration_seconds=time.monotonic() - started,
                          error=f"{error.__class__.__name__}: {error}")
        raise
    return BackgroundCommand(process, argv, cwd, env, started)


def open_http(opener: Callable[..., Any], request: Any, *, timeout: float,
              env: Mapping[str, str] | None = None) -> tuple[int, bytes, Mapping[str, str]]:
    """Perform and record an HTTP request, including non-2xx responses."""
    started = time.monotonic()
    method = getattr(request, "method", None) or "GET"
    url = getattr(request, "full_url", str(request))
    request_body = getattr(request, "data", None)
    if isinstance(request_body, bytes):
        request_body = request_body.decode(errors="replace")
    elif request_body is not None and not isinstance(request_body, str):
        request_body = str(request_body)
    try:
        with opener(request, timeout=timeout) as response:
            headers = getattr(response, "headers", {})
            status = getattr(response, "status", None) or getattr(response, "code", None) or 200
            result = (status, response.read(), dict(headers.items()) if hasattr(headers, "items") else {})
    except HTTPError as error:
        result = (error.code, error.read(), dict(error.headers.items()) if error.headers else {})
    except (URLError, OSError, TimeoutError) as error:
        record_invocation(kind="http", argv=[method], cwd=Path.cwd(), env=env,
                          exit_code=None, duration_seconds=time.monotonic() - started,
                          url=url, request_body=request_body,
                          error=f"{error.__class__.__name__}: {error}")
        raise
    record_invocation(kind="http", argv=[method], cwd=Path.cwd(), env=env,
                      exit_code=result[0], stdout=result[1].decode(errors="replace"),
                      duration_seconds=time.monotonic() - started, url=url,
                      request_body=request_body)
    return result
