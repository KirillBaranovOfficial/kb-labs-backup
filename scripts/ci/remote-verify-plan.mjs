#!/usr/bin/env node
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';

export function parseAffectedPackages(output) {
  let envelope;
  try {
    envelope = JSON.parse(output);
  } catch (error) {
    throw new Error(`kb-devkit affected --json returned invalid JSON: ${error.message}. Rerun ./tools/kb-devkit/kb-devkit affected --json in the checked-out workspace.`);
  }
  const packages = envelope?.data?.packages;
  if (envelope?.ok !== true || !Array.isArray(packages) || packages.some((name) => typeof name !== 'string' || !name)) {
    throw new Error('kb-devkit affected --json did not return a successful package list. Rerun ./tools/kb-devkit/kb-devkit affected --json in the checked-out workspace.');
  }
  if (new Set(packages).size !== packages.length) throw new Error('kb-devkit affected --json returned a duplicate package name. Rerun ./tools/kb-devkit/kb-devkit affected --json after checking the workspace registry.');
  return packages;
}

export function createTestMatrix(packages, independentStages, buildDependentStages, packageTestStage, shardLimit) {
  if (!Number.isSafeInteger(shardLimit) || shardLimit < 1) throw new Error('Verification profile maxParallel must be a positive integer.');
  if (new Set(packages).size !== packages.length) throw new Error('Affected package selection contains a duplicate package.');
  const testStages = [...independentStages, ...buildDependentStages];
  if (new Set(testStages).size !== testStages.length) throw new Error('Verification profile contains a duplicate test stage.');
  const shardCount = Math.min(packages.length, shardLimit);
  const packageShards = Array.from({ length: shardCount }, (_, index) => ({
    stage: packageTestStage,
    shard: index + 1,
    packages: [],
  }));
  packages.forEach((packageName, index) => packageShards[index % shardCount].packages.push(packageName));
  const independent = { include: independentStages.map((stage) => ({ stage })) };
  const dependent = { include: [...buildDependentStages.map((stage) => ({ stage })), ...packageShards] };
  return {
    independent,
    dependent,
    include: [...independent.include, ...dependent.include],
  };
}

function main() {
  const [profileName, affectedPath, planPath, outputPath] = process.argv.slice(2);
  if (!profileName || !affectedPath || !planPath || !outputPath) throw new Error('Usage: node scripts/ci/remote-verify-plan.mjs <stream|candidate> <affected-json> <plan-json> <github-output>');
  const packages = parseAffectedPackages(readFileSync(affectedPath, 'utf8'));
  const profile = JSON.parse(readFileSync(new URL('./verification-profile.json', import.meta.url), 'utf8'));
  const independentStages = profile.independentTestStages[profileName];
  const buildDependentStages = profile.buildDependentTestStages[profileName];
  if (!independentStages || !buildDependentStages) throw new Error(`Unknown verification profile ${profileName}; use stream or candidate.`);
  const matrix = createTestMatrix(packages, independentStages, buildDependentStages, profile.packageTestStage, profile.maxParallel);
  writeFileSync(planPath, `${JSON.stringify(matrix)}\n`);
  appendFileSync(outputPath, `test_matrix=${JSON.stringify({ include: matrix.include })}\nindependent_matrix=${JSON.stringify(matrix.independent)}\nbuild_dependent_matrix=${JSON.stringify(matrix.dependent)}\nbuild_stage=${profile.buildStage}\npackages=${packages.join(',')}\nmax_parallel=${profile.maxParallel}\n`);
  process.stdout.write(`Planned ${matrix.include.length} test shard(s) from ${packages.length} kb-devkit-selected package(s).\n`);
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) main();
