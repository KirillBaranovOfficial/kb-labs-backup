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
| S-045.1 | Send JSON-RPC `initialize` to `POST /api/v1/projects/$PROJECT_ID/mcp` with protocol version `2025-03-26`, empty capabilities, and client info | HTTP 200 JSON-RPC response contains `result.protocolVersion: "2025-03-26"`. |
| S-045.2 | Send JSON-RPC `tools/list` to the same project endpoint | The result includes tools named `commit__commit`, `workflow__workflow_run`, and `kb-labs__mcp_diagnostics`; every listed tool has an object `inputSchema`. |
| S-045.3 | Call `kb-labs__mcp_diagnostics` with empty arguments | The JSON-RPC result is not marked `isError` and its text content includes `loaded cleanly`. |
