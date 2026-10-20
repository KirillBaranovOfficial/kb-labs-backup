/**
 * @module @kb-labs/sdk/adapters/infra
 *
 * Public surface for infrastructure-level adapter packages that must compose
 * with platform services, resource broker queueing, and notifier wiring.
 *
 * Application-level adapters (LLM, storage, analytics, etc) should NOT import
 * from this sub-path — they need only `@kb-labs/sdk/adapters`.
 *
 * Thin re-export layer over `@kb-labs/core-runtime` and
 * `@kb-labs/core-resource-broker`.
 */

// Shared retry classification facade for infrastructure adapters.
export {
  classifyFailure,
  decideRetry,
  DEFAULT_TRANSIENT_RETRY_POLICY,
} from '@kb-labs/core-retry';

// Notifier runtime wiring (used by adapters/notifier-router)
export type {
  NotifierAdapterOptions,
  NotifierRoutingRule,
} from '@kb-labs/core-runtime';

// Resource broker (used by adapters/notifier-router for queued channels)
export type { IResourceBroker } from '@kb-labs/core-resource-broker';
export { createQueuedNotifierChannel } from '@kb-labs/core-resource-broker';
