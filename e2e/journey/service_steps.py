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


@dataclass
class JourneyResult:
    schema: str
    run_id: str
    started_at: str
    duration_seconds: float
    status: str
    first_failure: dict | None
    steps: list[dict]


class Journey:
    def __init__(self, mode: str, run_id: str = "local"):
        if mode not in {"local", "secured"}:
            raise ValueError(f"unsupported access mode: {mode}")
        self.mode = mode
        self.run_id = run_id
        self.started_at = datetime.now(timezone.utc).isoformat()
        self.started = time.monotonic()
        self.steps: list[StepResult] = []

    def step(self, name: str, capability: str, check: Callable[[], str | None]) -> StepResult:
        started = time.monotonic()
        try:
            message = check()
            status = "PASS" if message is None else "FAIL"
        except Exception as error:  # Include a concise error, not a raw traceback.
            status = "FAIL"
            message = str(error) or error.__class__.__name__
        result = StepResult(name, capability, self.mode, status,
                            round(time.monotonic() - started, 3), message or "passed")
        self.steps.append(result)
        return result

    def blocked(self, name: str, capability: str, reason: str) -> StepResult:
        result = StepResult(name, capability, self.mode, "BLOCKED", 0.0, reason)
        self.steps.append(result)
        return result

    def result(self) -> JourneyResult:
        first = next((asdict(step) for step in self.steps if step.status == "FAIL"), None)
        failed = any(step.status == "FAIL" for step in self.steps)
        blocked = any(step.status == "BLOCKED" for step in self.steps)
        status = "FAIL" if failed else "BLOCKED" if blocked else "PASS"
        return JourneyResult("kb.service-journey/v1", self.run_id, self.started_at,
                             round(time.monotonic() - self.started, 3), status, first,
                             [asdict(step) for step in self.steps])

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
