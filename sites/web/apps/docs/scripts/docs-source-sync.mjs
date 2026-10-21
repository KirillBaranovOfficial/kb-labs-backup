import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { commandSources, sourceFragmentSources } from '../lib/doc-source-map.mjs';
import { renderGeneratedDocSource } from '../lib/expand-doc-sources.mjs';
import { parseNodeEnginePolicy } from '../../../../../core/plugin-runtime/src/platform-resolve/node-engine.mjs';

const docsDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(docsDir, '../../../..');

export function sourceFileForCommand(command) {
  return commandSources[command]?.source;
}

export function collectGeneratedSources() {
  const generated = Object.fromEntries(Object.entries(commandSources).map(([command, source]) => {
    const sourceText = readFileSync(path.join(repoRoot, source.source), 'utf8');
    return [command, { source: source.source, examples: findCommand(sourceText, source.source, command, source.selector) }];
  }));
  const nodePolicySource = sourceFragmentSources.nodePolicy;
  const rootManifest = JSON.parse(readFileSync(path.join(repoRoot, nodePolicySource), 'utf8'));
  const { requiredMajor } = parseNodeEnginePolicy(rootManifest.engines?.node);
  return {
    commands: generated,
    nodePolicy: { source: nodePolicySource, requiredMajor },
  };
}

export function sourceFileForNodePolicy() {
  return sourceFragmentSources.nodePolicy;
}

function parseStringArray(sourceText, sourcePath, command) {
  const values = [];
  for (const line of sourceText.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('//') || trimmed.startsWith('/*') || trimmed.startsWith('*')) continue;
    const match = trimmed.match(/^(['"])(.*)\1,?$/);
    if (!match) throw new Error(`Command "${command}" examples must be one string literal per line (${sourcePath})`);
    const value = match[2].replace(/\\(['"\\])/g, '$1');
    values.push(value);
  }
  return values;
}

function findCommand(sourceText, sourcePath, command, selector) {
  const declaration = new RegExp(`\\bconst\\s+${selector}\\s*=\\s*defineSystemCommand\\(\\{`);
  const commandMatch = declaration.exec(sourceText);
  if (!commandMatch) throw new Error(`Command "${command}" selector "${selector}" was not found in ${sourcePath}`);
  const start = commandMatch.index + commandMatch[0].length;
  const commandEnd = sourceText.indexOf('\n});', start);
  if (commandEnd < 0) throw new Error(`Command "${command}" declaration is unclosed in ${sourcePath}`);
  const examplesMatch = /\bexamples\s*:\s*\[/.exec(sourceText.slice(start, commandEnd));
  if (!examplesMatch) throw new Error(`Command "${command}" has no statically readable examples in ${sourcePath}`);
  const arrayStart = start + examplesMatch.index + examplesMatch[0].length;
  let cursor = arrayStart;
  let quote;
  let escapedChar = false;
  while (cursor < commandEnd) {
    const char = sourceText[cursor++];
    if (quote) {
      if (escapedChar) escapedChar = false;
      else if (char === '\\') escapedChar = true;
      else if (char === quote) quote = undefined;
    } else if (char === "'" || char === '"') {
      quote = char;
    } else if (char === ']') {
      return parseStringArray(sourceText.slice(arrayStart, cursor - 1), sourcePath, command);
    }
  }
  throw new Error(`Unclosed examples array for "${command}" (${sourcePath})`);
}

export function renderCommandExamples(command, generatedSources, match) {
  const sourcePath = commandSources[command]?.source;
  const selector = commandSources[command]?.selector;
  if (!sourcePath || !selector) throw new Error(`Command "${command}" is not in the docs source allowlist`);
  const generated = generatedSources?.[command];
  if (generated && generated.source !== sourcePath) {
    throw new Error(`Generated source for "${command}" points to unexpected path ${generated.source}`);
  }
  const sourceData = generated ?? {
    source: sourcePath,
    examples: findCommand(readFileSync(path.join(repoRoot, sourcePath), 'utf8'), sourcePath, command, selector),
  };
  if (sourceData.examples.length === 0) throw new Error(`Command "${command}" has no examples`);
  return renderGeneratedDocSource(command, { [command]: sourceData }, match);
}

export function getDocsDir() {
  return docsDir;
}
