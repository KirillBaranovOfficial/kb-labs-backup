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
| S-039.1 | Run `kb-create apply --json --index release-index.json --request-platform-root "$PLATFORM_ROOT" --service-profile default --scenario commit` | Exits 0; JSON has `ok: true`, `operation: "apply"`, a `receipt` whose `plan.request.platformRoot` is the isolated root, and non-empty `logPath`. |
| S-039.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true`, `installed: true`, `host.state: "running"`, and `installation.receiptId` equal to the receipt ID in `$PLATFORM_ROOT/.kb/v2/receipt.json`. |
| S-039.3 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | A healthy install exits 0 with `ok: true`; if an installed manifest requirement is missing, it exits 1 and the corresponding finding includes its `owner`, `path`, and `hint`. |
