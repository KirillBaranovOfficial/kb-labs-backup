import assert from 'node:assert/strict';
import test from 'node:test';

import { loadComposePorts } from './ports.mjs';

test('compose port validation reads service targets when host ports are configurable', () => {
  const ports = loadComposePorts();

  assert.deepEqual(ports.get('kb-gateway'), [4000]);
  assert.deepEqual(ports.get('kb-marketplace-registry'), [5071]);
});
