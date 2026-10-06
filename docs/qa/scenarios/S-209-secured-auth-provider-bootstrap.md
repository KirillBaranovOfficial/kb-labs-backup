---
id: S-209
title: Secured install — configure sign-in providers after the first administrator signs in
persona: team-admin
area: security
priority: P1
status: planned
release: false
modes: [secured]
surfaces: [cli]
---

## Goal

An operator can turn a fresh secured installation into one with additional sign-in providers, such as OIDC, without any unauthenticated configuration path. Email and password sign-in works out of the box. The bootstrap administrator signs in with it first, then configures the other providers as an authenticated, authorized user.

## Prerequisites

- [ ] Fresh secured V2 installation with bootstrap email and tenant configured (S-100).
- [ ] No CLI session in the operator's HOME.
- [ ] An OIDC identity provider reachable from the gateway. In the journey, this is the IdP stub.

## Steps

| # | Action | Expected |
|---|---|---|
| S-209.1 | On the fresh install, request `GET <gateway-url>/auth/providers` | HTTP 200 JSON lists `email-password` with kind `password`. No other provider is configured, and no configuration step was needed. |
| S-209.2 | Without a CLI session, run `kb config set --scope platform gateway.auth.providers '<providers-json>' --json` | The command exits non-zero with `KB_AUTH_REQUIRED` and the `kb auth login` hint (S-041.7). The platform config file is unchanged. There is no unauthenticated or "local operator" bypass. |
| S-209.3 | Sign in with the bootstrap administrator's email and password (S-100.1), then repeat the command from S-209.2 with `email-password` plus an OIDC provider | The command exits 0. The platform config file records both providers, and the result names that file. Secrets are stored as environment references (`clientSecretEnv`), never as values. |
| S-209.4 | Sign in as a non-admin tenant member and run the same command | The command exits non-zero with the catalogued authorization error (403 class, S-104). The platform config file is unchanged. |
| S-209.5 | Apply the change as the command result instructs (for example, `kb-create restart --platform-root <platform-root>` when the gateway does not reload auth config), then request `GET <gateway-url>/auth/providers` | Both providers are listed. The OIDC sign-in flow then works as described in S-109. |

## Notes

Owner decision, 2026-10-06: a secured platform has no unauthenticated configuration path. Email and password sign-in is always available. The bootstrap administrator signs in first and configures any additional sign-in providers as an authorized user. The rejected alternatives were a local-operator bypass for platform config writes on the platform machine, and provider flags on `kb-create`.

The case is `planned`, not part of the release gate. SSO / OIDC and role-based access (RBAC) are a separate later stream, and for now the identity provider is only a stub. Until this case is implemented, the release journey must not configure OIDC providers before sign-in. The secured journey works with the out-of-the-box `email-password` provider. Any platform config it needs (such as `gateway.auth.cookieSecure` for plain-HTTP test runs) is set after the bootstrap admin signs in.
