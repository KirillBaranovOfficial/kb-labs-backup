"""Shared, redacted invocation evidence for journey commands and HTTP calls."""
from __future__ import annotations

from contextvars import ContextVar
import json
import inspect
import os
import pty
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import threading
import time
from typing import Any, Callable, Mapping, NoReturn
from urllib.error import HTTPError, URLError

from platform_state_dump import _redact_text

_STEP_ID: ContextVar[str] = ContextVar("journey_step_id", default="journey.setup")
_REPORT_ROOT: ContextVar[Path | None] = ContextVar("journey_report_root", default=None)


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


def _redact(value: str, env: Mapping[str, str] | None = None) -> str:
    secrets = [value for key, value in (env or {}).items()
               if value and any(part in key.upper() for part in ("PASSWORD", "TOKEN", "SECRET", "API_KEY"))]
    for secret in secrets:
        value = value.replace(secret, "[REDACTED]")
    return _redact_text(value, "")


def _redact_argv(argv: list[str], env: Mapping[str, str]) -> list[str]:
    result = []
    redact_next = False
    for arg in argv:
        if redact_next:
            result.append("[REDACTED]")
            redact_next = False
            continue
        result.append(_redact(str(arg), env))
        if str(arg).lower().lstrip("-").replace("_", "-") in {
            "password", "token", "secret", "api-key", "private-key",
        }:
            redact_next = True
    return result


def record_invocation(*, kind: str, argv: list[str], cwd: str | Path | None,
                      env: Mapping[str, str] | None, exit_code: int | None,
                      stdout: str = "", stderr: str = "", duration_seconds: float,
                      url: str | None = None, request_body: str | None = None,
                      error: str | None = None) -> dict[str, Any] | None:
    root = _REPORT_ROOT.get()
    if root is None:
        path = os.getenv("KB_JOURNEY_INVOCATIONS")
        root = Path(path) if path else None
    if root is None:
        return None
    env = env or {}
    record = {
        "step_id": _STEP_ID.get(),
        "kind": kind,
        "argv": _redact_argv(argv, env),
        "url": _redact(url, env) if url else None,
        "request_body": _redact(request_body, env) if request_body is not None else None,
        "cwd": str(cwd or Path.cwd()),
        "home_label": env.get("KB_JOURNEY_HOME_LABEL", "main"),
        "exit_code": exit_code,
        "stdout": _redact(stdout, env),
        "stderr": _redact(stderr, env),
        "error": _redact(error, env) if error else None,
        "duration_seconds": round(duration_seconds, 3),
    }
    target = root / "invocations" / "calls.jsonl"
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(record, ensure_ascii=False) + "\n")
    return record


def invocation_link(step_id: str) -> str | None:
    root = _REPORT_ROOT.get()
    if root is None:
        path = os.getenv("KB_JOURNEY_INVOCATIONS")
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
                    monitor_process: Callable[[int], None] | None = None,
                    start_new_session: bool = False,
                    raise_on_timeout: bool = True) -> tuple[subprocess.CompletedProcess[str], float]:
    """Run a command on a PTY and record its complete transcript and case step."""
    started = time.monotonic()
    master, slave = pty.openpty()
    process = None
    transcript = bytearray()
    timed_out = False
    try:
        process = subprocess.Popen(argv, cwd=str(cwd) if cwd else None,
                                   env=dict(env) if env is not None else None,
                                   stdin=slave, stdout=slave, stderr=slave,
                                   close_fds=True, start_new_session=start_new_session)
        os.close(slave)
        deadline = started + timeout
        while process.poll() is None and time.monotonic() < deadline:
            if monitor_process:
                monitor_process(process.pid)
            readable, _, _ = select.select([master], [], [], 0.2)
            if not readable:
                continue
            try:
                chunk = os.read(master, 8192)
            except OSError:
                remaining = max(0, deadline - time.monotonic())
                try:
                    process.wait(timeout=remaining)
                except subprocess.TimeoutExpired:
                    timed_out = True
                break
            if not chunk:
                continue
            transcript.extend(chunk)
            if on_output:
                answer = on_output(transcript.decode(errors="replace"))
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
    finally:
        try:
            os.close(slave)
        except OSError:
            pass
        os.close(master)
    rendered = transcript.decode(errors="replace")
    duration = time.monotonic() - started
    error = f"timed out after {timeout}s" if timed_out else None
    record_invocation(kind="command", argv=argv, cwd=cwd, env=env,
                      exit_code=process.returncode, stdout=rendered,
                      duration_seconds=duration, error=error)
    if timed_out and raise_on_timeout:
        raise subprocess.TimeoutExpired(argv, timeout, output=rendered)
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
