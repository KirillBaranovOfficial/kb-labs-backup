---
id: S-012
title: Marketplace — Update an installed plugin pin
persona: solo-developer
area: marketplace
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user updates an installed plugin to the version resolved by the configured registry and confirms the project pin.

## Prerequisites

- [ ] The project has an older version of `@kb-labs/commit` installed.
- [ ] The configured registry contains a newer compatible version.

## Steps

| # | Action | Expected |
|---|---|---|
| S-012.1 | Run `kb marketplace update @kb-labs/commit --scope project` | The command completes and reports the updated package ID and exact version, or reports that there is nothing to update. |
| S-012.2 | Run `kb marketplace plugins list --scope project` and inspect `.kb/marketplace.lock` | Both show the exact newly resolved version; the lock records its integrity. |
| S-012.3 | Run `kb commit --help` | The commit command group remains available after the update. |
| S-012.4 | Request update of an unknown package with `kb marketplace update @kb-labs/not-published --scope project` | The command reports a package-specific failure in its warnings and does not change the existing `@kb-labs/commit` pin. |
