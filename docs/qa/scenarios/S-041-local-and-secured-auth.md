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

- [ ] Local host with no saved human session, secured host, and test member account on secured host.

## Steps

| # | Action | Expected |
|---|---|---|
| S-041.1 | In local mode, run `kb auth status --json` | JSON reports `ok: true` and `session.authenticated: false`; local access does not require a human session. |
| S-041.2 | In local mode, send `GET <gateway-url>/auth/me` through the local gateway | HTTP 200 JSON contains the local machine identity's `userId` and `tenantId`. |
| S-041.3 | In secured mode, send `GET <gateway-url>/auth/me` without a session or bearer token | HTTP 401 JSON is `{"error":"Unauthorized","message":"Not authenticated"}`. |
| S-041.4 | Run `kb auth login --gateway-url <url> --email <member> --password <password>`, then `kb auth status --json` and `kb auth tokens --json` | Status reports `session.authenticated: true` and the member email; token listing returns JSON with `ok: true`. |
