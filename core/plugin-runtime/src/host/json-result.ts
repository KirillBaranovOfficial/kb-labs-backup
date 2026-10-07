/** Normalize command output to the shared ADR-0049 CLI result envelope. */
export function normalizeJsonResult(value: unknown): Record<string, unknown> {
  return { ok: true, data: value };
}
