---
id: S-051
title: Errors — show the unified actionable error envelope
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli, api, agent]
---

## Goal

Common project, marketplace, and configuration failures expose stable error data and a concrete recovery action.

## Prerequisites

- [ ] Running host and fixtures for an unregistered project, unknown package, and uninstalled adapter.

## Steps

| # | Action | Expected |
|---|---|---|
| S-051.1 | From an initialized but unregistered project directory, run `kb project show` | Exits non-zero; output is `The project {path} is not in the registry (the folder may have moved). [KB_PROJECT_UNKNOWN]` followed by `Hint: Register it with "kb project add {path}".`; `{path}` is the current path, redacted to `~` under the home directory; no stack trace or JSON object is printed. |
| S-051.2 | Run `kb marketplace install @kb-labs/not-published` | Exits non-zero; the shared human renderer prints `Plugin @kb-labs/not-published was not found in the registry. [KB_PLUGIN_NOT_FOUND]` and `Hint: kb marketplace search`; no raw stack trace or JSON object is printed. |
| S-051.3 | Run `kb adapter bind storage @kb-labs/not-installed` | Exits non-zero; the shared human renderer prints `Adapter package @kb-labs/not-installed is not installed on this platform. [KB_ADAPTER_NOT_INSTALLED]` and `Hint: kb marketplace install @kb-labs/not-installed --scope platform`; no raw stack trace or JSON object is printed. |
| S-051.4 | Repeat each failing command with `--json` | Each command returns `{ok:false,error:{code,message,hint}}` using the shared error envelope: marketplace install uses `KB_PLUGIN_NOT_FOUND`, message `Plugin @kb-labs/not-published was not found in the registry.`, hint `kb marketplace search`; adapter bind uses `KB_ADAPTER_NOT_INSTALLED`, message `Adapter package @kb-labs/not-installed is not installed on this platform.`, hint `kb marketplace install @kb-labs/not-installed --scope platform`. Human output omits stack traces and raw JSON. |
