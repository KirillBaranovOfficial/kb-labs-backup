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
| S-041.1 | In local mode, request `GET /auth/me` without an Authorization header | HTTP 200 JSON identifies `userId: "local-admin"` (or `handle: "local-admin"`); no saved CLI session is needed. |
| S-041.2 | In secured mode, request `GET /auth/me` without an Authorization header | HTTP 401; the route does not return a user profile. |
| S-041.3 | In secured mode, log in as the configured administrator and request `GET /auth/users` with the returned access token | HTTP 200 JSON contains the users collection; the token is accepted for the protected admin operation. |
| S-041.4 | In secured mode, reuse an access token after host restart | `GET /auth/me` returns HTTP 401; a fresh login with the same configured account returns HTTP 200 and that account's email. |
| S-041.5 | In local mode, run `kb auth status --json` | JSON reports `ok: true` and `session.authenticated: false`; local access does not require a human session. |
| S-041.6 | Run `kb auth login --gateway-url <url> --email <member> --password <password>`, then `kb auth status --json` and `kb auth tokens --json` | Status reports `session.authenticated: true` and the member email; token listing returns JSON with `ok: true`. |
