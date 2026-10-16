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
| S-261.1 | Run `kb-devkit check` in the workspace | The local suite check reads the devkit `go-binary` registry and runs every registered module's non-e2e Go tests with a fresh `HOME`, cleared `KB_PLATFORM_ROOT`, `KB_HOME`, `KB_NET_OFFSET` and `GOFLAGS`, and both configured temporary-directory variants. Empty registry discovery or a discovery error fails the gate. A failing check's JSON output retains its issue detail and the exact `kb-devkit check --only <name>` rerun command. |
| S-261.2 | Open a pull request or push to `master` that triggers the installer journey workflow and inspect its native macOS smoke | The reusable workflow builds one candidate from that change and the native `macos-latest` job downloads it, runs `kb-create install` into a temporary platform root under a fresh `HOME`, then passes `kb --version`, `kb doctor --json`, `kb project add <worktree> --json`, and `kb qa cases list --json`. The Docker container matrix remains Linux-only. |
| S-261.3 | Inspect a completed nightly user journey run | Its computed scoreboard is published to the single configured GitHub issue, which is updated on later runs; the run summary and artifact retain the same scoreboard. |

## Evidence

Registry and environment tests cover S-261.1 through the local `go-module-suite` workspace check, which reads the `go-binary` registry; the native workflow contract test covers S-261.2; nightly issue-sync tests cover S-261.3. The external GitHub macOS smoke and nightly issue update are release evidence the orchestrator must observe on the backup repository PR and nightly run. This case remains non-release until those external outcomes are recorded.

## Round 2

The owner moved the Go module suite to local gates and specified a native macOS install from the reusable workflow's candidate artifact. The macOS container journey row was removed because GitHub macOS runners do not provide Docker.

## Round 3

The native macOS job delegates its setup to repository scripts shared with the container journey. A failing kb-devkit external check keeps the issue detail and rerun command in its JSON output.
