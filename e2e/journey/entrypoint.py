#!/usr/bin/env python3
"""Run the journey while recording a result if Python startup itself fails."""
from __future__ import annotations

import json
import os
import runpy
import sys
import traceback
from datetime import datetime, timezone
from pathlib import Path

from platform_state_dump import dump_platform_state
from scoreboard import write_scoreboards


def write_case_scoreboards(summary_path: str | Path) -> None:
    report_dir = Path(summary_path).parent
    catalog = Path(__file__).resolve().parent / "scenarios"
    if not catalog.is_dir():
        catalog = Path(__file__).resolve().parents[2] / "docs" / "qa" / "scenarios"
    write_scoreboards(report_dir, catalog)


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
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(result, indent=2) + "\n")


def run_runner(runner_path: str | Path, summary_path: str | Path, mode: str) -> int:
    try:
        namespace = runpy.run_path(str(runner_path), run_name="journey_runner_module")
    except Exception as error:
        try:
            dump_platform_state(
                Path.home() / "kb-platform", Path(summary_path).parent, mode=mode,
                front=os.getenv("KB_JOURNEY_FRONT_DOOR", "agent"),
                scenario=os.getenv("KB_JOURNEY_SCENARIO", "commit"),
                variant=os.getenv("KB_JOURNEY_VARIANT", "default"),
                first_failure=f"runner startup: {error.__class__.__name__}: {error}",
                env=os.environ, admin_password=os.getenv("KB_JOURNEY_ADMIN_PASSWORD", ""),
            )
        except Exception as dump_error:
            print(f"Journey state dump failed: {dump_error.__class__.__name__}: {dump_error}", file=sys.stderr)
        try:
            record_runner_failure(summary_path, mode, error, "startup")
            write_case_scoreboards(summary_path)
        except OSError as write_error:
            print(f"Journey runner startup failed; could not write result: {write_error}", file=sys.stderr)
        print(f"Journey runner startup failed: {error.__class__.__name__}: {error}", file=sys.stderr)
        return 1
    try:
        return int(namespace["main"]())
    except Exception as error:
        try:
            diagnostic = Path(summary_path).with_name("journey-diagnostics.log")
            with diagnostic.open("a", encoding="utf-8") as stream:
                stream.write("\n== Journey runner traceback ==\n")
                stream.writelines(traceback.format_exception(error))
                stream.write("\n")
        except OSError as diagnostic_error:
            print(f"Journey traceback could not be written: {diagnostic_error}", file=sys.stderr)
        try:
            report = Path(summary_path)
            environment = namespace.get("env")
            dump = namespace.get("STATE_DUMP_GUARD")
            write_dump = dump.dump if dump is not None else dump_platform_state
            write_dump(
                namespace.get("PLATFORM", Path.home() / "kb-platform"), report.parent,
                mode=mode, front=os.getenv("KB_JOURNEY_FRONT_DOOR", "agent"),
                scenario=os.getenv("KB_JOURNEY_SCENARIO", "commit"),
                variant=os.getenv("KB_JOURNEY_VARIANT", "default"),
                first_failure=f"{error.__class__.__name__}: {error}",
                env=namespace.get("LAST_COMMAND_ENV") or environment or os.environ,
                cwd=namespace.get("LAST_COMMAND_CWD"),
                admin_password="JourneyPassw0rd!" if mode == "secured" else "",
            )
        except Exception as dump_error:
            print(f"Journey state dump failed: {dump_error.__class__.__name__}: {dump_error}", file=sys.stderr)
        try:
            record_runner_failure(summary_path, mode, error, "crash")
            write_case_scoreboards(summary_path)
        except OSError as write_error:
            print(f"Journey runner crashed; could not write result: {write_error}", file=sys.stderr)
        print(f"Journey runner crashed: {error.__class__.__name__}: {error}", file=sys.stderr)
        return 1
    finally:
        cleanup = namespace.get("cleanup")
        if callable(cleanup):
            cleanup()


def main() -> int:
    variant = os.getenv("KB_JOURNEY_VARIANT", "default")
    mode = "secured" if variant.endswith("secured") else "local"
    runner_path = Path("/opt/kb-journey/runner.py")
    summary_path = os.getenv("KB_JOURNEY_SUMMARY", "/summary/journey-summary.md")
    return run_runner(runner_path, summary_path, mode)


if __name__ == "__main__":
    raise SystemExit(main())
