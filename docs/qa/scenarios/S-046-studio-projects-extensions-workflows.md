---
id: S-046
title: S-046 — Manage project plugins and adapters in Studio
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
| S-046.1 | Open Studio and select project A | Project A is named as the active project. |
| S-046.2 | Open Plugins and Adapters | Only the extensions bound/installed for project A are shown. |
| S-046.3 | Select project B and open the same views | Project B is active and its distinct plugin and adapter set is shown. |
