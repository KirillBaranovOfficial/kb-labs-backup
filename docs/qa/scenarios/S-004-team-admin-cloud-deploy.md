---
id: S-004
title: S-004 — Deploy a platform to a remote host
persona: team-admin
area: platform-core
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An operator deploys KB Labs to a remote host with the current `kb-deploy` interface and verifies the published services.

## Prerequisites

- [ ] `kb-deploy` and a valid deployment config with SSH access to a test host.

## Steps

| # | Action | Expected |
|---|---|---|
| S-004.1 | Run `kb-deploy status` | Configured targets and the last deployment state are listed. |
| S-004.2 | Run `kb-deploy run --config deploy.yaml` | Deployment reports each completed stage and a final result. |
| S-004.3 | Open the configured Studio URL and sign in | Studio loads and the admin can access the deployed platform. |
| S-004.4 | Repeat `kb-deploy run --config deploy.yaml` with unchanged inputs | Deployment completes and all configured health checks pass. |

This covers remote deployment through `tools/kb-deploy/README.md`; container delivery targets remain separate planned cases.
