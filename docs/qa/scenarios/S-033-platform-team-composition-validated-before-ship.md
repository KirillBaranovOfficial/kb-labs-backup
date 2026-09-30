---
id: S-033
title: S-033 — Validate a platform composition before shipping
persona: platform-team
area: platform-core
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A platform team resolves a sealed release index and request before publication and catches invalid packages or slots.

## Prerequisites

- [ ] V2 release index and request manifest for a disposable composition.

## Steps

| # | Action | Expected |
|---|---|---|
| S-033.1 | Run `kb-create plan --index release-index.json --input request.json --json` | JSON describes the resolved plan without applying it. |
| S-033.2 | Use a request with an unknown slot or unavailable package | Plan fails with a structured error naming the invalid slot/package. |
| S-033.3 | Correct the request and run the same plan command | Plan resolves the package graph and selected artifacts. |
