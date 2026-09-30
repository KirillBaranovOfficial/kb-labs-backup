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
| S-051.2 | Run `kb marketplace install @kb-labs/not-published` | Exits non-zero; human output names `@kb-labs/not-published` and includes an actionable hint. **Product question:** confirm the stable error `code` and exact next command for an unavailable registry package. |
| S-051.3 | Run `kb adapter bind storage @kb-labs/not-installed` | Exits non-zero; output is `Adapter package @kb-labs/not-installed is not installed on this platform.` followed by `Run: kb marketplace install @kb-labs/not-installed --scope platform`; no raw stack trace or JSON object is printed. **Product question:** this path currently exposes no error code. |
| S-051.4 | Repeat each failing command with `--json` | Project show returns `{ok:false,error:{code,message,hint}}`; adapter bind returns `{ok:false,error:<message>,hint:<command>}`. **Product question:** specify the stable JSON shape for unavailable marketplace packages and whether adapter bind must adopt the shared error envelope. Human output omits stack traces and raw JSON. |
