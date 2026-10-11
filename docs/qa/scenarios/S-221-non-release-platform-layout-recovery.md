---
id: S-221
title: Platform — crash-safe version-root handoff and recovery failures
persona: solo-developer
area: platform-core
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

Prove platform layout transitions and restart recovery at failure boundaries that cannot be induced safely in an installed release journey.

## Steps

| # | Action | Expected |
|---|---|---|
| S-221.1 | Interrupt initial activation and update activation at each durable handoff boundary, then reopen the layout | During update handoff, install-root `current` always resolves to a complete tree and recorded recovery root exists. During first install, the original complete tree or completed version tree remains recoverable; an interrupted install can be rerun with `kb-create install --platform-root ROOT`. No active link points to a partial tree. |
| S-221.2 | Make the target host unhealthy, then separately corrupt recovery metadata, remove the recovery root, and make the restored host fail startup; exercise direct and supervised restart | Every path returns a catalogued `KB_UPDATE_HOST_NOT_HEALTHY` diagnostic with the attempted root, recovery stage, failure reason, and exact `kb-create doctor --platform-root ROOT` next command. No raw or joined error is printed. |
| S-221.3 | Start from a directory that is not represented by the install-root container layout and invoke update, restart, and rollback | Each operation returns catalogued `KB_PLATFORM_LAYOUT_UNSUPPORTED`, explains that the root is not a valid install-root container, and gives the exact command `kb-create install --platform-root NEW_ROOT` after naming how to choose a fresh writable path. The directory bytes remain unchanged. |
| S-221.4 | Run the full install/update/restart/uninstall lifecycle inside a fresh temporary parent and inspect that parent after each command | No path outside the install root changes except a user-requested shim location and documented per-user state; after uninstall, the parent is empty. |
| S-221.5 | Start the host, make the installation metadata unreadable, then run `kb-create status --json` and `kb-create stop --json` using the install root | Status reports the host from its durable install-root process record, and stop succeeds and waits for that host to exit without requiring a valid active layout. |

## Pass criteria

The version-root layout is created by fresh installation and used consistently by update, host lifecycle, rollback, doctor, and uninstall. Handoff recovery never exposes a missing or partial active root. Recovery errors remain typed at both direct and supervised command boundaries.
