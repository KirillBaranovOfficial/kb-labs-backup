---
id: S-014
title: Commit plugin — Generate a commit with a real LLM
persona: solo-developer
area: plugins
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer uses an explicitly configured real LLM provider to draft and apply a commit message for staged changes.

## Prerequisites

- [ ] The `@kb-labs/commit` plugin is installed and enabled in the project.
- [ ] A real LLM adapter is installed, bound at platform scope, and configured with valid platform credentials.
- [ ] A Git repository contains staged changes.

## Steps

| # | Action | Expected |
|---|---|---|
| S-014.1 | Run `kb commit commit --dry-run --json` | JSON output contains a generated plan with `plan.metadata.llmUsed: true` and `applied: false`; no commit is created and the index and working tree are unchanged. |
| S-014.2 | Inspect the generation result/diagnostics | The selected real LLM provider is identified; the result does not claim heuristic generation. |
| S-014.3 | Run `kb commit commit --json` | JSON output reports `applied: true`; a commit is created and `git log -1 --format=%s` prints its generated subject. |

A real provider and credentials are not guaranteed in the platform release contour, so this case is not a release gate.
