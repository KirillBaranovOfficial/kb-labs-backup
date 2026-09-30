---
id: S-124
title: Projects — Inspect, recover, and remove project registrations
persona: solo-developer
area: projects
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Inspect and manage project registrations from the CLI and Studio, recover from an uninitialized or invalid path, and remove a registry entry without deleting project data.

## Prerequisites

- [ ] Two initialized project directories with distinct names and extension sets.
- [ ] An isolated `KB_HOME` project registry for CLI commands.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-124.1 | From an initialized project directory run `kb project add <project-path> --json` | JSON contains `ok: true`, `project.id`, canonical `project.path`, and `project.name`. |
| S-124.2 | Run `kb project list --json` | JSON contains both registered projects with their IDs, names, paths, and status. |
| S-124.3 | Run `kb project show <project-id> --json` | JSON contains that project's ID, name, path, `.kb/` declaration, status, and state directory. |
| S-124.4 | Run `kb project show <unregistered-project-path> --json` for a moved or never-registered folder | JSON reports `ok: false` with error code `KB_PROJECT_UNKNOWN` and hint `Register it with "kb project add <unregistered-project-path>".`; the registry is unchanged. |
| S-124.5 | Run `kb project add <uninitialized-path>` without `--init` | No registry entry is created; human output says the folder has no `.kb/` and prints the exact recovery command `kb project add <uninitialized-path> --init`. |
| S-124.6 | Run `kb project add <uninitialized-path> --init --json` | JSON contains `ok: true`, `initialized: true`, and the project record; the folder now contains the minimal `.kb/` declaration. |
| S-124.7 | In Studio Projects choose Add project and submit a path that has no `.kb/` directory | Studio asks `Initialize project?`; choosing `Back` leaves the folder and registry unchanged. |
| S-124.8 | Run `kb project remove <project-id> --yes --json` | JSON contains `ok: true` and the removed project; `kb project list --json` no longer includes it, while its folder, `.kb/`, and runtime state directory remain. |
