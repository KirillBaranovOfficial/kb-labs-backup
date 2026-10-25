import test from 'node:test';
import assert from 'node:assert/strict';
import { stageFinishedLine, stageStartedLine } from './remote-profile-progress.mjs';

test('formats a visible start and terminal line with exit code and duration', () => {
  assert.equal(stageStartedLine('kb-devkit-check', 'kb-devkit check --json'), '[START] kb-devkit-check command=kb-devkit check --json\n');
  assert.equal(stageFinishedLine('kb-devkit-check', { exitCode: 1, signal: null, durationMs: 1250 }), '[DONE] kb-devkit-check exitCode=1 durationMs=1250\n');
  assert.equal(stageFinishedLine('journey-unit-suite', { exitCode: null, signal: 'SIGTERM', durationMs: 360_000 }), '[DONE] journey-unit-suite exitCode=signal:SIGTERM durationMs=360000\n');
});
