---
id: S-039
title: Agent — Apply a V2 request as JSON
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [agent, cli]
---

## Goal

An automation agent installs the exact platform and project selection from a request without interactive prompts, and receives actionable machine-readable failures for invalid requests.

## Prerequisites

- [ ] Sealed release index and isolated platform/project roots.
- [ ] Valid request JSON for the success path; a separate copy can be edited for negative paths.

## Steps

| # | Action | Expected |
|---|---|---|
| S-039.1 | Run `kb-create apply --index release-index.json --input request.json --json` | Standard output is valid JSON with `ok: true`, a receipt identifying installed components, and a log path. |
| S-039.2 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | JSON reports the installed versions and selections recorded in the apply receipt. |
| S-039.3 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | JSON reports `ok: true`, or findings with `owner`, `code`, and an actionable `hint`; no stack trace is emitted. |
| S-039.4 | Copy `request.json`, set its required `platformRoot` field to an empty string, then run `kb-create apply --index release-index.json --input invalid-request.json --json` | Process exits non-zero and emits a JSON error envelope with stable `code`, `message`, and `hint` naming `platformRoot` and the corrective action; the installed platform state is unchanged. |
