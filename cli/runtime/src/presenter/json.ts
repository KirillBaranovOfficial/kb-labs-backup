import type { Presenter } from "./types";
import type { SystemContext } from "../cli-context";
import { createErrorEnvelope, getCatalogEntry } from "@kb-labs/core-platform";

export function normalizeJsonResult(value: unknown): Record<string, unknown> {
  if (value !== null && typeof value === "object" && "ok" in value) {
    const result = value as Record<string, unknown>;
    if (result.ok === true) {
      const data = { ...result };
      const meta = data.meta;
      const payload = data.data;
      delete data.ok;
      delete data.meta;
      delete data.data;
      return {
        ok: true,
        data: payload === undefined ? data : payload,
        ...(meta === undefined ? {} : { meta }),
      };
    }
    if (result.ok === false) {
      const rawError = result.error;
      const error = rawError !== null && typeof rawError === "object"
        ? rawError as Record<string, unknown>
        : { message: String(rawError ?? "Command failed") };
      const code = typeof error.code === "string" && getCatalogEntry(error.code)
        ? error.code
        : "KB_RUNTIME_UNEXPECTED";
      const rawDetails = error.details !== null && typeof error.details === "object"
        ? error.details as Record<string, unknown>
        : {};
      const details = Object.fromEntries(
        Object.entries(rawDetails).filter((entry): entry is [string, string] => typeof entry[1] === "string"),
      );
      const envelope = createErrorEnvelope(code, {
        ...(Object.keys(details).length > 0 ? { details } : {}),
        ...(typeof error.message === "string" ? { message: error.message } : {}),
        ...(typeof error.hint === "string" ? { hint: error.hint } : {}),
        ...(typeof error.correlationId === "string"
          ? { correlationId: error.correlationId }
          : code === "KB_RUNTIME_UNEXPECTED" ? { correlationId: crypto.randomUUID() } : {}),
      });
      return { ok: false, error: envelope };
    }
  }
  return { ok: true, data: value };
}

export function createJsonPresenter(): Presenter & {
  setContext(context: SystemContext): void;
} {
  let _context: SystemContext | null = null;

  return {
    isTTY: false,
    isQuiet: false,
    isJSON: true,
    write: (_line) => {},
    info: (_line) => {},
    warn: (_line) => {},
    error: (line) =>
      console.log(JSON.stringify(normalizeJsonResult({ ok: false, error: { message: line } }))),
    json: (payload) => {
      console.log(JSON.stringify(normalizeJsonResult(payload)));
      // System commands use simple JSON output
    },
    setContext(ctx: SystemContext) {
      _context = ctx;
    },
  };
}

export type JsonPresenter = ReturnType<typeof createJsonPresenter>;
