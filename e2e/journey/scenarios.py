#!/usr/bin/env python3
"""Read and validate the shared local/release/nightly journey contract."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "e2e/journey/scenarios.json"
LEVELS = {"release", "nightly", "full"}


def load_manifest() -> dict:
    return json.loads(MANIFEST.read_text())


def selectors(manifest: dict) -> list[str]:
    result = [item["selector"] for item in manifest["scenarios"] if item["coverage"] == "covered" and "selector" in item]
    builtins = manifest["builtInScenarios"]
    scenarios_dir = ROOT / builtins["directory"]
    ids = sorted(path.stem for path in scenarios_dir.glob("*.json") if path.stem not in builtins["exclude"])
    result.extend(builtins["selector"].format(id=scenario_id) for scenario_id in ids)
    return list(dict.fromkeys(result))


def validate(manifest: dict) -> list[str]:
    errors: list[str] = []
    ids: set[str] = set()
    for item in manifest["scenarios"]:
        scenario_id = item.get("id")
        if not scenario_id or scenario_id in ids:
            errors.append(f"scenario id is missing or duplicated: {scenario_id!r}")
        ids.add(scenario_id)
        if item.get("coverage") == "covered":
            if not item.get("step"):
                errors.append(f"{scenario_id}: covered scenario has no step")
            else:
                step_sources = item["step"].split(":", 1)[0].split(" plus ")
                for step_source in step_sources:
                    if not (ROOT / step_source).is_file():
                        errors.append(f"{scenario_id}: step source does not exist: {step_source}")
            if "selector" not in item and "selectorSource" not in item:
                errors.append(f"{scenario_id}: covered scenario has no runner selector")
        elif item.get("coverage") == "not_covered":
            if item.get("step") is not None or not item.get("reason") or not item.get("estimate"):
                errors.append(f"{scenario_id}: NOT COVERED needs null step, reason, and sizing estimate")
        else:
            errors.append(f"{scenario_id}: invalid coverage {item.get('coverage')!r}")

    run_script = (ROOT / "e2e/journey/run.sh").read_text()
    supported = {"agent", "agent-input", "human", "negative", "service-local", "service-secured"}
    for selector in selectors(manifest):
        prefix = selector.split(":", 1)[0]
        if prefix not in supported or f"{prefix}:" not in run_script:
            errors.append(f"runner has no selector implementation for {selector}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["list", "validate"])
    parser.add_argument("--level", choices=sorted(LEVELS), default="release")
    args = parser.parse_args()
    manifest = load_manifest()
    errors = validate(manifest)
    uncovered = [item for item in manifest["scenarios"] if item["coverage"] == "not_covered"]
    if args.command == "validate":
        if errors:
            print("Scenario manifest invalid: " + "; ".join(errors), file=sys.stderr)
            return 1
        if uncovered:
            print("NOT COVERED scenarios fail the release contract:", file=sys.stderr)
            for item in uncovered:
                print(f"- {item['id']}: {item['reason']} ({item['estimate']})", file=sys.stderr)
            print(f"Implement the user flow, then rerun `python3 e2e/journey/scenarios.py validate --level {args.level}`.", file=sys.stderr)
            return 1
        print("Scenario manifest is valid and fully covered")
        return 0
    if errors:
        print("Scenario manifest invalid: " + "; ".join(errors), file=sys.stderr)
        return 1
    print(json.dumps(selectors(manifest)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
