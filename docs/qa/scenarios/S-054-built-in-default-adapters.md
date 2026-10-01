---
id: S-054
title: Fresh install works with built-in default adapters
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A fresh platform install has useful local persistent adapters without asking
the user to choose providers, add adapter bindings, or install an external
adapter service. Every selected default is replaceable with
`kb adapter bind <slot> <package>`; changing providers does not migrate or
delete data already stored by the previous provider.

## Prerequisites

- [ ] A clean platform root and one initialized project.
- [ ] The platform package registry is available for the install itself.

## Steps

| # | Action | Expected |
|---|---|---|
| S-054.1 | Install the platform in local mode and in secured mode using the default install flow without selecting adapters or adding adapter config, then run `kb adapter list --json` from each initialized project | Both installs resolve the same built-in providers for every platform slot with a persistent local implementation: `storage` is `@kb-labs/adapters-fs` with base directory `<project-root>/.kb/data`; `documentDatabase` and `kvStore` are `@kb-labs/adapters-sqlite`; `analytics` is `@kb-labs/adapters-analytics-sqlite`; `eventBus` is `@kb-labs/adapters-eventbus-cache` backed by the local StateBroker; `logger` is Pino; and `logs` is `HybridLogReader` backed by the logger ring buffer and SQLite persistence. These rows report their resolved provider and never fall back to `inmemory`. `cache` uses the local StateBroker; `invoke`, `artifacts`, `config`, and `notifier` use in-process implementations. `llm`, `embeddings`, `vectorStore`, and `snapshotManager` are reported unconfigured. No default requires an external network service. |
| S-054.2 | Immediately after a clean platform install, with the installed host running and no optional provider or external adapter service configured, run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json`, then run `kb-create doctor --platform-root "$PLATFORM_ROOT"` | JSON has `ok`, `installed`, `running`, `reachable`, and `platform.ok` set to `true`; the text form shows `Installed: yes`, `Running: yes`, and `Reachable: yes`. Both forms list every slot returned by `GET /health/adapters` with its `slot`, `mode`, and `implementation`. `storage` is `real` with implementation `@kb-labs/adapters-fs`; `logs` is `real` with implementation `HybridLogReader`. |
| S-054.3 | From a registered project with no adapter config, run the documented `e2e-hello` workflow and inspect its completed record | The run succeeds and `<project-root>/.kb/data/workflows/<runId>.json` exists with the same `runId` and terminal `success` status. The project's missing adapter config inherits the installed platform defaults. |
| S-054.4 | Inspect `kb adapter list --json` for each defaulted plugin-visible slot and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Every defaulted plugin-visible row reports `networkRequired: false`; doctor reports local defaults healthy and unconfigured optional providers without treating them as platform failures. |
| S-054.5 | Stage a small tracked-file change, run `kb config set commit.llm.enabled true --json`, then run `kb commit generate --json` with no LLM adapter bound | The command returns a typed `KB_ADAPTER_NOT_CONFIGURED` error envelope with `details.service: "llm"` and an action beginning `kb adapter bind llm `. It exits cleanly without a stack trace or a fabricated commit plan. |
| S-054.6 | With the Mind plugin available, no vector-store adapter bound, and no existing index, run `kb mind search --text "adapter defaults" --json` | The command returns a typed `KB_ADAPTER_NOT_CONFIGURED` error envelope with `details.service: "vectorStore"` and an action beginning `kb adapter bind vectorStore `. It exits cleanly without a stack trace or an empty-success search result. |
| S-054.7 | Stop the installed host, then run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` and its text form | Both forms report that adapter status cannot be checked because the host is stopped and give the exact next command `kb-create start --platform-root "$PLATFORM_ROOT"`; neither form prints a raw connection error. |
| S-054.8 | Bind `@kb-labs/adapters-openai` to the platform `llm` slot with `kb config set --scope platform platform.adapters.llm @kb-labs/adapters-openai` and leave both `OPENAI_API_KEY` and `platform.adapterOptions.llm.apiKey` unset; run `kb commit generate --json`, then `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | The command returns typed `KB_ADAPTER_CREDENTIALS_MISSING` naming `OPENAI_API_KEY` and the exact supported configuration command `kb config set platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}' --allow-missing-env`; its hint says no request was sent. Doctor identifies the missing OpenAI secret without printing a value. |
| S-054.9 | In fresh local and secured installs, create an administrator account where secured mode is enabled, complete a workflow run, write one document and one KV value through the platform's installed service APIs, run `kb-create restart --platform-root "$PLATFORM_ROOT"`, then read each item again | The administrator account remains usable; the completed workflow run, document, and KV value remain available with their original IDs and contents. `kb adapter list --json` and `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` show the configured persistent built-in providers for `documentDatabase`, `kvStore`, `storage`, `analytics`, and the log slots in both modes. |
| S-054.10 | On a fresh install with an analytics row, remove `analytics` from `platform.adapters` and `platform.adapterOptions` in `$PLATFORM_ROOT/.kb/kb.config.jsonc`, restart the host, and run `kb adapter list --json` and `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | The installed platform resolves `@kb-labs/adapters-analytics-sqlite` from its local default adapter manifest; the slot is real and healthy without a user-added binding. Existing `.kb/analytics/analytics.sqlite` stays on disk and is used by that provider. |
| S-054.11 | Write an analytics event to the default SQLite database, then run `kb adapter bind analytics <another-installed-analytics-package>` and inspect the selected adapter and the original database path | The selected provider is the requested package. The original SQLite database remains on disk and is not migrated or deleted; the replacement starts with only the data it owns. |

## Dependencies

- Workflow run-record persistence through the configured `IStorage` adapter is
  owned by the workflow stream. S-054.3 depends on that feature.
- S-054.5 and S-054.6 require the commit and Mind command plugins respectively;
  their expected missing-capability errors are owned by the adapter/runtime
  error path.
- `embeddings` stays unconfigured because the repository has no adequate local
  embedding implementation; semantic search with a populated vector index
  therefore also needs an embeddings adapter. `snapshotManager` is optional and
  unconfigured because the existing local filesystem provider implements a
  different runtime contract.
