---
id: S-038
title: Team admin — Missing production secret fails at service startup
persona: team-admin
area: platform-core
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An administrator receives an actionable startup error when a required config environment reference is unset in a production service container.

## Prerequisites

- [ ] A service image with a config value referencing `${REQUIRED_SECRET}`.
- [ ] Docker Engine is installed and running.

## Steps

| # | Action | Expected |
|---|---|---|
| S-038.1 | Start the service container with `NODE_ENV=production` and without `REQUIRED_SECRET` | The process exits non-zero during startup; the diagnostic names `REQUIRED_SECRET` and gives the next action to provide it. |
| S-038.2 | Start the same container with `REQUIRED_SECRET` set to a valid test value | The service starts and its documented health endpoint returns HTTP 200 within 30 seconds. |
| S-038.3 | Run a development service container with `NODE_ENV=development` and the same secret unset | Startup behavior follows the development config contract; the operator can distinguish an unresolved reference from a resolved secret in its diagnostic. |

This planned container-level case is separate from the existing config-loader unit behavior and is outside the platform-core release gate until the kb-deploy phase.
