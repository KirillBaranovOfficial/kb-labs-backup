---
id: S-047
title: S-047 — Uninstall a marketplace plugin
persona: solo-developer
area: marketplace
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user removes one marketplace plugin while preserving other project plugins and source files.

## Prerequisites

- [ ] Two plugins installed in a project, with one target package ID.

## Steps

| # | Action | Expected |
|---|---|---|
| S-047.1 | Run `kb marketplace uninstall <package-id>` from the project root | Target plugin is removed from the project. |
| S-047.2 | Run `kb marketplace plugins list` | Target is absent; unrelated plugin remains installed and enabled. |
| S-047.3 | Inspect the project lock, invoke the removed target command, and invoke the unrelated plugin | Lock no longer pins target; its command is no longer discoverable; the unrelated command still works. |
