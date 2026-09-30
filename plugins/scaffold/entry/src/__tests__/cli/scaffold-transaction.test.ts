import { afterEach, describe, expect, it } from 'vitest';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { writeAndRegisterScaffold } from '../../commands/scaffold-transaction.js';

const roots: string[] = [];

async function makeRoot(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), 'kb-scaffold-transaction-'));
  roots.push(root);
  return root;
}

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe('writeAndRegisterScaffold', () => {
  it('removes newly generated files when marketplace registration fails', async () => {
    const parent = await makeRoot();
    const outRoot = join(parent, 'plugin');

    await expect(writeAndRegisterScaffold(
      outRoot,
      async () => { await mkdir(outRoot); await writeFile(join(outRoot, 'package.json'), '{}'); },
      async () => { throw new Error('registration rejected'); },
    )).rejects.toThrow('registration rejected');

    await expect(readFile(outRoot)).rejects.toMatchObject({ code: 'ENOENT' });
  });

  it('restores previous output when forced replacement cannot be registered', async () => {
    const parent = await makeRoot();
    const outRoot = join(parent, 'plugin');
    await mkdir(outRoot);
    await writeFile(join(outRoot, 'previous.txt'), 'keep');

    await expect(writeAndRegisterScaffold(
      outRoot,
      async () => { await mkdir(outRoot); await writeFile(join(outRoot, 'new.txt'), 'new'); },
      async () => { throw new Error('registration rejected'); },
    )).rejects.toThrow('registration rejected');

    expect(await readFile(join(outRoot, 'previous.txt'), 'utf8')).toBe('keep');
  });
});
