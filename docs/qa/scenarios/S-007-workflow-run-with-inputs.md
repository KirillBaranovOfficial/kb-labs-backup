---
id: S-007
title: S-007 — Run a workflow with inputs
persona: solo-developer
area: workflow
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user starts a workflow with a JSON input and verifies the job received the supplied value.

## Prerequisites

- [ ] Valid project workflow with a declared `message` input and a step that outputs it.
- [ ] In secured mode, the CLI can log in as a project user with `workflows:write` permission.

## Steps

| # | Action | Expected |
|---|---|---|
| S-007.1 | Run `kb workflow run --workflow-id=<project>/hello --input='{"message":"hello"}'` | CLI returns the created run ID. |
| S-007.2 | Run `kb workflow runs view <runId>` | Run reaches a terminal status and reports the submitted input. |
| S-007.3 | Run `kb workflow runs logs <runId>` | Step output contains `hello`, confirming the job received the input. |
| S-007.4 | In secured mode, with no CLI session, run `kb workflow defs list --json` | CLI exits non-zero with JSON error code `KB_AUTH_REQUIRED` and hint `Run "kb auth login --gateway-url <gateway-url> --email <email>".`; the configured gateway URL is rendered when available and login prompts for the password. |
