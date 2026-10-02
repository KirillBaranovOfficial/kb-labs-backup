---
id: S-124
title: Projects — Inspect, register, and remove project registrations
persona: solo-developer
area: projects
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Inspect and manage project registrations from the CLI and Studio, register paths whether or not they contain `.kb/`, and remove a registry entry without deleting project data.

## Prerequisites

- [ ] Two project directories with distinct names and extension sets; one has no `.kb/` directory.
- [ ] An isolated `KB_HOME` project registry for CLI commands.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-124.1 | From outside every registered project run `kb project add <project-path> --json` | JSON contains `ok: true`, `project.id`, canonical `project.path`, and `project.name`; registration does not require a current project. |
| S-124.2 | Run `kb project list --json` | JSON contains both registered projects with their IDs, names, paths, and status. |
| S-124.3 | Run `kb project show <project-id> --json` | JSON contains that project's ID, name, path, declaration presence, status, and state directory; declaration presence does not determine registry membership. |
| S-124.4 | Run `kb project show <unregistered-project-path> --json` for a moved or never-registered folder | JSON reports `ok: false` with error code `KB_PROJECT_UNKNOWN` and hint `Register it with "kb project add <unregistered-project-path>".`; the registry is unchanged. |
| S-124.5 | Run `kb project add <project-path> --json` for a directory without `.kb/` | JSON contains `ok: true` and the registered project record; the registry contains its canonical path and the directory remains unchanged. |
| S-124.6 | Run `kb project add <project-path>` for the same directory and inspect human output | The command reports that the project was registered; it does not require or create a `.kb/` directory. |
| S-124.7 | In Studio Projects choose Add project and submit a path that has no `.kb/` directory | Studio registers the project without asking to initialize it or changing files in the project directory. |
| S-124.8 | Run `kb project remove <project-id> --yes --json` | JSON contains `ok: true` and the removed project; `kb project list --json` no longer includes it, while its folder, `.kb/`, and runtime state directory remain. |
| S-124.9 | From outside every registered project run `kb project list --json` | The command succeeds and lists the platform registry contents without requiring a current project. |
