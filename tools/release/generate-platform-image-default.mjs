import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const outputPath = resolve(process.argv[2] ?? '.kb/deploy/image-default/kb.config.json');
const config = {
  platform: { deployment: { topology: 'cloud' } },
  gateway: {
    access: { mode: 'secured' },
    auth: { bootstrap: { adminEmail: 'admin@example.com' } },
  },
};

await mkdir(dirname(outputPath), { recursive: true });
await writeFile(outputPath, `${JSON.stringify(config, null, 2)}\n`);
