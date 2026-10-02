#!/usr/bin/env python3
"""Select journey entry points from release cases claimed by executable flows."""
from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

from invocations import invocation_context, run_command


SERVICE_SOURCES = {
    "e2e/journey/service_runner.py",
    "e2e/journey/steps/auth.py",
    "e2e/journey/steps/accounts.py",
    "e2e/journey/studio_steps.py",
}


def selectors_from_coverage(results: list[dict]) -> list[str]:
    selected: set[str] = set()
    for coverage in results:
        mode = coverage.get("mode")
        if mode not in {"local", "secured"}:
            raise ValueError(f"unsupported QA coverage mode: {mode!r}")
        for case in coverage.get("cases", []):
            if case.get("status") != "active" or case.get("release") is not True:
                continue
            for step in case.get("steps", []):
                for claim in step.get("claims", []):
                    if claim.get("kind") not in {"journey", "e2e"}:
                        continue
                    source = claim.get("source", "")
                    if source in SERVICE_SOURCES or source.startswith("e2e/studio/"):
                        prefix = "service-local" if mode == "local" else "service-secured"
                        selected.add(f"{prefix}:explore")
                    elif source == "e2e/journey/runner.py":
                        selected.add("agent:commit")
    return sorted(selected)


def main() -> int:
    results = []
    for mode in ("local", "secured"):
        command = ["pnpm", "-s", "kb", "qa", "cases", "coverage", "--level", "all", "--mode", mode, "--json"]
        with invocation_context(step_id="journey.case-selection"):
            result = run_command(command, cwd=Path.cwd(), env=os.environ, timeout=120)
        if result.returncode != 0:
            raise subprocess.CalledProcessError(result.returncode, command, output=result.stdout, stderr=result.stderr)
        results.append(json.loads(result.stdout))
    print(json.dumps(selectors_from_coverage(results)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
