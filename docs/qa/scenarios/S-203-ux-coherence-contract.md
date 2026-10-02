---
id: S-203
title: Consistent actionable outcomes across KB Labs
persona: solo-developer
area: platform-core
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, api, agent, studio]
---

## Goal

As a KB Labs user or automation author, I can tell whether an operation succeeded, understand and act on a failure, and consume the same machine-readable result across commands, HTTP, MCP, and Studio. Unexpected failures can be correlated with platform logs without exposing stack traces in user-facing output.

## Prerequisites

- [ ] Local and secured platform fixtures with registries of all CLI commands, HTTP routes, MCP tools, and Studio error boundaries.
- [ ] Registry-provided valid, invalid-input, typed-failure, and unexpected-failure fixtures; an unexpected failure can be correlated with structured logs.

## Steps

| # | Action | Expected |
|---|---|---|
| S-203.1 | In machine mode, invoke every registered TS and Go CLI command, HTTP route, and MCP tool through its registry using valid fixture input | Each successful result uses `{ "ok": true, "data": ... }` with optional `meta`. Command, route, or tool payloads (including doctor/status objects) are inside `data`. Machine CLI stdout contains exactly one JSON result and no diagnostics. HTTP retains its status semantics; MCP returns the envelope in `structuredContent` and keeps successful MCP signaling. Go tool CLIs apply this contract at their shared command boundary for every registered `kb-dev`, `kb-devkit`, and `kb-create` command, including install, update, rollback, and doctor; no handler writes directly to terminal output. |
| S-203.2 | Invoke every registered CLI command and HTTP route with invalid input and a typed failure; invoke every MCP tool with invalid input, an undeclared argument for tools without declared inputs, and the built-in MCP diagnostics tool without authorization in secured mode | Each failure uses `{ "ok": false, "error": ... }`, where `error` is the platform `ErrorEnvelope` with a stable `KB_*` code, message, hint, and correlation ID. Every code emitted by a Go tool exists in the platform error catalog; a missing code is rendered as `KB_RUNTIME_UNEXPECTED`. CLI exits 2 for invalid usage/input and 1 for other operation failures; HTTP preserves the appropriate non-2xx status; MCP sets `isError: true`, puts the envelope in `structuredContent`, and includes its readable message and hint in text content. A secured request without a token returns HTTP 401 with the same error envelope. Go tool CLIs, including kb-create, use exit 2 only for invalid usage/input and exit 1 for every other failure. Invalid input and typed operation failures from kb-create install, update, rollback, and doctor include a catalogued code, actionable hint, and correlation ID in both human and JSON modes. No surface returns a raw error or stack. |
| S-203.3 | Cause an unexpected failure in every registered CLI command, HTTP route, and MCP tool | Each failure is `KB_RUNTIME_UNEXPECTED` and includes a correlation ID. The matching structured log entry contains the stack and same correlation ID. Human output, machine responses, MCP content, and Studio UI contain no stack; MCP retains `isError: true` and shows the correlation ID, while surfaces with actions offer copy diagnostics. Go tool CLIs, including kb-create, assign a correlation ID to every failure at their shared boundary and log unexpected-error stacks as structured records carrying that same ID. |
| S-203.4 | Run every registered CLI command in human mode against the valid and failure fixtures | One shared renderer shows the error code, plain-language message, why it failed when known, and the exact next step. It reports success only after the handler returns normally; a void/undefined handler return means successful command execution and serializes as `data: null` in machine mode. Command postconditions remain the command's responsibility and are checked by its product case. |
| S-203.5 | Cause route, widget, and plugin-page failures by enumerating the live Studio router, widget, and federation registries; decode shared valid and invalid API-result fixtures; visit empty, loading, failed, unconfigured, and unavailable states | Every registered boundary presents a safe failure with a stable `KB_*` code, plain-language message, actionable hint, and correlation ID. Typed failures retain their envelope fields; unexpected failures use `KB_RUNTIME_UNEXPECTED` and a generated correlation ID. Copy diagnostics contains the code and correlation ID, never a raw error or stack. The API client decodes valid shared envelopes and turns invalid responses into a typed safe error using the platform's canonical UX-result validator through a browser-safe package subpath. Raw errors and stacks do not appear in the UI or ordinary `console.error`/`console.warn` calls; unexpected stack details are retained only by structured diagnostics. Empty, loading, failed, unconfigured, and unavailable states are visibly and accessibly distinguishable, with a recovery action where one exists. |
| S-203.6 | Run the automated registry coverage check for all registered CLI commands, HTTP routes, MCP tools, and Studio error boundaries | The check discovers entries from the live registries rather than a hand-maintained list, applies the valid and relevant failure fixtures, and reports every registration as covered or identifies the missing result/error contract. For MCP, every tool produced from the registry is exercised against the shared result schema and valid/failure fixtures. Go tool CLI coverage walks the live Cobra command trees for `kb-dev`, `kb-devkit`, and kb-create; for every leaf it invokes help and invalid input, then invokes a safe read-only command in every command group in machine mode and validates the single envelope against `contracts/ux-result/`. The catalog check discovers error codes emitted by Go sources and verifies each against the platform catalog. Adding a registration or leaf without the shared boundary or fixture coverage, or emitting an uncatalogued code, makes the check fail. |
| S-203.7 | Run `kb-dev watch --json`, `kb-devkit watch --json`, or `kb-create install --json` through the shared CLI boundary and observe normal completion and a stream failure | Each event is one JSONL record shaped as `{ "ok": true, "data": <event> }`. Normal completion ends with `{ "ok": true, "data": null, "meta": { "stream": { "terminal": true, "exitCode": 0 } } }`. A failure emits `{ "ok": false, "error": <ErrorEnvelope> }` with a correlation ID, then a terminal record with exit code 1 or 2. No raw event or error line bypasses the shared stream renderer. |

## Contract reference

Machine results use the shared schema and fixtures in `contracts/ux-result/`. Every CLI and HTTP/MCP machine result is an envelope: success is `{ "ok": true, "data": <payload>, "meta"?: {...} }`; failure is `{ "ok": false, "error": <ErrorEnvelope> }`. The platform error type is defined in `core/platform/src/error-envelope`.
