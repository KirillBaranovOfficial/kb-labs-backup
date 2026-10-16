import { expect } from 'vitest';

const STACK_FRAME = /(?:^|\n)\s*(?:at\s+.+:\d+:\d+\)?|.+@.+:\d+:\d+)\s*(?=\n|$)/;

export function assertNoStackTraceLeak(envelope: unknown): void {
  const leaks: string[] = [];

  const inspect = (value: unknown, path: string): void => {
    if (typeof value === 'string') {
      if (STACK_FRAME.test(value)) {
        leaks.push(`${path} contains a stack frame`);
      }
      return;
    }
    if (Array.isArray(value)) {
      value.forEach((entry, index) => inspect(entry, `${path}[${index}]`));
      return;
    }
    if (value !== null && typeof value === 'object') {
      for (const [key, entry] of Object.entries(value)) {
        const childPath = `${path}.${key}`;
        if (key === 'stack') {
          leaks.push(`${childPath} is a stack property`);
        }
        inspect(entry, childPath);
      }
    }
  };

  inspect(envelope, 'envelope');
  expect(leaks).toEqual([]);
}
