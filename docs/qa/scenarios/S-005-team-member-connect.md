---
id: S-005
title: S-005 — Team member connects to a secured platform
persona: team-member
area: security
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A team member installs the client, signs into a secured gateway, and runs an authorized project operation using the team platform.

## Prerequisites

- [ ] Secured host, registered project, member account, and assigned project access.

## Steps

| # | Action | Expected |
|---|---|---|
| S-005.1 | Install or apply the V2 client with `kb-create apply --json` using the provided sealed index and request | JSON reports success and the selected platform/project roots. |
| S-005.2 | Run `kb auth login --gateway-url <url> --email <member> --password <password>` | Login succeeds and stores a session for the secured gateway. |
| S-005.3 | Run `kb auth status` | Shows the signed-in member and gateway. |
| S-005.4 | Run an authorized operation against the registered project | Operation succeeds under the member’s account. |
| S-005.5 | Attempt the same operation without a session or with insufficient access | Gateway returns an actionable authentication or authorization response. |
