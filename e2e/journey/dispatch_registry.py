#!/usr/bin/env python3
"""Single source for journey claim routing and runner entry points."""
from __future__ import annotations

import json
import sys
from dataclasses import asdict, dataclass
from invocations import print_report_text


@dataclass(frozen=True)
class Runner:
    front_door: str
    scenario: str
    variant: str
    service_profile: str = ""
    journey_sources: tuple[str, ...] = ("e2e/journey/runner.py",)


RUNNERS = {
    "agent:explore": Runner("agent", "explore", "default"),
    "agent:commit": Runner("agent", "commit", "default"),
    "agent-secured:commit": Runner("agent", "commit", "secured"),
    "agent-input:commit": Runner("agent", "commit", "input"),
    "human:default": Runner("human", "explore", "human-default"),
    "human:nondefault": Runner("human", "commit", "human-nondefault"),
    "negative:wrong-node": Runner("agent", "explore", "wrong-node"),
    "negative:occupied-port": Runner("agent", "explore", "occupied-port"),
    "service-local:explore": Runner(
        "agent", "explore", "default", "nightly",
        ("e2e/journey/runner.py", "e2e/journey/service_runner.py")),
    "service-secured:explore": Runner(
        "human", "explore", "secured", "nightly",
        ("e2e/journey/runner.py", "e2e/journey/service_runner.py")),
}
RUNNER_GROUPS = {
    "service-modes": ("agent:commit", "service-secured:explore"),
}

def runners_for_source(source: str, mode: str) -> set[str]:
    if source in {
        "e2e/journey/service_runner.py",
        "e2e/journey/steps/auth.py",
        "e2e/journey/steps/accounts.py",
        "e2e/journey/studio_steps.py",
    } or source.startswith("e2e/studio/"):
        return {"service-local:explore" if mode == "local" else "service-secured:explore"}
    if source.startswith("e2e/journey/"):
        return {"agent:commit" if mode == "local" else "agent-secured:commit"}
    return set()


def selector_supports_claim_sources(selector: str, selected_runners: list[str]) -> bool:
    required = set(RUNNER_GROUPS.get(selector, (selector,)))
    return bool(required) and required.issubset(set(selected_runners))


def main() -> int:
    if len(sys.argv) == 3 and sys.argv[1] == "--runner":
        runner = RUNNERS.get(sys.argv[2])
        if runner is None:
            raise ValueError(f"unknown journey runner selector: {sys.argv[2]}")
        print_report_text(json.dumps(asdict(runner)), flush=True)
        return 0
    if len(sys.argv) == 3 and sys.argv[1] == "--dispatch":
        selector = sys.argv[2]
        selectors = RUNNER_GROUPS.get(selector, (selector,))
        runners = []
        for item in selectors:
            runner = RUNNERS.get(item)
            if runner is None:
                raise ValueError(f"unknown journey runner selector: {item}")
            runners.append(asdict(runner))
        print_report_text(json.dumps(runners), flush=True)
        return 0
    if len(sys.argv) == 3 and sys.argv[1] == "--supports":
        selection = json.load(sys.stdin)
        return 0 if selector_supports_claim_sources(sys.argv[2], selection.get("runners", [])) else 2
    raise ValueError("usage: dispatch_registry.py --runner <selector> | --dispatch <selector> | --supports <selector>")


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as error:
        print_report_text(str(error), stream=sys.stderr, flush=True)
        raise SystemExit(2)
    except Exception as error:
        print_report_text(f"Journey dispatch failed: {error.__class__.__name__}: {error}",
                          stream=sys.stderr, flush=True)
        raise SystemExit(1)
