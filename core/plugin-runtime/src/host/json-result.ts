import { toErrorEnvelope, type ToEnvelopeOptions } from '@kb-labs/core-platform';

/** Normalize command output to the shared ADR-0049 CLI result envelope. */
export function normalizeJsonResult(value: unknown, errorOptions: ToEnvelopeOptions = {}): Record<string, unknown> {
  if (value !== null && typeof value === 'object' && 'ok' in value) {
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
      return { ok: false, error: toErrorEnvelope(result.error, errorOptions) };
    }
  }
  return { ok: true, data: value };
}
