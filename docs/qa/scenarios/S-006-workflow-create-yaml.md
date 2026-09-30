---
id: S-006
title: S-006 — Define a workflow from YAML
persona: solo-developer
area: workflow
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer authors a project workflow in YAML and verifies that the workflow service accepts and lists the definition.

## Prerequisites

- [ ] Project runtime and workflow plugin available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-006.1 | Create `.kb/workflows/hello.yml` with a valid workflow ID, name, trigger, and job | File follows the workflow schema. |
| S-006.2 | Run `kb workflow lint` | Definition passes validation with no reported errors. |
| S-006.3 | Run `kb workflow defs list` | `hello` appears among project workflow definitions. |
| S-006.4 | Introduce a missing required field and run lint again | Lint identifies the invalid definition and field. |
