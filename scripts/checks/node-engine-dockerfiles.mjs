import { readFileSync } from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

export function trackedDockerfiles(repositoryRoot) {
  const result = spawnSync('git', ['ls-files', '-z', '--', '*Dockerfile*'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
  });
  if (result.status !== 0) {
    throw new Error(result.stderr.trim() || `git ls-files exited ${result.status}`);
  }
  return result.stdout.split('\0').filter(Boolean);
}

export function parseDockerfileFromInstructions(source) {
  const lines = source.split(/\r?\n/);
  const escape = dockerfileEscapeCharacter(lines);
  const instructions = [];
  const globalArgs = new Map();
  let sawFrom = false;
  for (let index = 0; index < lines.length;) {
    const firstLine = index + 1;
    let instruction = lines[index].trim();
    index += 1;
    while (endsWithUnescaped(instruction, escape) && index < lines.length) {
      instruction = `${instruction.slice(0, -1)} ${lines[index].trim()}`;
      index += 1;
    }
    if (instruction === '' || instruction.startsWith('#')) continue;

    const tokens = instruction.split(/\s+/);
    const keyword = tokens.shift().toUpperCase();
    if (keyword === 'ARG' && !sawFrom) {
      const assignment = /^([A-Za-z_][A-Za-z0-9_]*)(?:=(.*))?$/.exec(tokens[0] ?? '');
      if (assignment?.[2] !== undefined) globalArgs.set(assignment[1], assignment[2]);
      continue;
    }
    if (keyword !== 'FROM') continue;
    sawFrom = true;

    if (tokens[0]?.startsWith('--')) tokens.shift();
    const image = tokens.shift();
    if (!image || (tokens.length > 0 && (tokens.length !== 2 || tokens[0].toUpperCase() !== 'AS'))) {
      instructions.push({ line: firstLine, error: 'malformed FROM instruction' });
      continue;
    }
    try {
      instructions.push({ line: firstLine, image: resolveImageArguments(image, globalArgs) });
    } catch (error) {
      instructions.push({ line: firstLine, error: error.message });
    }
  }
  return instructions;
}

export function nodeImageIssues(repositoryRoot, requiredMajor) {
  const issues = [];
  for (const relativeFile of trackedDockerfiles(repositoryRoot)) {
    const absoluteFile = path.join(repositoryRoot, relativeFile);
    const source = readFileSync(absoluteFile, 'utf8');
    for (const from of parseDockerfileFromInstructions(source)) {
      if (from.error) {
        issues.push({ file: `${relativeFile}:${from.line}`, message: from.error });
        continue;
      }

      const image = from.image;
      if (image !== 'node' && !image.startsWith('node:') && !image.startsWith('node@')) continue;

      const tag = image.startsWith('node:') ? image.slice('node:'.length).split('@')[0] : '';
      const match = /^(0|[1-9]\d*)(?:[.-]|$)/.exec(tag);
      if (!match) {
        issues.push({
          file: `${relativeFile}:${from.line}`,
          message: `FROM image ${JSON.stringify(image)} must have a numeric Node major tag matching ${requiredMajor}.x`,
        });
        continue;
      }
      const imageMajor = Number(match[1]);
      if (imageMajor !== requiredMajor) {
        issues.push({
          file: `${relativeFile}:${from.line}`,
          message: `FROM image ${JSON.stringify(image)} uses Node ${imageMajor}; root package.json requires ${requiredMajor}.x`,
        });
      }
    }
  }
  return issues;
}

function resolveImageArguments(image, globalArgs) {
  return image.replace(/\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g, (_match, braced, bare) => {
    const name = braced ?? bare;
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || !globalArgs.has(name)) {
      throw new Error(`FROM image ${JSON.stringify(image)} uses an unresolved ARG or variable`);
    }
    return globalArgs.get(name);
  });
}

function dockerfileEscapeCharacter(lines) {
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed === '') continue;
    if (!trimmed.startsWith('#')) break;
    const directive = /^#\s*escape\s*=\s*([`\\])\s*$/i.exec(trimmed);
    if (directive) return directive[1];
  }
  return '\\';
}

function endsWithUnescaped(instruction, escape) {
  let count = 0;
  for (let index = instruction.length - 1; index >= 0 && instruction[index] === escape; index -= 1) count += 1;
  return count % 2 === 1;
}
