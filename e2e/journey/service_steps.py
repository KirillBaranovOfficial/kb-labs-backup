#!/usr/bin/env python3
"""Small named-step framework shared by installed service journeys."""
from __future__ import annotations

import json
import time
import traceback
import os
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
        self.selected_steps = set(filter(None, os.getenv("KB_JOURNEY_SELECTED_STEPS", "").split(",")))
        self.selection = list(filter(None, os.getenv("KB_JOURNEY_CASES", "").split(",")))

    def step(self, name: str, capability: str, check: Callable[[], str | None], cases: Iterable[str] = (),
             details: Callable[[], dict[str, Any]] | None = None, *, force: bool = False) -> StepResult:
        case_ids = list(cases)
        if self.selected_steps and not force and (not case_ids or not self.selected_steps.intersection(case_ids)):
            result = StepResult(name, capability, self.mode, "SKIPPED", 0.0, "not selected", case_ids)
            self.steps.append(result)
            return result
        started = time.monotonic()
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

    def skipped(self, name: str, capability: str, message: str = "not selected") -> StepResult:
        result = StepResult(name, capability, self.mode, "SKIPPED", 0.0, message, [])
        self.steps.append(result)
        return result

    def blocked(self, name: str, capability: str, reason: str, cases: Iterable[str] = ()) -> StepResult:
        case_ids = list(cases)
        if self.selected_steps and (not case_ids or not self.selected_steps.intersection(case_ids)):
            return self.skipped(name, capability)
        result = StepResult(name, capability, self.mode, "BLOCKED", 0.0, reason, case_ids)
        self.steps.append(result)
        return result

    def result(self) -> JourneyResult:
        first = next((_step_dict(step) for step in self.steps if step.status == "FAIL"), None)
        failed = any(step.status == "FAIL" for step in self.steps)
        blocked = any(step.status == "BLOCKED" for step in self.steps)
        status = "FAIL" if failed else "BLOCKED" if blocked else "PASS"
        claims = {}
        for step in self.steps:
            if step.status not in {"PASS", "FAIL"}:
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
        if self.selection:
            case_run.update({"partial": True, "selection": self.selection})
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
                 states: dict[str, dict[str, object]] | None = None,
                 selected_groups: Iterable[str] = (), state_producers: dict[str, str] | None = None):
        self.journey = journey
        self.dependencies = dependencies
        self.diagnostics_path = Path(diagnostics_path) if diagnostics_path else None
        self.states = dict(states or {})
        self.selected_groups = set(selected_groups)
        self.state_producers = dict(state_producers or {})

    def dependency_closure(self) -> set[str]:
        """Return selected groups plus the groups that produce their required states."""
        groups = set(self.selected_groups)
        pending = list(groups)
        states: set[str] = set()
        while pending:
            group = pending.pop()
            for state in self.dependencies.get(group, ()):
                if state in states:
                    continue
                states.add(state)
                producer = self.state_producers.get(state)
                if producer and producer not in groups:
                    groups.add(producer)
                    pending.append(producer)
        return groups

    def _state_required(self, state: str, group: str) -> bool:
        if not self.journey.selected_steps:
            return True
        groups = self.dependency_closure()
        return group in groups or any(state in self.dependencies.get(name, ()) for name in groups)

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
        if not self._state_required(state, group):
            return self.journey.skipped(f"state probe: {state}", "journey.state")
        def checked() -> str | None:
            try:
                return check()
            except Exception:
                self._write_traceback(group, f"state probe: {state}")
                raise

        result = self.journey.step(f"state probe: {state}", "journey.state", checked, force=True)
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
        case_ids = list(cases)
        if self.journey.selected_steps and (not case_ids or not self.journey.selected_steps.intersection(case_ids)):
            return self.journey.step(name, capability, lambda: None, cases=case_ids)
        blocker = self._blocker(group)
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
        if self.journey.selected_steps and case_ids and not self.journey.selected_steps.intersection(case_ids):
            self.journey.skipped(name, capability)
            return "SKIPPED"
        if self.journey.selected_steps:
            self.selected_groups.add(group)
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
            self.journey.step(name, capability, lambda: str(error) or error.__class__.__name__, force=True)
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
                 lambda: message if separator else failure or "journey failed before capability checks", force=True)
    return journey.write_json(path)


def record_candidate_provenance(journey: Journey, checkout_commit: str,
                                candidate: dict[str, str]) -> StepResult:
    """Add candidate source identity to the journey and fail before case claims."""
    candidate_commit = candidate.get("commit", "")
    candidate_version = candidate.get("version", "")
    details = {
        "checkoutCommit": checkout_commit,
        "candidateCommit": candidate_commit,
        "candidateVersion": candidate_version,
    }
    if candidate.get("identityError"):
        details["identityError"] = candidate["identityError"]
    if not checkout_commit or not candidate_commit:
        details["code"] = "KB_JOURNEY_CANDIDATE_IDENTITY_MISSING"
        failure = ("candidate source identity is missing: "
                   f"checkout commit={checkout_commit or '<missing>'}, candidate commit={candidate_commit or '<missing>'}")
        if candidate.get("identityError"):
            failure += f"; {candidate['identityError']}"
    elif not (checkout_commit.casefold().startswith(candidate_commit.casefold())
              or candidate_commit.casefold().startswith(checkout_commit.casefold())):
        details["code"] = "KB_JOURNEY_CANDIDATE_MISMATCH"
        failure = (f"candidate source commit {candidate_commit} ({candidate_version or 'unknown version'}) "
                   f"does not match checkout commit {checkout_commit}")
    else:
        failure = None

    return journey.step("candidate provenance", "journey.orchestration",
                        lambda: failure, details=lambda: details)
