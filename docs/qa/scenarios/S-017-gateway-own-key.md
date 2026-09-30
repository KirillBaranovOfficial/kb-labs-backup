---
id: S-017
title: S-017 — Configure a project’s own LLM key
persona: solo-developer
area: gateway
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

A developer configures an LLM adapter key through the supported config layer and makes an LLM-backed operation available.

## Prerequisites

- [ ] OpenAI adapter installed and a valid key supplied through the local secret environment.

## Steps

| # | Action | Expected |
|---|---|---|
| S-017.1 | Set `OPENAI_API_KEY` in the local secret environment | Secret is available to the platform process and is not written in plain text to project config. |
| S-017.2 | Configure the LLM adapter through Studio’s adapter form or `kb config set` for the appropriate scope | Effective adapter selection and key reference are saved at the selected scope. |
| S-017.3 | Run an LLM-backed command | Request succeeds using the configured provider. |
| S-017.4 | Inspect user config and command output | Key value is not printed or persisted in plain text. |
