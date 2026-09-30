---
id: S-040
title: Human — complete the V2 install wizard
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer uses the wizard to choose a supported install and receives a request to inspect before applying.

## Prerequisites

- [ ] A sealed release index and isolated roots.

## Steps

| # | Action | Expected |
|---|---|---|
| S-040.1 | Run `kb-create wizard --index release-index.json --request-platform-root "$PLATFORM_ROOT"` | Wizard presents compatible scenarios and prompts. |
| S-040.2 | Choose a scenario and save the emitted request | Request JSON contains the chosen scenario and roots. |
| S-040.3 | Run `kb-create plan --index release-index.json --input request.json --json` | Plan returns `ok: true` and the selected graph. |
| S-040.4 | Apply the reviewed request | Output contains a receipt. |
