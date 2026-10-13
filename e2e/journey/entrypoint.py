#!/usr/bin/env python3
"""Run the journey while recording a result if Python startup itself fails."""
from __future__ import annotations
from journey_values import journey_value

import json
import os
import runpy
import sys
import traceback
from datetime import datetime, timezone
from pathlib import Path

from platform_state_dump import dump_platform_state, failure_dump_environment
from dispatch_registry import RUNNERS
from scoreboard import write_scoreboards
from journey_resources import case_catalog_root, journey_module
from invocations import print_report_text, write_report_json, write_report_text


def write_case_scoreboards(summary_path: str | Path) -> None:
    report_dir = Path(summary_path).parent
    write_scoreboards(report_dir, case_catalog_root())


def record_runner_failure(summary_path: str | Path, mode: str, error: Exception, phase: str) -> None:
    name = "runner startup" if phase == "startup" else "runner crash"
    step = {
        "name": name,
        "capability": "journey.runner",
        "mode": mode,
        "status": "FAIL",
        "duration_seconds": 0.0,
        "message": f"{error.__class__.__name__}: {error}",
    }
    target = Path(summary_path).with_name(f"service-{mode}.json")
    try:
        result = json.loads(target.read_text())
        if not isinstance(result, dict) or not isinstance(result.get("steps"), list):
            raise ValueError("existing journey result has an invalid shape")
    except (OSError, json.JSONDecodeError, ValueError):
        result = {
            "schema": "kb.service-journey/v1",
            "run_id": f"runner-{mode}",
            "started_at": datetime.now(timezone.utc).isoformat(),
            "duration_seconds": 0.0,
            "status": "FAIL",
            "first_failure": step,
            "steps": [],
        }
    result["steps"].append(step)
    result["status"] = "FAIL"
    result["first_failure"] = result.get("first_failure") or step
    write_report_json(target, result)


def run_runner(runner_path: str | Path, summary_path: str | Path, mode: str) -> int:
    try:
        namespace = runpy.run_path(str(runner_path), run_name="journey_runner_module")
    except Exception as error:
        try:
            dump_platform_state(
                Path.home() / "kb-platform", Path(summary_path).parent, mode=mode,
                front=journey_value("KB_JOURNEY_FRONT_DOOR", "agent"),
                scenario=journey_value("KB_JOURNEY_SCENARIO", "commit"),
                variant=journey_value("KB_JOURNEY_VARIANT", "default"),
                first_failure=f"runner startup: {error.__class__.__name__}: {error}",
                env=os.environ, admin_password=journey_value("KB_JOURNEY_ADMIN_PASSWORD", ""),
            )
        except Exception as dump_error:
            print_report_text(f"Journey state dump failed: {dump_error.__class__.__name__}: {dump_error}",
                              stream=sys.stderr, flush=True)
        try:
            record_runner_failure(summary_path, mode, error, "startup")
            write_case_scoreboards(summary_path)
        except OSError as write_error:
            print_report_text(f"Journey runner startup failed; could not write result: {write_error}",
                              stream=sys.stderr, flush=True)
        print_report_text(f"Journey runner startup failed: {error.__class__.__name__}: {error}",
                          stream=sys.stderr, flush=True)
        return 1
    try:
        return int(namespace["main"]())
    except Exception as error:
        try:
            diagnostic = Path(summary_path).with_name("journey-diagnostics.log")
            write_report_text(diagnostic, "\n== Journey runner traceback ==\n"
                              + "".join(traceback.format_exception(type(error), error, error.__traceback__))
                              + "\n", append=True)
        except OSError as diagnostic_error:
            print_report_text(f"Journey traceback could not be written: {diagnostic_error}",
                              stream=sys.stderr, flush=True)
        try:
            report = Path(summary_path)
            environment = namespace.get("env")
            dump = namespace.get("STATE_DUMP_GUARD")
            write_dump = dump.dump if dump is not None else dump_platform_state
            write_dump(
                namespace.get("PLATFORM", Path.home() / "kb-platform"), report.parent,
                mode=mode, front=journey_value("KB_JOURNEY_FRONT_DOOR", "agent"),
                scenario=journey_value("KB_JOURNEY_SCENARIO", "commit"),
                variant=journey_value("KB_JOURNEY_VARIANT", "default"),
                first_failure=f"{error.__class__.__name__}: {error}",
                env=failure_dump_environment(
                    namespace.get("ACTIVE_MAIN_ENV"), namespace.get("LAST_COMMAND_ENV"),
                    environment or os.environ,
                ),
                cwd=namespace.get("LAST_COMMAND_CWD"),
                admin_password="JourneyPassw0rd!" if mode == "secured" else "",
            )
        except Exception as dump_error:
            print_report_text(f"Journey state dump failed: {dump_error.__class__.__name__}: {dump_error}",
                              stream=sys.stderr, flush=True)
        try:
            record_runner_failure(summary_path, mode, error, "crash")
            write_case_scoreboards(summary_path)
        except OSError as write_error:
            print_report_text(f"Journey runner crashed; could not write result: {write_error}",
                              stream=sys.stderr, flush=True)
        print_report_text(f"Journey runner crashed: {error.__class__.__name__}: {error}",
                          stream=sys.stderr, flush=True)
        return 1
    finally:
        cleanup = namespace.get("cleanup")
        if callable(cleanup):
            cleanup()


def main() -> int:
    selector = journey_value("KB_JOURNEY_RUNNER_SELECTOR", "agent:commit")
    runner = RUNNERS.get(selector)
    if runner is None:
        raise ValueError(f"unknown registered journey runner {selector!r}")
    runner_path = journey_module("runner.py")
    summary_path = journey_value("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")
    return run_runner(runner_path, summary_path, runner.mode)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print_report_text(f"Journey runner failed: {error.__class__.__name__}: {error}",
                          stream=sys.stderr, flush=True)
        raise SystemExit(1)
