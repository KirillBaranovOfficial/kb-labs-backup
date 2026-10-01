---
name: intent-flow
globs:
  - "docs/qa/scenarios/**"
  - "docs/product/**"
description: Turn a product intent ("users should be able to…", a bug report, a feature idea) into a change through product cases first — case, then tests top-down, then code. Use at the start of any task that changes user-visible behavior of KB Labs.
---

# Intent flow: intent → case → tests → code

The case catalog in `docs/qa/scenarios/` is the product specification ([ADR-0050](../../docs/adr/0050-product-cases-are-the-specification.md)). A task starts from an intent, not from a plan or from code. Follow these steps in order, and don't skip ahead.

## 1. Restate the intent as user value

Write one sentence: who the user is (persona), what they want to achieve, and why it matters. If the intent has no user-visible outcome (a pure refactor or build change), no case change is needed. Say so explicitly and go to step 4 with the existing claims as the safety net.

Check `docs/product/decisions.md` for decisions that bound the intent: release scope, supported OS, roles, adapter defaults, scheduling model. If the intent contradicts a recorded decision, stop and ask the owner. Don't silently override a decision.

## 2. Find or write the case

1. Search the catalog: `node ./cli/bin/dist/bin.js qa cases list --json` and `rg -l '<keyword>' docs/qa/scenarios`.
2. If a case covers the capability, edit it: change the step's Expected, add a step with the next free id, or split a case that has grown two goals. Never renumber existing step ids; claims reference them.
3. Otherwise, add a case using the next free `S-###` id and the frontmatter in `docs/qa/README.md`. Set `status: planned` until it is implemented, and set `release: true` only for platform-core and Commit-plugin behavior.
4. Every step is `Action → observable Expected`: a command, click or request, and what the user can see (output, exit code, page state, file, HTTP status, typed error `code` and hint). Verify each command, flag, route, field and UI selector against source.
5. Validate: `node ./cli/bin/dist/bin.js qa cases validate --json` must report no diagnostics.

The case change is the first commit: `docs(qa): …`.

## 3. Write failing tests, top-down

| Level | Where | Claim syntax | Counts for release |
|---|---|---|---|
| Journey (installed platform, real services) | `e2e/journey/` step modules | `cases=["S-###.n"]` on the step | yes |
| Studio e2e | `e2e/studio/` Playwright specs wired into the journey runner | `@S-###.n` in the test title | yes |
| Integration and unit | package `__tests__`, Go `_test.go` | `// case: S-###.n` above the test | no (fast feedback only) |

Start with the journey or Playwright step that proves the user outcome, then add the integration and unit tests that pin the mechanics. Run them and confirm they fail for the right reason. Journey unit tests run with `python3 -m unittest discover -s e2e/journey -p 'test_*.py'`. The full journey needs the local gate (`scripts/local-gate.sh batch`).

The tests are the second commit: `test(…): …`.

## 4. Implement

Write the smallest change that turns the tests green. User-facing failures use the ADR-0049 error envelope (`code`, `message`, `hint`). Build what you touch with `corepack pnpm -r --filter "<pkg>..." build`, then run its type-check and tests. No legacy aliases or compatibility readers: the product has one consumer, so breaks are clean.

When the case's steps pass, change `status: planned` to `active`. The implementation is the third commit.

## 5. Update derived artifacts

- **Docs:** update the user docs the change affects (follow `docs-authoring`). Docs describe what the cases promise, not more.
- **Decisions:** if you made a product call, add a dated entry to `docs/product/decisions.md` naming the cases it affects.
- **Coverage:** `node ./cli/bin/dist/bin.js qa cases coverage --level release --json` shows PASS, FAIL, NOT RUN or NOT COVERED per step. A release case isn't done until its steps are PASS in a journey run.

## Report

List the commits in order (case → tests → code → docs), the steps each test claims, the verification output, what the full gate still has to confirm, and any product decision you made, with your recommendation for the owner.
