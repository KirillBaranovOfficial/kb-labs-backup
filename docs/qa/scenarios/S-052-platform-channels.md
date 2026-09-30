---
id: S-052
title: Platform — install and update across release channels
persona: solo-developer
area: platform-core
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Install and move an installation between stable and canary platform releases
while being able to identify the selected channel and immutable artifact.

## Prerequisites

- [ ] A sealed release index with stable and canary entries and verified canary tarballs.

## Steps

| # | Action | Expected |
|---|---|---|
| S-052.1 | Run `kb-create apply --platform-channel canary --policy strict --json` against a fresh platform root and sealed canary index | Resolution uses the index's canary channel pointer and returns the exact prerelease version and SHA-256 in the plan and receipt. |
| S-052.2 | Inspect the receipt, then run `kb-create status --json` and `kb-create doctor --json` | The receipt, status and doctor identify both the selected channel (`canary`) and exact installed version. |
| S-052.3 | Run `kb-create update --platform-channel canary --json` on a stable installation, then update with `--platform-channel stable` | Each update resolves the selected index pointer and restarts on that exact version; switching back to stable is allowed even when it selects an older version. |
| S-052.4 | Request an older stable version using `kb-create update --platform-version VERSION --json` | The current update path accepts a resolvable older version; rollback remains the snapshot-based recovery operation. |

## Pass criteria

The release index is authoritative for channel resolution. The current CLI can
resolve channel pointers and receipts preserve the request, but `status` reports
only the exact platform version and doctor has no channel field. This case stays
planned until status and doctor expose the channel, and candidate preparation
supplies a sealed canary prerelease index for the journey.
