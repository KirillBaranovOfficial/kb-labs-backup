---
id: S-181
title: Installer — choose a channel and seal an install request
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Choose the platform release channel and artifact source for a fresh install,
and verify that secret values do not enter the plan, request or receipt.

## Prerequisites

- [ ] Stable and canary sealed indexes with platform artifacts and fixture versions.
- [ ] Separate empty platform roots for stable, canary and secret-input installs.
- [ ] An environment variable containing a required secret.
- [ ] A strict compatibility fixture whose selected plugin is incompatible with the platform.

## Steps

| # | Action | Expected |
|---|---|---|
| S-181.1 | Run `kb-create plan --index STABLE_INDEX --platform-channel stable --policy compatible --request-platform-root STABLE_ROOT --json`, apply that request, then run `kb-create status --platform-root STABLE_ROOT --json` | Plan JSON resolves channel `stable` to an exact platform version and artifact hashes; the receipt-backed status reports channel `stable` and that exact installed version. |
| S-181.2 | Run `kb-create plan --index CANARY_INDEX --platform-channel canary --policy compatible --request-platform-root CANARY_ROOT --json`, apply that request, then run `kb-create status --platform-root CANARY_ROOT --json` | Plan JSON resolves channel `canary` to an exact platform version and artifact hashes; the receipt-backed status reports channel `canary` and that exact installed version. |
| S-181.3 | Run `kb-create apply --index INDEX --platform-channel stable --request-platform-root SECRET_ROOT --secret-env gateway.bootstrap.password=ADMIN_PASSWORD --json` with `ADMIN_PASSWORD` set, then inspect stdout, the request and the active receipt | Apply succeeds; no serialized output, request or receipt contains the secret value or environment-variable name. The non-secret request may identify the required secret by ID only. |
| S-181.4 | Starting with a separate empty `OFFLINE_ROOT`, run an online `kb-create apply --index INDEX --platform-channel stable --request-platform-root OFFLINE_ROOT --json` to fetch and checksum-verify its artifacts into that root's cache, then run `kb-create plan --index INDEX --platform-channel stable --offline --request-platform-root OFFLINE_ROOT --json` | The online apply succeeds; the offline plan reports `data.plan.request.source` as `offline`; every artifact reports `resolvedFrom: "cache"` and omits `resolvedUrl`, so the plan shows no registry/network resolution. |
| S-181.5 | Save the active receipt, then run `kb-create plan --index INCOMPATIBLE_INDEX --plugins fixture-plugin@VERSION --policy strict --request-platform-root ROOT --json` where the fixture is outside the strict compatibility matrix | Planning fails with the typed incompatibility error naming the rejected package and correction hint. The active receipt and installed files remain unchanged because refusal precedes apply. |

## Pass criteria

Channel selection and offline/secret inputs apply to a fresh install request.
The related update-channel and installed-version behavior remains owned by
S-023, S-024 and S-052; S-053 owns update and general compatibility checks.
This case covers install-time selection and request sealing only.
