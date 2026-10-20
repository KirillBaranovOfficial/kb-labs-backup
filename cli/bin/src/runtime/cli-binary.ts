import manifest from '../../package.json' with { type: 'json' };

const registeredBinaryNames = Object.keys(manifest.bin);
if (registeredBinaryNames.length !== 1 || !registeredBinaryNames[0]) {
  throw new Error(`CLI bin registry must declare exactly one binary; found ${registeredBinaryNames.length}`);
}

export const cliBinaryName = registeredBinaryNames[0];
