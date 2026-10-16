---
id: S-258
title: Installed CLI help and actionable unknown-command hints
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

As a KB Labs user, I can discover commands from the installed CLI and recover from a mistyped command using a hint that names the executable I invoked and a working help command.

## Steps

| # | Action | Expected |
|---|---|---|
| S-258.1 | Run the installed `kb` and `kb-create` binaries with root `--help`, root `help`, and an unknown command | Each help invocation exits 0 and prints usage. Each unknown command exits non-zero with catalogued code `KB_RUNTIME_UNKNOWN_COMMAND`; its rendered hint names the invoked binary and gives that binary's existing root `--help` command. Catalog rendering exposes no unresolved template parameter. |

## Evidence

The installed CLI journey step runs these commands from the isolated installed `HOME` and claims `S-258.1` in both local and secured modes.
