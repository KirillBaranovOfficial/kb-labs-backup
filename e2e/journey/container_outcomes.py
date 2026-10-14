"""Capture container exits and diagnostics before the journey Compose project is removed."""
from __future__ import annotations

import argparse
import json
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Protocol
from urllib.parse import quote
import http.client
import socket
from invocations import run_harness_command


class DockerBackend(Protocol):
    def inspect(self, container_id: str) -> dict: ...
    def logs(self, container_id: str) -> dict[str, str]: ...
    def events(self, since: str) -> list[dict]: ...
    def metrics(self, container_id: str) -> dict: ...


def decode_engine_log_stream(data: bytes, container_id: str) -> dict[str, str]:
    stdout = bytearray()
    stderr = bytearray()
    offset = 0
    while offset < len(data):
        if offset + 8 > len(data):
            raise ValueError(f"Docker Engine returned a truncated log frame for {container_id}")
        stream, padding, size = data[offset], data[offset + 1:offset + 4], int.from_bytes(data[offset + 4:offset + 8], "big")
        if stream not in (1, 2) or padding != b"\0\0\0" or offset + 8 + size > len(data):
            raise ValueError(f"Docker Engine returned an invalid log frame for {container_id}")
        target = stdout if stream == 1 else stderr
        target.extend(data[offset + 8:offset + 8 + size])
        offset += 8 + size
    return {"stdout": stdout.decode("utf-8", errors="replace"),
            "stderr": stderr.decode("utf-8", errors="replace")}


class DockerCLI:
    """Read Docker Engine state through the CLI and Engine logs endpoint."""

    def _run(self, *args: str) -> str:
        result = run_harness_command(["docker", *args])
        if result.returncode:
            detail = result.stderr.strip() or result.stdout.strip() or f"exit {result.returncode}"
            raise RuntimeError(f"docker {' '.join(args)} failed: {detail}")
        return result.stdout

    def inspect(self, container_id: str) -> dict:
        values = json.loads(self._run("inspect", container_id))
        if len(values) != 1 or not isinstance(values[0], dict):
            raise ValueError(f"docker inspect returned no single container for {container_id}")
        return values[0]

    def _engine_socket(self) -> tuple[str, str]:
        host = self._run("context", "inspect", "--format", "{{json .Endpoints.docker.Host}}").strip()
        endpoint = json.loads(host)
        if not isinstance(endpoint, str) or not endpoint.startswith("unix://"):
            raise RuntimeError(f"container log stream requires a local Docker Engine socket; got {endpoint!r}")
        version = self._run("version", "--format", "{{.Server.APIVersion}}").strip()
        if not version:
            raise RuntimeError("docker version did not report the Engine API version")
        return endpoint.removeprefix("unix://"), version

    def logs(self, container_id: str) -> dict[str, str]:
        inspect = self.inspect(container_id)
        if inspect.get("Config", {}).get("Tty") is True:
            output = self._engine_request(container_id, "stdout=1&stderr=1")
            return {"stdout": output.decode("utf-8", errors="replace"), "stderr": ""}
        data = self._engine_request(container_id, "stdout=1&stderr=1")
        return decode_engine_log_stream(data, container_id)

    def _engine_request(self, container_id: str, query: str) -> bytes:
        socket_path, version = self._engine_socket()

        class UnixHTTPConnection(http.client.HTTPConnection):
            def connect(self):
                self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                self.sock.connect(socket_path)

        connection = UnixHTTPConnection("localhost")
        path = f"/v{version}/containers/{quote(container_id, safe='')}/logs?{query}&timestamps=0&follow=0"
        connection.request("GET", path)
        response = connection.getresponse()
        body = response.read()
        connection.close()
        if response.status != 200:
            raise RuntimeError(f"Docker Engine logs returned HTTP {response.status}: {body.decode(errors='replace')}")
        return body

    def events(self, since: str) -> list[dict]:
        until = datetime.now(timezone.utc).isoformat()
        result = self._run("events", "--since", since, "--until", until, "--filter", "type=container",
                           "--format", "{{json .}}")
        events = []
        for line in result.splitlines():
            event = json.loads(line)
            if event.get("Action") in {"die", "oom", "kill"}:
                events.append(event)
        return events

    def metrics(self, container_id: str) -> dict:
        observed_at = datetime.now(timezone.utc).isoformat()
        inspect = self.inspect(container_id)
        if inspect.get("State", {}).get("Running") is not True:
            return {"memoryCurrentBytes": None, "memoryPeakBytes": None, "freeDiskBytes": None,
                    "observedAt": observed_at, "unavailable": "container had exited before final metrics sampling"}
        probe = self._run("exec", container_id, "sh", "-c",
                          "printf '%s\\n' \"$(cat /sys/fs/cgroup/memory.current 2>/dev/null || true)\" "
                          "\"$(cat /sys/fs/cgroup/memory.peak 2>/dev/null || true)\"; df -Pk / | tail -n 1")
        lines = probe.splitlines()
        memory_current = int(lines[0]) if len(lines) > 0 and lines[0].isdigit() else None
        memory_peak = int(lines[1]) if len(lines) > 1 and lines[1].isdigit() else None
        disk_parts = lines[2].split() if len(lines) > 2 else []
        free_disk = int(disk_parts[3]) * 1024 if len(disk_parts) > 3 and disk_parts[3].isdigit() else None
        return {"memoryCurrentBytes": memory_current, "memoryPeakBytes": memory_peak,
                "freeDiskBytes": free_disk, "observedAt": observed_at}


def _verdict(container: dict, matching_events: list[dict]) -> str:
    state = container["state"]
    name = container["name"].lstrip("/") or container["id"][:12]
    if state.get("OOMKilled") is True:
        return f"{name} killed by OOM (exit {state.get('ExitCode')})"
    kill = next((event for event in reversed(matching_events) if event.get("Action") == "kill"), None)
    if kill is not None:
        signal = kill.get("Actor", {}).get("Attributes", {}).get("signal") or state.get("Error") or "signal recorded"
        return f"{name} killed by {signal}"
    code = state.get("ExitCode")
    if code not in (None, 0):
        reason = state.get("Error") or container["stderr"].strip().splitlines()[-1:]
        reason_text = reason if isinstance(reason, str) else (reason[0] if reason else "no stderr reason")
        return f"{name} exited {code} after reason {reason_text}"
    return f"{name} exited {code if code is not None else 'unknown'}"


def collect_container_outcomes(backend: DockerBackend, container_ids: list[str], report_dir: str | Path,
                               *, since: str) -> list[dict]:
    report = Path(report_dir)
    report.mkdir(parents=True, exist_ok=True)
    inspected_containers = [backend.inspect(container_id) for container_id in container_ids]
    identities = {str(inspected.get("Id") or requested)
                  for requested, inspected in zip(container_ids, inspected_containers)}
    events = [event for event in backend.events(since)
              if event.get("Actor", {}).get("ID") in identities or event.get("id") in identities]
    containers = []
    for container_id, inspected in zip(container_ids, inspected_containers):
        identity = str(inspected.get("Id") or container_id)
        logs = backend.logs(identity)
        metrics = backend.metrics(identity)
        samples_path = report / "container-metrics.jsonl"
        if samples_path.is_file():
            samples = [json.loads(line) for line in samples_path.read_text(encoding="utf-8").splitlines() if line]
            samples = [sample for sample in samples if sample.get("containerId") == identity]
            if samples:
                latest = samples[-1]["metrics"]
                peaks = [sample["metrics"].get("memoryPeakBytes") for sample in samples]
                peaks = [peak for peak in peaks if isinstance(peak, int)]
                metrics = {**latest, "memoryPeakBytes": max(peaks, default=latest.get("memoryPeakBytes")),
                           "source": "last live sample before container exit"}
        state = inspected.get("State")
        if not isinstance(state, dict):
            raise ValueError(f"docker inspect omitted State for container {identity}")
        matching_events = [event for event in events
                           if event.get("Actor", {}).get("ID") == identity
                           or event.get("id") == identity]
        item = {"id": identity, "name": str(inspected.get("Name") or identity),
                "state": {key: state.get(key) for key in ("ExitCode", "OOMKilled", "Error", "FinishedAt")},
                "stdout": logs["stdout"], "stderr": logs["stderr"],
                "events": matching_events, "metrics": metrics}
        item["verdict"] = _verdict(item, matching_events)
        containers.append(item)
    record = {"capturedAt": datetime.now(timezone.utc).isoformat(), "events": events,
              "containers": containers}
    (report / "container-outcomes.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    verdict_lines = [container["verdict"] for container in containers]
    verdict_text = "\n".join(verdict_lines) + ("\n" if verdict_lines else "No journey containers were present at teardown.\n")
    (report / "container-verdicts.txt").write_text(verdict_text, encoding="utf-8")
    summary = report / "journey-summary.md"
    with summary.open("a", encoding="utf-8") as stream:
        stream.write("\n## Container outcomes\n\n")
        stream.write("\n".join(f"- {line}" for line in verdict_lines) + "\n" if verdict_lines else "- No journey containers were present at teardown.\n")
    return containers


def monitor_container(backend: DockerBackend, container_id: str, report_dir: str | Path) -> None:
    """Sample persistent cgroup peak and final live current/disk while a named container runs."""
    report = Path(report_dir)
    samples_path = report / "container-metrics.jsonl"
    report.mkdir(parents=True, exist_ok=True)
    while True:
        inspected = backend.inspect(container_id)
        if inspected.get("State", {}).get("Running") is not True:
            return
        identity = str(inspected.get("Id") or container_id)
        sample = {"containerId": identity, "metrics": backend.metrics(identity)}
        with samples_path.open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(sample) + "\n")
        time.sleep(0.5)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report-dir", required=True)
    parser.add_argument("--since", default="now")
    parser.add_argument("--container", action="append", default=[])
    parser.add_argument("--monitor")
    args = parser.parse_args(argv)
    try:
        backend = DockerCLI()
        if args.monitor:
            monitor_container(backend, args.monitor, args.report_dir)
            return 0
        results = collect_container_outcomes(backend, args.container, args.report_dir, since=args.since)
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as error:
        print(f"Could not capture journey container outcomes: {error}", file=sys.stderr)
        return 1
    for item in results:
        print(item["verdict"], flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
