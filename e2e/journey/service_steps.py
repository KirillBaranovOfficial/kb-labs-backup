#!/usr/bin/env python3
"""Small named-step framework shared by installed service journeys."""
from __future__ import annotations

import json
import time
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Iterable


@dataclass
class StepResult:
    name: str
    capability: str
    mode: str
    status: str
    duration_seconds: float
    message: str
    cases: list[str]


@dataclass
class JourneyResult:
    schema: str
    run_id: str
    started_at: str
    duration_seconds: float
    status: str
    first_failure: dict | None
    steps: list[dict]
    case_run: dict


class Journey:
    def __init__(self, mode: str, run_id: str = "local"):
        if mode not in {"local", "secured"}:
            raise ValueError(f"unsupported access mode: {mode}")
        self.mode = mode
        self.run_id = run_id
        self.started_at = datetime.now(timezone.utc).isoformat()
        self.started = time.monotonic()
        self.steps: list[StepResult] = []

    def step(self, name: str, capability: str, check: Callable[[], str | None], cases: Iterable[str] = ()) -> StepResult:
        started = time.monotonic()
        try:
            message = check()
            status = "PASS" if message is None else "FAIL"
        except Exception as error:  # Include a concise error, not a raw traceback.
            status = "FAIL"
            message = str(error) or error.__class__.__name__
        result = StepResult(name, capability, self.mode, status,
                            round(time.monotonic() - started, 3), message or "passed", list(cases))
        self.steps.append(result)
        return result

    def blocked(self, name: str, capability: str, reason: str, cases: Iterable[str] = ()) -> StepResult:
        result = StepResult(name, capability, self.mode, "BLOCKED", 0.0, reason, list(cases))
        self.steps.append(result)
        return result

    def result(self) -> JourneyResult:
        first = next((asdict(step) for step in self.steps if step.status == "FAIL"), None)
        failed = any(step.status == "FAIL" for step in self.steps)
        blocked = any(step.status == "BLOCKED" for step in self.steps)
        status = "FAIL" if failed else "BLOCKED" if blocked else "PASS"
        claims = {}
        for step in self.steps:
            if step.status == "BLOCKED":
                continue
            for case_id in step.cases:
                previous = claims.get(case_id)
                if step.status == "FAIL" or previous != "FAIL":
                    claims[case_id] = step.status
        case_run = {
            "schema": "kb.qa-case-run/v1",
            "runId": self.run_id,
            "mode": self.mode,
            "claims": [{"claimId": case_id, "kind": "journey", "status": result} for case_id, result in sorted(claims.items())],
        }
        return JourneyResult("kb.service-journey/v1", self.run_id, self.started_at,
                             round(time.monotonic() - self.started, 3), status, first,
                             [asdict(step) for step in self.steps], case_run)

    def write_json(self, path: str | Path) -> JourneyResult:
        result = self.result()
        target = Path(path)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(asdict(result), indent=2) + "\n")
        return result


def run_steps(journey: Journey, steps: Iterable[tuple[str, str, Callable[[], str | None]]]) -> JourneyResult:
    for name, capability, check in steps:
        journey.step(name, capability, check)
    return journey.result()


def record_runner_failure(path: str | Path, mode: str, failure: str) -> JourneyResult:
    """Preserve a pre-service runner failure in the capability result file."""
    name, separator, message = failure.partition(": ")
    journey = Journey(mode, f"runner-{mode}")
    journey.step(name if separator else "journey runner", "journey.install",
                 lambda: message if separator else failure or "journey failed before capability checks")
    return journey.write_json(path)
