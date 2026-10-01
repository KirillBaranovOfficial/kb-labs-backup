const SUBJECT = 'use deterministic journey plan'

function response(prompt) {
  const files = [...prompt.matchAll(/^\s*- (.+?) \([^\n]*\)$/gm)]
    .map((match) => match[1])

  if (files.length === 0) {
    throw new Error('The deterministic LLM prompt did not contain changed file paths')
  }

  return JSON.stringify({
    needsMoreContext: false,
    requestedFiles: [],
    commits: [{
      id: 'c1',
      type: 'chore',
      scope: 'e2e',
      message: SUBJECT,
      files,
      releaseHint: 'none',
      breaking: false,
      reasoning: {
        newBehavior: false,
        fixesBug: false,
        internalOnly: true,
        explanation: 'Fixed conventional-commit subject returned by the E2E fixture.',
        confidence: 1,
      },
    }],
  })
}

export class DeterministicLLM {
  async complete(prompt) {
    const content = response(prompt)
    return {
      content,
      usage: { promptTokens: 1, completionTokens: 1 },
      model: 'e2e-deterministic',
      stopReason: 'end_turn',
    }
  }

  async *stream(prompt) {
    yield response(prompt)
  }
}

export function createAdapter() {
  return new DeterministicLLM()
}

export default createAdapter
