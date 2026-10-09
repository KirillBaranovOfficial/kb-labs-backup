---
id: S-220
title: Gateway — validate project service URL identifiers
persona: platform-maintainer
area: gateway
priority: P1
status: active
release: false
modes: [local]
surfaces: [api]
---

## Goal

Reject project service declarations that cannot define an unambiguous public URL
namespace before the Gateway starts serving requests.

## Steps

| # | Action | Expected |
|---|---|---|
| S-220.1 | Assemble project API declarations with a duplicate `projectApi.serviceId`, then with an invalid service ID such as `rest/api` | Assembly fails with catalogued `KB_CONFIG_INVALID`, identifies `serviceManifest.projectApi`, and says to correct the manifest's `projectApi.serviceId` and `projectApi.rewritePrefix` before restarting the host. |
| S-220.2 | Configure a project API upstream that accepts a request but never returns headers, then call it through the Gateway with a finite `gateway.projectRequestTimeoutMs` | The Gateway ends the exchange with HTTP 504 and the catalogued `KB_RUNTIME_TIMEOUT` UX error, aborts the upstream request, and releases the project service lease exactly once. |
