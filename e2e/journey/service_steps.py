#!/usr/bin/env python3
"""Small named-step framework shared by installed service journeys."""
from __future__ import annotations

import json
import time
import traceback
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, Iterable
from invocations import invocation_context, invocation_link


@dataclass
class StepResult:
    name: str
    capability: str
    mode: str
    status: str
    duration_seconds: float
    message: str
    cases: list[str]
    details: dict[str, Any] | None = None


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

    def step(self, name: str, capability: str, check: Callable[[], str | None], cases: Iterable[str] = (),
             details: Callable[[], dict[str, Any]] | None = None) -> StepResult:
        started = time.monotonic()
        case_ids = list(cases)
        try:
            with invocation_context(step_id=",".join(case_ids) or name):
                message = check()
            if isinstance(message, str) and message.startswith("BLOCKED:"):
                status = "BLOCKED"
                message = message.removeprefix("BLOCKED:").strip()
            else:
                status = "PASS" if message is None else "FAIL"
        except Exception as error:  # Include a concise error, not a raw traceback.
            status = "FAIL"
            message = str(error) or error.__class__.__name__
        step_details = details() if details is not None else None
        if status == "FAIL":
            reference = invocation_link(",".join(case_ids) or name)
            if reference:
                step_details = dict(step_details or {})
                step_details["invocation_evidence"] = reference
        result = StepResult(name, capability, self.mode, status,
                            round(time.monotonic() - started, 3), message or "passed", case_ids,
                            step_details)
        self.steps.append(result)
        return result

    def blocked(self, name: str, capability: str, reason: str, cases: Iterable[str] = ()) -> StepResult:
        result = StepResult(name, capability, self.mode, "BLOCKED", 0.0, reason, list(cases))
        self.steps.append(result)
        return result

    def result(self) -> JourneyResult:
        first = next((_step_dict(step) for step in self.steps if step.status == "FAIL"), None)
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
                             [_step_dict(step) for step in self.steps], case_run)

    def write_json(self, path: str | Path) -> JourneyResult:
        result = self.result()
        target = Path(path)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(asdict(result), indent=2) + "\n")
        return result


class FlowGraph:
    """Gate journey groups on probed state, independent of product assertions."""

    def __init__(self, journey: Journey, dependencies: dict[str, tuple[str, ...]],
                 diagnostics_path: str | Path | None = None,
                 states: dict[str, dict[str, object]] | None = None):
        self.journey = journey
        self.dependencies = dependencies
        self.diagnostics_path = Path(diagnostics_path) if diagnostics_path else None
        self.states = dict(states or {})

    def _blocker(self, group: str) -> tuple[str, dict[str, object]] | None:
        for state in self.dependencies.get(group, ()):
            result = self.states.get(state)
            if result is None:
                return state, {"passed": False, "evidence": "state probe has not run",
                               "group": "journey", "step": "missing probe", "claim": "no case claim"}
            if result.get("passed") is not True:
                return state, result
        return None

    def probe_state(self, state: str, group: str, step: str, check: Callable[[], str | None]) -> StepResult:
        """Run and record an explicit state precondition probe."""
        def checked() -> str | None:
            try:
                return check()
            except Exception:
                self._write_traceback(group, f"state probe: {state}")
                raise

        result = self.journey.step(f"state probe: {state}", "journey.state", checked)
        self.states[state] = {
            "passed": result.status == "PASS",
            "evidence": "probe passed" if result.status == "PASS" else result.message,
            "group": group,
            "step": step,
            "claim": "no case claim",
        }
        return result

    def _write_traceback(self, group: str, name: str) -> None:
        if self.diagnostics_path:
            self.diagnostics_path.parent.mkdir(parents=True, exist_ok=True)
            with self.diagnostics_path.open("a", encoding="utf-8") as stream:
                stream.write(f"\n== unexpected exception in {group}/{name} ==\n")
                stream.write(traceback.format_exc())

    def step(self, name: str, group: str, capability: str, check: Callable[[], str | None],
             cases: Iterable[str] = ()) -> StepResult:
        blocker = self._blocker(group)
        case_ids = list(cases)
        if blocker:
            state, evidence = blocker
            result = self.journey.blocked(
                name, capability,
                f"blocked by state {state}: {evidence['evidence']} (last mutated by "
                f"{evidence['group']}/{evidence['step']} ({evidence['claim']}))",
                cases=case_ids,
            )
            return result

        def checked() -> str | None:
            try:
                return check()
            except Exception:
                self._write_traceback(group, name)
                raise

        result = self.journey.step(name, capability, checked, cases=case_ids)
        return result

    def flow(self, name: str, group: str, capability: str, action: Callable[[], object],
             cases: Iterable[str] = (), *, always_run: bool = False,
             record_empty_success: bool = True) -> str:
        """Run an outer flow; return PASS, FAIL, or BLOCKED for dependency wiring."""
        case_ids = list(cases)
        blocker = None if always_run else self._blocker(group)
        if blocker:
            state, evidence = blocker
            self.journey.blocked(name, capability,
                                 f"blocked by state {state}: {evidence['evidence']} (last mutated by "
                                 f"{evidence['group']}/{evidence['step']} ({evidence['claim']}))",
                                 cases=case_ids)
            return "BLOCKED"

        start = len(self.journey.steps)
        try:
            outcome = action()
        except Exception as error:
            self._write_traceback(group, name)
            self.journey.step(name, capability, lambda: str(error) or error.__class__.__name__)
            outcome = None
        emitted = self.journey.steps[start:]
        if isinstance(outcome, str):
            self.journey.step(name, capability, lambda: outcome, cases=case_ids)
        elif not emitted and record_empty_success:
            self.journey.step(name, capability, lambda: None, cases=case_ids)

        group_steps = self.journey.steps[start:]
        failed = next((step for step in group_steps if step.status == "FAIL"), None)
        blocked = next((step for step in group_steps if step.status == "BLOCKED"), None)
        if failed:
            return "FAIL"
        if blocked:
            return "BLOCKED"
        return "PASS"


def run_steps(journey: Journey, steps: Iterable[tuple[str, str, Callable[[], str | None]]]) -> JourneyResult:
    for name, capability, check in steps:
        journey.step(name, capability, check)
    return journey.result()


def _step_dict(step: StepResult) -> dict[str, Any]:
    result = asdict(step)
    if step.details is None:
        result.pop("details")
    return result


def record_runner_failure(path: str | Path, mode: str, failure: str) -> JourneyResult:
    """Preserve a pre-service runner failure in the capability result file."""
    name, separator, message = failure.partition(": ")
    journey = Journey(mode, f"runner-{mode}")
    journey.step(name if separator else "journey runner", "journey.install",
                 lambda: message if separator else failure or "journey failed before capability checks")
    return journey.write_json(path)
