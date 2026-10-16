#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { load } from 'js-yaml';

const config = load(readFileSync(new URL('../devkit.yaml', import.meta.url), 'utf8'));
const registry = config.workspace.categories['go-binary'].match;
process.stdout.write(`${JSON.stringify(registry)}\n`);
