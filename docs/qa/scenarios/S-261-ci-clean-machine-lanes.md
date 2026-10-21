---
id: S-261
title: Local gates prove Go modules and GitHub CI proves native macOS installation
persona: platform-developer
area: platform-core
priority: P1
status: planned
release: false
modes: [local]
surfaces: [cli]
---

## Goal

As a KB Labs maintainer, I can see clean-machine CI evidence for every registered Go module and for the local installer on macOS, so platform regressions are caught on both supported operating systems.

## Steps

| # | Action | Expected |
|---|---|---|
| S-261.1 | Run `kb-devkit check` in the workspace | The local suite check reads the devkit `go-binary` registry and runs every registered module's non-e2e Go tests with a fresh `HOME`, cleared `KB_PLATFORM_ROOT`, `KB_HOME`, `KB_NET_OFFSET` and `GOFLAGS`, and two real-world temporary-directory variants: a short child of `/tmp` and one short child of the platform default temp directory. Before testing, it derives the host's Unix socket pathname capacity and refuses a variant that cannot fit the TSX IPC socket path, with a named diagnostic and an exact rerun command that sets `TMPDIR=/tmp` and preserves the registered check name or direct entry arguments. Empty registry discovery or a discovery error also fails the gate. A failing check's JSON output retains its issue detail and the exact `kb-devkit check --only <name>` rerun command. |
| S-261.2 | Open a pull request or push to `master` that triggers the installer journey workflow and inspect its native macOS smoke | The reusable workflow builds one candidate from that change and the native `macos-latest` job downloads it, runs `kb-create install` into a temporary platform root under a fresh `HOME`, then passes `kb --version`, `kb doctor --json`, `kb project add <worktree> --json`, and `kb qa cases list --json`. The Docker container matrix remains Linux-only. |
| S-261.3 | Inspect a completed nightly user journey run | Its computed scoreboard is published to the single configured GitHub issue, which is updated on later runs; the run summary and artifact retain the same scoreboard. Blocking roots are grouped by the structured error envelope captured for their failing invocations (`code` + `area`, with `stage` when present); each group lists its member root step IDs and is ordered by total unblocked claims. A failing step whose invocation has no structured envelope (including assertion failures on a successful response and timeouts) appears in a visible “no envelope” group with its step ID. An invocation row that cannot be parsed is reported by line number; JSONL records are framed only by LF, so U+0085, U+2028, and U+001C–U+001E inside a JSON string remain part of one valid row. A step that cannot run because a prerequisite failed is recorded as BLOCKED with that prerequisite's step ID, and the pyramid groups it under its failed producer. |

## Evidence

Registry and environment tests cover S-261.1 through the local `go-module-suite` workspace check, which reads the `go-binary` registry; the native workflow contract test covers S-261.2; the journey scoreboard test uses the minimal x104-j86 fixture to cover structured root grouping for S-261.3, and nightly issue-sync tests cover publication. The external GitHub macOS smoke and nightly issue update are release evidence the orchestrator must observe on the backup repository PR and nightly run. This case remains non-release until those external outcomes are recorded.

## Round 2

The owner moved the Go module suite to local gates and specified a native macOS install from the reusable workflow's candidate artifact. The macOS container journey row was removed because GitHub macOS runners do not provide Docker.

## Round 3

The native macOS job delegates its setup to repository scripts shared with the container journey. A failing kb-devkit external check keeps the issue detail and rerun command in its JSON output.

## Round 5

The two TMPDIR variants use short roots at their real system locations. The suite checks that each root can fit a TSX Unix socket path before starting any Go package tests and reports `GO_SUITE_TMPDIR_SOCKET_PATH_TOO_LONG` when it cannot.

## Round 6

When a TMPDIR variant cannot fit the measured TSX socket path, its diagnostic includes a copyable command that reruns the same registered check or direct suite invocation with `TMPDIR=/tmp`.
