---
id: S-012
title: S-012 — Update a pinned marketplace plugin
persona: solo-developer
area: marketplace
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user updates an installed plugin to a newer available version and verifies the new pin and command.

## Prerequisites

- [ ] Registry contains two versions of a plugin; project currently has the older version pinned.

## Steps

| # | Action | Expected |
|---|---|---|
| S-012.1 | Run `kb marketplace update <package-id>` | Update completes or reports the available version when explicitly requested. |
| S-012.2 | Run `kb marketplace plugins list` and inspect `.kb/marketplace.lock` | Project records the resolved exact version and integrity. |
| S-012.3 | Invoke a command from the updated plugin | Command remains available and returns its documented result. |
