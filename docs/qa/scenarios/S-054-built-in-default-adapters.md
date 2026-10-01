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

A fresh platform install has a useful local adapter pack without asking the user
to choose providers or requiring an external adapter service.

## Prerequisites

- [ ] A clean platform root and one initialized project.
- [ ] The platform package registry is available for the install itself.

## Steps

| # | Action | Expected |
|---|---|---|
| S-054.1 | Install the platform using the default install flow without selecting adapters, then run `kb adapter list --json` from the initialized project | The output lists all 16 plugin-visible slots and their effective provider: `storage` is `@kb-labs/adapters-fs` in `real` mode with `baseDir` `<project-root>/.kb/data`; `cache` and workflow state use the platform-local StateBroker; `logger` is Pino; `analytics`, `documentDatabase`, and `kvStore` use SQLite; `eventBus`, `invoke`, `artifacts`, `config`, and `notifier` use in-process implementations; `logs` is `HybridLogReader` backed by the local logger ring buffer and SQLite persistence; `llm`, `embeddings`, `vectorStore`, and `snapshotManager` are reported unconfigured. No default requires an external network service. |
| S-054.2 | Immediately after a clean platform install, with the installed host running and no optional provider or external adapter service configured, run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json`, then run `kb-create doctor --platform-root "$PLATFORM_ROOT"` | JSON has `ok`, `installed`, `running`, `reachable`, and `platform.ok` set to `true`; the text form shows `Installed: yes`, `Running: yes`, and `Reachable: yes`. Both forms list every slot returned by `GET /health/adapters` with its `slot`, `mode`, and `implementation`. `storage` is `real` with implementation `@kb-labs/adapters-fs`; `logs` is `real` with implementation `HybridLogReader`. |
| S-054.3 | From a registered project with no adapter config, run the documented `e2e-hello` workflow and inspect its completed record | The run succeeds and `<project-root>/.kb/data/workflows/<runId>.json` exists with the same `runId` and terminal `success` status. The project's missing adapter config inherits the installed platform defaults. |
| S-054.4 | Inspect `kb adapter list --json` for each defaulted plugin-visible slot and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Every defaulted plugin-visible row reports `networkRequired: false`; doctor reports local defaults healthy and unconfigured optional providers without treating them as platform failures. |
| S-054.5 | Stage a small tracked-file change, run `kb config set commit.llm.enabled true --json`, then run `kb commit generate --json` with no LLM adapter bound | The command returns a typed `KB_ADAPTER_NOT_CONFIGURED` error envelope with `details.service: "llm"` and an action beginning `kb adapter bind llm `. It exits cleanly without a stack trace or a fabricated commit plan. |
| S-054.6 | With the Mind plugin available, no vector-store adapter bound, and no existing index, run `kb mind search --text "adapter defaults" --json` | The command returns a typed `KB_ADAPTER_NOT_CONFIGURED` error envelope with `details.service: "vectorStore"` and an action beginning `kb adapter bind vectorStore `. It exits cleanly without a stack trace or an empty-success search result. |
| S-054.7 | Stop the installed host, then run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` and its text form | Both forms report that adapter status cannot be checked because the host is stopped and give the exact next command `kb-create start --platform-root "$PLATFORM_ROOT"`; neither form prints a raw connection error. |
| S-054.8 | Bind `@kb-labs/adapters-openai` to the platform `llm` slot with `kb config set --scope platform platform.adapters.llm @kb-labs/adapters-openai` and leave both `OPENAI_API_KEY` and `platform.adapterOptions.llm.apiKey` unset; run `kb commit generate --json`, then `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | The command returns typed `KB_ADAPTER_CREDENTIALS_MISSING` naming `OPENAI_API_KEY` and the exact supported configuration command `kb config set platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}' --allow-missing-env`; its hint says no request was sent. Doctor identifies the missing OpenAI secret without printing a value. |

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
