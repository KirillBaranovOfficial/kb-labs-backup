# ADR-0050: Product Cases Are the Specification (Case-First TDD)

**Date:** 2026-10-01
**Status:** Accepted
**Deciders:** KB Labs Team
**Last Reviewed:** 2026-10-01
**Tags:** process, qa, testing, docs

## Context

Releases were verified by hand and by tests written after the code. Tests
described what the code happened to do, docs drifted from both, and nothing
said what a user should be able to do. The owner cannot test the whole system
by hand each release, so a passing end-to-end run has to mean the user's flow
works.

## Decision

**The product case catalog `docs/qa/scenarios/*.md` is the product truth.** A
case describes a user's goal as steps (`S-###.n`), each an exact action and an
observable outcome. If a case says X, the product does X. Code, tests, docs
and reports follow the catalog; when they disagree with a case, they are wrong
unless the case is provably stale, and then the case is fixed first.

**All work is case-first TDD:**

1. **Case.** Before any plan or code, write a new case or edit an existing one:
   what the user expects and how it must work. New behavior starts as
   `status: planned`. Case edits that change behavior are approved by the owner.
2. **Tests, top-down from the case.** An end-to-end step claims the case steps
   it verifies and fails until the feature exists. Then integration and unit
   tests are derived from the same case steps.
3. **Implementation** until the tests pass, then the case becomes `active`, and
   `release: true` when it gates releases.

**Test pyramid, one scenario.** Every level serves the same case:

| Level | Proves | Claim |
|---|---|---|
| End-to-end journey (clean install, real CLI/HTTP/Studio, both modes) | The user's flow works as the case says | `cases=["S-###.n"]`, `@S-###.n` |
| Integration (package or service boundary, real dependencies where cheap) | The contract behind a step | `// case: S-###.n` |
| Unit | The logic behind a step | `// case: S-###.n` |

A release case passes only on an executed end-to-end claim. Lower levels give
fast feedback and pinpoint failures; they never substitute for the journey.

**Reports and docs derive from cases.** `kb qa cases` validates the catalog,
maps claims and computes PASS / FAIL / NOT RUN / NOT COVERED per step for the
local gate, the release candidate and nightly alike. Its output comes as JSON
for agents and Markdown/HTML for people. User docs reference the cases they
describe, and a case change marks those pages stale.

## Consequences

- A feature without a case is not started, and a behavior change without a case
  edit is not merged.
- Coverage gaps are visible per user flow, not per file.
- Writing the case first costs time up front. That is intended: it is where
  product decisions are made.
- Tests that assert a route answer instead of the user-visible outcome do not
  satisfy a case.

## Related

- ADR-0025: the destructive action protocol, which cases assert.
- ADR-0049: error envelope. Negative case steps assert its code and hint.
- `plugins/qa/README.md`: `kb qa cases`.
