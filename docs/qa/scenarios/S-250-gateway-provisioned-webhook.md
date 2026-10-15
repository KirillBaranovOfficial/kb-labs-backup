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
| S-250.2 | POST the fixture payload to the returned `/webhooks/<PLUGIN_ID>/<EVENT>` URL with the namespace header and an HMAC computed with the provisioned secret, then inspect the platform's durable invocation history | HTTP 200 returns `{ "status": "ok", "webhookId": "<id>", "result": <handler-result> }`; the plugin handler receives the payload through the plugin execution boundary, and a durable user-inspectable history record contains that `webhookId` and the handler outcome. **This is a failing claim today:** no durable webhook invocation history surface exists. |
| S-250.3 | Repeat S-250.2 with an invalid signature | HTTP 401 returns the Gateway authentication error envelope; the handler is not invoked. |
| S-250.4 | POST to `/webhooks/<UNKNOWN_PLUGIN>/<UNKNOWN_EVENT>` with a validly shaped request | HTTP 404 returns `{ "error": "Webhook not found" }`; no plugin handler is invoked. |
| S-250.5 | DELETE `/api/v1/webhooks/<PLUGIN_ID>/<EVENT>` with the active Gateway credentials, then deliver again using the former secret | The revoke request returns HTTP 200; delivery with the former secret returns HTTP 401 because the secret has been removed. |
| S-250.6 | In secured mode, repeat the provision request without a credential and repeat it with an invalid credential | Each request returns HTTP 401 and no secret is created. |

## Decision note

**D48:** Per ADR-0023, webhook delivery invokes only the manifest-declared plugin handler; it does not start a workflow. The `webhookId` and handler outcome must be available in durable, user-inspectable history. The current Gateway webhook owner does not provide that history surface, so S-250.2 remains a failing claim until the Gateway webhook history owner adds it.

## Source

The webhook admin API is registered in `services/gateway/app/src/webhook/admin-routes.ts`; CLI commands wrap the same API in `cli/commands/src/commands/system/webhook/`. Delivery dispatch and its response are in `services/gateway/app/src/webhook/router.ts:264-320`. `docs/adr/0023-webhook-system.md` defines manifest-declared webhook handlers and secret provisioning. `docs/product/decisions.md` says inbound workflow triggers are unsupported. The router currently returns the successful result and logs only dispatch failures; it has no durable success history owner.
