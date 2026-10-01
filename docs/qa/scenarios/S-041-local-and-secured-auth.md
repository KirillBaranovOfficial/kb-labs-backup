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
| S-041.4 | In secured mode, log in through `POST /auth/login/cli` and save its access and refresh tokens; restart the host and perform an update and rollback, calling `GET /auth/me` with the saved access token after each lifecycle operation; then send `POST /auth/logout/cli` with `{"refreshToken":"<saved-refresh-token>"}`, call `GET /auth/me` again with the old access token, and log in again | The original session remains authenticated after restart, update, and rollback while its access token is unexpired and not revoked (`GET /auth/me` returns HTTP 200). Logout revokes that session, so the same access token then receives HTTP 401. A fresh login succeeds and `GET /auth/me` returns HTTP 200 for the same account. |
| S-041.5 | In local mode, run `kb auth status --json` | JSON reports `ok: true` and `session.authenticated: false`; local access does not require a human session. |
| S-041.6 | Run `kb auth login --gateway-url <url> --email <member> --password <password>`, then `kb auth status --json` and `kb auth tokens --json` | Status reports `session.authenticated: true` and the member email; token listing returns JSON with `ok: true`. |
