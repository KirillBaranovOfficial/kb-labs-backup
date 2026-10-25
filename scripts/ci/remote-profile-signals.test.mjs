import test from 'node:test';
import assert from 'node:assert/strict';
import { parseVerdict } from './remote-verdict.mjs';
import { interruptActiveStage } from './remote-profile-signals.mjs';

test('records the active profile stage and signal before saving the verdict', () => {
  const stages = [];
  let killedWith;
  let saved;
  const active = {
    name: 'journey-unit-suite',
    startedAt: 120,
    recorded: false,
    child: { kill(signal) { killedWith = signal; } },
  };
  const stage = interruptActiveStage('SIGTERM', active, stages, () => {
    saved = parseVerdict({ schemaVersion: 1, sha: 'a'.repeat(40), status: 'fail', jobs: stages }, 'a'.repeat(40));
  }, 175);

  assert.equal(killedWith, 'SIGTERM');
  assert.equal(stage.name, 'journey-unit-suite');
  assert.equal(stage.status, 'fail');
  assert.equal(stage.signal, 'SIGTERM');
  assert.equal(stage.durationMs, 55);
  assert.deepEqual(stage.failures, [{
    name: 'journey-unit-suite signal',
    message: 'journey-unit-suite terminated by signal SIGTERM',
  }]);
  assert.equal(stages[0], stage);
  assert.equal(active.recorded, true);
  assert.equal(saved.jobs[0].signal, 'SIGTERM');
  assert.equal(saved.jobs[0].name, 'journey-unit-suite');
});
