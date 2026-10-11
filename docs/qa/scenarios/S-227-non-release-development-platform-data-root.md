---
id: S-227
title: Runtime — resolve development platform adapter paths from one data root
persona: solo-developer
area: config
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

Keep relative paths in development platform adapter configuration under the
workspace's `.kb/data` root when a command runs from another registered
project.

## Steps

| # | Action | Expected |
|---|---|---|
| S-227.1 | Resolve the platform layout from a development workspace, then resolve a relative platform adapter path while the command cwd is a different project | The path is beneath `<workspace>/.kb/data`; changing the command cwd does not change its resolution base. |

## Pass criteria

Development and installed platform roots come from the shared layout contract.
Platform adapter paths never use the command cwd as their base.
