---
id: S-014
title: S-014 — Generate and apply an AI commit with a real LLM
persona: solo-developer
area: plugins
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer uses the commit plugin with configured gateway credentials to draft and apply a commit message from staged changes.

## Prerequisites

- [ ] Git repository with staged changes and a configured LLM adapter or secured gateway.

## Steps

| # | Action | Expected |
|---|---|---|
| S-014.1 | Run `kb commit commit --dry-run` | A proposed message is printed and no commit is created. |
| S-014.2 | Confirm the output identifies the configured LLM path rather than heuristic fallback | Output makes the selected generation path observable. |
| S-014.3 | Run `kb commit commit --yes` | A commit is created with a message describing the staged changes. |
