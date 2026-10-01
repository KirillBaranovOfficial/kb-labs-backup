import { readFileSync } from 'node:fs';

export function readPlatformReleaseConfig(path) {
  const source = readFileSync(path, 'utf8');
  const json = path.endsWith('.jsonc') ? stripJsonComments(source) : source;
  const config = JSON.parse(json);
  const flow = config.profiles?.find(profile => profile.id === 'default')?.products?.release?.flows?.platform;
  const adapters = config.platform?.adapters;
  if (!Array.isArray(flow?.platformMembers) || flow.platformMembers.length === 0) {
    throw new Error('release.flows.platform.platformMembers must be a non-empty array');
  }
  if (!adapters || typeof adapters !== 'object' || Array.isArray(adapters)) {
    throw new Error('platform.adapters is required for a platform release index');
  }
  return {
    platformMembers: flow.platformMembers,
    platformRequires: Object.keys(adapters).filter(slot => Boolean(adapters[slot])),
    adapters,
    adapterOptions: config.platform.adapterOptions ?? {},
  };
}

function stripJsonComments(source) {
  let output = '';
  let inString = false;
  let escaped = false;
  for (let index = 0; index < source.length; index += 1) {
    const current = source[index];
    const next = source[index + 1];
    if (inString) {
      output += current;
      if (escaped) escaped = false;
      else if (current === '\\') escaped = true;
      else if (current === '"') inString = false;
    } else if (current === '"') {
      inString = true;
      output += current;
    } else if (current === '/' && next === '/') {
      while (index < source.length && source[index] !== '\n') index += 1;
      output += '\n';
    } else if (current === '/' && next === '*') {
      index += 2;
      while (index < source.length && !(source[index] === '*' && source[index + 1] === '/')) index += 1;
      index += 1;
    } else {
      output += current;
    }
  }
  return output.replace(/,(\s*[}\]])/g, '$1');
}
