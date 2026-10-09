import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { emitManifestJson } from '../../infra/devkit/tsup/emit-manifest-json.mjs';

test('shared manifest emitter serializes only the named manifest export', async () => {
  const fixture = mkdtempSync(join(tmpdir(), 'kb-manifest-json-emitter-'));
  try {
    const modulePath = join(fixture, 'manifest.mjs');
    const outputPath = join(fixture, 'package', 'dist', 'manifest.json');
    const manifest = { schema: 'kb.adapter/1', id: '@kb-labs/fixture-adapter', version: '1.0.0' };
    writeFileSync(modulePath,
      `export const manifest = ${JSON.stringify(manifest)};\nexport default { schema: 'wrong-shape' };\n`);

    await emitManifestJson({ manifestModule: modulePath, outputPath });

    assert.deepEqual(JSON.parse(readFileSync(outputPath, 'utf8')), manifest);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});
