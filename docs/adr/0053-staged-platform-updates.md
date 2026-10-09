# ADR-0053: Stage platform updates and switch versions atomically

**Date:** 2026-10-08
**Status:** Draft
**Deciders:** KB Labs Team
**Tags:** platform, update, recovery, cli

## Context

The current V2 update mutates the active package tree and uses the `swap`
operation to stop, switch, and restart the host. That exposes user state to
partial update failures and interrupts running services before the user can
choose when to restart. A recovery snapshot exists, but it is not a substitute
for leaving the active installation untouched until a candidate has passed
verification.

Updates also need to explain the complete version change. A target can move
platform and SDK packages, binaries, services, adapters, and plugins together,
and a jump may include release notes from multiple intervening releases.

## Decision

### Stage, verify, then switch

`update` creates a staged installation beside the active version. It downloads
and verifies artifact checksums, materializes manifests, renders configuration,
and runs doctor checks against the staged tree. Only a fully verified stage may
become active, through an atomic active-version pointer/receipt switch.

Until that switch, the user's active files, receipt, generated config, secrets,
data, and running host remain unchanged. On any pre-switch failure, the stage
is removed and a typed error identifies the failed phase and gives the exact
next command. An interrupted operation is journaled so status, doctor, and the
next update can diagnose or recover it; no partial tree may report healthy.

### Keep services running until an explicit restart

Update does not stop or restart services. A running process keeps using the old
version, whose files remain available on disk until that process restarts. The
update result names every affected service and gives the exact restart command.
`status` and `doctor` report both the running and installed versions and the
same pending restart object returned by update:
`pendingRestart: { services: [{ id, runningVersion }], installedVersion,
command }`. The object is absent when no restart is pending. Human output lists
the services and command. A stopped host starts from the newly installed
version.

Before a restart stops services, the platform-owned in-flight-work query returns
workflow runs, service jobs, and long-command registrations. Each item has
`kind`, `id`, and `owningService`; the query reads authoritative platform state
and never infers work from OS processes. Non-interactive restart returns
`KB_RESTART_WORK_IN_FLIGHT` with those items and a hint to wait or rerun with
`--interrupt`. Interactive restart asks whether to wait or proceed. Update
never invokes this query to interrupt work because it never restarts services.

### Recovery and rollback

The previous version remains available after a successful switch and is the
rollback target. If a restarted candidate fails its health check, the launcher
automatically restores the named pre-update snapshot, starts the prior version,
and returns a typed error naming the snapshot and operation log. Manual rollback
to a selected snapshot remains supported. `update` refuses downgrades.

Keep the active version, previous version (the rollback target), and at most two
recovery snapshots. Prune older versions and snapshots only after the new
version restarts successfully and passes health checks. Doctor reports disk
use for kept versions and snapshots. There is no byte cap.

### Explain the change

The plan and result list each changed component as `from -> to`, the estimated
download size, added and removed packages, progress and duration for each
phase, and release notes for every release between installed and target
versions. Notes are read from `releaseNotes`, a markdown string on each
platform release entry in the sealed release index. A markdown string keeps
notes self-contained and available offline, while sealing binds the notes to
the immutable release entry. Stream r05 owns release-flow population; this ADR
does not change release scripts. S-214.10 proves the sealed field and offline
planning across a multi-release update.

Human output uses one concise format; `--json` uses the shared UX result
envelope. Every failure is typed and includes an actionable hint.

## Consequences

### Positive

- Failed downloads, installs, or staged verification do not damage the active
  installation or interrupt services.
- Users choose when to restart and can see pending restart state before doing
  so.
- Recovery always names a concrete snapshot and log.
- A plan makes component, package, and release-note changes reviewable before
  mutation.

### Negative

- Two platform versions coexist until restart and old-version retention ends,
  so disk use needs a bounded snapshot/version retention policy.
- The launcher needs a durable operation journal and an atomic active-version
  switch that remain correct across process termination.
- Restart safety depends on platform APIs exposing all supported in-flight
  work classes.

## Open questions

- What durable operation-journal states and exact recovery commands are
  exposed by status, doctor, and update?

## References

- [S-023 — Platform update](../qa/scenarios/S-023-platform-update.md)
- [S-024 — Platform rollback](../qa/scenarios/S-024-platform-rollback.md)
- [ADR-0045 — Executable roles and launcher control channel](./0045-executables-roles-and-launcher-control-channel.md)
- [ADR-0051 — UX Coherence Contract](./0051-ux-coherence-contract.md)
