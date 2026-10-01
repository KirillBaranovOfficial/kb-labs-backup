---
id: S-201
title: Projects — Filter analytics, logs, and dashboard by project
persona: solo-developer
area: studio
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio, api]
---

## Goal

Compare activity across repositories in Studio while each dashboard, analytics
view, and log history shows only the selected project's data.

## Prerequisites

- [ ] Projects A and B are registered and produce distinguishable analytics events, workflow runs, and logs.
- [ ] Analytics and project runtime log sources are configured.

## Steps

| # | Action | Expected |
|---|---|---|
| S-201.1 | Select Project A in Studio and open the dashboard, Analytics overview/events, and Observability logs | Each view identifies Project A as its scope and shows A's seeded activity only. |
| S-201.2 | Switch to Project B and reopen those views | The views identify Project B and show B's events and logs only; Project A's event payloads, run IDs, and log records are absent. |
| S-201.3 | Query each project's analytics API through `/api/v1/projects/<PROJECT_ID>/analytics/events` and inspect workflow run history through that project's API | Responses include only the requested project's event and run data; changing the project ID changes the data source without a client-side filter over combined results. |
| S-201.4 | Return to Project A and inspect the dashboard totals and latest activity | Values match A's own seeded activity and remain unchanged by B's events. |

## Dependencies

- S-021 covers project selection for workflow runs in Studio.
- S-121 covers analytics filters and adapter usage in Studio.
- Current project analytics APIs are built over project routing; project-aware
  observability and dashboard behavior needs end-to-end confirmation.
