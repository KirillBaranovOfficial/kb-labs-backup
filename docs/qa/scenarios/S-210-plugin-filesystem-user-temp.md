---
id: S-210
title: Plugin filesystem access works in a user's temporary project directory
persona: plugin-author
area: plugins
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A plugin author can run a plugin from a project in the operating system's user-owned temporary directory while the runtime continues to deny system files.

## Prerequisites

- [ ] Installed V2 platform and registered project rooted under the OS-provided user temp directory.
- [ ] Linked demo plugin command uses `ctx.runtime.fs` to write and read `.kb/output/runtime-temp-probe.txt`.

## Steps

| # | Action | Expected |
|---|---|---|
| S-210.1 | From the registered project under the user temp directory, run `kb demo hello` | The command succeeds and `.kb/output/runtime-temp-probe.txt` contains the exact value written through `ctx.runtime.fs`; the CLI does not report a filesystem permission error. |
