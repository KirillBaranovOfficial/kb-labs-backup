---
id: S-186
title: Configuration — change release-critical core settings
persona: solo-developer
area: platform-core
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Set supported core configuration values through the user configuration
command, observe their runtime effect, and get a typed error for invalid
values without losing the prior setting.

## Prerequisites

- [ ] A disposable platform and project config with no higher-precedence overrides for the selected keys.
- [ ] Two deterministic workflow runs and a fixture plugin command suitable for checking execution isolation.

## Steps

| # | Action | Expected |
|---|---|---|
| S-186.1 | Run `kb config set platform.execution.mode worker-pool --scope platform` and inspect `kb config get platform.execution.mode --json` | The platform config records `worker-pool`; plugin commands execute through the worker-pool backend after the runtime reloads the effective config. |
| S-186.2 | Run `kb config set platform.core.workflows.maxConcurrent 4 --scope platform`, then run five long enough fixture workflows concurrently | At most four workflow runs execute at once; the remaining run waits until a slot is free. |
| S-186.3 | Save the prior value, then run `kb config set platform.execution.mode turbo --scope platform --json` | The command exits nonzero with `KB_CONFIG_INVALID` naming `platform.execution.mode`; the invalid value is not written and the prior effective value remains. |

## Pass criteria

The supported path is `kb config set`; `PlatformUserConfigSchema` validates
`platform.execution.mode` and `platform.core.workflows.maxConcurrent`, and the
runtime consumes the execution mode and workflow concurrency limits. The case
is planned because the two runtime effects and reload boundary do not yet have
a user-level journey. Gateway, auth/session, projects, logging and other
workflow keys remain uncased until a source-backed setting inventory chooses
their release-critical observable outcomes.
