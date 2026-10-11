---
id: S-187
title: Install — select and register a local project
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Install the local platform, register a selected project without writing KB Labs
files into its repository, and verify the platform and project from the install
flow.

## Prerequisites

- [ ] A sealed release index and isolated platform and project roots.
- [ ] The project directory exists and is not registered yet.

## Steps

| # | Action | Expected |
|---|---|---|
| S-187.1 | Run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT"` and enter `"$PROJECT_ROOT"` when asked for the project directory | The wizard displays the canonical project path and the install plan identifies both the platform root and project path. |
| S-187.2 | Complete the installation | The platform config exists under `$PLATFORM_ROOT/.kb/`; the project is in the platform project registry with its canonical path and derived project ID; no KB Labs files or directories are created in `$PROJECT_ROOT`. |
| S-187.3 | Inspect the `Verification` result printed by `kb-create install`, then run the two `Next` commands | The verification result reports the installed platform and registered project ID and canonical path; `kb-create status` and `kb project show` confirm those values. |
| S-187.4 | Run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT" --project-root "$PROJECT_ROOT"` | Non-interactive install selects and registers the canonical project path without creating KB Labs files in its repository. |
| S-187.5 | Run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT"` without selecting a project and inspect the success text | The platform installs without registering a project, and the next step gives the exact command `kb project add <path>`. |
| S-187.6 | Run `kb-create install --json --index release-index.json --platform-root "$PLATFORM_ROOT" --service-profile default --scenario custom --scenario-answers '{"access.mode":"local"}' --project-root "$PROJECT_ROOT"`, then run `kb project show "$PROJECT_ROOT" --json` | JSON install registers the canonical project path; project show exits 0 and returns that path and its derived project ID. |
| S-187.7 | Run `kb-create install --json --index release-index.json --platform-root "$PLATFORM_ROOT" --project-root "$MISSING_PROJECT_ROOT"`, where the project path does not exist | The command exits non-zero with catalogued `KB_INSTALL_INPUT_REQUIRED`, explains that the selected project directory must exist, and gives a copyable `mkdir -p` command for that path or tells the user to pass an existing project directory. No platform root, receipt, launcher shim, or project registry entry is created. |
| S-187.8 | Run `kb-create plan --json --index release-index.json --platform-root "$PLAN_ROOT" --project-root "$MISSING_PROJECT_ROOT"` and `kb-create apply --json --index release-index.json --platform-root "$APPLY_ROOT" --project-root "$MISSING_PROJECT_ROOT"`, with the selected project path absent | Both commands exit non-zero with catalogued `KB_INSTALL_INPUT_REQUIRED` and the project-root correction hint; neither creates its platform root. Request validation rejects the missing project before plan resolution or apply. |
| S-187.9 | Install and register a project, remove or move that project directory, then run `kb-create update --index release-index.json --input "$RECEIPT_REQUEST" --platform-root "$PLATFORM_ROOT"`, where `RECEIPT_REQUEST` is the request recorded in the installed receipt | Update succeeds using the recorded request without requiring the project directory to exist, because update does not register or write to the project. |

## Pass criteria

The installer owns project selection, registration, and project-aware
verification. Project registration stores identity and runtime state under the
platform's machine state; it never creates project configuration or other KB
Labs files inside the selected repository. Omitting a project leaves the
platform usable and prints the exact command needed to register one later.
S-039/S-040 cover request apply and platform install health; S-043 covers
project registry operations; S-181 covers install-time release-channel and
request sealing.
