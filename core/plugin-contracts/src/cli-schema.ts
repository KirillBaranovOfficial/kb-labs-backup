export type CliFlagType = 'string' | 'boolean' | 'number' | 'array';

interface CliFlagBase {
  name: string;
  alias?: string;
  default?: unknown;
  description?: string;
  required?: boolean;
  secret?: boolean;
}

export type CliFlagDecl =
  | (CliFlagBase & { type: 'string'; choices?: readonly string[] })
  | (CliFlagBase & { type: 'boolean' })
  | (CliFlagBase & { type: 'number' })
  | (CliFlagBase & { type: 'array' });

export interface CliSchemaIssue {
  path: Array<string | number>;
  message: string;
}

export type CliSchemaResult<T> =
  | { success: true; data: T }
  | { success: false; error: { issues: CliSchemaIssue[] } };

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

function validateFlag(value: unknown): CliSchemaIssue[] {
  if (!isRecord(value)) { return [{ path: [], message: 'Flag declaration must be an object' }]; }
  const issues: CliSchemaIssue[] = [];
  if (typeof value.name !== 'string' || !/^[a-z0-9-]+$/.test(value.name)) {
    issues.push({ path: ['name'], message: 'Flag name must be lowercase alphanumeric with hyphens' });
  }
  if (!['string', 'boolean', 'number', 'array'].includes(String(value.type))) {
    issues.push({ path: ['type'], message: 'Flag type must be string, boolean, number, or array' });
  }
  if (value.alias !== undefined && (typeof value.alias !== 'string' || !/^[a-z]$/i.test(value.alias))) {
    issues.push({ path: ['alias'], message: 'Alias must be a single letter' });
  }
  if (value.description !== undefined && typeof value.description !== 'string') {
    issues.push({ path: ['description'], message: 'Description must be a string' });
  }
  if (value.required !== undefined && typeof value.required !== 'boolean') {
    issues.push({ path: ['required'], message: 'Required must be a boolean' });
  }
  if (value.secret !== undefined && typeof value.secret !== 'boolean') {
    issues.push({ path: ['secret'], message: 'Secret must be a boolean' });
  }
  if (value.choices !== undefined) {
    if (value.type !== 'string') {
      issues.push({ path: ['choices'], message: 'Choices are only allowed for string type flags' });
    } else if (!Array.isArray(value.choices) || value.choices.some(choice => typeof choice !== 'string')) {
      issues.push({ path: ['choices'], message: 'Choices must be an array of strings' });
    }
  }
  return issues;
}

function parseWith<T>(value: unknown, issues: CliSchemaIssue[]): CliSchemaResult<T> {
  return issues.length === 0
    ? { success: true, data: value as T }
    : { success: false, error: { issues } };
}

export const CliFlagDeclSchema = {
  safeParse(value: unknown): CliSchemaResult<CliFlagDecl> {
    return parseWith(value, validateFlag(value));
  },
};

export interface CliCommandDecl {
  path: string;
  describe: string;
  longDescription?: string;
  flags?: CliFlagDecl[];
  examples?: string[];
  handler: string;
  category?: string;
  aliases?: string[];
  operationType?: 'read' | 'mutate' | 'execute' | 'analyze';
  [field: string]: unknown;
}

export const CliCommandDeclSchema = {
  safeParse(value: unknown): CliSchemaResult<CliCommandDecl> {
    if (!isRecord(value)) {
      return { success: false, error: { issues: [{ path: [], message: 'Command declaration must be an object' }] } };
    }
    const issues: CliSchemaIssue[] = [];
    for (const field of ['path', 'describe', 'handler'] as const) {
      if (typeof value[field] !== 'string' || value[field].length === 0) {
        issues.push({ path: [field], message: `${field} must be a non-empty string` });
      }
    }
    for (const field of ['longDescription', 'category'] as const) {
      if (value[field] !== undefined && typeof value[field] !== 'string') {
        issues.push({ path: [field], message: `${field} must be a string` });
      }
    }
    for (const field of ['examples', 'aliases'] as const) {
      if (value[field] !== undefined && (!Array.isArray(value[field]) || value[field].some(item => typeof item !== 'string'))) {
        issues.push({ path: [field], message: `${field} must be an array of strings` });
      }
    }
    if (value.flags !== undefined) {
      if (!Array.isArray(value.flags)) {
        issues.push({ path: ['flags'], message: 'Flags must be an array' });
      } else {
        value.flags.forEach((flag, index) => {
          for (const issue of validateFlag(flag)) {
            issues.push({ path: ['flags', index, ...issue.path], message: issue.message });
          }
        });
      }
    }
    if (value.operationType !== undefined && !['read', 'mutate', 'execute', 'analyze'].includes(String(value.operationType))) {
      issues.push({ path: ['operationType'], message: 'operationType is not a supported command type' });
    }
    return parseWith(value, issues);
  },
};
