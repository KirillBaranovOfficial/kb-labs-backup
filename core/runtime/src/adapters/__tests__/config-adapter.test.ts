import { afterEach, describe, expect, it } from 'vitest';
import { ConfigAdapter } from '../config-adapter.js';

afterEach(() => {
  delete globalThis.__KB_RAW_CONFIG__;
  delete globalThis.__KB_EFFECTIVE_CONFIG__;
});

describe('ConfigAdapter', () => {
  it('reads product configuration from the active profile only', async () => {
    globalThis.__KB_RAW_CONFIG__ = {
      knowledge: { source: 'legacy' },
      profiles: [{ id: 'default', products: { mind: { source: 'profile' } } }],
    };

    await expect(new ConfigAdapter().getConfig('mind')).resolves.toEqual({ source: 'profile' });

    globalThis.__KB_RAW_CONFIG__ = { knowledge: { source: 'legacy' } };
    await expect(new ConfigAdapter().getConfig('mind')).resolves.toBeUndefined();
  });
});
