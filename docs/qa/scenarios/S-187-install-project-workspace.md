---
id: S-187
title: Install — initialize and verify a local project workspace
persona: solo-developer
area: platform-core
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Install the local platform and initialize a selected project directory so the
developer can verify both the running platform and the registered project from
the install flow.

## Prerequisites

- [ ] A sealed release index and isolated platform and project roots.
- [ ] The isolated project directory exists and is not registered yet.

## Steps

| # | Action | Expected |
|---|---|---|
| S-187.1 | Run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT"` and select `"$PROJECT_ROOT"` when asked for the project directory | The wizard displays the canonical project path and the install plan identifies both the platform root and project root. |
| S-187.2 | Complete the installation | The platform config exists under `$PLATFORM_ROOT/.kb/`; the project has `$PROJECT_ROOT/.kb/kb.config.jsonc` pointing to the selected platform and a project registry entry whose path is `$PROJECT_ROOT`. |
| S-187.3 | Run the verification command printed by `kb-create install` | One status result reports the installed/running platform and the registered project ID and canonical project path. |
| S-187.4 | From a clean project directory, run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT"` and inspect the success text | The next command names the selected project path and is directly usable to verify the installed platform and project. |

## Pass criteria

The interactive install must own project selection, project initialization,
registration, and a verification command that shows both platform and project
state. In this worktree, the `install` wizard calls `RequestScenario` without a
project-path prompt and prints the generic `cd ~/any-project && kb commit`
message (`tools/kb-create/v2/cmd/kb-create-v2/main.go:455-460,530`). A direct
request can set `ProjectRoot`; rendering then writes a project `.kb` pointer
(`tools/kb-create/v2/render/render.go:206-225`), while project registration is
a separate `kb project add --init` operation (`cli/commands/src/commands/system/project/add.ts:2,22`).
`kb-create status` reports host health but no registered project
(`tools/kb-create/v2/cmd/kb-create-v2/host.go:83-93`). The product behavior in
these steps is therefore planned until the install flow selects and registers
the project and exposes both results in its verification path. S-039/S-040
cover request apply and platform install health; S-043 covers project registry
operations; S-181 covers install-time release-channel and request sealing.
