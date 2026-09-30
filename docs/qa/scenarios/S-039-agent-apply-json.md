---
id: S-039
title: Agent — apply a V2 request as JSON
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [agent, cli]
---

## Goal

An automation agent installs the exact platform and project selection from a request without interactive prompts.

## Prerequisites

- [ ] A sealed release index and valid request JSON; isolated platform and project roots.

## Steps

| # | Action | Expected |
|---|---|---|
| S-039.1 | Run `kb-create apply --index release-index.json --input request.json --json` | JSON has `ok: true`, receipt, and log path. |
| S-039.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | Installed components match the receipt. |
| S-039.3 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Returns `ok: true` or manifest findings with owner, code, and hint. |
