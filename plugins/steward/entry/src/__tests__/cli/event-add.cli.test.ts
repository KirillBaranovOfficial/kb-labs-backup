import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mockCLIInput, createCapturedUI, createMockContext } from '@kb-labs/sdk/testing';

vi.mock('@kb-labs/steward-core', () => ({
  addEvent: vi.fn(),
}));

import { addEvent } from '@kb-labs/steward-core';
import command from '../../commands/event-add.js';

const mockedAddEvent = vi.mocked(addEvent);

beforeEach(() => {
  mockedAddEvent.mockReset();
});

describe('steward:event.add', () => {
  it('records a manual note against a subject', async () => {
    mockedAddEvent.mockResolvedValue({
      id: 'evt_1',
      at: 1,
      kind: 'note',
      subjectType: 'person',
      subjectId: 'per_1',
      text: 'Talked at the conference',
      createdAt: 1,
      updatedAt: 1,
    });

    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await command.execute(
      ctx,
      mockCLIInput({
        flags: { 'subject-type': 'person', 'subject-id': 'per_1', text: 'Talked at the conference' },
      }),
    );

    expect(result.ok).toBe(true);
    expect(mockedAddEvent).toHaveBeenCalledWith({
      subjectType: 'person',
      subjectId: 'per_1',
      text: 'Talked at the conference',
      kind: 'note',
    });
    expect(captured.infos[0]?.message).toContain('person:per_1');
  });

  it('rejects an unknown --subject-type', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await command.execute(
      ctx,
      mockCLIInput({ flags: { 'subject-type': 'bogus', 'subject-id': 'x' } }),
    );

    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe('KB_RUNTIME_INPUT_INVALID');
    }
    expect(mockedAddEvent).not.toHaveBeenCalled();
    if (!result.ok) { expect(result.error.hint).toBeTruthy(); }
  });

  it('missing --subject-id is a validation error', async () => {
    const { ui, captured } = createCapturedUI();
    const ctx = createMockContext({ ui });
    const result = await command.execute(ctx, mockCLIInput({ flags: { 'subject-type': 'person' } }));

    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe('KB_RUNTIME_INPUT_INVALID');
    }
    expect(mockedAddEvent).not.toHaveBeenCalled();
    if (!result.ok) { expect(result.error.hint).toBeTruthy(); }
  });
});
