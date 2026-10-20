---
id: S-035
title: Team admin — Evaluate a published platform service image
persona: team-admin
area: platform-core
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An administrator with Docker installed starts a published platform service image and verifies its baked default composition without building the monorepo.

## Prerequisites

- [ ] Docker Engine is installed and running.
- [ ] A published service image and its documented port and required secrets are available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-035.1 | Run the image with its documented required environment and port mapping, without mounting a config file | The container remains running and does not enter a restart loop. |
| S-035.2 | Inspect the service's effective runtime config through its documented diagnostic surface | The effective config contains resolved values and no literal unresolved `${VAR}` placeholders. |
| S-035.3 | Request the documented health endpoint | It returns HTTP 200 with a healthy status within 30 seconds of container start. |
| S-035.4 | Restart the same container without changing its inputs and repeat the health request | The service returns healthy again and the baked default config remains unchanged. |

This container evaluation flow remains planned until the kb-deploy delivery phase defines supported images and health endpoints.
