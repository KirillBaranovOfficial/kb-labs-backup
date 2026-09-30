import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { defineCommand, handleError, type CLIInput, type CommandResult, type PluginContextV3 } from '@kb-labs/sdk';

interface InitFlags { json?: boolean; }

const EXAMPLE = `name: hello
version: 1.0.0
description: A local workflow example
on:
  manual: true
jobs:
  hello:
    runsOn: local
    steps:
      - name: Print marker
        id: hello
        uses: builtin:shell
        with:
          command: echo hello-workflow
`;

export default defineCommand<unknown, CLIInput<InitFlags>, unknown>({
  id: 'workflow:init',
  description: 'Create a runnable local workflow example',
  handler: {
    async execute(ctx: PluginContextV3, input: CLIInput<InitFlags>): Promise<CommandResult> {
      const output = join(process.cwd(), '.kb', 'workflows', 'hello.yml');
      try {
        await mkdir(join(process.cwd(), '.kb', 'workflows'), { recursive: true });
        await writeFile(output, EXAMPLE, { encoding: 'utf8', flag: 'wx' });
        const result = { ok: true, path: '.kb/workflows/hello.yml' };
        if (input.flags.json) { ctx.ui?.json?.(result); }
        else {
          ctx.ui?.success?.('Created .kb/workflows/hello.yml');
          ctx.ui?.write?.('Next: kb workflow lint && kb workflow run --workflow-id=hello\n');
        }
        return { ok: true, result };
      } catch (error) {
        if (error instanceof Error && 'code' in error && error.code === 'EEXIST') {
          const message = 'Workflow file already exists: .kb/workflows/hello.yml';
          if (input.flags.json) { ctx.ui?.json?.({ ok: false, error: message }); }
          else { ctx.ui?.error?.(`${message}. Choose another project or rename the existing file.`); }
          return { ok: false, error: message };
        }
        handleError(ctx, error, input.flags.json ?? false);
        return { ok: false, error: 'Could not create workflow example' };
      }
    },
  },
});
