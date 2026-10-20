---
id: S-250
title: Gateway — provision and deliver a plugin webhook
persona: plugin-developer
area: gateway
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, api]
---

## Goal

Provision a secret for a manifest-declared plugin webhook through the Gateway REST API, receive a signed external delivery, and verify the handler result and delivery failures.

## Prerequisites

- [ ] A running Gateway and a plugin installed in the active namespace with a manifest-declared single-instance webhook using HMAC authentication and a deterministic handler.
- [ ] In secured mode, an authenticated user or machine token for the namespace; in local mode, the local Gateway identity is used.
- [ ] A client that can send the manifest's configured signature header and the required `x-kb-namespace` header.

## Steps

| # | Action | Expected |
|---|---|---|
| S-250.1 | POST `{"pluginId":"<PLUGIN_ID>","event":"<EVENT>"}` to `/api/v1/webhooks/provision` with the active Gateway credentials | HTTP 200 returns the webhook delivery URL and the generated secret once; a subsequent authenticated GET `/api/v1/webhooks` lists that declared endpoint with `provisioned: true` and never returns the secret. |
| S-250.2 | Deliver the signed fixture payload to the returned webhook URL; inspect the result with authenticated `GET /api/v1/webhooks/history/<webhookId>` and `kb webhook history <webhookId>` | HTTP 200 returns `{ "status": "ok", "webhookId": "<id>", "result": <handler-result> }`; the plugin handler receives the payload through the plugin execution boundary. Both history readers show the same durable record: webhook ID, project, received-at time, plugin and handler identity, `delivered` outcome, duration, and correlation ID. No payload or secret is stored. A handler failure is recorded as `handler_error` with its catalogued code and is inspectable through both readers. The default history retention is bounded to the latest 100 records per project, plugin, event, and instance endpoint. |
| S-250.3 | Repeat S-250.2 with an invalid signature and with a body larger than the 1 MiB Gateway body limit, then inspect each result with authenticated `GET /api/v1/webhooks/history/<webhookId>` and `kb webhook history <webhookId>` | The signature request returns HTTP 401 with the Gateway authentication error envelope; the oversized request returns HTTP 413 with catalogued code `KB_RUNTIME_PAYLOAD_TOO_LARGE`; both include `X-Webhook-Id`, and neither invokes the handler. Both readers show exactly one `rejected` record for each request, with its rejection code, webhook ID, project, received-at time, plugin and handler identity, duration, and correlation ID; no payload or secret is stored. |
| S-250.4 | POST to `/webhooks/<UNKNOWN_PLUGIN>/<UNKNOWN_EVENT>` with a validly shaped request | HTTP 404 returns `{ "error": "Webhook not found" }`; no plugin handler is invoked. |
| S-250.5 | DELETE `/api/v1/webhooks/<PLUGIN_ID>/<EVENT>` with the active Gateway credentials, then deliver again using the former secret | The revoke request returns HTTP 200; delivery with the former secret returns HTTP 401 because the secret has been removed. |
| S-250.6 | In secured mode, repeat the provision request without a credential and repeat it with an invalid credential | Each request returns HTTP 401 and no secret is created. |

## Decision note

**D48 / D72:** Per ADR-0023, webhook delivery invokes only the manifest-declared plugin handler; it does not start a workflow. Every delivery attempt admitted to a declared Gateway webhook route produces one durable, payload-free history record, including authentication, signature, and body-size rejection and handler failure. Operators inspect records through REST and CLI. History keeps the latest 100 records per webhook endpoint (project, plugin, event, and instance) by default, with pruning owned by the persistence store.

## Source

The webhook history writer is `services/gateway/app/src/webhook/history-store.ts`, called at the Gateway ingress boundary in `services/gateway/app/src/webhook/router.ts`; authenticated REST inspection is in `services/gateway/app/src/webhook/admin-routes.ts`, and the CLI reader is `cli/commands/src/commands/system/webhook/webhook-history.ts`. `docs/adr/0023-webhook-system.md` defines manifest-declared webhook handlers and secret provisioning. `docs/product/decisions.md` says inbound workflow triggers are unsupported.
