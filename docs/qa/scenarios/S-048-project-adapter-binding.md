---
id: S-048
title: Adapters — configure platform bindings and reject project overrides
persona: solo-developer
area: config
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Configure adapter bindings, options, and LLM model settings once at platform
scope. Projects use that platform configuration and can attach plugins
independently without changing adapter behavior.

## Prerequisites

- [ ] Platform installed with the built-in filesystem storage adapter and two registered projects.
- [ ] An adapter package is installed in the platform package graph.
- [ ] A plugin is available to attach to a project.

## Steps

| # | Action | Expected |
|---|---|---|
| S-048.1 | Run `kb adapter bind storage @kb-labs/adapters-fs --json` from the platform root | The command reports success and stores `platform.adapters.storage` in the platform configuration. Both registered projects resolve the same storage adapter. |
| S-048.2 | Run `kb config set --scope platform platform.adapterOptions.storage '{"baseDir":"storage"}' --json` from the platform root, then run `kb adapter list --json` from Projects A and B | The platform configuration contains the options; both projects report the platform binding and the same base directory under the platform data root. Neither project contains adapter configuration. |
| S-048.3 | Add `platform.adapters.storage` to Project A's `.kb/kb.config.json` and run `kb adapter list --json` from Project A; repeat with legacy top-level `adapters.storage` | Each command fails with typed code `KB_ADAPTER_PROJECT_SCOPE`; the message identifies project adapter configuration as unsupported and the hint directs the user to move `platform.adapters` and `platform.adapterOptions` to the platform config. |
| S-048.4 | Attach the available plugin to Project A with `kb marketplace install <plugin-package> --scope project --json`, then inspect plugins in Projects A and B | Project A lists the attached plugin; Project B does not. Both projects continue to resolve the same platform adapter binding. |
| S-048.5 | Inspect Project A and Project B `.kb/kb.config.json[c]` after the plugin attachment | Neither project config contains adapter bindings, adapter options, LLM keys, or model overrides; platform configuration remains the single source for those settings. |
| S-048.6 | Run `kb adapter unbind storage --json` from the platform root, then run the `e2e-hello` workflow from Project A and inspect its storage row and run record | The platform storage binding and its options are removed. Project A still resolves the built-in filesystem default, and the workflow record is written under `<KB_HOME>/state/<projectId>/storage/workflows/<runId>.json`. |

## Dependencies

- S-043 covers project-scoped plugin registration and isolation.
