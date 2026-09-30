---
id: S-033
title: Platform team — Resolve and validate a composition before shipping
persona: platform-team
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A platform team resolves a V2 release index and request before applying an installation and sees a specific diagnostic for an invalid package selection.

## Prerequisites

- [ ] A V2 release index JSON and a request JSON for a disposable composition.

## Steps

| # | Action | Expected |
|---|---|---|
| S-033.1 | Run `kb-create plan --index release-index.json --input request.json --json` | JSON output has `ok: true` and describes the resolved plan; no installation is applied. |
| S-033.2 | Change the request to select an unknown slot or package and repeat the plan command | The command exits non-zero and reports a structured resolution error naming the unavailable selection. |
| S-033.3 | Correct the request and rerun the plan command | JSON output has `ok: true` and includes the resolved package graph and selected artifacts. |
