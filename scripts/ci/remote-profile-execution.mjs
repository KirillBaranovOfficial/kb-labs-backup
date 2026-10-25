import { boundFailureUnits, requireFailureUnits } from './remote-stage-results.mjs';

export async function runProfileStages({ stageNames, adapters, execute, onStage, shouldStop = () => false, stages = [] }) {
  for (const name of stageNames) {
    if (shouldStop()) break;
    const adapter = adapters[name];
    const command = adapter.command.join(' ');
    const startedAt = performance.now();
    const startedAtMs = Date.now();
    let result;
    try {
      result = await execute(name, adapter);
    } catch (error) {
      result = { code: 1, stdout: '', stderr: errorMessage(error), executionError: true };
    }
    const durationMs = Math.round(performance.now() - startedAt);
    let failures = [];
    let status = result.code === 0 ? 'pass' : 'fail';
    if (result.code !== 0) {
      try {
        failures = requireFailureUnits(name, adapter.failures(result));
      } catch (error) {
        failures = [{ name: `${name} result adapter`, message: [errorMessage(error), result.stderr].filter(Boolean).join('\n').slice(0, 4_000) }];
      }
    }
    if (result.executionError) failures = [{ name: `${name} stage execution`, message: result.stderr.slice(0, 4_000) }];
    if (result.signal) failures.unshift({ name: `${name} signal`, message: `${name} terminated by signal ${result.signal}` });
    let stage;
    try {
      const boundedFailures = boundFailureUnits(failures);
      stage = {
        name,
        command,
        startedAtMs: result.startedAtMs ?? startedAtMs,
        endedAtMs: result.endedAtMs ?? Date.now(),
        status,
        failures: boundedFailures,
        logTail: boundedFailures.map((failure) => `${failure.name}\n${failure.message}`).join('\n\n').slice(0, 1_000),
        exitCode: result.code,
        signal: result.signal ?? null,
        durationMs: result.durationMs ?? durationMs,
      };
    } catch (error) {
      const message = errorMessage(error).slice(0, 4_000);
      stage = {
        name,
        command,
        startedAtMs,
        endedAtMs: Date.now(),
        status: 'fail',
        failures: [{ name: `${name} stage handling`, message }],
        logTail: message.slice(0, 1_000),
        exitCode: result.code,
        signal: result.signal ?? null,
        durationMs,
      };
    }
    const alreadyRecorded = result.recordedBySignal === true;
    if (!alreadyRecorded) stages.push(stage);
    if (onStage) {
      try {
        await onStage(stage, stages, result, alreadyRecorded);
      } catch (error) {
        stage.status = 'fail';
        stage.failures = boundFailureUnits([...stage.failures, {
          name: `${name} stage handling`,
          message: errorMessage(error),
        }]);
        stage.logTail = stage.failures.map((failure) => `${failure.name}\n${failure.message}`).join('\n\n').slice(0, 1_000);
      }
    }
  }
  return stages;
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
