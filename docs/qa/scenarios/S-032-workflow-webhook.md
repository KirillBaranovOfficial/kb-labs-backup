---
id: S-032
title: S-032 — Workflow sends an outbound webhook on completion (not shipped)
persona: solo-developer
area: workflow
priority: P1
status: historical
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Historical design record for a workflow automatically sending an outbound webhook after a run completes.

## Removed capability and evidence

The current workflow engine supports incoming webhook triggers (`plugins/workflow/engine/src/workflow-repository.ts:409-412`) but does not implement an outbound completion webhook. The workflow README lists run, cron, and live SSE behavior (`plugins/workflow/README.md:65-92,113-137`), and the approval ADR explicitly rejects a separate external webhook system (`plugins/workflow/docs/adr/0009-approval-steps.md:81`). This completion-notification capability is not present; incoming webhook triggers are a distinct supported capability.
