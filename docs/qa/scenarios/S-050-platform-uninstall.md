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

A user removes a V2 installation only after a human confirms its scope on a terminal, or an agent explicitly passes `--yes` (ADR-0025). Platform artifacts are removed; separate project files remain.

## Prerequisites

- [ ] Working V2 install and separate project directory with user files.

## Steps

| # | Action | Expected |
|---|---|---|
| S-050.1 | Inspect the target root and project paths before invoking uninstall | The selected platform root is the intended V2 installation and project files are outside that root. |
| S-050.2 | In a terminal, run `kb-create uninstall --platform-root "$PLATFORM_ROOT"` | Prompt names the platform root, installed services, active receipt and saved snapshots to remove, and says project files are kept. |
| S-050.3 | Answer `n` | Exits with `KB_INSTALL_OPERATION_CANCELLED`; the installation is untouched. |
| S-050.4 | Run `kb-create uninstall --platform-root "$PLATFORM_ROOT" --json` without a TTY | Exit 2 with `KB_INSTALL_CONFIRMATION_REQUIRED`; the hint is the exact rerun command with `--yes`; nothing is removed. |
| S-050.5 | Human path: rerun in a terminal and answer `y`. Agent path: rerun with `--yes --json` | Returns `ok: true`, operation `uninstall`, snapshot metadata and log path; installed platform artifacts are removed. |
| S-050.6 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | No installed platform receipt is reported. |
| S-050.7 | Inspect separate project directory | Workflows, config, source files and plugin source remain. |

## Pass criteria

No uninstall runs without a human's affirmative terminal answer or an explicit `--yes`; project files are preserved.
