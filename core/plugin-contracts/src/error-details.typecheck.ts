import { ConfigError, PluginError } from './errors.js';
import type { CataloguedCommandErrorFor, CommandFailure } from './handlers.js';
import type { ProductErrorCode } from '@kb-labs/core-platform/error-envelope';

new ConfigError('Invalid config.', { path: 'llm.model' });

// @ts-expect-error Catalogued plugin errors cannot emit undeclared details.
new PluginError('Invalid config.', 'KB_CONFIG_INVALID', { path: 'llm.model', reason: 'bad-value' });

// @ts-expect-error Required catalogue details cannot be omitted by the producer.
new PluginError('Invalid config.', 'KB_CONFIG_INVALID');

const cataloguedFailure: CommandFailure = {
  ok: false,
  error: {
    code: 'KB_CONFIG_INVALID',
    message: 'Invalid config.',
    details: { path: 'llm.model' },
  },
};

// @ts-expect-error Product error producers own their actionable hint.
const productFailureWithoutHint: CommandFailure = { ok: false, error: {
  code: 'COMMIT_NOTHING_STAGED' as ProductErrorCode,
  message: 'Nothing is staged.',
} };

const invalidCataloguedError: CataloguedCommandErrorFor<'KB_CONFIG_INVALID'> = {
  code: 'KB_CONFIG_INVALID',
  message: 'Invalid config.',
  hint: 'Correct llm.model.',
  // @ts-expect-error The code-specific handler bridge rejects undeclared fields.
  details: { path: 'llm.model', reason: 'bad-value' },
};

declare const productCode: ProductErrorCode;

const productFailure: CommandFailure = {
  ok: false,
  error: {
    code: productCode,
    message: 'Invalid input.',
    hint: 'Correct the input.',
    details: { field: 'email' },
  },
};

void cataloguedFailure;
void productFailureWithoutHint;
void invalidCataloguedError;
void productFailure;
