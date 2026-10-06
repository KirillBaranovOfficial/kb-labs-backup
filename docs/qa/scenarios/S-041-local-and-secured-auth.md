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
| S-041.4 | In secured mode, log in through `POST /auth/login/cli`, update the platform, restart the host, run a protected CLI command with the saved session, roll back, and run another protected CLI command with the same session | When the token contract is unchanged, the saved user session remains valid through update, rollback, and host restart, and both protected CLI commands succeed. If the host rejects a saved user token, the protected command exits unsuccessfully with `KB_AUTH_REQUIRED` and hint `Run "kb auth login --gateway-url <gateway-url> --email <email> --password <password>".` with the configured gateway URL rendered. The journey covers the unchanged-contract path; rejection is covered by the auth-gate unit test. Each host start authenticates its machine connection again from stored machine registration credentials, independently of the user session. |
| S-041.5 | In local mode, run `kb auth status --json` | JSON is an ADR-0049 success envelope whose `data.session.authenticated` is `false`; local access does not require a human session. |
| S-041.6 | Run `kb auth login --gateway-url <url> --email <member> --password <password>`, then `kb auth status --json` and `kb auth tokens --json` | Status is an ADR-0049 success envelope whose `data.session.authenticated` is `true` and whose session contains the member email; token listing returns an ADR-0049 success envelope. |
| S-041.7 | In secured mode, use a fresh user HOME with no CLI session, then run `kb config show --json`, `kb workflow defs list --json`, `kb marketplace plugins list --json`, `kb marketplace plugins list --scope platform --json`, and a command from a freshly linked plugin with no auth declaration | Every command exits non-zero before config-dependent handler setup, gateway transport resolution, its handler, or an upstream runs, and returns exactly one ADR-0049 JSON envelope: `code: "KB_AUTH_REQUIRED"`, `message: "Authentication is required."`, and hint `Run "kb auth login --gateway-url <gateway-url> --email <email> --password <password>".` with the configured gateway URL rendered. Missing gateway configuration cannot replace this response. The platform-owned command and plugin dispatch use the same gate. Plugin metadata cannot opt out of it. |
| S-041.8 | With a fresh user HOME and no CLI session, run `kb auth login --help`, `kb auth status --json`, `kb version`, and `kb health` | Help and version output normally; auth status returns an ADR-0049 success envelope with `data.session.authenticated: false`; health returns its diagnostic result. The platform allowlist is owned by the CLI, not plugin metadata. |
| S-041.9 | In secured mode, log in as a member and run `kb auth reset-link --user-id <user-id> --json` | The request reaches the permission check only after authentication and returns `KB_AUTH_FORBIDDEN` with the checked permission `users:write`; it does not return `KB_AUTH_REQUIRED`. |
| S-041.10 | In local mode, run `kb workflow defs list --json`, `kb marketplace plugins list --json`, and a linked plugin command without a saved session | The platform gate is a no-op; commands proceed to their product checks. |
| S-041.11 | In secured mode, send an anonymous request to `GET /api/v1/studio/registry`, `GET /api/v1/projects/$PROJECT_ID/workflow/health`, and `POST /api/v1/projects/$PROJECT_ID/mcp` | Each request stops at the gateway and returns HTTP 401 with the `KB_AUTH_REQUIRED` catalog envelope; the service, project runtime, or MCP tool handler is not reached. |
| S-041.12 | After logging in, run the platform-owned `kb config show --json`, workflow, marketplace, and linked-plugin commands from S-041.7 | Authentication passes and each command proceeds to its product checks; each returns its normal response or its own product-precondition error, never `KB_AUTH_REQUIRED`. |
| S-041.13 | In secured mode, send a declared webhook request with its valid manifest-configured signature, then send it with a missing or invalid signature; also send an unsigned request to an undeclared `/webhooks/...` path | The declared valid request reaches its webhook handler without a user session. Missing/invalid signatures return HTTP 401 with the `KB_AUTH_REQUIRED` catalog envelope before the handler runs. An undeclared path cannot bypass the gateway gate by resembling a webhook route. |
| S-041.14 | In secured mode, send `OPTIONS /api/v1/mcp` with `Origin` and `Access-Control-Request-Method: POST`, then send an OPTIONS request without `Access-Control-Request-Method` to a protected route | The CORS preflight gets only the configured CORS response, without dispatching to a session, tool, service or plugin handler. An ordinary OPTIONS request remains behind the auth gate and returns HTTP 401 with the `KB_AUTH_REQUIRED` envelope. |
| S-041.15 | In secured mode, request `GET /health/auth` first from a loopback peer using a loopback Host and no forwarding headers, then repeat with a remote peer, any forwarding header, or a non-loopback Host | A local unproxied operator request reaches the route's own guard and receives readiness output without a user session. Every other request receives a plain 404 before readiness is computed; it never receives 401/403 or readiness details. |
| S-041.16 | Through the Gateway, request the registry package list, public package metadata, and package stats anonymously, then request private package metadata anonymously and as an authenticated caller with access | Public catalog reads remain available without a user session. Private package access is decided by the registry's `canAccess`: anonymous callers receive 403, and an authenticated caller's forwarded namespace is used for the access decision. |
| S-041.17 | In secured mode, with a fresh user HOME and no CLI session, run `kb config show --json` from a directory that is not registered with `kb project add` | The command exits non-zero with exactly one ADR-0049 JSON envelope containing `KB_AUTH_REQUIRED`, its authentication-required message, and the `kb auth login` hint with the configured gateway URL; project registry resolution does not replace the auth response. |

## Notes

- 2026-10-06 owner decision: a saved CLI session survives platform update, rollback, and host restart while the token contract is unchanged. A token rejected by the host yields `KB_AUTH_REQUIRED` with the login hint. The host machine connection authenticates again on each start. No legacy token compatibility path is retained.
