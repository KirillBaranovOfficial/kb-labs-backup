import { afterAll } from 'vitest';
import * as fs from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';

const testHome = await fs.mkdtemp(path.join(os.tmpdir(), 'kb-plugin-runtime-home-'));
process.env.HOME = testHome;

afterAll(async () => {
  await fs.rm(testHome, { recursive: true, force: true });
});
