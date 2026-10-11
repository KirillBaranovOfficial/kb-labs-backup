---
id: S-226
title: Project runtime — report unhealthy service health as a typed error
persona: platform-developer
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [api]
---

## Goal

Diagnose a project service whose declared health endpoint is unhealthy or unreachable.

## Steps

| # | Action | Expected |
|---|---|---|
| S-226.1 | Request `GET /api/v1/projects/<project-id>/<service-id>/health` when the declared health endpoint reports an unhealthy status or cannot be reached | Gateway returns HTTP 502 with the catalogued `KB_PROJECT_RUNTIME_START_FAILED` UX error envelope. The message identifies the project service, and the hint gives `kb logs query --source <service-id> --level error --limit 20` as the next command. |

## Pass criteria

The project router uses the service manifest's declared health endpoint for every project service. Failures return one catalogued UX error; healthy responses return the service health payload in the shared success envelope.
