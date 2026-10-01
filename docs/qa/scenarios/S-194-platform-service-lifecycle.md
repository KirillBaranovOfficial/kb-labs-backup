---
id: S-194
title: Platform — Select and manage services across install, update, and rollback
persona: solo-developer
area: platform-core
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A platform owner chooses official services during install and can later add,
enable, or disable them without losing the sealed release, receipt, or rollback
history. A service's CLI entry is available only while its service is enabled.

## Prerequisites

- [ ] A sealed release index that declares workflow, MCP, and at least one other official service with its associated CLI plugin.
- [ ] A disposable platform root and a named rollback snapshot.
- [ ] An account authorized to manage the platform root; in secured mode this is the platform administrator.

## Steps

| # | Action | Expected |
|---|---|---|
| S-194.1 | Run `kb-create install --registry REGISTRY` and select workflow and MCP while leaving another optional service unselected | The install plan and receipt list workflow and MCP in the service graph. `kb workflow --help` is available because workflow is enabled; the MCP endpoint is available because MCP is enabled; the unselected service and any CLI plugin associated with it are absent. |
| S-194.2 | On the installed platform, run `kb-create service add <service-id> --platform-root PLATFORM_ROOT --index RELEASE_INDEX` for a service present in the sealed index but absent from the receipt | The launcher verifies the index and exact artifact hashes, installs only the missing service artifacts, keeps existing services and plugins, and records the resulting service set in the active receipt. A secured-mode member is refused; a platform administrator can perform the operation. |
| S-194.3 | Run `kb-create service disable workflow --platform-root PLATFORM_ROOT`, then run `kb-create service status --platform-root PLATFORM_ROOT` and `kb workflow --help` | Workflow is stopped and reported disabled, its CLI commands are unavailable, its package remains recorded for re-enable and rollback, and unrelated services remain available. |
| S-194.4 | Run `kb-create service enable workflow --platform-root PLATFORM_ROOT`, then run `kb-create service status --platform-root PLATFORM_ROOT` and `kb workflow --help` | Workflow is reported enabled and healthy, its CLI commands are available, and the other services keep their previous state. |
| S-194.5 | Run `kb-create update --index NEW_RELEASE_INDEX --request-platform-root PLATFORM_ROOT`, then inspect the active receipt and run `kb-create service status --platform-root PLATFORM_ROOT` | The update applies compatible versions of the installed service set and preserves each service's enabled state; it does not silently add or remove services. |
| S-194.6 | Run `kb-create rollback --platform-root PLATFORM_ROOT --snapshot SNAPSHOT_ID --yes`, then inspect the active receipt and run `kb-create service status --platform-root PLATFORM_ROOT` | The prior platform and service artifact versions, exact service set, and enabled states are restored together; the service CLI availability matches the restored state. |

## Pass criteria

The installer and post-install service lifecycle share the sealed artifact and
receipt model. Service selection and state survive updates and are restored by
rollback. This case is planned pending owner confirmation of the service
management command and secured-mode authorization surface.
