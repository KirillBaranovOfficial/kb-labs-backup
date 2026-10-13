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
| S-050.3 | Answer `n` in the terminal prompt | Exits 1; terminal output includes `KB_INSTALL_OPERATION_CANCELLED`; the active receipt remains. |
| S-050.4 | Run `kb-create uninstall --platform-root "$PLATFORM_ROOT" --registry "$REGISTRY" --json` without a TTY | Exits 2; JSON `error.code` is `KB_INSTALL_CONFIRMATION_REQUIRED`, and `error.hint` is exactly ``rerun `kb-create uninstall --platform-root "$PLATFORM_ROOT" --registry $REGISTRY --json --yes````, including the command's surrounding backticks; the active receipt remains. |
| S-050.5 | In a terminal, rerun `kb-create uninstall --platform-root "$PLATFORM_ROOT"` and answer `y` | Exits 0; human output shows an uninstall completion summary without a raw JSON result, including whether the `kb` command was removed, remains with its canonical validated installation owner, or was left untouched because its owner could not be verified; `current`, `versions/`, and every installer-owned state entry declared by `platform-layout.json` are removed. The install root itself is removed only if empty; foreign entries remain and are named in the summary. |
| S-050.6 | In an agent invocation, run `kb-create uninstall --platform-root "$PLATFORM_ROOT" --yes --json` | Exits 0; stdout is `{ "ok": true, "data": ... }`, with `operation: "uninstall"`, snapshot metadata, `logPath` inside `data`, and `shimAction` equal to `removed`, `left`, or `left-foreign`; `left` includes `shimOwner` as the canonical validated installation root, while `left-foreign` omits an owner claim. Any preserved foreign entries are listed; `current`, `versions/`, and every declared installer-owned state entry are removed. The install root is removed only if empty. |
| S-050.7 | Inspect the separate project directory after either successful path | The project root still exists and `$PROJECT_ROOT/note.txt` still contains `journey first value`. |
| S-050.8 | Put `keep.txt` in the install root and an unrelated sibling directory beside it, then uninstall the installation | Uninstall preserves `keep.txt` and the sibling byte-for-byte, removes only entries declared as owned by the validated installation layout, leaves the non-empty install root in place, and names `keep.txt` in the result. |

## Pass criteria

No uninstall runs without a human's affirmative terminal answer or an explicit `--yes`; project files are preserved.
