/**
 * @module @kb-labs/host-app/envelope
 *
 * Response envelope shared by every host-owned `/api/v1/*` route
 * (`projects-routes.ts`, `plugins-routes.ts`): `{ ok:
 * true, data }` / `{ ok: false, error }`, where `error` is the unified error
 * envelope (ADR-0049). Mirrors the REST API's shape so Studio's HTTP client
 * and error mapper handle every host route unchanged.
 */

import type { FastifyReply } from "fastify";
import { createErrorEnvelope, normalizeHttpFailure, statusForHttpFailure, type ErrorEnvelope } from "@kb-labs/core-platform";

export function sendEnvelope(reply: FastifyReply, envelope: ErrorEnvelope): FastifyReply {
  const result = normalizeHttpFailure(envelope, {
    correlationId: String(reply.request.id || ""),
    statusCode: 500,
  });
  return reply.code(statusForHttpFailure(result, 500)).send(result);
}

export function sendData(reply: FastifyReply, data: unknown, status = 200): FastifyReply {
  return reply.code(status).send({ ok: true, data });
}

export function invalidBody(field: string, expected: string): ErrorEnvelope {
  return createErrorEnvelope("KB_RUNTIME_INPUT_INVALID", {
    cause: `"${field}" ${expected}.`,
    details: { field },
  });
}
