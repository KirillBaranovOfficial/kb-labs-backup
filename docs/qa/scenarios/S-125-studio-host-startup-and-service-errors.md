---
id: S-125
title: Studio — Navigate a fresh host and recover from unavailable services
persona: solo-developer
area: studio
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

Reach the useful Studio pages on a new host, understand which project is active, and get a clear next action when the gateway or an individual data source is unavailable.

## Prerequisites

- [ ] Fresh platform installation with one registered project, Studio enabled, and the workflow pages available in Studio.
- [ ] A secured-mode account for the secured run.

## Steps

| # | Action | Expected |
|---|--------|----------|
| S-125.1 | Open the Studio base URL on a fresh local-mode host | Dashboard loads without a login prompt and the sidebar exposes Dashboard, Projects, Workflows, Marketplace, Analytics, and Observability. |
| S-125.2 | Use the project control to select the registered project, then open Projects, Workflows, Marketplace, Analytics, and Observability from navigation | Each page title matches the selected navigation item; the active project name remains visible while navigating. |
| S-125.3 | Repeat S-125.1 on a secured-mode host before login | The login page is shown; after valid login, Studio opens at Dashboard with navigation and the registered project control. |
| S-125.4 | Stop the gateway and reload the dashboard | Studio shows an error state naming the unavailable gateway and the exact next command `kb-dev status`; it does not show a blank page or an infinite loading spinner. |
| S-125.5 | Restore the gateway but make the analytics source unavailable, then open Analytics | Analytics shows an error naming the unavailable analytics source and the exact next command `kb-dev status`; Dashboard and Projects remain navigable. |
