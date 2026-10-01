---
id: S-034
title: Platform team — Publish a complete service image matrix
persona: platform-team
area: platform-core
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A release pipeline builds and publishes a tagged container image for every supported backend service from its production deployment artifact.

## Prerequisites

- [ ] A release commit and its version are available to the image pipeline.
- [ ] The service image matrix and container registry credentials are configured.

## Steps

| # | Action | Expected |
|---|---|---|
| S-034.1 | Run the release image pipeline for the commit | Each service in the supported release matrix produces a production deployment artifact and a Docker image. |
| S-034.2 | Inspect the published registry tags for the release version | Every supported service has an image tagged with the release version and the same source commit. |
| S-034.3 | Run the pre-publish composition validation for each service image | The release is blocked if a baked config selects a package absent from its image lock. |
| S-034.4 | Start the complete release composition with its deployment config | All supported services become healthy; registry services wait for their state dependency, and workflow has one replica unless explicitly configured otherwise. |

This planned release-engineering case is outside the platform-core release gate until the kb-deploy phase is complete.
