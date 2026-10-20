---
id: S-032
title: Workflow — Unshipped webhook and Git push trigger design
persona: solo-developer
area: workflow
priority: P1
status: historical
release: false
modes: [local, secured]
surfaces: [cli]
---

## Historical record

Workflow-level inbound webhook and Git push trigger declarations were never backed
by a delivery path. The trigger shapes are removed from the workflow schema and
conversion layer. This record does not describe plugin webhook endpoints, which
are a separate supported surface. Reconsider workflow triggers only after an
ingress route, authentication, payload, and retry contract are specified.
