// Vitest setup for studio app
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (media: string) => ({
      matches: false,
      media,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

// Automatically clean up rendered components after each test so DOM state
// does not leak across tests in the same file.
afterEach(() => {
  cleanup();
});
