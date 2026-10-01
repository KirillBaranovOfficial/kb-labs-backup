---
id: S-183
title: Studio — change a supported platform setting
persona: team-admin
area: studio
priority: P1
status: planned
release: false
modes: [secured]
surfaces: [studio, api]
---

## Goal

An administrator edits a supported platform setting in Studio, sees the saved
value after reload, and receives a field-level validation error without losing
the last valid value.

## Prerequisites

- [ ] An administrator and a member account in secured mode.
- [ ] A supported platform setting schema and Settings write API.

## Steps

| # | Action | Expected |
|---|---|---|
| S-183.1 | Open Studio **Settings** as an administrator | The page exposes editable fields only for the supported core settings `platform.execution.mode` and `platform.core.workflows.maxConcurrent`; adapter bindings and read-only platform status are not editable here. |
| S-183.2 | Set `platform.execution.mode` to `worker-pool`, save, reload Settings and inspect the saved value | Studio confirms the save and shows `worker-pool` after reload; subsequent plugin command execution uses the worker-pool backend. |
| S-183.3 | Set `platform.core.workflows.maxConcurrent` to `4`, save, and reload Settings | Studio confirms the save and shows `4` after reload; the workflow scheduler allows at most four concurrent workflow runs. |
| S-183.4 | Submit an invalid value such as `turbo` for `platform.execution.mode` | The field shows a validation error naming `platform.execution.mode`; the previously saved `worker-pool` value remains in storage and after reload. |
| S-183.5 | Sign in as a member without `platform-settings:write`, open Settings, then send a direct settings mutation request | The Settings page has no write control, and the mutation request is denied without changing either saved setting. |

## Pass criteria

Current Studio Settings contain user preferences and a read-only System
Settings component. The router gates Settings with `platform-settings:read`,
but there are no platform-setting edit controls, save API or `platform-settings:write`
authorization check. The case is planned until those exist. The named fields
are grounded in the runtime execution and core workflow schemas; the exact
Studio form and mutation route remain unimplemented.
