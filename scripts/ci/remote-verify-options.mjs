import path from 'node:path';

export function usage() {
  return 'Usage: node scripts/ci/remote-verify.mjs <stream> --out <path> | --attach <verify-ref-or-run-id> --out <path>';
}

export function parseArgs(args, cwd = process.cwd()) {
  let stream;
  let attach;
  let out;
  for (let index = 0; index < args.length; index += 1) {
    const item = args[index];
    if (item === '--attach' || item === '--out') {
      const value = args[++index];
      if (!value || value.startsWith('--')) throw new Error(`${usage()}\nMissing value for ${item}`);
      if (item === '--attach') attach = value;
      else out = value;
    } else if (!item.startsWith('--') && !stream) {
      stream = item;
    } else {
      throw new Error(`${usage()}\nUnexpected argument: ${item}`);
    }
  }
  if (!out) throw new Error(`${usage()}\nThe --out path is required; pass a destination outside the worktree, such as ../reports/<stream>.remote-verify.json.`);
  if (Boolean(attach) === Boolean(stream)) throw new Error(`${usage()}\nProvide exactly one stream name or --attach value.`);
  if (stream && !/^[a-zA-Z0-9._-]+$/.test(stream)) throw new Error(`${usage()}\nStream name may contain only letters, numbers, dot, underscore, and hyphen.`);
  return { stream, attach, out: path.resolve(cwd, out) };
}
