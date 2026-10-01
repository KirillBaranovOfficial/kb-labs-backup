#!/usr/bin/env python3
"""Aggregate service journey JSON and sync nightly status/failure issues."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

from invocations import open_http


def fingerprint(step: dict) -> str:
    key = "|".join(str(step.get(field, "")) for field in ("capability", "mode", "name"))
    return hashlib.sha256(key.encode()).hexdigest()[:16]


def load_results(directory: str | Path) -> list[dict]:
    root = Path(directory)
    if not root.is_dir():
        return [{"steps": [{"capability": "journey.runner", "mode": "unknown",
                            "name": "result directory", "status": "FAIL",
                            "message": f"journey result directory is missing: {root}"}]}]
    paths = sorted(path for path in root.glob("*.json")
                   if path.name.startswith("service-") or path.name in {"workflow.json", "installer-job-result.json"})
    if not paths:
        return [{"steps": [{"capability": "journey.runner", "mode": "unknown",
                            "name": "result files", "status": "FAIL",
                            "message": f"no journey result files found in {root}"}]}]
    results = []
    for path in paths:
        try:
            result = json.loads(path.read_text())
            if (not isinstance(result, dict) or not isinstance(result.get("steps"), list)
                    or any(not isinstance(step, dict) for step in result["steps"])):
                raise ValueError("expected a JSON object with an array of step objects")
            for step in result["steps"]:
                if any(not isinstance(step.get(field), str) or not step[field]
                       for field in ("capability", "mode", "name")):
                    raise ValueError("each step needs non-empty capability, mode, and name fields")
                if step.get("status") not in {"PASS", "FAIL", "BLOCKED"}:
                    raise ValueError("each step status must be PASS, FAIL, or BLOCKED")
                if not isinstance(step.get("message"), str):
                    raise ValueError("each step needs a message string")
                try:
                    float(step.get("duration_seconds", 0))
                except (TypeError, ValueError) as error:
                    raise ValueError("each step duration_seconds must be numeric") from error
            results.append(result)
        except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
            mode = path.stem.removeprefix("service-")
            results.append({"steps": [{"capability": "journey.runner", "mode": mode,
                                       "name": "result file parse",
                                       "status": "FAIL",
                                       "message": f"could not parse {path.name}: {error}"}]})
    return results


def aggregate(results: list[dict], previous: list[dict] | None = None,
              profiles: dict[str, set[str]] | None = None,
              journey_exits: dict[str, int] | None = None,
              case_coverage: list[dict] | None = None) -> dict:
    if not results:
        results = [{"steps": [{"capability": "journey.runner", "mode": "unknown",
                               "name": "result files", "status": "FAIL",
                               "message": "no journey results were provided to the aggregator"}]}]
    capabilities: dict[str, dict] = {}
    failures: dict[str, dict] = {}
    for result in results:
        for step in result.get("steps", []):
            key = f"{step.get('capability', 'unknown')}:{step.get('mode', 'unknown')}"
            item = capabilities.setdefault(key, {"capability": step.get("capability"), "mode": step.get("mode"), "status": "PASS", "duration_seconds": 0.0, "first_failure": None})
            item["duration_seconds"] += float(step.get("duration_seconds", 0))
            if step.get("status") == "FAIL":
                item["status"] = "FAIL"
                item["first_failure"] = item["first_failure"] or step
                issue = dict(step)
                issue["fingerprint"] = fingerprint(step)
                failures[issue["fingerprint"]] = issue
            elif step.get("status") == "BLOCKED":
                if item["status"] == "PASS":
                    item["status"] = "BLOCKED"
                item["first_failure"] = item["first_failure"] or step
    if not capabilities:
        step = {"capability": "journey.runner", "mode": "unknown", "name": "capability results",
                "status": "FAIL", "duration_seconds": 0.0,
                "message": "journey result files contained no capability steps"}
        capabilities["journey.runner:unknown"] = {"capability": "journey.runner", "mode": "unknown",
                                                   "status": "FAIL", "duration_seconds": 0.0,
                                                   "first_failure": step}
        issue = dict(step)
        issue["fingerprint"] = fingerprint(step)
        failures[issue["fingerprint"]] = issue
    for mode, declared in (profiles or {}).items():
        reported = {key.split(":", 1)[0] for key, item in capabilities.items()
                    if item.get("mode") == mode}
        for missing in sorted(declared - reported):
            key = f"{missing}:{mode}"
            detail = {"capability": missing, "mode": mode, "name": "required capability result",
                      "status": "BLOCKED", "duration_seconds": 0.0,
                      "message": f"profile {mode} declares {missing}, but the journey produced no result"}
            capabilities[key] = {"capability": missing, "mode": mode, "status": "BLOCKED",
                                 "duration_seconds": 0.0, "first_failure": detail}
    for mode, exit_code in (journey_exits or {}).items():
        if exit_code != 0:
            key = f"journey.runner:{mode}"
            detail = {"capability": "journey.runner", "mode": mode,
                      "name": "journey process exit", "status": "FAIL",
                      "duration_seconds": 0.0,
                      "message": f"{mode} journey process exited with status {exit_code}"}
            item = capabilities.setdefault(key, {"capability": "journey.runner", "mode": mode,
                                                  "status": "FAIL", "duration_seconds": 0.0,
                                                  "first_failure": detail})
            item["status"] = "FAIL"
            item["first_failure"] = item["first_failure"] or detail
            issue = dict(detail)
            issue["fingerprint"] = fingerprint(detail)
            failures[issue["fingerprint"]] = issue
    prior_caps = {f"{step.get('capability')}:{step.get('mode')}": step
                  for result in (previous or []) for step in result.get("steps", [])}
    for result in (previous or []):
        for item in result.get("capabilities", []):
            prior_caps[f"{item.get('capability')}:{item.get('mode')}"] = item
    delta = {"fixed": [], "new": [], "persisting": []}
    for key, capability in capabilities.items():
        old = prior_caps.get(key, {}).get("status")
        if old == "FAIL" and capability["status"] == "PASS":
            delta["fixed"].append(key)
        elif capability["status"] == "FAIL":
            delta["persisting" if old == "FAIL" else "new"].append(key)
    coverage_failed = any(item.get("releasePassed") is not True for item in (case_coverage or []))
    return {"schema": "kb.nightly-report/v1", "generated_at": datetime.now(timezone.utc).isoformat(),
            "status": "FAIL" if failures or coverage_failed else "BLOCKED" if any(x["status"] == "BLOCKED" for x in capabilities.values()) else "PASS",
            "capabilities": sorted(capabilities.values(), key=lambda x: (x["capability"], x["mode"])),
            "failures": list(failures.values()), "delta": delta,
            "case_coverage": case_coverage or [],
            "duration_seconds": round(sum(x["duration_seconds"] for x in capabilities.values()), 3)}


class GitHubClient:
    def __init__(self, repository: str, token: str):
        self.repository = repository
        self.token = token
        self.base = f"https://api.github.com/repos/{repository}/issues"

    def _request(self, method: str, path: str = "", payload: dict | None = None):
        data = json.dumps(payload).encode() if payload is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method,
                                         headers={"Authorization": f"Bearer {self.token}",
                                                  "Accept": "application/vnd.github+json",
                                                  "X-GitHub-Api-Version": "2022-11-28",
                                                  "Content-Type": "application/json"})
        try:
            status, raw, _ = open_http(urllib.request.urlopen, request, timeout=20,
                                      env={"GH_TOKEN": self.token})
        except urllib.error.URLError as error:
            raise RuntimeError(f"GitHub issue API request failed: {error}") from error
        if status < 200 or status >= 300:
            raise RuntimeError(f"GitHub issue API returned HTTP {status}: {raw.decode(errors='replace')[:300]}")
        return json.loads(raw) if raw else {}

    def list_open(self):
        page = 1
        found = []
        while True:
            items = self._request("GET", f"?state=all&per_page=100&page={page}")
            found.extend(items)
            if len(items) < 100:
                return found
            page += 1

    def create(self, title: str, body: str, labels: list[str]):
        return self._request("POST", payload={"title": title, "body": body, "labels": labels})

    def update(self, number: int, **fields):
        return self._request("PATCH", f"/{number}", fields)


def sync_issues(client, report: dict, pinned_number: int, run_url: str = "") -> dict:
    issues = client.list_open()
    by_marker = {label_name: issue for issue in issues for label in issue.get("labels", [])
                 for label_name in [label if isinstance(label, str) else label.get("name", "")]
                 if label_name.startswith("nightly-fingerprint:")}
    status_body = render_summary(report, run_url)
    status_issue = next((issue for issue in issues if issue.get("number") == pinned_number), None) if pinned_number else None
    status_issue = status_issue or next((issue for issue in issues if issue.get("title") == "Nightly status" and "nightly-status" in [label if isinstance(label, str) else label.get("name") for label in issue.get("labels", [])]), None)
    if status_issue:
        client.update(status_issue["number"], title="Nightly status", body=status_body, state="open")
    else:
        client.create("Nightly status", status_body, ["nightly", "nightly-status"])
    active = {item["fingerprint"] for item in report.get("failures", [])}
    changed = {"created": [], "updated": [], "closed": []}
    for failure in report.get("failures", []):
        marker = f"nightly-fingerprint:{failure['fingerprint']}"
        issue = by_marker.get(marker)
        title = f"Nightly failure: {failure['capability']} ({failure['mode']})"
        body = render_failure(failure, run_url)
        if issue:
            client.update(issue["number"], title=title, body=body, state="open")
            changed["updated"].append(failure["fingerprint"])
        else:
            client.create(title, body, ["nightly", "bug", marker])
            changed["created"].append(failure["fingerprint"])
    for marker, issue in by_marker.items():
        key = marker.removeprefix("nightly-fingerprint:")
        if issue.get("state") == "open" and key not in active:
            client.update(issue["number"], state="closed")
            changed["closed"].append(key)
    return changed


def render_summary(report: dict, run_url: str = "") -> str:
    lines = [f"Nightly capability report — {report.get('generated_at', '')}", "",
             "| Capability | Mode | Status | Duration | First broken step |", "|---|---|---|---:|---|"]
    for item in report.get("capabilities", []):
        failure = item.get("first_failure") or {}
        lines.append(f"| {item['capability']} | {item['mode']} | {item['status']} | {item['duration_seconds']:.1f}s | {failure.get('name', '')}: {failure.get('message', '')} |")
    lines.extend(["", f"Overall: **{report.get('status', 'UNKNOWN')}**; duration {report.get('duration_seconds', 0):.1f}s."])
    for coverage in report.get("case_coverage", []):
        cases = coverage.get("cases", [])
        incomplete = sum(1 for item in cases if item.get("release") and item.get("status") == "active"
                         and item.get("statusResult") not in {"PASS", "NOT REQUIRED"})
        lines.append(f"Case coverage ({coverage.get('mode', 'unknown')}): **{'PASS' if coverage.get('releasePassed') else 'FAIL'}**; {incomplete} active release case(s) incomplete.")
    lines.append(f"Delta: {json.dumps(report.get('delta', {}), sort_keys=True)}")
    if run_url:
        lines.append(f"Run: {run_url}")
    return "\n".join(lines)


def render_failure(failure: dict, run_url: str) -> str:
    lines = ["Automated nightly service journey failure.", "", f"- Fingerprint: `{failure['fingerprint']}`",
             f"- Capability: `{failure['capability']}`", f"- Mode: `{failure['mode']}`",
             f"- Step: `{failure['name']}`", f"- Message: {failure['message']}"]
    if run_url:
        lines.extend(["", f"- Run: {run_url}"])
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--results", required=True)
    parser.add_argument("--previous")
    parser.add_argument("--profile", action="append", choices=["local", "secured"], default=[])
    parser.add_argument("--journey-exit", action="append", default=[], metavar="MODE=CODE")
    parser.add_argument("--case-coverage", action="append", default=[], metavar="FILE")
    parser.add_argument("--json", default="nightly-report.json")
    parser.add_argument("--summary", default="nightly-report.md")
    parser.add_argument("--sync-issues", action="store_true")
    args = parser.parse_args()
    from service_runner import declared_capabilities
    exits = {}
    for value in args.journey_exit:
        mode, separator, code = value.partition("=")
        if not separator or mode not in {"local", "secured"}:
            parser.error(f"invalid --journey-exit {value!r}; expected local=CODE or secured=CODE")
        try:
            exits[mode] = int(code)
        except ValueError:
            parser.error(f"invalid --journey-exit code in {value!r}")
    profiles = {mode: declared_capabilities(mode) for mode in args.profile}
    case_coverage = []
    for path in args.case_coverage:
        try:
            case_coverage.append(json.loads(Path(path).read_text()))
        except (OSError, UnicodeError, json.JSONDecodeError) as error:
            case_coverage.append({"schema": "kb.qa-case-coverage/v1", "mode": "unknown",
                                  "releasePassed": False, "cases": [],
                                  "diagnostics": [f"could not read case coverage {path}: {error}"]})
    report = aggregate(load_results(args.results), load_results(args.previous) if args.previous else [],
                       profiles, exits, case_coverage)
    Path(args.json).parent.mkdir(parents=True, exist_ok=True)
    Path(args.summary).parent.mkdir(parents=True, exist_ok=True)
    Path(args.json).write_text(json.dumps(report, indent=2) + "\n")
    Path(args.summary).write_text(render_summary(report, os.getenv("GITHUB_RUN_URL", "")) + "\n")
    if args.sync_issues:
        repository = os.environ["GITHUB_REPOSITORY"]
        token = os.environ["GITHUB_TOKEN"]
        pinned = int(os.environ["NIGHTLY_STATUS_ISSUE"])
        changes = sync_issues(GitHubClient(repository, token), report, pinned, os.getenv("GITHUB_RUN_URL", ""))
        print(json.dumps(changes, sort_keys=True))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
