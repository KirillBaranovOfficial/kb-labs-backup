export function interruptActiveStage(signal, active, stages, saveVerdict, now) {
  if (!active) return undefined;
  signalStageProcessTree(active.child, signal);
  const durationMs = Math.round(now - active.startedAt);
  const message = `${active.name} terminated by signal ${signal}`;
  const endedAtMs = Date.now();
  const startedAtMs = endedAtMs - durationMs;
  const stage = {
    name: active.name,
    command: active.command,
    startedAtMs,
    endedAtMs,
    status: 'fail',
    failures: [{ name: `${active.name} signal`, message }],
    logTail: message,
    exitCode: null,
    signal,
    durationMs,
  };
  stages.push(stage);
  active.recorded = true;
  saveVerdict();
  return stage;
}

function signalStageProcessTree(child, signal) {
  if (!child.pid || process.platform === 'win32') {
    child.kill(signal);
    return;
  }
  try {
    process.kill(-child.pid, signal);
  } catch (error) {
    if (error.code !== 'ESRCH') throw error;
    child.kill(signal);
  }
}
