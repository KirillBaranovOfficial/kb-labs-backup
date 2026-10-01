import test from 'node:test'
import assert from 'node:assert/strict'
import { DeterministicLLM } from './index.js'

test('returns a parseable conventional-commit plan for changed files', async () => {
  const result = await new DeterministicLLM().complete('Files changed:\n- README.md (modified, +1/-0, IsNewFile: false)')
  const plan = JSON.parse(result.content)
  assert.equal(plan.commits[0].type, 'chore')
  assert.equal(plan.commits[0].message, 'use deterministic journey plan')
  assert.deepEqual(plan.commits[0].files, ['README.md'])
})
