---
id: S-019
title: S-019 — Cumulative gateway usage and quota (not shipped)
persona: solo-developer
area: gateway
priority: P1
status: historical
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

Historical product-design record for account-wide token usage and quota reporting.

## Removed capability and evidence

The current gateway exposes token usage metadata on individual LLM responses, but does not provide a cumulative usage or quota capability. The gateway route implementation returns per-response usage (`services/gateway/app/src/llm/routes.ts:177-180,211-214`); repository QA observations record that no `kb gateway usage` command or usage API endpoint exists (`docs/qa/runs/2026-06-05.md:27-28,57,156-159`). This case remains historical until cumulative usage/quota is implemented.
