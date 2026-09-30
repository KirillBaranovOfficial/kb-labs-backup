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


def fingerprint(step: dict) -> str:
    key = "|".join(str(step.get(field, "")) for field in ("capability", "mode", "name"))
    return hashlib.sha256(key.encode()).hexdigest()[:16]


def load_results(directory: str | Path) -> list[dict]:
    return [json.loads(path.read_text()) for path in sorted(Path(directory).glob("*.json"))]


def aggregate(results: list[dict], previous: list[dict] | None = None) -> dict:
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
            elif step.get("status") == "BLOCKED" and item["status"] == "PASS":
                item["status"] = "BLOCKED"
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
    return {"schema": "kb.nightly-report/v1", "generated_at": datetime.now(timezone.utc).isoformat(),
            "status": "FAIL" if failures else "BLOCKED" if any(x["status"] == "BLOCKED" for x in capabilities.values()) else "PASS",
            "capabilities": sorted(capabilities.values(), key=lambda x: (x["capability"], x["mode"])),
            "failures": list(failures.values()), "delta": delta,
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
            with urllib.request.urlopen(request, timeout=20) as response:
                raw = response.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"GitHub issue API returned HTTP {error.code}: {error.read().decode(errors='replace')[:300]}") from error

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
    parser.add_argument("--json", default="nightly-report.json")
    parser.add_argument("--summary", default="nightly-report.md")
    parser.add_argument("--sync-issues", action="store_true")
    args = parser.parse_args()
    report = aggregate(load_results(args.results), load_results(args.previous) if args.previous else [])
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
