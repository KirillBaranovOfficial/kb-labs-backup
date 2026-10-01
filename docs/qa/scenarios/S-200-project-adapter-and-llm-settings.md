---
id: S-200
title: Projects — Apply effective adapter and model settings
persona: solo-developer
area: config
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Give each repository the data location and model behavior it needs while
keeping provider credentials and adapter bindings managed at platform scope.

## Prerequisites

- [ ] One installed platform and two registered projects.
- [ ] Platform defaults include filesystem storage and configured LLM credentials.
- [ ] A project-specific storage directory and a second supported model are available for the fixture.

## Steps

| # | Action | Expected |
|---|---|---|
| S-200.1 | Run `kb adapter list --json` and `kb config show --json` from Project A and Project B without project overrides | Both projects resolve the platform adapter bindings and defaults; runtime data still has a distinct project-owned location and does not mix between repositories. |
| S-200.2 | Configure Project B to use a separate storage base directory and a different supported LLM model, without writing provider credentials into the repository; inspect effective config in B and A | B reports the selected data location and model; A continues to use platform defaults. Secrets remain in the platform credential store and are not printed in config output. |
| S-200.3 | Run a workflow and an LLM-backed command in each project, then inspect the storage locations and safe provider/model diagnostics | Each project's persisted data uses its selected project data location; B uses its selected model, A uses the platform default, and credential values are never exposed. |
| S-200.4 | Remove Project B's project-level options and inspect effective config in both projects | B returns to platform defaults; A's configuration and data location remain unchanged. |

## Open product decision

Recommended contract: adapter bindings, providers, and credentials are
platform-level; projects may select safe per-project options such as data
directories and model names. The owner has not decided whether project-specific
model selection is needed. Keep this case planned until that boundary is
confirmed; S-048 remains the active contract for its existing project storage
override behavior.

## Dependencies

- S-048 covers project-level storage adapter binding and base-directory override.
- S-017 and S-018 cover LLM key and provider configuration.
