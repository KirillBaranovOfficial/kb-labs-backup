import { describe, expect, it, vi } from 'vitest';
import { ServicePortConfigError } from '@kb-labs/core-platform';
import type { Presenter } from '@kb-labs/cli-runtime';
import { InteractiveInputRequiredError } from '@kb-labs/shared-cli-ui/interactive';
import { defineSystemCommand } from '@kb-labs/shared-command-kit';
import type { PluginContextV3 } from '@kb-labs/plugin-contracts';
import { handleExecutionError } from './execution-error.js';
import { platform } from '@kb-labs/core-runtime';

describe('handleExecutionError', () => {
  // case: S-203.1
  it('logs unexpected causes with the same correlation id without rendering the cause', () => {
    const cause = new Error('loopback request failed');
    const loggerError = vi.spyOn(platform.logger, 'error').mockImplementation(() => undefined);
    const json = vi.fn();
    const presenter = {
      isJSON: true,
      json,
      error: vi.fn(),
    } as unknown as Presenter;

    const exitCode = handleExecutionError(cause, presenter);
    const rendered = json.mock.calls[0]?.[0] as { error: { code: string; correlationId: string } };

    expect(exitCode).toBe(1);
    expect(rendered.error.code).toBe('KB_RUNTIME_UNEXPECTED');
    expect(loggerError).toHaveBeenCalledWith('CLI command failed unexpectedly', cause, {
      correlationId: rendered.error.correlationId,
    });
    expect(JSON.stringify(rendered)).not.toContain(cause.message);
    expect(JSON.stringify(rendered)).not.toContain(cause.stack);
    loggerError.mockRestore();
  });

  it('renders a catalogued usage failure for required non-TTY input', () => {
    const presenter = {
      isJSON: true,
      json: vi.fn(),
      error: vi.fn(),
    } as unknown as Presenter;

    const exitCode = handleExecutionError(
      new InteractiveInputRequiredError('Add the required value flag.', 'The command needs an input value.'),
      presenter,
    );

    expect(exitCode).toBe(2);
    expect(presenter.json).toHaveBeenCalledWith({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_RUNTIME_INPUT_INVALID',
        message: 'The provided input is invalid.',
        hint: 'Add the required value flag.',
        cause: 'The command needs an input value.',
      }),
    });
  });

  it('uses the same usage exit mapping at the command and CLI error boundaries', async () => {
    const error = new InteractiveInputRequiredError('kb command --required-input', 'The prompt is unavailable.');
    const command = defineSystemCommand({
      name: 'required-input',
      scope: 'platform',
      jsonTest: { safeArgs: [] },
      description: 'Required input fixture',
      flags: { json: { type: 'boolean' } },
      handler: async () => { throw error; },
    });
    const commandJson = vi.fn();
    const commandContext = {
      requestId: 'required-input-test',
      ui: { json: commandJson, error: vi.fn() },
      platform: { logger: { error: vi.fn() } },
    } as unknown as PluginContextV3;
    const presenter = {
      isJSON: true,
      json: vi.fn(),
      error: vi.fn(),
    } as unknown as Presenter;

    const commandExitCode = await command.run(commandContext, [], { json: true });
    const cliExitCode = handleExecutionError(error, presenter);

    expect(commandExitCode).toBe(2);
    expect(cliExitCode).toBe(commandExitCode);
    expect(commandJson).toHaveBeenCalledWith(expect.objectContaining({
      error: expect.objectContaining({ code: 'KB_RUNTIME_INPUT_INVALID', cause: 'The prompt is unavailable.' }),
    }));
    expect(presenter.json).toHaveBeenCalledWith(expect.objectContaining({
      error: expect.objectContaining({ code: 'KB_RUNTIME_INPUT_INVALID', cause: 'The prompt is unavailable.' }),
    }));
  });

  // case: S-203.16
  it('renders invalid service ports as catalogued JSON errors', () => {
    const presenter = {
      isJSON: true,
      json: vi.fn(),
      error: vi.fn(),
    } as unknown as Presenter;

    const exitCode = handleExecutionError(
      new ServicePortConfigError('GATEWAY_PORT', 'must be an integer from 1 through 65535'),
      presenter,
    );

    expect(exitCode).toBe(1);
    expect(presenter.json).toHaveBeenCalledWith({
      ok: false,
      error: expect.objectContaining({
        code: 'KB_CONFIG_INVALID',
        message: 'Environment variable "GATEWAY_PORT" must be an integer from 1 through 65535.',
        hint: 'Run "unset GATEWAY_PORT", then retry the command to use the service default port.',
      }),
    });
  });
});
