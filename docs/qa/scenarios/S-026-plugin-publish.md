---
id: S-026
title: S-026 — Publish a plugin to the marketplace registry
persona: plugin-author
area: marketplace
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A plugin author publishes a built plugin package to the configured marketplace registry.

## Prerequisites

- [ ] Plugin package passes validation and author is authenticated to a test registry.

## Steps

| # | Action | Expected |
|---|---|---|
| S-026.1 | Run `kb hub publish` from the plugin package context | Validation and publication complete successfully. |
| S-026.2 | Inspect the test registry for the published package and version | Registry metadata matches the package manifest. |
| S-026.3 | Install the published version into a disposable project | Marketplace resolves and installs that exact version. |
