---
id: S-018
title: Switch the platform LLM provider
persona: solo-developer
area: gateway
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A platform operator changes the configured LLM adapter and confirms all projects resolve that platform provider.

## Prerequisites

- [ ] Two LLM adapter packages are installed at platform scope and have valid provider credentials available to the platform process.
- [ ] Two registered projects use the same platform configuration.

## Steps

| # | Action | Expected |
|---|---|---|
| S-018.1 | From the platform root, run `kb config set --scope platform platform.adapters.llm @kb-labs/adapters-openai` | Platform config records the OpenAI adapter; Projects A and B resolve that adapter. |
| S-018.2 | Run an LLM-backed command from each project | Both requests use the platform OpenAI adapter. |
| S-018.3 | From the platform root, run `kb config set --scope platform platform.adapters.llm @kb-labs/adapters-vibeproxy` and repeat both commands | Both requests use the VibeProxy adapter; neither project config is changed. |
