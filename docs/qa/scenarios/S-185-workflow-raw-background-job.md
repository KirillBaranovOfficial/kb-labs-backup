---
id: S-185
title: Workflow — submit and inspect a raw background job
persona: solo-developer
area: workflow
priority: P2
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Submit a registered job handler directly as a background task and inspect its
execution result by job ID.

## Prerequisites

- [ ] The workflow daemon is running.
- [ ] A registered handler `fixture:background-task` returns a deterministic result.

## Steps

| # | Action | Expected |
|---|---|---|
| S-185.1 | Run `kb workflow job run --handler=fixture:background-task --input='{"value":"hello"}' --json` | JSON reports a submitted job with an ID and initial status; the handler receives the parsed input. |
| S-185.2 | Run `kb workflow status --job-id=JOB_ID --json` using the returned ID | JSON reports the job ID, status, handler result or error, and start/finish details once the task completes. |
| S-185.3 | Run the submission command with `--wait` | The command waits for a terminal job status and reports completion or failure for that job. |

## Pass criteria

The job command and its status inspection exist, but the status command is
explicitly deprecated in favor of `kb workflow runs status <runId>`, which
describes workflow runs rather than raw jobs. This case records the current
registered behavior for owner ranking; direct raw-job support is outside the
release contour until the owner decides whether it remains a user API.
