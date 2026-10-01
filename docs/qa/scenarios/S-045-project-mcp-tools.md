---
id: S-045
title: MCP — Connect to a project tool catalog
persona: solo-developer
area: agents
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [agent, api]
---

## Goal

An agent client connects to the selected project's MCP endpoint, discovers that project's available command tools, and reads project diagnostics over Streamable HTTP.

## Prerequisites

- [ ] Host running and project registered; the project MCP runtime is enabled.
- [ ] The selected project has the `commit` plugin installed and at least one plugin command manifest.
- [ ] In secured mode, a valid gateway bearer token is available to the agent client.

## Steps

| # | Action | Expected |
|---|---|---|
| S-045.1 | Send JSON-RPC `initialize` to `POST /api/v1/projects/$PROJECT_ID/mcp` with protocol version `2025-03-26`, empty capabilities, and client info | HTTP 200 JSON-RPC response contains `result.protocolVersion: "2025-03-26"`. |
| S-045.2 | Send JSON-RPC `tools/list` to the same project endpoint | The result includes tools named `kb-labs-commit__commit`, `workflow__workflow_run`, and `kb-labs__mcp_diagnostics`; every listed tool has an object `inputSchema`. Plugin tool names use the sanitized manifest plugin id, followed by `__` and the command path with spaces replaced by underscores. |
| S-045.3 | Call `kb-labs__mcp_diagnostics` with empty arguments | The JSON-RPC result is not marked `isError` and its text content includes `loaded cleanly`. |
| S-045.4 | In local mode, repeat initialize and `tools/list` without `Authorization` | Requests succeed through the loopback host and return the selected project's tool catalog. |
| S-045.5 | In secured mode, call `tools/list` first without a bearer token and then with an invalid bearer token | Both requests return HTTP 401 and do not return a tool catalog. |
