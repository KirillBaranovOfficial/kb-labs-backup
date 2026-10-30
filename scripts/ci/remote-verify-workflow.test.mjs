import assert from 'node:assert/strict';
import { cpSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { load } from 'js-yaml';
import test from 'node:test';
import { parseAffectedSelection } from './remote-verify-plan.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const readYaml = (file) => load(readFileSync(path.join(root, file), 'utf8'));

// case: S-262.7
test('remote verification builds once and fans out independent and artifact-dependent test matrices', () => {
  const workflow = readYaml('.github/workflows/remote-verify.yml');
  const prepare = workflow.jobs.prepare;
  const build = workflow.jobs.build;
  const independent = workflow.jobs['independent-test-shards'];
  const dependent = workflow.jobs['build-dependent-test-shards'];
  const internalTools = workflow.jobs['internal-tool-test-shards'];
  const finalize = workflow.jobs.verdict;
  const prepareResultUpload = prepare.steps.find((step) => step.name === 'Upload prepare stage result');
  const aggregateStep = finalize.steps.find((step) => step.name === 'Aggregate commit-bound verdict');
  const prepareSteps = prepare.steps;
  const buildSteps = build.steps;
  const independentSteps = independent.steps;
  const dependentSteps = dependent.steps;

  assert.ok(prepare.steps.some((step) => step.run === 'node scripts/ci/run-remote-prepare.mjs'));
  assert.equal(prepareResultUpload.if, 'always()');
  assert.equal(prepareResultUpload.with.name, 'remote-result-prepare');
  assert.equal(prepareResultUpload.with.path, 'stage-result-prepare.json');
  assert.equal(aggregateStep.env.PREPARE_JOB_RESULT, '${{ needs.prepare.result }}');
  assert.equal(build.needs, 'prepare');
  assert.equal(independent.needs, 'prepare');
  assert.equal(independent.strategy.matrix, '${{ fromJSON(needs.prepare.outputs.independent_matrix) }}');
  assert.equal(dependent.needs, 'build');
  assert.equal(internalTools.needs, 'prepare');
  assert.equal(internalTools['continue-on-error'], true);
  assert.equal(internalTools.strategy.matrix, '${{ fromJSON(needs.prepare.outputs.internal_tool_matrix) }}');
  assert.equal(internalTools['timeout-minutes'], '${{ fromJSON(needs.prepare.outputs.shard_timeout_minutes) }}');
  assert.equal(dependent.strategy.matrix, '${{ fromJSON(needs.build.outputs.build_dependent_matrix) }}');
  assert.equal(independent.strategy['fail-fast'], "${{ inputs.profile != 'candidate' }}");
  assert.equal(dependent.strategy['fail-fast'], "${{ inputs.profile != 'candidate' }}");
  assert.equal(independent.strategy['max-parallel'], '${{ fromJSON(needs.prepare.outputs.max_parallel) }}');
  assert.equal(dependent.strategy['max-parallel'], '${{ fromJSON(needs.build.outputs.max_parallel) }}');
  assert.equal(build.outputs.build_dependent_matrix, '${{ needs.prepare.outputs.build_dependent_matrix }}');
  assert.equal(prepare.outputs.selected_package_count, '${{ steps.plan.outputs.selected_package_count }}');
  assert.equal(prepare.outputs.internal_tool_matrix, '${{ steps.plan.outputs.internal_tool_matrix }}');
  assert.equal(prepare.outputs.internal_tool_packages, '${{ steps.plan.outputs.internal_tool_packages }}');
  assert.equal(prepare.outputs.shard_timeout_minutes, '${{ steps.plan.outputs.shard_timeout_minutes }}');
  assert.equal(build.outputs.shard_timeout_minutes, '${{ needs.prepare.outputs.shard_timeout_minutes }}');
  assert.equal(independent['timeout-minutes'], '${{ fromJSON(needs.prepare.outputs.shard_timeout_minutes) }}');
  assert.equal(dependent['timeout-minutes'], '${{ fromJSON(needs.build.outputs.shard_timeout_minutes) }}');
  assert.equal(build.outputs.selected_package_count, '${{ needs.prepare.outputs.selected_package_count }}');
  assert.equal(independent.env.REMOTE_SELECTED_PACKAGE_COUNT, '${{ needs.prepare.outputs.selected_package_count }}');
  assert.equal(dependent.env.REMOTE_SELECTED_PACKAGE_COUNT, '${{ needs.build.outputs.selected_package_count }}');
  assert.equal(aggregateStep.env.REMOTE_SELECTED_PACKAGE_COUNT, "${{ needs.prepare.outputs.selected_package_count || '0' }}");
  const buildRun = buildSteps.find((step) => step.name === 'Build selected workspace packages once');
  assert.equal(buildRun.env.REMOTE_STAGE, '${{ needs.prepare.outputs.build_stage }}');
  assert.equal(buildRun.env.REMOTE_RESOURCE_SAMPLES, 'stage-resource-samples.jsonl');
  assert.ok(!prepareSteps.some((step) => step.name === 'Install workspace dependencies'));
  assert.ok(buildSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(independentSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(dependentSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(independentSteps.some((step) => step.uses?.startsWith('actions/download-artifact@')));
  assert.ok(dependentSteps.some((step) => step.uses?.startsWith('actions/download-artifact@')));
  assert.ok(dependentSteps.some((step) => step.run === 'tar xf workspace-dist.tar'));
  assert.ok(buildSteps.some((step) => step.uses?.startsWith('actions/upload-artifact@')));
  const resourceUpload = buildSteps.find((step) => step.name === 'Upload build result and resource evidence');
  assert.equal(resourceUpload.if, 'always()');
  assert.equal(resourceUpload.with.path, 'stage-result-build.json\nstage-resource-samples.jsonl\n');
  assert.equal(aggregateStep.env.BUILD_JOB_RESULT, '${{ needs.build.result }}');
  assert.equal(aggregateStep.env.BUILD_RESOURCE_SAMPLES, 'remote-results/stage-resource-samples.jsonl');
  assert.equal(aggregateStep.env.GH_TOKEN, '${{ github.token }}');
  assert.ok(finalize.needs.includes('independent-test-shards'));
  assert.ok(finalize.needs.includes('build-dependent-test-shards'));
  assert.ok(finalize.needs.includes('internal-tool-test-shards'));
  assert.equal(aggregateStep.env.REMOTE_INTERNAL_TOOL_MATRIX, '${{ needs.prepare.outputs.internal_tool_matrix }}');
  assert.equal(buildSteps.filter((step) => step.name === 'Build selected workspace packages once').length, 1);
});

// case: S-262.7
test('remote verification reuses the existing lockfile, Go module, and kb-devkit CAS cache owners', () => {
  const workflow = readYaml('.github/workflows/remote-verify.yml');
  const steps = workflow.jobs.prepare.steps;
  const nodeSetup = steps.find((step) => step.uses?.startsWith('actions/setup-node@'));
  const goSetup = steps.find((step) => step.uses?.startsWith('actions/setup-go@'));
  const cas = readYaml('.github/actions/devkit-cas/action.yml');
  const cache = cas.runs.steps.find((step) => step.uses?.startsWith('actions/cache@'));
  const profile = JSON.parse(readFileSync(path.join(root, 'scripts/ci/verification-profile.json'), 'utf8'));

  assert.equal(nodeSetup.with.cache, 'pnpm');
  assert.equal(goSetup.with['cache-dependency-path'], 'tools/kb-create/go.sum');
  assert.equal(cache.with.path, '.kb/devkit/');
  assert.match(cache.with.key, /\$\{\{ github\.sha \}\}/);
  const toolchain = cas.runs.steps.find((step) => step.name === 'Identify cache toolchain partition');
  assert.equal(toolchain.id, 'toolchain');
  assert.match(toolchain.run, /node --version/);
  assert.match(toolchain.run, /go version/);
  assert.match(cache.with.key, /runner\.arch/);
  assert.match(cache.with.key, /steps\.toolchain\.outputs\.partition/);
  assert.doesNotMatch(cache.with.key, /hashFiles\(/);
  assert.equal(cache.with['restore-keys'].trim(), 'devkit-cas-${{ runner.os }}-${{ runner.arch }}-${{ steps.toolchain.outputs.partition }}-');
  assert.equal(cas.outputs['cache-hit'].value, '${{ steps.restore.outputs.cache-hit }}');
  assert.ok(profile.buildDependentTestStages.candidate.includes('journey-unit-suite'));
  assert.ok(!profile.buildDependentTestStages.stream.includes('journey-unit-suite'));
});

// case: S-262.7
test('workflow base-ref wiring feeds the plan from the exact remote changed-path manifest', () => {
  const workflow = readYaml('.github/workflows/remote-verify.yml');
  const checkout = workflow.jobs.prepare.steps.find((step) => step.uses?.startsWith('actions/checkout@'));
  const event = { repository: { default_branch: 'fixture-default' } };
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-selection-hosted-shape-'));
  const checkoutRoot = path.join(temporaryRoot, 'checkout');
  try {
    assert.equal(checkout.with['fetch-depth'], 0);
    assert.equal(workflow.env.DEFAULT_BRANCH, '${{ github.event.repository.default_branch }}');
    assert.equal(workflow.jobs.prepare.env.VERIFICATION_PROFILE, "${{ github.event.inputs.profile || 'stream' }}");

    execFileSync('git', ['clone', '--shared', '--no-checkout', root, checkoutRoot], { stdio: 'pipe' });
    execFileSync('git', ['checkout', '--detach', 'HEAD'], { cwd: checkoutRoot, stdio: 'pipe' });
    const baseline = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: checkoutRoot, encoding: 'utf8' }).trim();
    execFileSync('git', ['update-ref', `refs/remotes/origin/${event.repository.default_branch}`, baseline], { cwd: checkoutRoot });
    execFileSync('git', ['update-ref', '-d', 'refs/remotes/origin/HEAD'], { cwd: checkoutRoot });
    assert.equal(spawnSync('git', ['show-ref', '--verify', '--quiet', 'refs/remotes/origin/HEAD'], { cwd: checkoutRoot }).status, 1);
    execFileSync('git', ['config', 'user.name', 'Remote verification fixture'], { cwd: checkoutRoot });
    execFileSync('git', ['config', 'user.email', 'fixture@example.invalid'], { cwd: checkoutRoot });
    execFileSync('sh', ['-c', 'printf "\\n" >> core/types/package.json'], { cwd: checkoutRoot });
    execFileSync('git', ['add', 'core/types/package.json'], { cwd: checkoutRoot });
    execFileSync('git', ['commit', '-m', 'fixture change'], { cwd: checkoutRoot, stdio: 'pipe' });

    cpSync(path.join(root, 'scripts/ci/remote-changed-files.mjs'), path.join(checkoutRoot, 'scripts/ci/remote-changed-files.mjs'));

    for (const profile of ['stream', 'candidate']) {
      const manifestPath = path.join(temporaryRoot, `${profile}-changed.json`);
      const result = spawnSync(process.execPath, ['scripts/ci/remote-changed-files.mjs'], {
        cwd: checkoutRoot,
        encoding: 'utf8',
        env: {
          ...process.env,
          VERIFICATION_PROFILE: profile,
          KB_DEVKIT_BASE_REF: profile === 'stream' ? `origin/${event.repository.default_branch}...HEAD` : execFileSync('git', ['hash-object', '-t', 'tree', '--stdin'], { cwd: checkoutRoot, input: '', encoding: 'utf8' }).trim(),
          KB_REMOTE_CHANGED_FILES_PATH: manifestPath,
        },
      });
      assert.equal(result.status, 0, `${profile} selection failed: ${result.stderr}`);
      const changedPaths = JSON.parse(readFileSync(manifestPath, 'utf8'));
      assert.ok(changedPaths.includes('core/types/package.json'), `${profile} did not include the committed package path`);
      const packages = ['@kb-labs/core'];
      const selection = {
        ok: true,
        data: {
          packages,
          internalToolPackages: [],
          changedPathCount: changedPaths.length,
          selectionReason: 'fixture package ownership',
        },
      };
      const parsed = parseAffectedSelection(JSON.stringify(selection), profile);
      assert.deepEqual(parsed.packages, packages);
      assert.deepEqual(parsed.internalToolPackages, []);
      assert.ok(parsed.changedPathCount > 0, `${profile} workflow inputs produced an empty diff: ${result.stdout}`);
    }
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
