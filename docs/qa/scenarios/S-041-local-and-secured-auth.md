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
- [ ] A linked plugin command and a declared, provisioned webhook with a test payload and signature.

## Steps

| # | Action | Expected |
|---|---|---|
| S-041.1 | In local mode, request `GET /auth/me` without an Authorization header | HTTP 200 JSON identifies `userId: "local-admin"` (or `handle: "local-admin"`); no saved CLI session is needed. |
| S-041.2 | In secured mode, request `GET /auth/me` without an Authorization header | HTTP 401; the route does not return a user profile. |
| S-041.3 | In secured mode, log in as the configured administrator and request `GET /auth/users` with the returned access token | HTTP 200 JSON contains the users collection; the token is accepted for the protected admin operation. |
| S-041.4 | In secured mode, log in through `POST /auth/login/cli` and save its access and refresh tokens; restart the host and perform an update and rollback, calling `GET /auth/me` with the saved access token after each lifecycle operation; then send `POST /auth/logout/cli` with `{"refreshToken":"<saved-refresh-token>"}`, call `GET /auth/me` again with the old access token, and log in again | The original session remains authenticated after restart, update, and rollback while its access token is unexpired and not revoked (`GET /auth/me` returns HTTP 200). Logout revokes that session, so the same access token then receives HTTP 401. A fresh login succeeds and `GET /auth/me` returns HTTP 200 for the same account. |
| S-041.5 | In local mode, run `kb auth status --json` | JSON reports `ok: true` and `session.authenticated: false`; local access does not require a human session. |
| S-041.6 | Run `kb auth login --gateway-url <url> --email <member> --password <password>`, then `kb auth status --json` and `kb auth tokens --json` | Status reports `session.authenticated: true` and the member email; token listing returns JSON with `ok: true`. |
| S-041.7 | In secured mode, with no saved session, run `kb workflow defs list --json`, `kb marketplace plugins list --json`, and a command from a freshly linked plugin with no auth declaration | Every command exits non-zero before its handler or upstream runs and returns the same ADR-0049 envelope: `code: "KB_AUTH_REQUIRED"`, `message: "Authentication is required."`, and `hint: "Run \\"kb auth login\\"."`. Plugin metadata cannot opt out of the platform gate. |
| S-041.8 | With no saved session, run `kb auth login --help`, `kb auth status --json`, `kb version`, and `kb health` | Help and version output normally; auth status reports the unauthenticated session; health returns its diagnostic result. The platform allowlist is owned by the CLI, not plugin metadata. |
| S-041.9 | In secured mode, log in as a member and run `kb auth reset-link --user-id <user-id> --json` | The request reaches the permission check only after authentication and returns `KB_AUTH_FORBIDDEN` with the checked permission `users:write`; it does not return `KB_AUTH_REQUIRED`. |
| S-041.10 | In local mode, run `kb workflow defs list --json`, `kb marketplace plugins list --json`, and a linked plugin command without a saved session | The platform gate is a no-op; commands proceed to their product checks. |
| S-041.11 | In secured mode, send an anonymous request to `GET /api/v1/studio/registry`, `GET /api/v1/projects/$PROJECT_ID/workflow/health`, and `POST /api/v1/projects/$PROJECT_ID/mcp` | Each request stops at the gateway and returns HTTP 401 with the `KB_AUTH_REQUIRED` catalog envelope; the service, project runtime, or MCP tool handler is not reached. |
| S-041.12 | After logging in, run the workflow, marketplace, and linked-plugin commands from S-041.7 | Authentication passes and each command proceeds to its product checks; each returns its normal response or its own product-precondition error. |
| S-041.13 | In secured mode, send a declared webhook request with its valid manifest-configured signature, then send it with a missing or invalid signature; also send an unsigned request to an undeclared `/webhooks/...` path | The declared valid request reaches its webhook handler without a user session. Missing/invalid signatures return HTTP 401 with the `KB_AUTH_REQUIRED` catalog envelope before the handler runs. An undeclared path cannot bypass the gateway gate by resembling a webhook route. |
