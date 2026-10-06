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
| S-023.1 | In secured mode, log in as the existing administrator and run `kb project list --json`; save the active V2 receipt, generated-config hashes, SHA-256 fingerprints of the private installation-secret entries, and `kb-create status --json` | Establishes a working saved session, installed version, and known-good host/config state without exposing secret values |
| S-023.2 | Build a request for the newer sealed index and run `kb-create plan --index INDEX --input REQUEST --json` | The plan resolves the requested N+1 platform version before mutation |
| S-023.3 | Run `kb-create update --index INDEX --input REQUEST --json` against the running host | The host restarts on N+1; output includes the new receipt, recovery snapshot and operation log |
| S-023.4 | After S-023.3, inspect the active receipt, generated config and `kb-create status --json`, compare installation-secret fingerprints, and confirm the saved session with the protected command from S-023.5 before signing in again | The same saved CLI session refreshes and runs the protected command before and after the host restart. Receipt and healthy host report N+1, generated config survives, installation-secret fingerprints are unchanged, and a fresh administrator login succeeds. |
| S-023.5 | Run `kb project list --json` from the existing project | The core project command exits successfully and returns JSON with `ok: true` and a `projects` array after the host restart |
| S-023.6 | Run the same update request again | The verified receipt remains on N+1 with the same resolved plan hash |
| S-023.7 | Update to a sealed release whose artifacts were published minutes ago, while an unrelated dependency is subject to pnpm's configured minimum release age | The exact plan artifacts install successfully because their sealed, checksum-verified versions are trusted; pnpm's release-age policy remains in force for dependencies outside the plan |

## Pass criteria

Update uses the resolved V2 plan and receipt/snapshot recovery model. It does
not rewrite user-provided secrets, regenerate installation secrets, infer a
plugin version, or call a legacy marketplace lock scan. The secured mode proves
that the saved administrator session refreshes and remains usable after the
host restart by running the protected project command from S-023.5.

The post-update project check uses a core CLI command because Commit is an
optional marketplace plugin, not a platform member (see the platform-members
decision in `docs/product/decisions.md`).
