---
id: S-197
title: Projects — Commit in one repository and review another
persona: solo-developer
area: plugins
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Use one installed platform to commit work in Project A and review changes in
Project B without crossing repository, configuration, or result boundaries.

## Prerequisites

- [ ] Project A and Project B are registered, initialized Git repositories.
- [ ] Commit is installed and configured for Project A; Review is installed and configured for Project B.
- [ ] Each project has its own staged or changed fixture and the required LLM settings.

## Steps

| # | Action | Expected |
|---|---|---|
| S-197.1 | From Project A, run `kb commit commit --dry-run --json` | The generated plan describes only Project A's staged change and uses Project A's effective LLM configuration; no commit is created in either repository. |
| S-197.2 | From Project A, run `kb commit commit`, then inspect `git -C "$PROJECT_A" log -1` and Project A's Commit history | The new commit exists only in Project A, and the plan/result is recorded in Project A's history and analytics. |
| S-197.3 | From Project B, run `kb review run --mode=heuristic --json` | The review contains only Project B's changed files and uses Project B as its repository root; no Project A file or configuration appears. |
| S-197.4 | Inspect Project B's Review history and analytics, then inspect Project A's history and analytics | The Review result appears under Project B; Project A retains its Commit result and has no record of Project B's Review. |
| S-197.5 | From Project A, run the Review command and from Project B, run the Commit command | Each command reports a project-scoped unavailable-plugin or configuration error with the next install/configuration command; neither command silently uses the other project's plugin or credentials. |

## Dependencies

- S-014 covers Commit generation and application.
- S-015 covers Review results.
- This case joins both flows across two registered repositories and requires
  project-specific history and analytics attribution.
