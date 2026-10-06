import { afterEach, describe, expect, it } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import { DiscoveryManager } from '../discovery-manager.js';
import {
  createDiscoveryInputFingerprinter,
  getDiscoveryInputPaths,
} from '../discovery-inputs.js';

describe('discovery input fingerprint', () => {
  let root: string;

  afterEach(async () => {
    if (root) await fs.rm(root, { recursive: true, force: true });
  });

  it('fingerprints the root files actually read during discovery, including JSONC config', async () => {
    root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-discovery-inputs-'));
    await fs.mkdir(path.join(root, '.kb'), { recursive: true });
    await fs.mkdir(path.join(root, 'packages', 'sample'), { recursive: true });
    await fs.writeFile(path.join(root, '.kb', 'marketplace.lock'), JSON.stringify({ schema: 'kb.marketplace/3', plugins: {} }));
    await fs.writeFile(path.join(root, '.kb', 'kb.config.jsonc'), '{\n  // policy config\n  "plugins": { "allow": ["example"] },\n}\n');
    await fs.writeFile(path.join(root, 'pnpm-workspace.yaml'), 'packages:\n  - packages/*\n');
    await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({ name: 'fixture-root' }));
    await fs.writeFile(path.join(root, 'packages', 'sample', 'package.json'), JSON.stringify({ name: 'sample' }));

    const observed = new Set<string>();
    const originalReadFile = fs.readFile;
    await new DiscoveryManager({
      root,
      workspace: true,
      onInputRead: (filePath) => observed.add(path.resolve(filePath)),
    }).discover();

    const fingerprinted = new Set(await getDiscoveryInputPaths([root]));
    const rootReads = [...observed].filter((file) => path.dirname(file) === root || file.startsWith(`${root}${path.sep}.kb${path.sep}`));
    expect(rootReads).toEqual(expect.arrayContaining([
      path.join(root, '.kb', 'marketplace.lock'),
      path.join(root, '.kb', 'kb.config.jsonc'),
      path.join(root, 'pnpm-workspace.yaml'),
    ]));
    expect(rootReads.every((file) => fingerprinted.has(file))).toBe(true);
  });

  it('stats every input on acquisition and hashes only after its stat signature changes', async () => {
    root = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-discovery-fingerprint-'));
    const configPath = path.join(root, '.kb', 'kb.config.jsonc');
    const content = new Map<string, Buffer>([[configPath, Buffer.from('{"plugins":[]}')]]);
    let modifiedAt = 1000;
    let statCalls = 0;
    let readCalls = 0;
    const fingerprint = createDiscoveryInputFingerprinter({
      stat: async (file) => {
        statCalls += 1;
        const value = content.get(file);
        if (!value) throw Object.assign(new Error('missing'), { code: 'ENOENT' });
        return { mtimeMs: modifiedAt, size: value.byteLength, ino: 9 };
      },
      readFile: async (file) => {
        readCalls += 1;
        const value = content.get(file);
        if (!value) throw Object.assign(new Error('missing'), { code: 'ENOENT' });
        return value;
      },
    });

    const first = await fingerprint([root]);
    const firstStats = statCalls;
    const firstReads = readCalls;
    expect(await fingerprint([root])).toBe(first);
    expect(statCalls).toBe(firstStats * 2);
    expect(readCalls).toBe(firstReads);

    // Same-size content can be invisible while the filesystem mtime is unchanged.
    content.set(configPath, Buffer.from('{"plugins":  }'));
    expect(await fingerprint([root])).toBe(first);
    expect(readCalls).toBe(firstReads);

    // Once mtime advances, the changed stat signature causes the contents to be hashed.
    modifiedAt += 1000;
    expect(await fingerprint([root])).not.toBe(first);
    expect(readCalls).toBeGreaterThan(firstReads);
  });
});
