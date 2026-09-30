---
id: S-018
title: Switch the configured project LLM provider
persona: solo-developer
area: gateway
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer changes the LLM adapter selected by one project and confirms the change does not rewrite platform or other project configuration.

## Prerequisites

- [ ] Two LLM adapter packages are installed at platform scope and have valid provider credentials available to the platform process.
- [ ] Two disposable projects have project-local configuration files.

## Steps

| # | Action | Expected |
|---|---|---|
| S-018.1 | From project A, run `kb config set --scope project platform.adapters.llm @kb-labs/adapters-openai` | Project A config records the OpenAI adapter in `platform.adapters.llm`. |
| S-018.2 | Run an LLM-backed command from project A | The request uses the OpenAI adapter. |
| S-018.3 | From project A, run `kb config set --scope project platform.adapters.llm @kb-labs/adapters-vibeproxy` and repeat the command | The request uses the VibeProxy adapter; project B and platform config retain their previous values. |
