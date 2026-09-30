---
id: S-032
title: Workflow — Outbound completion webhook (not shipped)
persona: solo-developer
area: workflow
priority: P1
status: historical
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Historical design record for a workflow automatically sending an outbound notification after a run completes.

## Removed capability and evidence

The current workflow service does not implement an outbound completion webhook or an inbound webhook delivery endpoint. Workflow YAML accepts an `on.webhook` trigger shape and converts it to a webhook trigger (`plugins/workflow/contracts/src/schemas.ts:198-219`; `plugins/workflow/engine/src/workflow-repository.ts:401-414`), but those declarations do not provide a callable HTTP ingress route. The workflow daemon API registers run, job, cron, approval, and stats routes, with no webhook delivery handler (`plugins/workflow/daemon/src/server.ts:113-124`). This historical case does not specify a supported webhook user journey; add one only after the public trigger URL, authentication, payload, and retry contract are defined.
