---
id: S-002
title: Solo — First project workflow
persona: solo-developer
area: workflow
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli, api]
---

## Goal

A developer creates the documented local workflow example in a registered project, runs it through the project-scoped workflow service, and inspects its result.

## Prerequisites

- [ ] KB Labs installed and host running; `kb status` reports a healthy host.
- [ ] The current project is registered. In an unregistered project, run `kb project add` from that project directory first.
- [ ] In secured mode, an authenticated gateway session is available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-002.1 | From the registered project directory, run `kb workflow init` | CLI reports `Created .kb/workflows/hello.yml` and prints `Next: kb workflow lint && kb workflow run --workflow-id=hello`. The file contains the manual `hello` workflow and a `builtin:shell` step whose command is `echo hello-workflow`. |
| S-002.2 | Run `kb workflow lint` | CLI reports `.kb/workflows/hello.yml` as valid and exits 0. |
| S-002.3 | Run `kb workflow run --workflow-id=hello` | Within 30 seconds, CLI returns a non-empty run ID for workflow `hello`. |
| S-002.4 | Run `kb workflow runs view <run-id>`, replacing the placeholder with the returned ID | Within 60 seconds, detail reports status `success`; the `hello` step output contains `hello-workflow`. |
| S-002.5 | Run `kb workflow runs list --workflow=hello --json` | JSON includes the same run ID, workflow ID `hello`, and status `success`. |
| S-002.6 | Run `kb workflow runs view <run-id>` with an unknown ID | CLI reports that the run was not found and gives a next action; it does not print a stack trace. |
