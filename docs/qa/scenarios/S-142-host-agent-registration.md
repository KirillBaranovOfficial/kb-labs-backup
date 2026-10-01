---
id: S-142
title: Workspace Agent — Register a machine with a gateway
persona: team-admin
area: agents
priority: P2
status: active
release: false
modes: [secured]
surfaces: [cli, api]
---

## Goal

An administrator registers a workspace machine with a gateway and verifies that the agent is connected under the chosen host name.

## Prerequisites

- [ ] Workspace Agent plugin installed on the machine; gateway is reachable and accepts workspace registration.
- [ ] A disposable HOME is used for this scenario so registration credentials do not affect another agent installation.

## Steps

| # | Action | Expected |
|---|---|---|
| S-142.1 | Run `kb workspace:register --gateway https://gateway.example.test --name test-machine --workspace "$PROJECT_ROOT" --json` | CLI confirms registration and writes the gateway credentials to `$HOME/.kb/agent.json`; secret fields are not printed. |
| S-142.2 | Start the Workspace Agent daemon using the documented host-agent service command, then run `kb workspace:status --json` | JSON reports `connected: true`, the registered host ID, gateway URL, and advertised capabilities. |
| S-142.3 | Run `kb workspace:list --gateway https://gateway.example.test --json` | JSON includes `test-machine` with its host ID, connected status, capabilities, and last-seen time. |
| S-142.4 | Run `kb workspace:register --gateway not-a-url --name test-machine --workspace "$PROJECT_ROOT" --json` | CLI reports the invalid gateway URL and the required URL format; it exits non-zero and does not replace the existing credential file. |
