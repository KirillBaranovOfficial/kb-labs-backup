---
id: S-243
title: Plugin execution — subprocess lifecycle and cancellation
persona: solo-developer
area: plugin-execution
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Run a plugin command in a child process, stop it on cancellation or timeout,
and receive a stable exit result.

## Prerequisites

- [ ] `platform.execution.mode` is `subprocess`.
- [ ] A fixture command returns a known value, fails with a catalogued error, runs long enough to cancel, and can spawn a child process.

## Steps

| # | Action | Expected |
|---|---|---|
| S-243.1 | Run `kb fixture execution healthy --json` | The CLI prints its JSON success result and exits with code `0`; the child and its IPC server close. |
| S-243.2 | Run `kb fixture execution typed-failure --json` | The CLI prints the catalogued error envelope and exits with code `1`. |
| S-243.3 | Run `kb fixture execution hold --json` and press Ctrl+C | The CLI reports the catalogued cancellation code, exits nonzero, and the child process and descendants exit. |
| S-243.4 | Start workflow `plugin-execution-cancel` and run `kb workflow runs cancel RUN_ID --json` | The step is cancelled with the same catalogued cancellation code as CLI cancellation; the subprocess and descendants exit. |
| S-243.5 | Run the fixture command past its configured timeout | The caller receives the catalogued timeout code; the subprocess, descendants, and per-execution IPC server close. |

## Decision notes

- D45: “`QUEUE_FULL`, `ACQUIRE_TIMEOUT`, `WORKER_CRASHED` and container-unavailable are catalogued codes with hints. A known execution state never surfaces as `KB_RUNTIME_UNEXPECTED`.” The same rule applies to subprocess cancellation and timeout outcomes.

## Source

`core/plugin-execution-factory/src/backends/subprocess.ts` owns per-call IPC,
timeout, signal forwarding, and cleanup. `cli/runtime/src/v3/execute-command.ts`
returns exit code `1` for failed execution. Workflow cancellation passes a
signal through `plugins/workflow/runtime/src/runners/sandbox-runner.ts`.
