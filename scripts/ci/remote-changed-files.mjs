#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';

const baseRef = process.env.KB_DEVKIT_BASE_REF;
const profile = process.env.VERIFICATION_PROFILE;
const manifestPath = process.env.KB_REMOTE_CHANGED_FILES_PATH;
if (!baseRef || !manifestPath || !['stream', 'candidate'].includes(profile)) throw new Error('VERIFICATION_PROFILE, KB_DEVKIT_BASE_REF, and KB_REMOTE_CHANGED_FILES_PATH are required by remote verification selection.');

const args = profile === 'stream'
  ? ['diff', '--name-only', '--diff-filter=ACDMRTUXB', baseRef, '--']
  : ['diff', '--name-only', '--diff-filter=ACDMRTUXB', baseRef, 'HEAD', '--'];
const paths = execFileSync('git', args, { encoding: 'utf8' }).split(/\r?\n/).filter(Boolean);
writeFileSync(manifestPath, `${JSON.stringify(paths)}\n`);
process.stdout.write(paths.length ? `${paths.join('\n')}\n` : '');
