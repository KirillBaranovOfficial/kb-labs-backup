#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { load } from 'js-yaml';

export function readGoBinaryRegistry(configPath = new URL('../devkit.yaml', import.meta.url)) {
  const config = load(readFileSync(configPath, 'utf8'));
  const registry = config?.workspace?.categories?.['go-binary']?.match;
  if (!Array.isArray(registry) || registry.some((pattern) => typeof pattern !== 'string' || !pattern)) {
    throw new Error('devkit go-binary registry must contain a non-empty match array');
  }
  return registry;
}

const configFlag = process.argv.indexOf('--config');
const configPath = configFlag >= 0 ? process.argv[configFlag + 1] : undefined;
if (configFlag >= 0 && !configPath) throw new Error('--config requires a path');
const registry = readGoBinaryRegistry(configPath);
process.stdout.write(`${JSON.stringify(registry)}\n`);
