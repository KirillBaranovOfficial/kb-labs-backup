import type { QACase, QACaseClaim, QACaseCoverage, QACaseMode, QACaseRunResult, QACaseStepResult } from '@kb-labs/qa-contracts';

export function computeQACaseCoverage(
  cases: QACase[],
  claims: QACaseClaim[],
  runs: QACaseRunResult[],
  mode: QACaseMode,
): QACaseCoverage {
  const matchingRuns = runs.filter(run => run.mode === mode);
  const coverageCases = cases.map(testCase => {
    if (!testCase.modes.includes(mode)) {
      return { id: testCase.id, title: testCase.title, status: testCase.status, release: testCase.release, statusResult: 'NOT REQUIRED' as const, steps: [] };
    }
    const steps = testCase.steps.map(step => {
      const stepClaims = claims.filter(claim => claim.stepId === step.id);
      const runStates = matchingRuns.flatMap(run => run.claims.filter(result => result.claimId === step.id).map(result => result.status));
      let status: QACaseStepResult;
      if (stepClaims.length === 0) status = 'NOT COVERED';
      else if (runStates.includes('FAIL')) status = 'FAIL';
      else if (runStates.includes('PASS')) status = 'PASS';
      else status = 'NOT RUN';
      return { stepId: step.id, status, claims: stepClaims };
    });
    const releaseStepsPass = !testCase.release || steps.every(step =>
      step.status === 'PASS' && matchingRuns.some(run => run.claims.some(result =>
        result.claimId === step.stepId && result.status === 'PASS' && result.kind !== 'unit')));
    const statuses = steps.map(step => step.status);
    const statusResult: QACaseCoverage['cases'][number]['statusResult'] = testCase.status !== 'active' ? 'NOT REQUIRED'
      : statuses.includes('FAIL') ? 'FAIL'
        : statuses.includes('NOT COVERED') ? 'NOT COVERED'
          : statuses.includes('NOT RUN') ? 'NOT RUN'
            : releaseStepsPass ? 'PASS' : 'FAIL';
    return { id: testCase.id, title: testCase.title, status: testCase.status, release: testCase.release, statusResult, steps };
  });
  const releasePassed = coverageCases.every(testCase =>
    testCase.status !== 'active' || !testCase.release || testCase.statusResult === 'PASS' || testCase.statusResult === 'NOT REQUIRED');
  return { schema: 'kb.qa-case-coverage/v1', mode, cases: coverageCases, releasePassed };
}
