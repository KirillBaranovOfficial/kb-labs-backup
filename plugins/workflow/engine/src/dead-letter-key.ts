export function workflowDeadLetterCacheKey(runId: string, jobId: string): string {
  return `workflow:dlq:${runId}:${jobId}`
}
