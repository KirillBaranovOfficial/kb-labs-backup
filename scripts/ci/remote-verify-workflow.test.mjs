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
  const finalize = workflow.jobs.verdict;
  const prepareSteps = prepare.steps;
  const buildSteps = build.steps;
  const independentSteps = independent.steps;
  const dependentSteps = dependent.steps;

  assert.equal(build.needs, 'prepare');
  assert.equal(independent.needs, 'prepare');
  assert.equal(independent.strategy.matrix, '${{ fromJSON(needs.prepare.outputs.independent_matrix) }}');
  assert.equal(dependent.needs, 'build');
  assert.equal(dependent.strategy.matrix, '${{ fromJSON(needs.build.outputs.build_dependent_matrix) }}');
  assert.equal(independent.strategy['fail-fast'], "${{ inputs.profile != 'candidate' }}");
  assert.equal(dependent.strategy['fail-fast'], "${{ inputs.profile != 'candidate' }}");
  assert.equal(independent.strategy['max-parallel'], '${{ fromJSON(needs.prepare.outputs.max_parallel) }}');
  assert.equal(dependent.strategy['max-parallel'], '${{ fromJSON(needs.build.outputs.max_parallel) }}');
  assert.equal(build.outputs.build_dependent_matrix, '${{ needs.prepare.outputs.build_dependent_matrix }}');
  assert.equal(prepare.outputs.selected_package_count, '${{ steps.plan.outputs.selected_package_count }}');
  assert.equal(build.outputs.selected_package_count, '${{ needs.prepare.outputs.selected_package_count }}');
  assert.equal(independent.env.REMOTE_SELECTED_PACKAGE_COUNT, '${{ needs.prepare.outputs.selected_package_count }}');
  assert.equal(dependent.env.REMOTE_SELECTED_PACKAGE_COUNT, '${{ needs.build.outputs.selected_package_count }}');
  assert.equal(finalize.steps.find((step) => step.name === 'Aggregate commit-bound verdict').env.REMOTE_SELECTED_PACKAGE_COUNT, '${{ needs.prepare.outputs.selected_package_count }}');
  assert.equal(buildSteps.find((step) => step.name === 'Build selected workspace packages once').env.REMOTE_STAGE, '${{ needs.prepare.outputs.build_stage }}');
  assert.ok(prepareSteps.some((step) => step.name === 'Resolve affected packages and test matrix'));
  assert.ok(buildSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(independentSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(dependentSteps.some((step) => step.uses === './.github/actions/devkit-cas'));
  assert.ok(independentSteps.some((step) => step.uses?.startsWith('actions/download-artifact@')));
  assert.ok(dependentSteps.some((step) => step.uses?.startsWith('actions/download-artifact@')));
  assert.ok(dependentSteps.some((step) => step.run === 'tar xf workspace-dist.tar'));
  assert.ok(buildSteps.some((step) => step.uses?.startsWith('actions/upload-artifact@')));
  assert.ok(finalize.needs.includes('independent-test-shards'));
  assert.ok(finalize.needs.includes('build-dependent-test-shards'));
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
  assert.match(cache.with.key, /hashFiles\(/);
  assert.equal(cas.outputs['cache-hit'].value, '${{ steps.restore.outputs.cache-hit }}');
  assert.ok(profile.buildDependentTestStages.candidate.includes('journey-unit-suite'));
  assert.ok(!profile.buildDependentTestStages.stream.includes('journey-unit-suite'));
});

// case: S-262.7
test('workflow base-ref wiring selects real workspace packages without origin/HEAD', () => {
  const workflow = readYaml('.github/workflows/remote-verify.yml');
  const checkout = workflow.jobs.prepare.steps.find((step) => step.uses?.startsWith('actions/checkout@'));
  const selectorStep = workflow.jobs.prepare.steps.find((step) => step.name === 'Resolve affected packages and test matrix');
  const event = { repository: { default_branch: 'main' } };
  const temporaryRoot = mkdtempSync(path.join(os.tmpdir(), 'remote-selection-hosted-shape-'));
  const checkoutRoot = path.join(temporaryRoot, 'checkout');
  const binary = path.join(temporaryRoot, 'kb-devkit');
  try {
    assert.equal(checkout.with['fetch-depth'], 0);
    assert.equal(workflow.env.DEFAULT_BRANCH, '${{ github.event.repository.default_branch }}');
    assert.equal(workflow.jobs.prepare.env.VERIFICATION_PROFILE, "${{ github.event.inputs.profile || 'stream' }}");
    assert.match(selectorStep.run, /resolve-remote-selection\.mjs/);

    execFileSync('git', ['clone', '--shared', '--no-checkout', root, checkoutRoot], { stdio: 'pipe' });
    execFileSync('git', ['checkout', '--detach', 'HEAD'], { cwd: checkoutRoot, stdio: 'pipe' });
    const head = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: checkoutRoot, encoding: 'utf8' }).trim();
    const baseline = execFileSync('git', ['rev-parse', 'local/main'], { cwd: root, encoding: 'utf8' }).trim();
    execFileSync('git', ['update-ref', `refs/remotes/origin/${event.repository.default_branch}`, baseline], { cwd: checkoutRoot });
    execFileSync('git', ['update-ref', '-d', 'refs/remotes/origin/HEAD'], { cwd: checkoutRoot });
    assert.equal(spawnSync('git', ['show-ref', '--verify', '--quiet', 'refs/remotes/origin/HEAD'], { cwd: checkoutRoot }).status, 1);
    assert.equal(execFileSync('git', ['rev-parse', 'HEAD'], { cwd: checkoutRoot, encoding: 'utf8' }).trim(), head);

    cpSync(path.join(root, 'scripts/ci/resolve-remote-selection.mjs'), path.join(checkoutRoot, 'scripts/ci/resolve-remote-selection.mjs'));
    cpSync(path.join(root, 'scripts/ci/remote-changed-files.mjs'), path.join(checkoutRoot, 'scripts/ci/remote-changed-files.mjs'));
    execFileSync('go', ['build', '-o', binary, '.'], { cwd: path.join(checkoutRoot, 'tools/kb-devkit'), stdio: 'pipe' });

    for (const profile of ['stream', 'candidate']) {
      const outputPath = path.join(temporaryRoot, `${profile}.json`);
      const result = spawnSync(process.execPath, ['scripts/ci/resolve-remote-selection.mjs', profile, outputPath, binary], {
        cwd: checkoutRoot,
        encoding: 'utf8',
        env: { ...process.env, DEFAULT_BRANCH: event.repository.default_branch, VERIFICATION_PROFILE: profile },
      });
      assert.equal(result.status, 0, `${profile} selection failed: ${result.stderr}`);
      const selection = JSON.parse(readFileSync(outputPath, 'utf8'));
      assert.ok(selection.data.packages.length > 0, `${profile} selected no packages`);
      assert.deepEqual(parseAffectedSelection(JSON.stringify(selection), profile), {
        packages: selection.data.packages,
        changedPathCount: selection.data.changedPathCount,
      });
      assert.equal(selection.selectedPackageCount, selection.data.packages.length);
      assert.ok(selection.data.changedPathCount > 0, `${profile} workflow inputs produced an empty diff: ${result.stdout}`);
      if (profile === 'candidate') assert.ok(selection.data.changedPathCount >= selection.selectedPackageCount);
    }
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
