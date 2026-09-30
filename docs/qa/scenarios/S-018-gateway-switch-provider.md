---
id: S-018
title: S-018 — Switch the configured LLM provider
persona: solo-developer
area: gateway
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

A developer changes the configured LLM provider and confirms a new request uses the selected provider.

## Prerequisites

- [ ] Two supported LLM adapters installed and configured with valid credentials.

## Steps

| # | Action | Expected |
|---|---|---|
| S-018.1 | Set the project’s `platform.adapters.llm` choice to provider A using Studio or `kb config set --scope project` | Project config records provider A selection. |
| S-018.2 | Run an LLM-backed command and inspect provider metadata in its result | Request uses provider A. |
| S-018.3 | Change the choice to provider B and run the command again | New request uses provider B without modifying another project’s config. |
