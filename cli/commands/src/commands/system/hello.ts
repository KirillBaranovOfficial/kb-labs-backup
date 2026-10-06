import { defineSystemCommand, type CommandResult } from '@kb-labs/shared-command-kit';
import { generateExamples } from '../../utils/generate-examples.js';

type HelloFlags = {
  json: { type: 'boolean'; description?: string };
};

type HelloResult = CommandResult & {
  message: string;
  status: 'ready';
};

export const hello = defineSystemCommand<HelloFlags, HelloResult>({
  name: 'hello',
  scope: 'platform',
  jsonTest: { safeArgs: [] },
  description: 'Print Hello World',
  longDescription: 'Prints "Hello World" — a simple smoke-test for CLI functionality',
  category: 'info',
  examples: generateExamples('hello', 'kb', [
    { flags: {} },
  ]),
  flags: {
    json: { type: 'boolean', description: 'Output in JSON format' },
  },
  analytics: {
    command: 'hello',
    startEvent: 'HELLO_STARTED',
    finishEvent: 'HELLO_FINISHED',
  },
  async handler(ctx, _argv, flags) {
    const message = 'Hello World';

    ctx.platform?.logger?.info('Hello command executed');

    if (!flags.json) {
      ctx.ui?.write(`${message}\n`);
    }

    return { ok: true, message, status: 'ready' };
  },
});
