---
id: PC-001
title: Clean V2 install from released artifacts
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Verify the published launcher can turn the released, sealed V2 index into a
verified platform installation. This is not a source-tree or cache-assisted
test.

## Environment

- [ ] Released `kb-create` binary in `PATH`
- [ ] Node and pnpm supported by the release
- [ ] Empty `PLATFORM_ROOT` and no pre-existing `node_modules` below it
- [ ] Downloaded `release-index.json` from the candidate or stable release
- [ ] `kb-dev` supplied by the release (or an explicit test double only in CI)

## Steps

| # | Action | Expected |
|---|---|---|
| PC-001.1 | `kb-create --version` | Prints launcher version without a stack trace |
| PC-001.2 | `kb-create wizard --index release-index.json --request-platform-root "$PLATFORM_ROOT" > request.json` | Wizard offers only compatible selections and emits one V2 request |
| PC-001.3 | `kb-create plan --index release-index.json --input request.json` | JSON response has `ok: true`, exact artifacts and a service graph; nothing is written under the root |
| PC-001.4 | `kb-create apply --index release-index.json --input request.json` | One verified application; output includes `receipt` and `logPath` |
| PC-001.5 | Inspect `$PLATFORM_ROOT/.kb/kb.config.jsonc` and `$PLATFORM_ROOT/.kb/devservices.yaml` | Both exist, parse, and match the selected profile and service graph |
| PC-001.6 | `kb-create doctor --platform-root "$PLATFORM_ROOT"` | JSON response is `ok: true`, or names each missing manifest requirement and its hint |
| PC-001.7 | `kb-dev status --json` | The services equal the receipt/devservices graph |
| PC-001.8 | Repeat `plan` with the same input | Identical plan hash/artifacts; no package or config mutation |

## Pass criteria

No artifact is selected from ambient `node_modules`; configuration and service
metadata come from the published manifests. Any failure has a stable error
code, a hint, a transcript under `.kb/logs/`, and a redacted diagnostic dossier
under `.kb/diagnostics/`.