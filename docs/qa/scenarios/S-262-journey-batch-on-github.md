---
id: S-262
title: Journey batch runs on GitHub with retrievable evidence
persona: developer
area: quality
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## User value

A journey maintainer can run the same product-evidence batch locally or on GitHub, identify each result by commit and mode, and retrieve each mode's reports together. Moving heavy runs off the development laptop preserves the evidence needed to review a batch.

## Preconditions

- The repository's journey stack and candidate preparation inputs are available.
- A GitHub runner can check out the selected commit and run Docker-based journeys.
- For remote dispatch, the local checkout has the configured backup remote and GitHub CLI authentication.

## Steps

| # | Action | Expected |
|---|---|---|
| S-262.1 | Run the in-repository journey batch entry locally and on a GitHub runner for the same commit | Both runs execute the same ordered product-evidence steps: reset, build gate, journey unit suite, candidate preparation, then local and secured service exploration. A Docker image build failure is retried once for that mode; other failures are returned without retry. The summary has exactly one final result for pass, failure, or interruption, and the batch metadata records elapsed seconds for reset, build, unit, candidate, each mode, and the whole run. |
| S-262.2 | Inspect the batch summary and the reports uploaded for each mode | The run is identified by commit and mode, and each mode's artifact contains that mode's journey reports, logs, and `batch-metadata.json` with its result and stage durations in the layout used by the local batch summary. |
| S-262.3 | Dispatch a remote batch with a baseline label and retrieve its artifacts | The harness pushes the current commit to a `batch/<tag>` ref, dispatches the batch workflow with the baseline, waits for its outcome, and places each available artifact at `reports/<tag>-<mode>/` before running the existing summary and triage. If the remote run fails or times out, the harness downloads all available artifacts, then names the run ID and `gh run view` command. |

## Evidence scope

These rows describe developer-operated batch infrastructure and are checked by hermetic shell/workflow tests. The installed journey batch itself is run by the batch gate; these rows do not claim release coverage.
