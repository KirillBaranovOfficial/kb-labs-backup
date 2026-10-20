---
id: S-215
title: Plugin execution — required prompt input is actionable
persona: workflow-author
area: workflow
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli, studio, api, agent]
---

## Goal

Make a plugin prompt that cannot be answered by a non-interactive execution host
fail visibly instead of silently substituting a default value.

## Steps

| # | Action | Expected |
|---|---|---|
| S-215.1 | Execute a plugin through a workflow, REST request, Studio action, MCP tool call, or worker with a UI prompt and no prompt responder | Execution fails with `KB_RUNTIME_INPUT_INVALID`; the cause identifies the unavailable prompt and the hint tells the caller to supply that value as command or request input. A caller may provide a more specific hint. No prompt default is substituted. |
