---
id: S-196
title: Projects — Register repositories and select the current project
persona: solo-developer
area: config
priority: P0
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Register several real repositories on one installed platform, see the registry,
and have CLI and Studio operations resolve the intended current project.

## Prerequisites

- [ ] One installed platform and two initialized Git repositories, Project A and Project B.
- [ ] Project A and Project B have distinct names and registered paths.

## Steps

| # | Action | Expected |
|---|---|---|
| S-196.1 | From outside both repositories, run `kb project add "$PROJECT_A" --json` and `kb project add "$PROJECT_B" --json` | Each command succeeds and returns a distinct project ID, name, and canonical absolute path. Both repository directories remain intact. |
| S-196.2 | Run `kb project list --json`, then run `kb project show` from inside each repository | The list contains both registrations; `project show` identifies Project A from A's cwd and Project B from B's cwd, including each project's ID and path. |
| S-196.3 | From a subdirectory of Project A, run `kb config show --json`; repeat from a subdirectory of Project B | Each command resolves the nearest registered project containing cwd and reads only that project's effective configuration. No persistent `kb project use` selection is required. |
| S-196.4 | From a directory that is not inside any registered project, run a project-scoped CLI command | The command fails with a typed `KB_PROJECT_UNKNOWN` error that explains no registered project contains cwd and gives the exact next action: `kb project add <path>` or `cd` into a registered project. A local-only command that needs no project still works there. |
| S-196.5 | Open Studio, select Project A, then select Project B in the active project selector | The selector shows the selected project name after each change; project pages load data for that selected ID. On reload, Studio restores the last selected registered project, or selects the first registered project if the saved ID is no longer registered. |

## Dependencies

- S-043 covers registry and extension isolation details.
- S-046 covers Studio project management and project-specific extension views.
- CLI current-project resolution by cwd is an owner-approved product expectation
  that is not yet completely represented by current root discovery.
