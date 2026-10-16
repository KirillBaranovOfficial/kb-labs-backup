import { describe, expectTypeOf, it } from 'vitest';
import type { HealthStatus, ResolvedExecutionMode } from '../execution-backend.js';

describe('resolved execution mode contract', () => {
  it('excludes roadmap remote from the mode and shared health contract', () => {
    expectTypeOf<HealthStatus['backend']>(undefined as never).toEqualTypeOf<ResolvedExecutionMode>();

    // @ts-expect-error remote is roadmap-only and cannot be reported as a resolved mode
    const remoteMode: ResolvedExecutionMode = 'remote';
    void remoteMode;
  });
});
