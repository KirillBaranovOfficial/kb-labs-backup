export function stageStartedLine(name, command) {
  return `[START] ${name} command=${command}\n`;
}

export function stageFinishedLine(name, { exitCode, signal, durationMs }) {
  return `[DONE] ${name} exitCode=${signal ? `signal:${signal}` : exitCode} durationMs=${durationMs}\n`;
}
