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
| S-124.3 | From outside every registered project, run `kb project show <project-id> --json` | JSON contains that project's ID, name, path, declaration presence, status, and state directory; `project show` is platform-scoped, resolves an explicit reference itself, and declaration presence does not determine registry membership. |
| S-124.4 | Run `kb project show <unregistered-project-path> --json` for a moved or never-registered folder | JSON reports `ok: false` with error code `KB_PROJECT_UNKNOWN` and hint `Register it with "kb project add <unregistered-project-path>".`; the registry is unchanged. |
| S-124.5 | Run `kb project add <project-path> --json` for a directory without `.kb/` | JSON contains `ok: true` and the registered project record; the registry contains its canonical path and the directory remains unchanged. |
| S-124.6 | Run `kb project add <project-path>` for the same directory and inspect human output | The command reports that the project was registered; it does not require or create a `.kb/` directory. |
| S-124.7 | In Studio Projects choose Add project and submit a path that has no `.kb/` directory | Studio registers the project without asking to initialize it or changing files in the project directory. |
| S-124.8 | Run `kb project remove <project-id> --yes --json` | JSON contains `ok: true` and the removed project; `kb project list --json` no longer includes it, while its folder, `.kb/`, and runtime state directory remain. |
| S-124.9 | From outside every registered project run `kb project list --json` | The command succeeds and lists the platform registry contents without requiring a current project. |
| S-124.10 | Inspect every live system command registered by the CLI and invoke it from an unregistered temporary directory | Each command declares `scope: "platform"` or `scope: "project"`. Platform-scoped commands run without resolving a project; project-scoped commands fail before command execution with the shared UX error envelope, code `KB_PROJECT_UNKNOWN`, and a hint to run `kb project add <path>` or change to a registered project. Bootstrap uses the declaration for every command and contains no command-name scope list. |
| S-124.11 | With a registered project's ID and canonical path, start or request its project runtime, then inspect the runtime health response | The runtime is healthy and identifies exactly the project ID and path in the registry; its project REST route responds successfully. It does not resolve project identity from its working directory or use the platform root as the project root. |
| S-124.12 | Start the project runtime with a host-passed project ID that does not match the registered path | Startup fails with a typed `KB_PROJECT_RUNTIME_IDENTITY_MISMATCH` error in the shared envelope and the hint `Run kb project list to verify the registered path, then restart the host.`; user-facing logs contain no raw stack trace. |
| S-124.13 | On a fresh installed platform, register a project and request its runtime health through the host using the registered project ID and canonical path | The runtime reaches `ready`, host health lists the registered project as ready, and the project's REST health route returns HTTP 200. |
