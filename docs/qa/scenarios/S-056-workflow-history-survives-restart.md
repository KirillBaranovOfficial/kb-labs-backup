---
id: S-056
title: S-056 — Read workflow history after host restart
persona: solo-developer
area: workflow
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A project user can inspect a completed workflow and its logs after the host and project runtime restart.

## Prerequisites

- [ ] A registered project using the platform's default or another persistent storage adapter.
- [ ] `kb-create` is installed for the journey environment.

## Steps

| # | Action | Expected |
|---|---|---|
| S-056.1 | Run a workflow that emits a known log line and wait for it to finish. | The CLI returns a run ID and the run reaches `success`. Its storage adapter record is at `workflows/<runId>.json` and contains `id`, `name`, `status`, `createdAt`, `trigger`, `metadata`, and `jobs`; each job contains `steps` with the output needed by history and logs views. The stored `id` matches the CLI run ID. |
| S-056.2 | Run `kb-create restart --platform-root <journey platform root>`. | The host restarts successfully and the registered project remains available. |
| S-056.3 | Run `kb workflow runs list`, `kb workflow runs view <runId>`, and `kb workflow runs logs <runId>`. | History still lists the same run ID and successful status; run detail shows its terminal status; logs still contain the known line. |
