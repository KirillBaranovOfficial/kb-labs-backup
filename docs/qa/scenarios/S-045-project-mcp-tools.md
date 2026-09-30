---
id: S-045
title: MCP — call project-scoped tools
persona: solo-developer
area: agents
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [agent, api]
---

## Goal

An agent discovers and calls diagnostic tools for the selected project through its MCP endpoint.

## Prerequisites

- [ ] Project runtime and MCP enabled for the project; MCP client.

## Steps

| # | Action | Expected |
|---|---|---|
| S-045.1 | Initialize the project MCP endpoint through the host project route | Initialization returns a valid server response. |
| S-045.2 | Call `tools/list` | Project tools and input schemas appear. |
| S-045.3 | Call a read-only diagnostic tool | Structured result identifies the selected project. |
