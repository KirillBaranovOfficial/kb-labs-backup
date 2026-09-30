# @kb-labs/workflow

> Workflow orchestration — run, schedule, and monitor multi-step jobs.

![license](https://img.shields.io/badge/license-MIT-green)
![platform](https://img.shields.io/badge/kb--labs-%3E%3D0.1.0-orange)
![tags](https://img.shields.io/badge/tags-workflow%20%7C%20jobs%20%7C%20cron%20%7C%20orchestration-lightgrey)

---

## Overview

Workflow executes multi-step jobs, manages a job queue, and runs cron
schedules in a project runtime managed by the KB Labs host. The CLI and Studio
use the project-aware host API, so local and secured projects share the same
workflow routes.

---

## Features

- Job queue with priority and retry support
- Cron scheduling — define recurring workflows with cron expressions
- Approval gates — pause a run and wait for human or programmatic sign-off
- Live run and log updates over server-sent events (SSE)
- Re-run failed runs from the point of failure
- Cancellation at job or run level
- Studio UI — dashboard, run history, definition browser, job queue, cron list

---

## Requirements

**KB Labs platform** `>= 0.1.0`

**Services**

The workflow CLI resolves the registered project from the current directory
and sends requests through the host. Local mode uses the loopback gateway
without login; secured mode uses the signed-in gateway session.

---

## Installation

```bash
pnpm kb marketplace install @kb-labs/workflow-entry
```

The host starts project runtimes on demand. A project with enabled scheduled
workflows starts when the host starts and stays warm while those schedules are
enabled.

---

## Commands

### Daemon

```bash
kb workflow health                     # check daemon is up
kb workflow metrics                    # queue stats: running, queued, completed, failed
```

### Runs

```bash
kb workflow runs list                  # list all workflow runs
kb workflow runs list --status=failed
kb workflow runs view <runId>          # full run details: jobs, steps, inputs, errors
kb workflow runs view <runId> --log-failed   # only failed step logs (fastest root cause)
kb workflow runs watch <runId>         # stream run events live
kb workflow runs rerun <runId>         # resubmit with same inputs
```

### Running workflows

```bash
kb workflow run --workflow-id=release-manager/create-release
kb workflow run --workflow-id=my-workflow --isolation=strict
```

### Jobs

```bash
kb workflow status --job-id=abc123     # job state + timing
kb workflow logs   --job-id=abc123     # execution logs
kb workflow logs   --job-id=abc123 --follow  # stream logs live
kb workflow list                       # active jobs
kb workflow list   --status=running
kb workflow list   --type=cron
kb workflow job-run --handler=mind:rag-query --input='{"text":"test"}' --wait
```

**Full command reference**

| Command | Description |
|---------|-------------|
| `kb workflow health` | Daemon health check |
| `kb workflow metrics` | Queue statistics |
| `kb workflow status` | Job state + timing |
| `kb workflow logs` | Job execution logs |
| `kb workflow list` | List active jobs |
| `kb workflow job-run` | Submit a raw job |
| `kb workflow run` | Run a workflow by ID |
| `kb workflow runs list` | List all runs |
| `kb workflow runs view` | Full run details |
| `kb workflow runs watch` | Stream run events live |
| `kb workflow runs rerun` | Re-run a workflow |

---

## REST API

Workflow requests use the selected project's host route:
`/api/v1/projects/{projectId}/workflow/...`. The host applies the project's
authentication policy and forwards the request to its runtime. Use the CLI
commands above or the SDK's project workflow API instead of calling a daemon
port directly.

---

## Studio

Adds a **Workflows** section to KB Labs Studio (sidebar order 10).

| Page | Route | Description |
|------|-------|-------------|
| Dashboard | `/p/workflows` | Queue metrics and recent activity |
| Runs | `/p/workflows/runs` | Run history with status filter |
| Run Detail | `/p/workflows/runs/:runId` | Steps, logs, approval gates |
| Definitions | `/p/workflows/definitions` | Registered workflow definitions |
| Definition Detail | `/p/workflows/definitions/:workflowId` | Definition + run history |
| Jobs | `/p/workflows/jobs` | Job queue browser |
| Crons | `/p/workflows/crons` | Scheduled cron jobs |

---

## Permissions

| Category | Scope | Reason |
|----------|-------|--------|
| Network | Host gateway origin | Project-scoped workflow API calls |
| Quotas | 30 sec timeout, 128 MB RAM | Lightweight HTTP client |

---

## Changelog

### 1.0.0

- Initial release: health, metrics, status, logs, list, job-run, run, runs-list/view/watch/rerun commands
- Project-scoped REST API with SSE run updates
- Studio: Dashboard, Runs, Definitions, Jobs, Crons

---

## License

MIT
