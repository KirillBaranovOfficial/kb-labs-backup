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
| S-023.1 | In local or secured mode, establish the existing user session when secured, then run `kb project list --json`; save the active V2 receipt, generated-config hashes, SHA-256 fingerprints of private installation-secret entries, and `kb-create status --json` | Establishes the installed N version and known-good host/config state without exposing secret values |
| S-023.2 | Build requests targeting N+1 patch, N+1 minor, and N+2 across two releases admitted by the release matrix; run `kb-create plan --index INDEX --input REQUEST --json` for each | Each plan resolves its target before mutation and lists every component as from -> to, estimated download size, added and removed packages, and the markdown `releaseNotes` field for every intervening platform release in the sealed index. |
| S-023.3 | Run `kb-create update --index INDEX --input REQUEST --json` against a running N host | Installs and verifies the target beside N, then atomically switches the active version without stopping services. The result lists component versions, intervening release notes, snapshot, operation log, and `pendingRestart: { services: [{ id, runningVersion }], installedVersion, command }`. N remains intact on disk. |
| S-023.4 | Inspect the active receipt, generated config, `kb-create status --json`, and `kb-create doctor --json` after S-023.3; compare installation-secret fingerprints | The receipt, status, and doctor identify installed N+1. Status and doctor contain the same `pendingRestart` object as update, naming services still running N; it is absent after restart. Config and secrets remain byte-for-byte unchanged. Human output lists services and the restart command. |
| S-023.5 | Run `kb project list --json` from the existing project before restarting | The core project command exits successfully with `ok: true`; the saved CLI session still works while the old host serves requests. |
| S-023.6 | Run the same update request when the target is already installed | Succeeds with a clear “already up to date” result, does not restart services, and leaves the receipt bytes unchanged. |
| S-023.7 | Update to a sealed release whose artifacts were published minutes ago, while an unrelated dependency is subject to pnpm's configured minimum release age | The exact plan artifacts install successfully because their sealed, checksum-verified versions are trusted; pnpm's release-age policy remains in force for dependencies outside the plan |
| S-023.8 | Run `kb-create restart --platform-root ROOT --json` after S-023.3 while work is active, then retry with `--interrupt`; also exercise interactive restart | Non-interactive restart refuses with `KB_RESTART_WORK_IN_FLIGHT`; details list each `{ kind, id, owningService }`, and the hint offers to wait or rerun with `--interrupt`. Interactive restart asks whether to wait or proceed. `--interrupt` proceeds and the pending restart clears. |
| S-023.9 | Repeat a patch, minor, and N-to-N+2 update with the host stopped | Update leaves services stopped; `kb-create start` starts the target. Platform, SDK, CLI `kb --version`, and the update-owned `kb-create` binary at `.kb/v2/bin/kb-create` report the unified target version. |
| S-023.10 | Update with compatible marketplace plugins, non-default adapters and secrets, optional services, two registered projects with per-project plugins/settings, SQLite analytics and workflow history, users/sessions, installation secrets, and generated config | Compatible plugin commands, adapter bindings/config/secrets, enabled service states, project registrations/settings, database rows, users/sessions, secrets, and generated config remain available and unchanged after restart. |
| S-023.11 | After restart, request Studio assets and list and invoke project MCP tools | Studio serves target-version assets without stale cached assets, and project MCP tools remain listed and runnable. |
| S-023.12 | Before switching, inject download/install failure, checksum mismatch, and staged manifest/config/doctor verification failures one at a time | Each exits nonzero with a typed error and actionable hint; active files, receipt, config, secrets, data, and running N host remain byte-for-byte unchanged; staged tree is removed. |
| S-023.13 | Terminate update during staging, then run `kb-create status --json`, `kb-create doctor --json`, and `kb-create update --json` | No half-installed version reports healthy. Each command detects the incomplete operation and recovers it or returns a typed diagnosis with the exact recovery command. |
| S-023.14 | Run update while a workflow run, long command, or service job is active | Update does not interrupt work or restart services. Platform-owned in-flight query returns workflow runs, jobs, and long-command registrations with `{ kind, id, owningService }`. |
| S-023.15 | Inspect recovery snapshots after repeated updates and measure disk use | Retain the active version, previous version, and at most two recovery snapshots; prune older versions/snapshots only after a healthy restart on the new version. `doctor` reports disk use for retained versions and snapshots; no byte cap applies. |

## Pass criteria

Update is forward-only, stages and verifies before switching, and never
restarts services. It uses the resolved V2 plan and receipt/snapshot recovery
model; preserves user data and installed contents; and reports changes,
progress, durations, and typed actionable errors in human and JSON formats.
Local and secured modes share these guarantees. The secured mode proves that
the saved administrator session remains usable across update and restart.

The post-update project check uses a core CLI command because Commit is an
optional marketplace plugin, not a platform member (see the platform-members
decision in `docs/product/decisions.md`).
