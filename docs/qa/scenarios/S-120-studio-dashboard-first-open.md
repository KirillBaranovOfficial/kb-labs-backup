---
id: S-120
title: Studio — Understand platform and project status from the dashboard
persona: solo-developer
area: studio
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

On first open, see whether the platform is ready and what needs attention; after registering a project, see its selected identity and useful project activity without confusing an empty installation with an error.

## Prerequisites

- [ ] Studio host started and user can access the dashboard.
- [ ] One fixture with no registered projects and one fixture with a registered project, a recent successful workflow run, and a plugin installed.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-120.1 | Open `/` on the host with no registered project | Dashboard shows Gateway health and readiness. When the gateway is available, status is `Healthy` or its reported readiness state; the page does not show fabricated project metrics. A project selector or empty-state message says `No registered project` and offers a path to Projects. |
| S-120.2 | Register the fixture project and select it in Studio | The active project control shows its name; project-scoped widgets identify the same project. |
| S-120.3 | Inspect the dashboard for the selected fixture project | Project activity shows the seeded workflow run and installed plugin, with the run's terminal status and plugin name/version. Platform status remains a separate gateway health/readiness indicator. |
| S-120.4 | Open the dashboard when the project has no workflow runs or installed plugins | Each project activity section shows an empty state naming the missing data and a route to Workflows or Plugins; the page does not show zero-valued data as an error. |
| S-120.5 | Open the dashboard when the gateway reports not ready | The dashboard displays the returned readiness state and identifies that the gateway is running but not ready. |

