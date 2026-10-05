---
id: S-189
title: Gateway — call an allowed adapter through the platform API
persona: service-developer
area: gateway
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [api]
---

## Goal

Discover the callable platform adapter methods, invoke a configured method,
and receive typed, client-safe errors for unsupported calls while streaming
async iterable results.

## Prerequisites

- [ ] A running gateway with an authenticated API client and configured analytics and LLM adapters.
- [ ] A fixture for a successful analytics call and a streaming LLM response.

## Steps

| # | Action | Expected |
|---|---|---|
| S-189.1 | Request the documented Platform API adapter/method catalog | The catalog lists only the supported pairs: `llm` (`complete`, `stream`, `chatWithTools`), `cache` (`get`, `set`, `delete`, `clear`), `vectorStore` (`search`, `upsert`, `delete`, `count`), `analytics` (`track`, `identify`, `flush`, `getEvents`, `getStats`, `getDailyStats`), `embeddings` (`embed`), `storage` (`read`, `write`, `delete`, `list`, `exists`), `eventBus` (`publish`, `subscribe`), `documentDatabase` (`find`, `findById`, `count`, `insertOne`, `insertMany`, `updateOne`, `updateMany`, `updateById`, `deleteMany`, `deleteById`, `bulkWrite`, `ensureCollection`, `ping`), and `kvStore` (`get`, `getMany`, `set`, `setMany`, `setIfNotExists`, `delete`, `exists`, `cas`, `incr`, `ttl`, `expire`, `persist`, `ping`). |
| S-189.2 | Send an authenticated `POST /platform/v1/analytics/track` with `{"args":["case.gateway.call",{"source":"case-fixture"}]}` | HTTP 200 returns the successful platform-call envelope and the event is recorded by analytics. |
| S-189.3 | Send authenticated calls to `POST /platform/v1/unknownAdapter/method` and `/platform/v1/analytics/unknownMethod` | Each returns a typed ADR-0049 error naming the unknown adapter or disallowed method, with a corrective hint and no stack trace in the response. |
| S-189.4 | Send an authenticated `POST /platform/v1/llm/stream` with a request that returns an `AsyncIterable` | The response uses `text/event-stream`, yields each result chunk, and ends with the `[DONE]` event. |
| S-189.5 | Send an authenticated stream request whose mock provider rejects the key before its first chunk, then a request whose provider stream becomes malformed after one chunk; check gateway health after each request | Before SSE headers, HTTP 502 contains `KB_CONFIG_LLM_KEY_INVALID`, a plain-language message, correlation ID, and a hint with the exact `kb config set --scope platform platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}'` command. After streaming starts, the terminal `event: error` contains catalogued `KB_CONFIG_LLM_PROVIDER_UNAVAILABLE`, a hint with the exact `kb config get platform.adapterOptions.llm.baseURL --scope platform --json` command, and no `[DONE]`. Both requests end cleanly, and the gateway remains healthy. |
| S-189.6 | Send an authenticated `POST /platform/v1/llm/stream` with `{"args":[]}` | HTTP 400 contains the typed `KB_RUNTIME_INPUT_INVALID` envelope; the gateway remains healthy. |

## Pass criteria

The route is `POST /platform/v1/:adapter/:method`; its static allowlist is in
`services/gateway/app/src/platform/routes.ts`. It streams async iterables as
SSE ending in `[DONE]`, emits catalogued provider errors when the adapter fails,
and returns `KB_RUNTIME_INPUT_INVALID` for a stream request without a prompt.
The case remains planned until S-189.1 has a documented adapter catalog endpoint
and the installed journey passes all steps.
