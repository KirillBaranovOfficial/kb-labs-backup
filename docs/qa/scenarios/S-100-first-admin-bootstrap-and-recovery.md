---
id: S-100
title: Secured install — bootstrap and recover the first administrator
persona: team-admin
area: security
priority: P0
status: active
release: true
modes: [secured]
surfaces: [cli]
---

## Goal

A secured installation has a usable first administrator, and an administrator can recover access after losing the password.

## Prerequisites

- [ ] Fresh secured V2 installation with bootstrap email and tenant configured.
- [ ] Platform root is isolated for this run.

## Steps

| # | Action | Expected |
|---|---|---|
| S-100.1 | Sign in to the gateway using the configured bootstrap email and password, then run `kb auth status --json` | JSON reports `session.authenticated: true` and the expected gateway URL and admin email. |
| S-100.2 | Stop the host, then run `kb-create doctor --platform-root <platform-root> --recover --email admin@example.test --tenant <tenant-id> --password-stdin --yes` and pipe a new password to stdin, with no registered project for the platform root | Recovery exits with status 0 and does not echo the password; it uses the explicit platform root without resolving a project. |
| S-100.3 | Start the host and sign in with the recovered password | Login succeeds and `GET /auth/me` returns the bootstrap admin's email and tenant ID. |
| S-100.4 | Run the same recovery command while the host is running | Command exits unsuccessfully with a typed `KB_*` error and actionable hint that recovery requires a stopped host and instructs `kb-create stop`, recovery, then `kb-create start`. |
