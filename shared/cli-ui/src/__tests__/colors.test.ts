import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { resolveColorSupport } from '../color-policy.js';

const cases = JSON.parse(readFileSync(new URL('../../../../tools/clikit/ui/color-cases.json', import.meta.url), 'utf8')) as Array<{
  name: string; tty: boolean; noColor: string; forceColor: string; term?: string; want: boolean;
}>;

describe('CLI color policy', () => {
  it.each(cases)('$name', ({ tty, noColor, forceColor, term, want }) => {
    expect(resolveColorSupport(tty, noColor, forceColor, term)).toBe(want);
  });
});
