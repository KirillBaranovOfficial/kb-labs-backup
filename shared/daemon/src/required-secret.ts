import { createErrorEnvelope } from '@kb-labs/core-platform';

export function requireEnvironmentSecret(
  variable: string,
  environment: NodeJS.ProcessEnv = process.env,
): string {
  const value = environment[variable];
  if (value) { return value; }

  const envelope = createErrorEnvelope('KB_CONFIG_SECRET_MISSING', { details: { variable } });
  throw new Error(
    `${envelope.code}: ${envelope.message} Hint: set ${variable} in the deployment secret configuration, then restart the service.`,
  );
}
