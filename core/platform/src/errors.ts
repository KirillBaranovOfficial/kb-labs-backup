/**
 * @module @kb-labs/core-platform/errors
 *
 * Platform-level error types shared by all adapter implementations.
 */

/**
 * Thrown by NoOp adapter implementations when a plugin tries to use a
 * platform service that has no honest fallback available (e.g. LLM,
 * Embeddings) and the operator hasn't configured a real adapter.
 *
 * Distinct from generic `Error` so callers can branch on it:
 *
 * ```ts
 * try {
 *   await platform.llm.complete(...);
 * } catch (err) {
 *   if (err instanceof AdapterUnavailableError) {
 *     // surface to user: "configure an LLM adapter"
 *   }
 * }
 * ```
 *
 * The gateway HTTP error mapper converts this into a structured response
 * `{code: 'ADAPTER_UNAVAILABLE', slot, reason}` for REST consumers.
 */
export class AdapterUnavailableError extends Error {
  /** Adapter slot, e.g. 'llm', 'embeddings', 'notifier'. */
  public readonly slot: string;
  /** Why the adapter is unavailable. */
  public readonly reason: 'not-configured' | 'load-failed';
  /** Optional corrective hint for a known adapter load failure. */
  public readonly hint?: string;
  /** Optional user-facing explanation for a known adapter load failure. */
  public readonly userMessage?: string;
  /** Optional copy-ready corrective command. */
  public readonly action?: { id: string; label: string; command: string };

  constructor(
    slot: string,
    reason: 'not-configured' | 'load-failed' = 'not-configured',
    message?: string,
    cause?: unknown,
    options?: { hint?: string; userMessage?: string; action?: { id: string; label: string; command: string } },
  ) {
    super(
      message ??
        `Adapter "${slot}" is not available (reason: ${reason}). ` +
          `Configure a real adapter in kb.config.json under \`adapters.${slot}\`.`,
      cause === undefined ? undefined : { cause },
    );
    this.name = 'AdapterUnavailableError';
    this.slot = slot;
    this.reason = reason;
    this.hint = options?.hint;
    this.userMessage = options?.userMessage;
    this.action = options?.action;
    Object.setPrototypeOf(this, AdapterUnavailableError.prototype);
  }
}

/** A bound adapter cannot run because a required credential is absent. */
export class AdapterCredentialsMissingError extends Error {
  public readonly slot: string;
  public readonly credential: string;
  public readonly configurationPath: string;

  constructor(slot: string, credential: string, configurationPath: string) {
    super(`Adapter "${slot}" is missing ${credential}. No request was sent.`);
    this.name = 'AdapterCredentialsMissingError';
    this.slot = slot;
    this.credential = credential;
    this.configurationPath = configurationPath;
    Object.setPrototypeOf(this, AdapterCredentialsMissingError.prototype);
  }
}
