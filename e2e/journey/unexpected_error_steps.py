"""Installed CLI checks for correlation-linked unexpected error diagnostics."""
from __future__ import annotations

import json
import shutil
from typing import Mapping

from invocations import logs_from_query_envelope, parse_unexpected_correlation_id, run_command
import redaction


def run_unexpected_error_steps(journey, *, mode: str, env: Mapping[str, str]) -> None:
    cli = env.get("KB_JOURNEY_CLI_PATH", "")
    platform_root = env.get("KB_JOURNEY_PLATFORM_ROOT", "")
    if not cli or not platform_root:
        journey.step("unexpected CLI trail", "cli", lambda: "BLOCKED: installed CLI and platform root are required",
                     cases=["S-203.26"])
        return

    def verify_state(host_state: str) -> str | None:
        for output_mode, args in (
            ("json", ["adapter", "list", "--json"]),
            ("human", ["adapter", "list"]),
        ):
            failed = run_command([cli, *args], cwd=platform_root, env=env,
                                 capture_output=True, text=True, check=False, timeout=120)
            is_unexpected, correlation_id = parse_unexpected_correlation_id(failed.stdout, failed.stderr)
            if not is_unexpected:
                return (f"{mode} {host_state} adapter list {output_mode} did not produce "
                        "KB_RUNTIME_UNEXPECTED; C1 is the only available real product failure path")
            if correlation_id is None:
                return f"{mode} {host_state} {output_mode} error omitted its correlation ID"
            if output_mode == "human" and correlation_id not in (failed.stdout + failed.stderr):
                return f"{mode} {host_state} human adapter list did not print its correlation ID"

            query = run_command(
                [cli, "logs", "query", "--correlation-id", correlation_id, "--json"],
                cwd=platform_root, env=env, capture_output=True, text=True,
                check=False, timeout=120,
            )
            if query.returncode != 0:
                return f"{mode} {host_state} diagnostic query exited {query.returncode}"
            try:
                result = json.loads(query.stdout)
            except json.JSONDecodeError as error:
                return f"{mode} {host_state} diagnostic query returned invalid JSON: {error}"
            logs = logs_from_query_envelope(result) if isinstance(result, dict) else []
            record = next((item for item in logs
                           if isinstance(item, dict) and item.get("correlationId") == correlation_id), None)
            if record is None:
                return f"{mode} {host_state} query did not return the matching correlation ID"
            if record.get("event") != "cli.unexpected_error" or not record.get("errorClass"):
                return f"{mode} {host_state} record omitted the unexpected error event or class"
            if not record.get("errorMessage") or not record.get("causeChain"):
                return f"{mode} {host_state} record omitted the error message or cause chain"
            safe_output = failed.stdout + failed.stderr
            if output_mode == "json":
                try:
                    envelope = json.loads(failed.stdout)
                except json.JSONDecodeError as error:
                    return f"{mode} JSON failure returned invalid JSON: {error}"
                error_envelope = envelope.get("error") if isinstance(envelope, dict) else None
                if not isinstance(error_envelope, dict) or any(key in error_envelope for key in ("cause", "stack", "causeChain")):
                    return f"{mode} JSON failure exposed a cause or stack field"
            cause_chain = record.get("causeChain")
            diagnostic_text = [record.get("errorMessage"), record.get("stack")]
            if isinstance(cause_chain, list):
                diagnostic_text.extend(item.get("message") for item in cause_chain if isinstance(item, dict))
                diagnostic_text.extend(item.get("stack") for item in cause_chain if isinstance(item, dict))
            for detail in diagnostic_text:
                if isinstance(detail, str) and detail and detail in safe_output:
                    return f"{mode} {output_mode} failure exposed a cause or stack"
            rendered = json.dumps(record, sort_keys=True)
            for name, value in env.items():
                if value and redaction.is_secret_key(name) and value in rendered:
                    return f"{mode} {host_state} log record contains the value of secret environment variable {name}"
        return None

    journey.step(
        "unexpected CLI error trail with host running",
        "cli",
        lambda: verify_state("host-running"),
        cases=["S-203.26"],
    )

    create_cli = shutil.which("kb-create", path=env.get("PATH"))
    if create_cli is None:
        journey.step("unexpected CLI error trail with host stopped", "cli",
                     lambda: "BLOCKED: installed kb-create is required to stop and restore the host",
                     cases=["S-203.26"])
        return
    try:
        stop = run_command([create_cli, "stop", "--platform-root", platform_root, "--json"],
                           cwd=platform_root, env=env, capture_output=True, text=True,
                           check=False, timeout=120)
        if stop.returncode != 0:
            journey.step("unexpected CLI error trail with host stopped", "cli",
                         lambda: f"kb-create stop exited {stop.returncode}", cases=["S-203.26"])
        else:
            journey.step(
                "unexpected CLI error trail with host stopped",
                "cli",
                lambda: verify_state("host-stopped"),
                cases=["S-203.26"],
            )
    finally:
        restored = run_command([create_cli, "start", "--platform-root", platform_root, "--json"],
                               cwd=platform_root, env=env, capture_output=True, text=True,
                               check=False, timeout=120)
        if restored.returncode != 0:
            journey.step("restore host after unexpected CLI trail", "cli",
                         lambda: f"kb-create start exited {restored.returncode}")
