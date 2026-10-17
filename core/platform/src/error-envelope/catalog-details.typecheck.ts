import { createErrorEnvelope } from './catalog.js';
import { AdapterCredentialsMissingError } from '../errors.js';

createErrorEnvelope('KB_CONFIG_INVALID', {
  details: { path: 'someSection.key', nextCommand: 'kb config set plugins.someSection.key 1' },
});

// @ts-expect-error The catalog does not declare reason as a KB_CONFIG_INVALID detail.
createErrorEnvelope('KB_CONFIG_INVALID', { details: { path: 'someSection.key', reason: 'project-scope-path' } });

// @ts-expect-error The catalog requires the path template parameter.
createErrorEnvelope('KB_CONFIG_INVALID', { details: { nextCommand: 'kb config set plugins.someSection.key 1' } });

// @ts-expect-error Required runtime template values stay required when a hint is overridden.
createErrorEnvelope('KB_RUNTIME_UNKNOWN_COMMAND', { hint: 'Use the command registry.' });

// @ts-expect-error Explicit text overrides do not remove required catalogue details.
createErrorEnvelope('KB_PLUGIN_NOT_FOUND', { message: 'Plugin missing.', hint: 'Install it.' });

// @ts-expect-error An error without catalogue detail fields cannot acquire ad hoc fields.
createErrorEnvelope('KB_AUTH_CLOCK_SKEW', { details: { reason: 'clock mismatch' } });

const credentialError = new AdapterCredentialsMissingError('llm', 'OPENAI_API_KEY', 'adapters.llm');

// @ts-expect-error Adapter errors expose only the details declared for their code.
credentialError.details.reason;
