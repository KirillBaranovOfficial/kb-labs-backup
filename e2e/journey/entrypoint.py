#!/usr/bin/env python3
"""Run the journey while recording a result if Python startup itself fails."""
from __future__ import annotations

import json
import os
import runpy
import sys
from datetime import datetime, timezone
from pathlib import Path


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
            record_runner_failure(summary_path, mode, error, "startup")
        except OSError as write_error:
            print(f"Journey runner startup failed; could not write result: {write_error}", file=sys.stderr)
        print(f"Journey runner startup failed: {error.__class__.__name__}: {error}", file=sys.stderr)
        return 1
    try:
        return int(namespace["main"]())
    except Exception as error:
        try:
            record_runner_failure(summary_path, mode, error, "crash")
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
