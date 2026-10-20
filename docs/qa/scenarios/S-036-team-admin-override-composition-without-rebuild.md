---
id: S-036
title: Team admin — Override a service composition without rebuilding
persona: team-admin
area: platform-core
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An administrator selects a different adapter already present in a published image by mounting a config file, without rebuilding that image.

## Prerequisites

- [ ] Docker Engine is installed and running.
- [ ] A published service image includes both the default adapter and the alternate adapter package.
- [ ] A mounted config file selects the alternate adapter and provides its required settings.

## Steps

| # | Action | Expected |
|---|---|---|
| S-036.1 | Run `docker run --mount type=bind,src="$PWD/kb.config.json",dst=/app/.kb/kb.config.json,readonly <composed-image>` | The service starts with the mounted config at `/app/.kb/kb.config.json`; the image still contains `/app/.kb/marketplace.lock`. |
| S-036.2 | Change the mounted config to select the other included adapter and restart the same container | The service uses the newly selected adapter without rebuilding the image. |
| S-036.3 | Prepare a request selecting an adapter package absent from the image lock and run `kb-create plan --index release-index.json --input request.json --json` | Planning exits non-zero with a structured diagnostic naming the missing package; the image is not started with an unresolved adapter selection. |

This planned composition override flow remains outside the platform-core release gate until the kb-deploy phase.
