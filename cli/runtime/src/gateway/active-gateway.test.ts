import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const mocks = vi.hoisted(() => ({
  loadPlatformConfig: vi.fn(),
  session: null as null | { gatewayUrl: string },
}));

vi.mock('@kb-labs/core-runtime', () => ({ loadPlatformConfig: mocks.loadPlatformConfig }));
vi.mock('./session.js', () => ({
  SessionManager: class { async load() { return mocks.session; } },
}));
vi.mock('./credentials.js', () => ({
  CredentialsManager: class { async load() { return null; } },
}));

import { resolveActiveGateway } from './active-gateway.js';

let root: string;

beforeEach(async () => {
  root = await mkdtemp(path.join(os.tmpdir(), 'kb-active-gateway-'));
  mocks.loadPlatformConfig.mockReset().mockResolvedValue({ platformRoot: root });
  mocks.session = null;
});

afterEach(async () => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  await rm(root, { recursive: true, force: true });
});

describe('resolveActiveGateway', () => {
  it('uses the receipt URL even when ambient dev network variables are set', async () => {
    await mkdir(path.join(root, '.kb', 'v2'), { recursive: true });
    await writeFile(path.join(root, '.kb', 'v2', 'receipt.json'), JSON.stringify({
      id: 'installed-b', verification: { hostHealthUrl: 'http://127.0.0.1:4250/health' },
    }));
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ installationId: 'installed-b' }), { status: 200 })));
    const env = { GATEWAY_PORT: '4300', GATEWAY_HOST: '127.0.0.2', KB_NET_OFFSET: '100' };

    await expect(resolveActiveGateway({ cwd: root, env })).resolves.toMatchObject({
      gatewayUrl: 'http://127.0.0.1:4250', installationId: 'installed-b',
    });
    expect(mocks.loadPlatformConfig).toHaveBeenCalledWith({ startDir: root, env, loadEnvFile: false });
  });

  it('uses the saved client session URL when there is no local installation receipt', async () => {
    mocks.session = { gatewayUrl: 'https://team-gateway.example.test' };

    await expect(resolveActiveGateway({
      cwd: root,
      env: { GATEWAY_PORT: '4300', KB_NET_OFFSET: '100' },
    })).resolves.toMatchObject({ gatewayUrl: 'https://team-gateway.example.test' });
  });

  it('uses workspace network variables when no receipt or client session exists', async () => {
    await expect(resolveActiveGateway({ cwd: root, env: { GATEWAY_PORT: '4300', KB_NET_OFFSET: '20' } }))
      .resolves.toMatchObject({ gatewayUrl: 'http://127.0.0.1:4320', platformRoot: root });
  });
});

describe('CLI Gateway resolution rail', () => {
  it('keeps Gateway address resolution and default-address policy in this helper', async () => {
    const cliRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');
    const helperPath = path.join(cliRoot, 'cli/runtime/src/gateway/active-gateway.ts');
    const sourceFiles: string[] = [];
    const visit = async (directory: string): Promise<void> => {
      for (const entry of await readdir(directory, { withFileTypes: true })) {
        const fullPath = path.join(directory, entry.name);
        if (entry.isDirectory()) {
          if (entry.name !== 'dist' && entry.name !== 'node_modules' && entry.name !== '__tests__') { await visit(fullPath); }
        } else if (entry.name.endsWith('.ts') && !entry.name.endsWith('.test.ts')) {
          sourceFiles.push(fullPath);
        }
      }
    };
    await visit(path.join(cliRoot, 'cli'));
    const violations: string[] = [];
    for (const sourcePath of sourceFiles) {
      if (sourcePath === helperPath) { continue; }
      const source = await (await import('node:fs/promises')).readFile(sourcePath, 'utf8');
      if (/(?:resolveCliGatewayTarget|resolveGatewayServiceAddress)\s*\(/.test(source)
        || /(?:127\.0\.0\.1|localhost):4000/.test(source)) {
        violations.push(path.relative(cliRoot, sourcePath));
      }
    }
    expect(violations).toEqual([]);
  });
});
