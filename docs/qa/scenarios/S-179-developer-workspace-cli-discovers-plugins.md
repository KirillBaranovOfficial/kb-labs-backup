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
| S-179.2 | Run `node ./cli/bin/dist/bin.js qa cases validate --json` from the checkout root | The workspace CLI loads its plugin manifests and validates the QA cases successfully. |
| S-179.3 | Run `node ./cli/bin/dist/bin.js --help` | Help lists workspace plugin command groups, including `qa`, `marketplace`, `workflow`, `commit`, `release`, `review`, `mind`, and `agent`. |
| S-179.4 | Run `node ./cli/bin/dist/bin.js marketplace sync --scope platform`, then inspect `.kb/marketplace.lock` | Workspace plugins are recorded under `plugins`; workspace adapters are recorded as platform entries under `adapters`. The lock retains every old workspace entity ID that still exists. |
| S-179.5 | Run `node ./cli/bin/dist/bin.js adapter list --json` from the checkout root | The configured workspace adapter packages appear as the effective implementations for `storage`, `cache`, `eventBus`, `documentDatabase`, `kvStore`, `logger`, `logRingBuffer`, `logPersistence`, `analytics`, and `serviceTransport`; `kvStore` retains `@kb-labs/adapters-sqlite/kv`. No adapter links in root `node_modules` are required. |
| S-179.6 | With the workspace CLI built but a configured workspace adapter package not built, run `node ./cli/bin/dist/bin.js qa cases validate --json` | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output that identifies the unbuilt workspace adapter and gives the exact next command `pnpm build`; it does not print a stack trace. |
| S-179.7 | In development mode, configure a package with an adapter manifest and a non-adapter package whose name begins `@kb-labs/adapters-`; list configured adapters | The package whose declared manifest is classified as an adapter resolves, regardless of its npm name; binding the non-adapter package fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` and identifies the invalid manifest binding. |
| S-179.8 | Configure one built and one unconfigured adapter in the platform workspace, then initialize the platform in development mode | Only the configured package is resolved; an unrelated workspace adapter is not imported or inspected. |
| S-179.9 | Set a project root distinct from the platform root and place workspace adapter packages in both roots | Workspace adapters are resolved only from the platform root; a project workspace cannot replace or add platform adapters. |
| S-179.10 | Initialize an installed platform that contains `pnpm-workspace.yaml` and workspace packages, without `platform.mode: development` | Workspace package resolution stays inactive and installed platform adapters continue to come from the marketplace lock. The kb-create rendered `.kb/kb.config.jsonc` does not contain the development marker. |
| S-179.11 | In development mode, bind a workspace package that has no KB adapter manifest `implements` value or no `createAdapter` export | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output that identifies the configured package as invalid and gives the exact next step to correct `platform.adapters` and rerun `node ./cli/bin/dist/bin.js qa cases validate --json`; the package is not silently skipped. |
| S-179.12 | Bind a package absent from both the platform workspace and platform marketplace lock | Startup fails with typed `KB_HOST_ADAPTER_UNAVAILABLE` output naming the missing package and the exact next command `kb marketplace install <package> --scope platform`; there is no silent skip. |
