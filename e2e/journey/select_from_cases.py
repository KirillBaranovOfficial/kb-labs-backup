#!/usr/bin/env python3
"""Select journey entry points from release cases claimed by executable flows."""
from __future__ import annotations

import json
import os
import sys
from pathlib import Path

from hermetic_cli import run_hermetic_cli
from invocations import decode_ux_success_result, describe_cli_failure, invocation_context, read_ux_cli_result


SERVICE_SOURCES = {
    "e2e/journey/service_runner.py",
    "e2e/journey/steps/auth.py",
    "e2e/journey/steps/accounts.py",
    "e2e/journey/studio_steps.py",
}


def read_coverage_result(stdout: str, returncode: int) -> dict:
    payload = decode_ux_success_result(read_ux_cli_result(stdout, returncode))
    if not isinstance(payload, dict):
        raise ValueError("QA coverage result data is not an object")
    return payload


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


def selection_from_coverage(results: list[dict], requested: str) -> dict:
    """Resolve requested cases/steps against the QA coverage claim registry."""
    registry: dict[str, set[str]] = {}
    for coverage in results:
        for case in coverage.get("cases", []):
            if case.get("status") != "active":
                continue
            case_id = case.get("id")
            if not isinstance(case_id, str):
                continue
            for step in case.get("steps", []):
                step_id = step.get("stepId")
                journey_claimed = any(
                    claim.get("kind") in {"journey", "e2e"}
                    and str(claim.get("source", "")).startswith(("e2e/journey/", "e2e/studio/", "e2e/"))
                    for claim in step.get("claims", [])
                )
                if journey_claimed and isinstance(step_id, str):
                    registry.setdefault(case_id, set()).add(step_id)

    case_ids = set(registry)
    step_ids = set().union(*registry.values()) if registry else set()
    tokens = [item.strip() for item in requested.split(",") if item.strip()]
    if not tokens:
        raise ValueError("KB_JOURNEY_CASES must contain at least one case or step ID")
    selected: set[str] = set()
    for token in tokens:
        if token in case_ids:
            selected.update(registry[token])
        elif token in step_ids:
            selected.add(token)
        else:
            raise ValueError(
                f"unknown or unclaimed journey case/step ID: {token}. "
                "Check available IDs with `node ./cli/bin/dist/bin.js qa cases list --json`, then rerun the journey."
            )
    return {"selection": sorted(set(tokens)), "steps": sorted(selected)}


def main() -> int:
    results = []
    for mode in ("local", "secured"):
        command = ["pnpm", "-s", "kb", "qa", "cases", "coverage", "--level", "all", "--mode", mode, "--json"]
        with invocation_context(step_id="journey.case-selection"):
            result = run_hermetic_cli(command, cwd=Path.cwd(), env=os.environ, timeout=120)
        if result.returncode != 0:
            raise ValueError(describe_cli_failure(" ".join(command), result.stdout, result.stderr, result.returncode))
        results.append(read_coverage_result(result.stdout, result.returncode))
    requested = os.environ.get("KB_JOURNEY_CASES", "")
    if requested:
        print(json.dumps(selection_from_coverage(results, requested)))
    else:
        print(json.dumps(selectors_from_coverage(results)))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
