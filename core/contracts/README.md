# @kb-labs/core-contracts

Core system interface contracts — zero runtime dependencies, zero implementations.

## Overview

Defines backend-independent execution data contracts and platform interface boundaries. Execution backend construction and normalized results are owned by `@kb-labs/plugin-execution-factory`.

## Interfaces

### `IPlatformGateway`

Low-level RPC interface for IPC/HTTP communication to platform services (used by child processes to call parent-side adapters):

```typescript
import type { IPlatformGateway, RequestContext } from '@kb-labs/core-contracts';

// Implementations: IPCGateway (child process), HTTPGateway (remote)
const result = await gateway.call({
  adapter: 'vectorStore',
  method: 'search',
  args: [queryVector, { limit: 10 }],
});
```

### `ISubprocessRunner`

Contract for spawning and managing subprocess workers:

```typescript
import type { ISubprocessRunner } from '@kb-labs/core-platform';

const runner: ISubprocessRunner = {
  spawn(options): ChildProcess { ... },
  kill(pid: number): void { ... }
};
```

## Observability Contract

`@kb-labs/core-contracts` also defines the platform observability contract for HTTP services.

Core constants:

- `OBSERVABILITY_CONTRACT_VERSION` — current contract version
- `OBSERVABILITY_SCHEMA` — schema id for observability payloads
- `OBSERVABILITY_CAPABILITIES` — optional capability vocabulary
- `CANONICAL_OBSERVABILITY_METRICS` — canonical Prometheus metric families
- `CANONICAL_SERVICE_LOG_FIELDS` — canonical structured log fields for service correlation

Core payloads:

- `ServiceObservabilityDescribe` — static service identity and capabilities
- `ServiceObservabilityHealth` — current structured health and runtime snapshot
- `ResourceSnapshot` — CPU, RSS, heap, event loop lag, active operations
- `ServiceOperationSample` — top operations with duration/error context
- `EvidenceBundle` — structured machine-readable evidence for future diagnosis and agent workflows

Expected service surfaces:

- `GET /metrics`
- `GET /observability/describe`
- `GET /observability/health`

Required contract fields:

- `contractVersion`
- `serviceId`
- `instanceId`
- `serviceType`
- `version`
- `environment`
- `startedAt`
- `dependencies`
- `metricsEndpoint`
- `healthEndpoint`
- `logsSource`
- `capabilities`

Recommended role split:

- `/health` — cheap public liveness or service snapshot for humans and orchestration
- `/ready` — readiness gate for startup and traffic admission
- `/observability/*` — structured diagnostics and runtime context for collectors, Studio, and agents

Validation helpers:

- `validateServiceObservabilityDescribe()` — validates contract version, capabilities, and required fields
- `validateServiceObservabilityHealth()` — validates structured runtime diagnostics payloads
- `checkCanonicalObservabilityMetrics()` — validates canonical Prometheus metric families

Canonical structured log correlation baseline:

- always present on structured service logs:
  - `serviceId`
  - `instanceId`
  - `logsSource`
- present on correlated operation and request logs where applicable:
  - `requestId`
  - `reqId`
  - `traceId`
  - `operation`
  - `route`
  - `method`
  - `url`

Correlation guidance:

- `route` must be normalized and template-based, never a raw high-cardinality path
- `operation` must use bounded names from the service's domain vocabulary
- shared helpers from `@kb-labs/shared-http` should be used so services emit the same field names and normalization rules

Onboarding rule:

- A new HTTP service integrates by implementing the contract and passing the shared compliance checks.
- Future collectors or plugins must not require service-specific parser logic for contract-compliant services.

## Types

| Type | Description |
|------|-------------|
| `ExecutionRequestMeta` | Metadata attached to a request (traceId, tenantId, priority) |
| `HealthStatus` | Backend health check response |
| `ExecutionStats` | Runtime metrics (queue depth, active requests) |
| `VectorQuery` / `VectorSearchResult` | Vector store RPC shapes |
| `LLMOptions` / `LLMResponse` | LLM adapter RPC shapes |

Execution request/response, subprocess runner, host resolver, and transport contracts are owned by `@kb-labs/core-platform`; import them from that package.

## License

KB Public License v1.1 © KB Labs
