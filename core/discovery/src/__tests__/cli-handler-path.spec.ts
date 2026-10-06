import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { resolveCliHandlerPath } from '../resolve-cli-handler.js';

describe('resolveCliHandlerPath', () => {
  // case: S-203.18
  it('resolves source-relative declarations under dist and keeps explicit dist paths', () => {
    const packageRoot = path.resolve('/workspace/plugins/example');
    expect(resolveCliHandlerPath(packageRoot, './commands/run.js#default'))
      .toBe(path.join(packageRoot, 'dist', 'commands', 'run.js'));
    expect(resolveCliHandlerPath(packageRoot, 'dist/commands/run.js#default'))
      .toBe(path.join(packageRoot, 'dist', 'commands', 'run.js'));
  });
});
