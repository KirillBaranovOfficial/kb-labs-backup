---
id: S-046
title: Studio — Switch project context and inspect its extensions
persona: solo-developer
area: studio
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [studio]
---

## Goal

A Studio user selects among registered projects and verifies that each shows its own plugin and adapter set.

## Prerequisites

- [ ] Studio host, two registered projects, and distinct plugin and adapter sets for each.

## Steps

| # | Action | Expected |
|---|---|---|
| S-046.1 | Open Studio and choose project A in the `Active project` selector | The selector displays project A's name. |
| S-046.2 | Open Plugins and Adapters | Only extensions installed or bound to project A are shown. |
| S-046.3 | Choose project B in the same selector and open Plugins and Adapters | The selector displays project B's name and its distinct plugin and adapter set appears; project A's extensions are absent. |
| S-046.4 | Open Projects | The table shows each registered project's name, path, runtime status, `.kb/` declaration, and last-used value; an empty registry instead shows `No projects registered` and `Add project`. |
| S-046.5 | Choose Add project, enter an initialized project folder, and select Register | A success message identifies the registered folder and the project appears in the table. |
| S-046.6 | Choose Remove for the new project's row and confirm with Unregister | The row disappears and Studio reports `Unregistered <name>; files were not touched.`; project folder and runtime state remain. |
