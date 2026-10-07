import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const SUPPORTED_BINARIES = [
  { id: 'kb-create', os: 'linux', arch: 'amd64', filename: 'kb-create-linux-amd64' },
  { id: 'kb-create', os: 'linux', arch: 'arm64', filename: 'kb-create-linux-arm64' },
];

export function buildBinaryManifest(binaryDirectory, baseUrl) {
  const binaries = SUPPORTED_BINARIES.map(({ filename, ...binary }) => {
    const binaryPath = join(binaryDirectory, filename);
    if (!existsSync(binaryPath)) {
      throw new Error(`required client binary ${filename} is missing from ${binaryDirectory}`);
    }
    const contents = readFileSync(binaryPath);
    return {
      ...binary,
      url: `${baseUrl.replace(/\/$/, '')}/${filename}`,
      filename,
      sha256: createHash('sha256').update(contents).digest('hex'),
    };
  });
  return { binaries };
}
