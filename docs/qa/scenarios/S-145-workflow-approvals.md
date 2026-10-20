---
id: S-145
title: Workflow — Approve or reject a pending step
persona: team-admin
area: workflow
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An authorized user explicitly approves or rejects a workflow approval step and can see the workflow continue or stop according to that decision.

## Prerequisites

- [ ] Host running and workflow commands available in a registered project.
- [ ] Workflow `approval-demo` has a `builtin:approval` step with a controlled downstream step.
- [ ] In secured mode, an authenticated user is authorized to resolve workflow approvals.

## Steps

| # | Action | Expected |
|---|---|---|
| S-145.1 | Start `approval-demo` with `kb workflow run --workflow-id=approval-demo`; poll `kb workflow runs view <run-id> --json` for up to 30 seconds | Run is waiting for approval; its approval step status is `waiting_approval`. |
| S-145.2 | Run `kb workflow runs approve <run-id> --action=approve --comment="Reviewed" --json` | JSON reports the run ID, approval step ID, action `approve`, and `resolved: true`; run resumes and its downstream step executes. |
| S-145.3 | Start a second `approval-demo` run and run `kb workflow runs approve <run-id> --action=reject --comment="Needs changes" --json` while it is waiting | JSON reports action `reject` and `resolved: true`; the rejection output includes the comment and the downstream approval gate follows its rejected path. |
| S-145.4 | Run `kb workflow runs approve <completed-run-id> --json` | CLI returns non-zero with a conflict response that the step is not waiting for approval; completed run state remains unchanged. |
| S-145.5 | Run `kb workflow runs approve <run-id> --action=maybe --json` | CLI returns non-zero and identifies the allowed actions `approve` and `reject`; no approval decision is written. |
