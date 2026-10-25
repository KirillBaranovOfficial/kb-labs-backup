const MAX_UNIT_MESSAGE = 4_000;
const MAX_FAILURES_BYTES = 8_000;

function failureUnit(name, message) {
  return { name: String(name).slice(0, 500), message: String(message).trim().slice(0, MAX_UNIT_MESSAGE) };
}

export function boundFailureUnits(failures) {
  const bounded = [];
  let bytes = 2;
  for (const item of failures) {
    const failure = failureUnit(item.name, item.message);
    const size = Buffer.byteLength(JSON.stringify(failure)) + (bounded.length ? 1 : 0);
    if (bytes + size > MAX_FAILURES_BYTES) break;
    bounded.push(failure);
    bytes += size;
  }
  return bounded;
}

export function vitestFailures(report, packageName) {
  const failures = [];
  for (const result of report.testResults ?? []) {
    if (result.status === 'failed' && (result.assertionResults ?? []).length === 0) {
      failures.push(failureUnit(`${packageName} › ${result.name}`, result.message));
    }
    for (const assertion of result.assertionResults ?? []) {
      if (assertion.status !== 'failed') continue;
      const suite = [...(assertion.ancestorTitles ?? [])].join(' › ');
      const testName = assertion.fullName || assertion.title || 'unnamed test';
      failures.push(failureUnit(
        `${packageName} › ${suite ? `${suite} › ` : ''}${testName}`,
        (assertion.failureMessages ?? []).join('\n'),
      ));
    }
  }
  return boundFailureUnits(failures);
}

export function checkFailures(report) {
  const failures = [];
  for (const result of report.results ?? report.data?.results ?? []) {
    for (const issue of result.issues ?? []) {
      if (issue.severity !== 'error') continue;
      failures.push(failureUnit(`${issue.check} · ${result.package}`, issue.message));
    }
  }
  return boundFailureUnits(failures);
}

export function goTestFailures(jsonLines) {
  const tests = new Map();
  for (const line of jsonLines.split(/\r?\n/).filter(Boolean)) {
    const event = JSON.parse(line);
    if (!event.Test) continue;
    const key = `${event.Package} › ${event.Test}`;
    const unit = tests.get(key) ?? { name: key, message: '' };
    if (event.Action === 'output') unit.message += event.Output ?? '';
    if (event.Action === 'fail') unit.failed = true;
    tests.set(key, unit);
  }
  return boundFailureUnits([...tests.values()].filter((unit) => unit.failed));
}

export function journeyFailures(result) {
  return boundFailureUnits([
    ...(result.failures ?? []).map((failure) => ({ name: failure.testId, message: failure.traceback })),
    ...(result.errors ?? []).map(({ testId, traceback }) => ({ name: testId, message: traceback })),
  ]);
}

export function failedWithoutUnit(stageName) {
  const message = 'stage failed without naming a failure';
  return [failureUnit(`${stageName}: ${message}`, message)];
}

export { MAX_FAILURES_BYTES, MAX_UNIT_MESSAGE };
