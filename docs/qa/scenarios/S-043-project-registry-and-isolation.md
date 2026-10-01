---
id: S-043
title: Projects — Register projects and isolate CLI extensions
persona: solo-developer
area: config
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Multiple registered projects retain distinct plugin and runtime state while sharing platform adapter configuration.

## Prerequisites

- [ ] Two initialized project directories, platform root, two plugins, and a platform adapter binding.

## Steps

| # | Action | Expected |
|---|---|---|
| S-043.1 | Run `kb project add "$PROJECT_A" --init --json` and `kb project add "$PROJECT_B" --init --json` | Each exits 0 and returns a `project.id`; the IDs are distinct and each `project.path` equals the corresponding absolute root. |
| S-043.2 | Run `kb project list --json` | JSON contains both project IDs and absolute paths. |
| S-043.3 | Install `@kb-labs/workflow` in project A and `@kb-labs/commit` in project B | Project A's `.kb/marketplace.lock` lists workflow and not commit; project B's lock lists commit and not workflow. |
| S-043.4 | Run `kb adapter list --json` from both project roots | Both projects resolve the same platform adapter bindings, and neither project config contains adapter settings. |
| S-043.5 | Run `kb project show` from each project root | Each command returns that root's registered project ID and absolute path. |
| S-043.6 | Run one project-scoped plugin command from each project root | The command resolves that project's plugin installation and does not resolve the other project's plugin. |
| S-043.7 | From project A, run a CLI command supplied by a plugin installed in the platform root | The platform plugin command is available alongside project A's plugins; a project plugin with the same ID takes precedence. |
