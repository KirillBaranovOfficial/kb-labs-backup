---
id: S-043
title: Projects — register and isolate project extensions
persona: solo-developer
area: config
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Multiple registered projects retain distinct plugin, adapter, and runtime state.

## Prerequisites

- [ ] Two initialized project directories, platform root, two plugins, and two platform-installed adapters.

## Steps

| # | Action | Expected |
|---|---|---|
| S-043.1 | Run `kb project add "$PROJECT_A" --init --json` and `kb project add "$PROJECT_B" --init --json` | Each exits 0 and returns a `project.id`; the IDs are distinct and each `project.path` equals the corresponding absolute root. |
| S-043.2 | Run `kb project list --json` | JSON contains both project IDs and absolute paths. |
| S-043.3 | Install `@kb-labs/workflow` in project A and `@kb-labs/commit` in project B | Project A's `.kb/marketplace.lock` lists workflow and not commit; project B's lock lists commit and not workflow. |
| S-043.4 | Run `kb adapter bind storage @kb-labs/adapters-fs` from project A and `kb adapter bind documentDatabase @kb-labs/adapters-sqlite` from project B | Project A's `.kb/kb.config.jsonc` has `platform.adapters.storage: "@kb-labs/adapters-fs"`; project B's config has `platform.adapters.documentDatabase: "@kb-labs/adapters-sqlite"`; each remains unchanged when switching projects. |
| S-043.5 | Run `kb project show` from each project root | Each command returns that root's registered project ID and absolute path. |
