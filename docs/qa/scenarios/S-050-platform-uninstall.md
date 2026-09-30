---
id: S-050
title: Platform — uninstall a V2 installation safely
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, agent]
---

## Goal

A user deliberately invokes the V2 uninstall operation and verifies that platform artifacts are removed while separate project files remain.

## Prerequisites

- [ ] Working V2 install and separate project directory with user files.

## Steps

| # | Action | Expected |
|---|---|---|
| S-050.1 | Inspect the target root and project paths before invoking uninstall | The selected platform root is the intended V2 installation and project files are outside that root. |
| S-050.2 | Run `kb-create uninstall --platform-root "$PLATFORM_ROOT" --json` | Returns `ok: true`, operation `uninstall`, snapshot metadata, and log path; installed platform artifacts are removed. |
| S-050.3 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | No installed platform receipt is reported. |
| S-050.4 | Inspect separate project directory | Workflows, config, source files, and plugin source remain. |

> Current CLI behavior: the V2 launcher exposes no explicit confirmation flag for uninstall. Treat invocation as the destructive confirmation; this differs from the general soft-confirmation policy in ADR-0025 and needs a product decision.
