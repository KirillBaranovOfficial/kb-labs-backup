---
id: S-104
title: Security — admins manage tenant access and members cannot manage users
persona: team-admin
area: security
priority: P0
status: active
release: true
modes: [secured]
surfaces: [api, cli]
---

## Goal

Tenant admins can grant member or admin access, while a member can use projects and workflows but cannot administer tenant users.

## Prerequisites

- [ ] Secured gateway with an admin and a member in the same tenant.

## Steps

| # | Action | Expected |
|---|---|---|
| S-104.1 | As admin request `GET <gateway-url>/auth/permissions` | HTTP 200 JSON `permissions` contains `users:read`, `users:write`, `invites:read`, and `invites:write`. |
| S-104.2 | As admin send `POST <gateway-url>/auth/invites` with JSON `{"email":"new-admin@example.test","role":"admin"}` | HTTP 201 returns an `inviteId` and `activationUrl` for the new tenant-admin invite. |
| S-104.3 | As member request `GET <gateway-url>/auth/permissions` | HTTP 200 JSON `permissions` contains `projects:read`, `projects:write`, `plugins:read`, `plugins:write`, `workflows:read`, and `workflows:write`; it excludes `users:read`, `users:write`, `invites:read`, and `invites:write`. |
| S-104.4 | As member request `GET <gateway-url>/auth/users` | HTTP 403 JSON contains `error: "Forbidden"` and `message: "Permission denied: users:read"`. |
| S-104.5 | As member send `POST <gateway-url>/auth/invites` with valid email and role JSON | HTTP 403 JSON contains `error: "Forbidden"` and `message: "Permission denied: invites:write"`. |
