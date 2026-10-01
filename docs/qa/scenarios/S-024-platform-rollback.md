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
| S-024.1 | Update the running host to N+1 and record the snapshot ID returned by the update | The immutable recovery snapshot captures version N before the update |
| S-024.2 | `kb-create rollback --platform-root "$PLATFORM_ROOT" --snapshot SNAPSHOT_ID --yes --json` | JSON result includes the restored snapshot and log path, and the host restarts |
| S-024.3 | Inspect the receipt, generated config and `kb-create status --json`; run `kb project list --json` from the existing project and log in again in secured mode | The host and receipt return to N, the generated config bytes exactly match their state before the update, the core project command exits successfully with `ok: true` and a `projects` array, and the existing admin account still authenticates |
| S-024.4 | Request a missing/unknown snapshot with `--yes --json` | Non-zero structured recovery error includes a hint; active install remains unchanged |
| S-024.5 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | The restored platform is reported healthy |

## Pass criteria

Rollback is receipt/snapshot based, preserves secrets, and never uses an
ambiguous “latest previous release” heuristic.

The project check uses a core CLI command because Commit is an optional
marketplace plugin, not a platform member (see the platform-members decision
in `docs/product/decisions.md`).
