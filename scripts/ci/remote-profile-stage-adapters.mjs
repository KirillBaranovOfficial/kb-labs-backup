import { boundFailureUnits, checkFailures, journeyFailures, runFailures } from './remote-stage-results.mjs';

export const stageAdapters = {
  'full-build': {
    command: ['./tools/kb-devkit/kb-devkit', 'run', 'build', '--json'],
    failures: (result) => runFailures(JSON.parse(result.stdout)),
  },
  'affected-unit-tests': {
    command: ['node', 'scripts/ci/run-affected-unit-tests.mjs'],
    failures: (result) => boundFailureUnits(JSON.parse(result.stdout).failures ?? []),
  },
  'kb-devkit-check': {
    command: ['./tools/kb-devkit/kb-devkit', 'check', '--json'],
    failures: (result) => checkFailures(JSON.parse(result.stdout)),
  },
  'kb-create-go-suite': {
    command: ['node', 'scripts/ci/go-module-suite.mjs', '--json', '--module', 'tools/kb-create'],
    failures: (result) => boundFailureUnits(JSON.parse(result.stdout).failures ?? []),
  },
  'journey-unit-suite': {
    command: ['python3', 'scripts/ci/run-unittest-suite.py'],
    failures: (result) => journeyFailures(JSON.parse(result.stdout)),
  },
};

export function assertStageAdaptersMatchProfile(profileStages) {
  const profileStageSet = new Set(profileStages);
  const adapterStageNames = Object.keys(stageAdapters);
  if (profileStageSet.size !== profileStages.length || profileStageSet.size !== adapterStageNames.length || adapterStageNames.some((name) => !profileStageSet.has(name))) {
    throw new Error('The verification profile stages and structured result adapters must match exactly');
  }
}
