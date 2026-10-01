---
id: S-193
title: Security — identify configured external destinations and local-only work
persona: security-conscious-administrator
area: security
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Understand which configured services can receive platform data, confirm
credentials stay secret, and verify a workflow with no external adapter makes
no outbound provider calls.

## Prerequisites

- [ ] An isolated platform with a configured external LLM provider and registry.
- [ ] A local-only workflow that uses no external adapters and a request recorder for outbound destinations.

## Steps

| # | Action | Expected |
|---|---|---|
| S-193.1 | Inspect the platform settings or diagnostics for the configured LLM provider and package registry | The user-visible settings or diagnostic result names each configured external destination and the operation that can send data there; it does not reveal secret values. |
| S-193.2 | Run the local-only workflow and inspect the outbound request recorder | The workflow completes without requests to an external LLM, vector provider, registry, or other external destination. |
| S-193.3 | Inspect settings and diagnostic output after the workflow | The configured destination names remain visible while secret values remain absent from output. |

## Pass criteria

The website promises “Nothing is sent externally without explicit user consent”
(`sites/web/apps/web/messages/en/security.json:5,39-55,113-127`); the product
also makes network requests to registry/release distribution and configured
LLM/vector providers. S-017 covers keeping the configured LLM key out of config
and output (`docs/qa/scenarios/S-017-gateway-own-key.md:17-34`), but no source-backed settings or diagnostic surface
found for enumerating external destinations, and no workflow contract proves
that a local-only run blocks all external calls. Keep this case planned and
outside release until the destination disclosure surface and local-only
network boundary are defined and observable.
