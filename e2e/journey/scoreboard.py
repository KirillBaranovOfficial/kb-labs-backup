#!/usr/bin/env python3
"""Score product-case claim coverage from installed-platform journey reports."""
from __future__ import annotations

import json
import re
import sys
from collections import defaultdict
from pathlib import Path
from typing import Iterable

CASE_STEP = re.compile(r"^\|\s*((?:S|PC)-\d+\.\d+)\s*\|", re.MULTILINE)
CLAIM_TAG = re.compile(r"@(S|PC)-(\d+\.\d+)")
MODES = ("local", "secured")


def load_cases(catalog: Path) -> list[dict]:
    cases = []
    for path in sorted(catalog.glob("*.md")):
        source = path.read_text(encoding="utf-8")
        if not source.startswith("---\n"):
            continue
        _, frontmatter, body = source.split("---", 2)
        fields = dict(re.findall(r"^(id|title|status|modes):\s*(.*?)\s*$", frontmatter, re.MULTILINE))
        if "id" not in fields:
            continue
        modes_match = re.search(r"\[([^]]*)\]", fields.get("modes", ""))
        modes = [value.strip() for value in modes_match.group(1).split(",") if value.strip()] if modes_match else []
        steps = sorted(set(CASE_STEP.findall(body)), key=claim_sort_key)
        cases.append({"id": fields["id"], "title": fields.get("title", ""),
                      "status": fields.get("status", ""), "modes": modes, "claims": steps})
    return cases


def claim_sort_key(claim: str) -> tuple[str, int]:
    case, step = claim.rsplit(".", 1)
    return case, int(step)


def _record(evidence: dict[str, list[dict]], claim_id: str, status: str, source: str, reason: str = "") -> None:
    if status not in {"PASS", "FAIL", "BLOCKED"}:
        return
    evidence[claim_id].append({"status": status, "source": source, **({"reason": reason} if reason else {})})


def _journey_evidence(data: dict, path: Path, evidence: dict[str, list[dict]]) -> None:
    for claim in data.get("case_run", {}).get("claims", []):
        if isinstance(claim, dict) and isinstance(claim.get("claimId"), str):
            _record(evidence, claim["claimId"], claim.get("status", ""), path.name)
    for step in data.get("steps", []):
        if not isinstance(step, dict):
            continue
        status = step.get("status")
        if status not in {"PASS", "FAIL", "BLOCKED"}:
            continue
        for claim_id in step.get("cases", []):
            if isinstance(claim_id, str):
                _record(evidence, claim_id, status, path.name, str(step.get("message", "")))


def _playwright_status(spec: dict) -> tuple[str, str]:
    tests = spec.get("tests", [])
    results = [result for test in tests for result in test.get("results", []) if isinstance(result, dict)]
    if any(result.get("status") in {"failed", "timedOut", "interrupted"} for result in results):
        return "FAIL", "Playwright test failed"
    if results and all(result.get("status") == "passed" for result in results):
        return "PASS", ""
    if results and all(result.get("status") == "skipped" for result in results):
        annotations = [annotation.get("description", "") for test in tests
                       for annotation in test.get("annotations", []) if isinstance(annotation, dict)]
        return "BLOCKED", "; ".join(item for item in annotations if item) or "Playwright test was skipped"
    if spec.get("ok") is True:
        return "PASS", ""
    return "FAIL", "Playwright spec did not pass"


def _walk_playwright(suites: Iterable[dict], path: Path, evidence: dict[str, list[dict]]) -> None:
    for suite in suites:
        for spec in suite.get("specs", []):
            if not isinstance(spec, dict):
                continue
            title = str(spec.get("title", ""))
            tags = set(spec.get("tags", []))
            tags.update(f"{match.group(1)}-{match.group(2)}" for match in CLAIM_TAG.finditer(title))
            status, reason = _playwright_status(spec)
            for claim_id in tags:
                if isinstance(claim_id, str) and re.fullmatch(r"(?:S|PC)-\d+\.\d+", claim_id):
                    _record(evidence, claim_id, status, path.name, reason)
        _walk_playwright(suite.get("suites", []), path, evidence)


def load_evidence(directory: Path, modes: Iterable[str]) -> dict[str, dict[str, list[dict]]]:
    evidence = {mode: defaultdict(list) for mode in modes}
    if not directory.is_dir():
        return evidence
    for path in sorted(directory.glob("*.json")):
        if path.name.startswith("case-scoreboard"):
            continue
        if not (path.name.startswith(("service-", "service-auth-", "installer-case-", "platform-"))
                or path.name.startswith("studio-playwright-")):
            continue
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, UnicodeError, json.JSONDecodeError):
            continue
        if not isinstance(data, dict):
            continue
        if path.name.startswith("studio-playwright-"):
            mode = "secured" if "secured" in path.stem else "local"
            if mode in evidence:
                _walk_playwright(data.get("suites", []), path, evidence[mode])
            continue
        mode = data.get("case_run", {}).get("mode")
        if mode not in evidence:
            mode = next((candidate for candidate in MODES if candidate in path.stem), None)
        if mode in evidence:
            _journey_evidence(data, path, evidence[mode])
    return evidence


def _status(claims: list[str], observed: dict[str, list[dict]], applicable_modes: list[str]) -> tuple[str, list[str]]:
    if not claims:
        return "Unclaimed", []
    all_results = [result for claim_id in claims for mode in applicable_modes
                   for result in observed.get(mode, {}).get(claim_id, [])]
    if not all_results:
        return "Unclaimed", []
    passed_claims = {claim_id for claim_id in claims
                     if any(result["status"] == "PASS" for mode in applicable_modes
                            for result in observed.get(mode, {}).get(claim_id, []))}
    complete = all(any(result["status"] == "PASS" for result in observed.get(mode, {}).get(claim_id, []))
                   and all(result["status"] == "PASS" for result in observed.get(mode, {}).get(claim_id, []))
                   for mode in applicable_modes for claim_id in claims)
    if complete:
        return "Fully green", sorted(passed_claims, key=claim_sort_key)
    if passed_claims:
        return "Partial", sorted(passed_claims, key=claim_sort_key)
    if any(result["status"] == "FAIL" for result in all_results):
        return "Red", []
    if any(result["status"] == "BLOCKED" for result in all_results):
        return "Blocked", []
    return "Unclaimed", []


def build_scoreboard(directory: Path, catalog: Path, modes: Iterable[str]) -> dict:
    modes = list(modes)
    cases = load_cases(catalog)
    evidence = load_evidence(directory, modes)
    observed_modes = [mode for mode in MODES if any(
        path.name.startswith((f"service-{mode}", f"service-auth-{mode}", f"installer-case-{mode}",
                             f"platform-{mode}", f"studio-playwright-{mode}"))
        for path in directory.glob("*.json"))] if directory.is_dir() else []
    rows = []
    for case in cases:
        relevant = [mode for mode in modes if mode in case["modes"]]
        if not relevant:
            status, passing = "Not applicable", []
        else:
            status, passing = _status(case["claims"], evidence, relevant)
        rows.append({**case, "status": status, "passingClaims": passing,
                     "claimedCount": sum(bool(evidence.get(mode, {}).get(claim_id)) for mode in relevant for claim_id in case["claims"]),
                     "claimCount": len(case["claims"])})
    return {"schema": "kb.journey-case-scoreboard/v1", "modes": modes, "observedModes": observed_modes,
            "fullyGreen": sum(row["status"] == "Fully green" for row in rows), "cases": rows}


def render_markdown(scoreboard: dict) -> str:
    modes = ", ".join(scoreboard["modes"]) or "none"
    counts = defaultdict(int)
    for case in scoreboard["cases"]:
        counts[case["status"]] += 1
    applicable = len(scoreboard["cases"]) - counts["Not applicable"]
    lines = [f"# Product case scoreboard ({modes})", "",
             f"Fully green: **{scoreboard['fullyGreen']} / {applicable}** applicable cases.", "",
             "| Status | Cases |", "|---|---:|"]
    for label in ("Fully green", "Partial", "Red", "Unclaimed", "Blocked", "Not applicable"):
        if counts[label]:
            lines.append(f"| {label} | {counts[label]} |")
    green_cases = [case for case in scoreboard["cases"] if case["status"] == "Fully green"]
    lines.extend(["", "Fully green cases: " + (", ".join(case["id"] for case in green_cases) or "none") + "."])
    return "\n".join(lines) + "\n"


def write_scoreboards(directory: Path, catalog: Path) -> None:
    outputs = {f"case-scoreboard-{mode}": build_scoreboard(directory, catalog, [mode]) for mode in MODES}
    outputs["case-scoreboard"] = build_scoreboard(directory, catalog, MODES)
    for stem, scoreboard in outputs.items():
        (directory / f"{stem}.json").write_text(json.dumps(scoreboard, indent=2) + "\n", encoding="utf-8")
        (directory / f"{stem}.md").write_text(render_markdown(scoreboard), encoding="utf-8")


def _fully_green(data: dict, mode: str) -> set[str]:
    return {case["id"] for case in data.get("cases", []) if case.get("status") == "Fully green"
            and mode in case.get("modes", [])}


def render_delta(previous_dir: Path, current_dir: Path) -> str:
    lines = [f"Case scoreboard delta: {previous_dir.name} → {current_dir.name}"]
    for mode in MODES:
        previous_path = previous_dir / f"case-scoreboard-{mode}.json"
        current_path = current_dir / f"case-scoreboard-{mode}.json"
        if not previous_path.exists() or not current_path.exists():
            lines.extend(["", f"{mode}: missing scoreboard input ({previous_dir if not previous_path.exists() else current_dir})"])
            continue
        previous = json.loads(previous_path.read_text(encoding="utf-8"))
        current = json.loads(current_path.read_text(encoding="utf-8"))
        if mode not in previous.get("observedModes", []) or mode not in current.get("observedModes", []):
            continue
        before, after = _fully_green(previous, mode), _fully_green(current, mode)
        lines.extend(["", f"{mode}: fully green {len(before)} → {len(after)}",
                      "  Became fully green: " + (", ".join(sorted(after - before)) or "none"),
                      "  Regressed: " + (", ".join(sorted(before - after)) or "none")])
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    if len(args) == 2:
        print(render_delta(Path(args[0]), Path(args[1])))
        return 0
    if len(args) == 3 and args[0] == "--write":
        write_scoreboards(Path(args[1]), Path(args[2]))
        return 0
    print("usage: scoreboard.py <runA-dir> <runB-dir> | --write <reports-dir> <catalog-dir>", file=sys.stderr)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
