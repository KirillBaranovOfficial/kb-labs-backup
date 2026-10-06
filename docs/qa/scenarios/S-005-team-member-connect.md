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
| S-005.1 | Run `kb-create apply --index <sealed-index.json> --input <request.json> --request-platform-root <platform-root> --json` | JSON contains `ok: true` and the requested isolated platform root. |
| S-005.2 | Run `kb auth login --gateway-url <url> --email <member>` and enter the password at the TTY prompt, or use `--password-stdin` for automation | Login succeeds and stores a session for the secured gateway. |
| S-005.3 | Run `kb auth status` | Shows the signed-in member and gateway. |
| S-005.4 | Send `GET <gateway-url>/auth/permissions` with the member session | HTTP 200 JSON `permissions` contains `projects:read` and excludes `users:read`. |
| S-005.5 | Send `GET <gateway-url>/auth/users` with the member session | HTTP 403 JSON has `error: "Forbidden"` and `message: "Permission denied: users:read"`. |
