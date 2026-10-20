---
id: S-015
title: AI Code Review — Review repository changes
persona: solo-developer
area: plugins
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer uses the marketplace-installed review plugin to inspect repository changes and receive actionable findings.

## Prerequisites

- [ ] The `@kb-labs/review` plugin is available from the marketplace and installed in the project.
- [ ] A Git repository contains a changed source file.

## Steps

| # | Action | Expected |
|---|---|---|
| S-015.1 | Run `kb review run --mode=heuristic` | The command completes and identifies the reviewed change set. |
| S-015.2 | Inspect the findings | Each finding includes a file path and line reference, or the output explicitly reports that no findings were produced. |
| S-015.3 | Run `kb review run --json` | Standard output is valid JSON containing the review result. |
| S-015.4 | Run `kb review run --mode=llm` without an LLM adapter bound | If the command requires an LLM, it returns the typed missing-capability error and `kb adapter bind` guidance; it does not silently switch to an undocumented LLM default. |
