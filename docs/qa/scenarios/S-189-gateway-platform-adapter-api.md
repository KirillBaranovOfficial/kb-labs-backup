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

## Pass criteria

The route is `POST /platform/v1/:adapter/:method`; its static allowlist is in
`services/gateway/app/src/platform/routes.ts:21-52,88-93`. It accepts calls
through the adapter and streams async iterables as SSE ending in `[DONE]`
(`:167-206`). Unknown adapter and method responses have codes
`ADAPTER_NOT_FOUND` and `METHOD_NOT_ALLOWED` (`:109-125`), and caught adapter
errors return `ADAPTER_ERROR` without a stack (`:207-219`). However, there is
no catalog endpoint or journey claim for this API, and these current error
objects do not include the ADR-0049 corrective `hint`. Keep this case planned
until a documented/discoverable catalog and the ADR-0049 error envelope are
available and the journey can call the route.
