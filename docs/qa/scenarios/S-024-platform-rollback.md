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
| S-024.1 | Update a running N host to N+1 and record the snapshot ID returned by update | The immutable recovery snapshot captures version N before the atomic switch in the install-root `installer/snapshots/` record store; it survives version-root pruning. Update leaves the host running N and reports the pending restart. |
| S-024.2 | `kb-create rollback --platform-root "$PLATFORM_ROOT" --snapshot SNAPSHOT_ID --yes --json` | JSON result includes the restored snapshot and log path, restores N as the installed and running version, and reports the services it starts. Rollback leaves the user shim path, link identity, and bytes unchanged; the shim continues through `current` to the restored version's CLI. |
| S-024.3 | Inspect the receipt, generated config and `kb-create status --json`; run `kb project list --json` from the existing project using the session saved before update, compare the installation-secret fingerprints, then sign in again as the existing administrator | The host and receipt return to N, the generated config bytes exactly match their state before update, the private secret-state record remains shared and is not restored from the recovery snapshot, the protected core project command succeeds with `ok: true` and a `projects` array, installation-secret fingerprints match their pre-update values, and a fresh administrator login succeeds after rollback. |
| S-024.4 | Request a missing/unknown snapshot with `--yes --json` | Non-zero structured recovery error includes a hint; active install remains unchanged |
| S-024.5 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | The restored platform is reported healthy |
| S-024.6 | After update, restart the host and make the N+1 health check fail | The launcher automatically restores the last-healthy root (N), starts N, and returns `KB_UPDATE_HOST_NOT_HEALTHY` naming the restored version, recovery snapshot, and operation log. Status and doctor report healthy N and persistent data is intact. |
| S-024.7 | Update N→N+1, restart healthy, update N+1→N+2, restart healthy so N+1 is pruned, then roll back using the snapshot ID returned by the first update | Rollback reads its immutable snapshot from the install-root `installer/snapshots/` store, independent of version-root retention, restores N and its launcher-managed files, and reports a healthy running host. If the required sealed artifacts are unavailable, it refuses with a catalogued diagnostic naming the missing artifact and the exact next command. |

## Pass criteria

Rollback is receipt/snapshot based, preserves installation secrets, and never
uses an ambiguous “latest previous release” heuristic.

The project check uses a core CLI command because Commit is an optional
marketplace plugin, not a platform member (see the platform-members decision
in `docs/product/decisions.md`).
