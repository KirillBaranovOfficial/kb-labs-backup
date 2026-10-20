/**
 * @module @kb-labs/core-contracts
 *
 * Core contracts - Interfaces and types for KB Labs core systems.
 *
 * This package contains all contract definitions (interfaces-only, no implementations):
 * - Platform gateway contracts (IPlatformGateway, RequestContext)
 *
 * Future: Platform contracts, Workflow contracts, etc.
 */

// Execution backend lifecycle metrics
export * from "./execution-backend.js";

// Platform gateway interface
export * from "./platform-gateway.js";

// Cancellation
export * from "./cancellation.js";

// Execution events
export * from "./execution-events.js";

// Retry
export * from "./retry.js";

// Observability
export * from "./observability.js";
export {
  CANONICAL_SERVICE_LOG_FIELDS,
  type CanonicalServiceLogField,
  type ServiceLogCorrelationContext,
} from "./observability.js";

// Identity & authentication (ADR-0020)
export * from "./permissions.js";
export * from "./identity-provider.js";
export * from "./policy.js";

// Project registry (ADR-0044)
export * from "./project-registry.js";
