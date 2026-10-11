import { readFileSync, writeFileSync } from 'node:fs';

const schemaPath = new URL('../src/auth-health-response.schema.json', import.meta.url);
const outputPath = new URL('../src/auth-health-response.enums.ts', import.meta.url);
const regenerateCommand = 'pnpm --filter @kb-labs/gateway-contracts generate:auth-health-enums';
const args = process.argv.slice(2);

if (args.length > 1 || (args.length === 1 && args[0] !== '--check')) {
  console.error('Usage: node ./scripts/generate-auth-health-enums.mjs [--check]');
  process.exit(2);
}

const schema = JSON.parse(readFileSync(schemaPath, 'utf8'));
const bootstrapStatuses = schema.properties?.bootstrap?.enum;
const issueSeverities = schema.properties?.issues?.items?.properties?.severity?.enum;

if (!isStringList(bootstrapStatuses) || !isStringList(issueSeverities)) {
  throw new Error('Gateway auth-health schema is missing bootstrap or issue severity enums.');
}

const generated = [
  '// Code generated from services/gateway/contracts/src/auth-health-response.schema.json; DO NOT EDIT.',
  '',
  renderEnum('gatewayBootstrapStatuses', bootstrapStatuses),
  '',
  renderEnum('gatewayAuthHealthIssueSeverities', issueSeverities),
  '',
].join('\n');

if (args.includes('--check')) {
  let current;
  try {
    current = readFileSync(outputPath, 'utf8');
  } catch {
    console.error(`Generated auth-health enums are missing. Run \`${regenerateCommand}\`.`);
    process.exit(1);
  }
  if (current !== generated) {
    console.error(`Generated auth-health enums are stale. Run \`${regenerateCommand}\`.`);
    process.exitCode = 1;
  }
} else {
  writeFileSync(outputPath, generated);
}

function isStringList(value) {
  return Array.isArray(value) && value.length > 0 && value.every((entry) => typeof entry === 'string');
}

function renderEnum(name, values) {
  const items = values.map((value) => `  ${JSON.stringify(value)},`).join('\n');
  return `export const ${name} = [\n${items}\n] as const;`;
}
