#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { runStage } from './remote-verify-stage.mjs';

const stage = process.env.REMOTE_STAGE;
const packageNames = process.env.REMOTE_PACKAGES ? process.env.REMOTE_PACKAGES.split(',') : undefined;
const shard = process.env.REMOTE_SHARD ? Number(process.env.REMOTE_SHARD) : undefined;
const packages = process.env.AFFECTED_PACKAGES ? process.env.AFFECTED_PACKAGES.split(',').filter(Boolean) : [];
const out = process.env.REMOTE_STAGE_RESULT;
if (!stage || !out) throw new Error('REMOTE_STAGE and REMOTE_STAGE_RESULT are required by the workflow shard invocation.');
const cache = {
  pnpm: process.env.PNPM_CACHE_HIT,
  go: process.env.GO_CACHE_HIT,
  devkit: process.env.DEVKIT_CACHE_HIT,
};
const result = await runStage({
  stage,
  packageNames,
  shard,
  packages,
  cache,
  runCommand: (command, args, options) => spawnSync(command, args, { ...options, encoding: 'utf8', env: process.env, maxBuffer: 16 * 1024 * 1024 }),
});
writeFileSync(out, `${JSON.stringify(result, null, 2)}\n`);
process.stdout.write(`${result.name}: ${result.status} (${result.durationMs} ms)\n${result.logTail}\n`);
process.exitCode = result.status === 'pass' ? 0 : 1;
