---
id: S-004
title: Team Admin — Deploy a platform to a remote host
persona: team-admin
area: platform-core
priority: P0
status: planned
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

An operator deploys a configured target to a remote host with the supported `kb-deploy` CLI and checks its deployment state.

## Prerequisites

- [ ] A deployment config with a test target, valid SSH access, Docker access, and registry credentials as required by that target.
- [ ] The repository is at a commit whose changed files match the target's `watch` paths, or the operator intends to deploy all targets.

## Steps

| # | Action | Expected |
|---|---|---|
| S-004.1 | Run `kb-deploy list` from the configured repository | The configured target name, image and host are listed. |
| S-004.2 | Run `kb-deploy --config .kb/deploy.yaml run <target>` | The output reports build, push and deploy results for that target; a failure identifies the failed stage and target. |
| S-004.3 | Run `kb-deploy --config .kb/deploy.yaml status` | The target's deployed short Git SHA and deployment timestamp are shown. |
| S-004.4 | Make a change under the target's `watch` paths and run `kb-deploy --config .kb/deploy.yaml run` | The affected target is built, pushed and deployed; unchanged targets are skipped. |

This remains planned until the kb-deploy delivery phase defines a supported customer deployment contour.
