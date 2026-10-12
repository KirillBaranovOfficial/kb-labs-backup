---
id: S-179
title: S-179 — Fresh developer checkout loads workspace plugins
persona: developer
area: developer-workspace
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

A developer can use the workspace CLI in a fresh monorepo checkout after installing dependencies and building the workspace. Configured adapter packages resolve from the platform workspace only when its platform config explicitly sets `platform.mode` to `development`. Adapter classification follows the declared KB adapter manifest, and only configured packages are resolved. Installed platforms use their marketplace lock even when they contain a pnpm workspace file.

## Prerequisites

- [ ] A fresh clone of the KB Labs monorepo with Node and pnpm available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-179.1 | Run `pnpm install --frozen-lockfile`, then `pnpm build` from the checkout root | Dependencies install and the workspace builds successfully without editing the committed marketplace lock. |
| S-179.2 | Run `node ./cli/bin/dist/bin.js qa cases validate --json` from the checkout root, then append an unclaimed step to an active release case in a scratch copy of `docs/qa/scenarios` and run the same command with that scratch copy as the working directory | The workspace CLI loads its plugin manifests and accepts the catalog when all claim gaps are in the checked-in baseline. A new gap fails validation with the step ID, case file, line, and exact next command `kb qa cases validate --json`; every baseline entry that no longer violates the rule also fails validation and identifies the baseline file and line. |
| S-179.3 | Run `node ./cli/bin/dist/bin.js --help` | Help lists workspace plugin command groups, including `qa`, `marketplace`, `workflow`, `commit`, `release`, `review`, `mind`, and `agent`. |
| S-179.4 | Run `node ./cli/bin/dist/bin.js marketplace sync --scope platform`, then inspect `.kb/marketplace.lock` | Workspace plugins are recorded under `plugins`; workspace adapters are recorded as platform entries under `adapters`. The lock retains every old workspace entity ID that still exists. |
| S-179.5 | Run `node ./cli/bin/dist/bin.js adapter list --json` from the checkout root while the host is stopped | The configured workspace adapter packages appear as the effective implementations for `storage`, `cache`, `eventBus`, `documentDatabase`, `kvStore`, `logger`, `logRingBuffer`, `logPersistence`, `analytics`, and `serviceTransport`; `kvStore` retains `@kb-labs/adapters-sqlite/kv`. Their runtime status is `unknown`, and each row points to `kb-create start --platform-root "<platform root>"`; no configured adapter is reported as `real` without host status. No adapter links in root `node_modules` are required. |
| S-179.6 | With the workspace CLI built but a configured workspace adapter package not built, run `node ./cli/bin/dist/bin.js qa cases validate --json` | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output that identifies the unbuilt workspace adapter and gives the exact next command `pnpm build`; it does not print a stack trace. |
| S-179.13 | With one plugin present in the workspace marketplace lock but its manifest build output missing, run `node ./cli/bin/dist/bin.js --help`, then invoke one command from that plugin with `--json` | Help succeeds and identifies the plugin as unavailable with the exact next command `pnpm build`; invoking the plugin command returns a typed unavailable-plugin error instead of `Unknown command` or a stack trace. |
| S-179.14 | Simulate installation of the sealed platform plugin package set by writing each installed package's `kb.plugin/3` manifest ID into `.kb/marketplace.lock`, then run CLI discovery | Every installed plugin loads without `PLUGIN_ID_MISMATCH`; each lock key equals the corresponding manifest `id`, including scoped IDs. |
| S-179.15 | Pass a marketplace lock with a plugin entry missing `package` to `writeMarketplaceLock` | The writer throws a typed `LOCK_SCHEMA_INVALID` error naming the invalid plugin ID and the next command `kb marketplace sync`; no lock file or temporary lock file is written. |
| S-179.16 | Put a schema v3 marketplace lock with a malformed SRI integrity in `.kb/marketplace.lock`, then run `kb-devkit check --json` | The workspace check rejects the lock with the same entry and validation reason as runtime lock validation, and its human-readable diagnostic gives the exact remediation `kb marketplace sync`. |
| S-179.17 | Add a workspace package that exports a `kb.adapter/1` manifest, then run `corepack pnpm --filter @kb-labs/core-runtime test` | The source-driven test discovers the adapter from the workspace package graph and requires every discovered adapter manifest to declare a boolean `networkRequired`; the new adapter needs no CLI package-name edit. |
| S-179.18 | Run the same build task twice while it fails because a dependency artifact is unavailable, then make that artifact available without changing the task's declared inputs and run `kb-devkit run build --json` | Both failed invocations exit nonzero and the second task executes again instead of replaying the cached failure. After the artifact is available, the command exits 0 and JSON reports `failed: 0`, `passed: 1`, and `cached: 0`; a later run may report the successful task as cached. |
| S-179.19 | From a fresh checkout with dependencies installed and no package `dist` outputs or devkit cache, run `kb-devkit run build --no-cache` | Every required workspace dependency builds before its consumer, the command exits 0, and the summary reports zero failed tasks. |
| S-179.20 | Build `tools/kb-devkit` from the checkout, then run its binary as `run build --no-cache` from a hermetic multi-package workspace with no `--packages` filter | Every package that declares the build task appears in the result, and the command does not report an empty package filter or an unexpected error. |
| S-179.21 | Run `kb-devkit run build --packages @missing/package` and `kb-devkit run missing-task` | Each command exits nonzero with a catalogued `KB_RUNTIME_INPUT_INVALID` diagnosis that says what is missing and gives the exact next command; neither prints an unexpected error or raw Go stack. |
| S-179.22 | From a clean git worktree with no changes, run `kb-devkit run build --affected --diff-only --json` | The command exits 0 and reports that no affected or dirty packages need work. |
| S-179.7 | In development mode, configure a package with an adapter manifest and a non-adapter package whose name begins `@kb-labs/adapters-`; list configured adapters | The package whose declared manifest is classified as an adapter resolves, regardless of its npm name; binding the non-adapter package fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` and identifies the invalid manifest binding. |
| S-179.8 | Configure one built and one unconfigured adapter in the platform workspace, then initialize the platform in development mode | Only the configured package is resolved; an unrelated workspace adapter is not imported or inspected. |
| S-179.9 | Set a project root distinct from the platform root and place workspace adapter packages in both roots | Workspace adapters are resolved only from the platform root; a project workspace cannot replace or add platform adapters. |
| S-179.10 | Initialize an installed platform that contains `pnpm-workspace.yaml` and workspace packages, without `platform.mode: development` | Workspace package resolution stays inactive and installed platform adapters continue to come from the marketplace lock. The kb-create rendered `.kb/kb.config.jsonc` does not contain the development marker. |
| S-179.11 | In development mode, bind a workspace package that has no KB adapter manifest `implements` value or no `createAdapter` export | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output that identifies the configured package as invalid and gives the exact next step to correct `platform.adapters` and rerun `node ./cli/bin/dist/bin.js qa cases validate --json`; the package is not silently skipped. |
| S-179.12 | Bind a package absent from both the platform workspace and platform marketplace lock | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output naming the missing package and the exact next command `kb marketplace install <package> --scope platform`; there is no silent skip. |
