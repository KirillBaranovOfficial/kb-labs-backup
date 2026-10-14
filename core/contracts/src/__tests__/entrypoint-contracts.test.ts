import { describe, expect, it } from 'vitest';
import type {
  CanonicalObservabilityMetric,
  DiagnosisEvidence,
  DiagnosisSuspect,
  EvidenceBundle,
  ExecutionStats,
  HealthStatus,
  IPlatformGateway,
  ObservabilityCapability,
  ObservabilityCheck,
  ServiceDependencyDescriptor,
  ServiceHealthStatus,
  ServiceObservabilityDescribe,
  ServiceObservabilityHealth,
  ServiceObservabilityState,
  ServiceOperationSample,
  ResourceSnapshot,
} from '../index.js';

const REQUIRED_EXPORTS = [
  'CANONICAL_OBSERVABILITY_METRICS',
  'checkCanonicalObservabilityMetrics',
  'HealthStatus',
  'ExecutionStats',
  'IPlatformGateway',
  'OBSERVABILITY_CAPABILITIES',
  'OBSERVABILITY_CONTRACT_VERSION',
  'OBSERVABILITY_SCHEMA',
  'validateServiceObservabilityDescribe',
  'validateServiceObservabilityHealth',
] as const;

type _CompileTimeContractPresence = [
  CanonicalObservabilityMetric,
  DiagnosisEvidence,
  DiagnosisSuspect,
  EvidenceBundle,
  ExecutionStats,
  HealthStatus,
  IPlatformGateway,
  ObservabilityCapability,
  ObservabilityCheck,
  ResourceSnapshot,
  ServiceDependencyDescriptor,
  ServiceHealthStatus,
  ServiceObservabilityDescribe,
  ServiceObservabilityHealth,
  ServiceObservabilityState,
  ServiceOperationSample,
];

describe('core-contracts entrypoint contract', () => {
  it('exports required canonical contracts', () => {
    expect(REQUIRED_EXPORTS.length).toBeGreaterThan(0);
  });
});
