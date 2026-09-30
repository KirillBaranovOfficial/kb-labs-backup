---
id: S-023
title: Platform — update from a new sealed index
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Update an existing verified V2 installation without resolving versions from
ambient state or losing a recoverable snapshot.

## Steps

| # | Action | Expected |
|---|---|---|
| S-023.1 | Save the active V2 receipt, generated-config hashes and `kb-create status --json` | Establishes the installed version and known-good host/config state |
| S-023.2 | Build a request for the newer sealed index and run `kb-create plan --index INDEX --input REQUEST --json` | The plan resolves the requested N+1 platform version before mutation |
| S-023.3 | Run `kb-create update --index INDEX --input REQUEST --json` against the running host | The host restarts on N+1; output includes the new receipt, recovery snapshot and operation log |
| S-023.4 | Inspect the active receipt, generated config and `kb-create status --json`; in secured mode, log in again as the existing admin | Receipt and healthy host report N+1, generated config survives, and the existing account still authenticates |
| S-023.5 | Run `kb commit --dry-run --scope root` from the existing project | The project command succeeds after the host restart |
| S-023.6 | Run the same update request again | The verified receipt remains on N+1 with the same resolved plan hash |

## Pass criteria

Update uses the resolved V2 plan and receipt/snapshot recovery model. It does
not rewrite user-provided secrets, infer a plugin version, or call a legacy
marketplace lock scan. The secured mode also proves that the pre-existing
administrator account remains usable after the host restart.
