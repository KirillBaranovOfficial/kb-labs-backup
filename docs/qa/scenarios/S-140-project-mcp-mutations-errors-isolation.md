---
id: S-140
title: MCP — Call project tools safely across projects
persona: solo-developer
area: agents
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [agent, api]
---

## Goal

An agent can invoke an authorized mutating project tool, receives structured errors for invalid calls, and cannot read or mutate another project's tools or data.

## Prerequisites

- [ ] Host running with projects A and B registered and their project-scoped MCP runtimes enabled.
- [ ] Project A has the `@kb-labs/commit` plugin installed at project scope and a prepared commit plan for a staged change in a disposable repository; project B is a separate project with a different repository and no project-scoped commit plugin.
- [ ] In secured mode, a valid bearer token is available; an invalid token can be supplied for the negative path.

## Steps

| # | Action | Expected |
|---|---|---|
| S-140.1 | On project A's MCP endpoint, call `tools/list` using `Content-Type: application/json` and `Accept: application/json, text/event-stream` | The catalog includes only tools available in project A; the commit command schema reflects its manifest-declared flags and required fields. |
| S-140.2 | Call `kb-labs-commit__commit_apply` with `{}` | MCP result has `isError: false` and text containing the new commit SHA; project A's Git history contains that commit and no remote push occurs. |
| S-140.3 | Call `tools/call` for `tool-that-does-not-exist` with `{}` | MCP returns `isError: true` with a typed error envelope containing stable `code`, `message`, and `hint`; the hint names the next valid discovery action. |
| S-140.4 | Call the commit tool with an argument whose type violates its listed `inputSchema` | MCP returns `isError: true` with a typed validation error envelope naming the argument and expected type; no command runs and repository state is unchanged. |
| S-140.5 | Call `tools/list` and `tools/call` against project B's endpoint from the same MCP client | Project B does not list project A's project-scoped commit tool; attempting that tool through B returns an unknown-tool error, and project A's staged change is not visible through B. |
| S-140.6 | In secured mode, call project A's `tools/list` with a missing token, then with an invalid bearer token | Each request returns HTTP 401 with an error response and no project tool catalog. |
