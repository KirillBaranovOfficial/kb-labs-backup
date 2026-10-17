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
- For a remote run, the local checkout has the configured backup remote and GitHub CLI authentication.

## Steps

| # | Action | Expected |
|---|---|---|
| S-262.1 | Run the in-repository journey batch entry locally and on a GitHub runner for the same commit | Both runs execute the same ordered product-evidence steps: reset, build gate, journey unit suite, candidate preparation, then local and secured service exploration. They use the same Compose file list and Verdaccio storage configuration in both environments. A Docker image build failure is retried once for that mode; other failures are returned without retry. The summary has exactly one final result for pass, failure, or interruption, renders commit and base-ref values as text without shell errors, and the batch metadata records elapsed seconds for reset, build, unit, candidate, each mode, and the whole run. If the affected-build base ref is absent, the entry names the missing ref and the next command `git fetch --all --prune`. Before the hosted batch, `kb-devkit check` verifies that every package selected by the release registry belongs to the live build graph, and reports any package with no build target. A clean shared build produces every release package output, including generated manifests for candidate fixtures; the build stage verifies declared release outputs and fails before candidate preparation with the package and exact build command when an output is missing. Candidate preparation repeats that shared check before packing. Tooling packages without a runtime release output are not included in the release registry. If the entry fails before either mode starts, its output still contains root `summary.md`, `batch-metadata.json`, and `build.log`. |
| S-262.2 | Inspect the batch summary and the reports uploaded for each mode | The run is identified by commit and mode, and each per-mode artifact contains the complete shared batch output root, including every root stage log and both mode directories, so later stage logs cannot be omitted from the artifact. On failure, `summary.md` names the failing stage log by a path relative to that artifact root. If the publisher cannot authenticate to or write to Verdaccio, it stops before package publication with a clear registry/auth diagnostic and a next command to inspect Verdaccio; it does not emit a per-package 404 cascade. If a journey container exits non-zero, the shared runner saves its complete logs in a named file under `reports/containers/`, and the failure's `Next:` points to that file. If setup fails before the batch entry writes a summary, the GitHub step summary still succeeds and reports `batch did not start: <failed step>`. Both per-mode artifact uploads remain non-failing when no files exist, so available evidence can still be uploaded. |
| S-262.3 | Push a remote batch to a `batch/<tag>` ref and retrieve its artifacts | Pushing the current commit starts the workflow from that ref without requiring the workflow file on the default branch. The checkout fetches the repository default branch, and the affected build uses its `origin/<default-branch>` ref. The harness queries the Actions runs API by branch, pushed SHA, and `push` event, then selects the run whose `path` is the workflow file path; it waits for that run and watches/downloads artifacts by run ID into `reports/<tag>-<mode>/` before running the existing summary and triage. If the run does not appear within the bounded wait, the harness names the API query as the next command. If the run fails or times out, the harness downloads all available artifacts, then names the run ID and `gh run view` command. Manual workflow dispatch remains available for reruns after the workflow is present on the default branch; its optional baseline input labels batch metadata and does not change the affected-build comparison. |

## Evidence scope

These rows describe developer-operated batch infrastructure and are checked by hermetic shell/workflow tests. The installed journey batch itself is run by the batch gate; these rows do not claim release coverage.
