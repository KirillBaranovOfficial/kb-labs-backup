---
id: S-024
title: Platform — rollback to a V2 snapshot
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Restore a known V2 snapshot after a bad update without recalculating an old
plan or manually editing generated configuration.

## Steps

| # | Action | Expected |
|---|---|---|
| S-024.1 | Apply a verified update and record its previous snapshot ID | Snapshot is immutable and associated with the platform root |
| S-024.2 | `kb-create rollback --platform-root "$PLATFORM_ROOT" --snapshot SNAPSHOT_ID --yes` | JSON result includes the restored snapshot and log path |
| S-024.3 | Inspect receipt, `kb.config.jsonc`, `devservices.yaml` and `kb-dev status --json` | All agree with the restored service graph |
| S-024.4 | Request a missing/unknown snapshot | Non-zero structured recovery error with a hint; active install remains unchanged |
| S-024.5 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT"` | Any remaining manifest requirement is reported explicitly |

## Pass criteria

Rollback is receipt/snapshot based, preserves secrets, and never uses an
ambiguous “latest previous release” heuristic.