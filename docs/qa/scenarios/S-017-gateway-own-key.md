---
id: S-017
title: Configure the platform LLM adapter with the user's own key
persona: solo-developer
area: gateway
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer stores an LLM adapter selection and secret reference at platform scope so LLM-backed project features can use the developer's provider.

## Prerequisites

- [ ] The LLM adapter package is installed at platform scope.
- [ ] `OPENAI_API_KEY` is available to the platform process through the local secret environment.

## Steps

| # | Action | Expected |
|---|---|---|
| S-017.1 | Run `kb config set --scope platform platform.adapters.llm @kb-labs/adapters-openai` | The platform user config records `@kb-labs/adapters-openai` at `platform.adapters.llm`. |
| S-017.2 | Run `kb config set --scope platform platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}'` | The platform user config stores the literal environment reference, not the key value. |
| S-017.3 | Run an LLM-backed command in a project using the platform config | The request uses the configured LLM adapter. |
| S-017.4 | Inspect the platform config and command output | Neither contains the resolved API key. |
| S-017.5 | From inside a registered project, run `kb config set --scope platform platform.adapterOptions.llm.baseURL 'https://llm.example.test/v1' --json` | The JSON `file` field names the installed platform's `.kb/kb.config.jsonc`; that file contains the value, the registered project's `.kb/kb.config.jsonc` is unchanged, and the result gives the exact `kb-create restart --platform-root <platform-root>` command needed to activate adapter changes. |

LLM configuration is optional for platform onboarding, so this case is not a core release gate.
