---
id: S-179
title: S-179 — Fresh developer checkout loads workspace plugins
persona: developer
area: developer-workspace
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

A developer can use the workspace CLI in a fresh monorepo checkout after installing dependencies and building the workspace.

## Prerequisites

- [ ] A fresh clone of the KB Labs monorepo with Node and pnpm available.

## Steps

| # | Action | Expected |
|---|---|---|
| S-179.1 | Run `pnpm install --frozen-lockfile`, then `pnpm build` from the checkout root | Dependencies install and the workspace builds successfully without editing the committed marketplace lock. |
| S-179.2 | Run `node ./cli/bin/dist/bin.js qa cases validate --json` from the checkout root | The workspace CLI loads its plugin manifests and validates the QA cases successfully. |
| S-179.3 | Run `node ./cli/bin/dist/bin.js --help` | Help lists workspace plugin command groups, including `qa`, `marketplace`, `workflow`, `commit`, `release`, `review`, `mind`, and `agent`. |
| S-179.4 | Run `node ./cli/bin/dist/bin.js marketplace sync --scope platform`, then inspect `.kb/marketplace.lock` | Workspace plugins are recorded under `plugins`; workspace adapters are recorded as platform entries under `adapters`. The lock retains every old workspace entity ID that still exists. |
