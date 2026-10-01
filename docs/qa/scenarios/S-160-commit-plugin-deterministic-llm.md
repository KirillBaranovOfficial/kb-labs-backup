---
id: S-160
title: Commit plugin — Generate a commit with the deterministic journey LLM
persona: solo-developer
area: plugins
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer uses the marketplace-installed commit plugin to generate and preview a commit plan with the deterministic LLM fixture bound through platform configuration; the missing-adapter path gives an actionable typed error.

## Prerequisites

- [ ] The `@kb-labs/commit` plugin is installed and enabled in the project.
- [ ] A Git repository contains staged changes.
- [ ] The deterministic journey LLM fixture adapter is installed and bound through platform `platform.adapters.llm` configuration.

## Steps

| # | Action | Expected |
|---|---|---|
| S-160.1 | Run `kb commit commit --dry-run` with the deterministic fixture adapter configured | JSON output contains a generated plan with `plan.metadata.llmUsed: true`, and no Git commit is created. |
| S-160.2 | Remove the platform LLM adapter binding and run `kb commit commit --dry-run` again | The command returns a typed error naming the missing `llm` capability and includes the `kb adapter bind llm <installed-adapter-package>` next-command hint; it does not silently use a built-in LLM. |
| S-160.3 | Restore the platform fixture binding and run `kb commit commit` | A commit is created; `git log -1 --format=%s` prints the conventional commit subject returned by the configured fixture. |
