---
id: S-041
title: Authentication — local access and secured login
persona: team-member
area: security
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli, studio, api]
---

## Goal

Local projects work without login; secured gateways require a valid session.

## Prerequisites

- [ ] Local and secured hosts; test member account on secured host.

## Steps

| # | Action | Expected |
|---|---|---|
| S-041.1 | In local mode, run `kb auth status` | Identifies local access without requiring a saved session. |
| S-041.2 | Call a protected project operation locally | Operation succeeds without a saved login. |
| S-041.3 | Call a secured project route before login | Returns an authentication error and non-success status. |
| S-041.4 | Run `kb auth login --gateway-url <url> --email <member> --password <password>` then `kb auth status` | Signed-in account is shown and authorized project operation succeeds. |
