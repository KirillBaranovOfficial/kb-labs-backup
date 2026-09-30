---
id: S-051
title: Errors — show the unified actionable error envelope
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, api, agent]
---

## Goal

Common project, marketplace, and configuration failures expose stable error data and a concrete recovery action.

## Prerequisites

- [ ] Running host and fixtures for an unregistered project, unknown package, and uninstalled adapter.

## Steps

| # | Action | Expected |
|---|---|---|
| S-051.1 | Run a project command from an unregistered directory | Human message identifies the issue and gives `kb project add` as the next command. |
| S-051.2 | Run `kb marketplace install <unknown-package>` | Actionable error names the unavailable package. |
| S-051.3 | Run `kb adapter bind storage <uninstalled-package>` | Error names the missing package and platform-scope install command. |
| S-051.4 | Repeat with `--json` | Stable error fields are present; human output contains no raw stack trace or JSON blob. |
