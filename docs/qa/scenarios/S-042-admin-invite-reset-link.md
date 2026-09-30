---
id: S-042
title: Team Admin — invite a member and issue a reset link
persona: team-admin
area: security
priority: P1
status: active
release: true
modes: [secured]
surfaces: [studio, cli, api]
---

## Goal

An administrator provisions a member with a one-time invite and recovers an existing member with a one-time reset link.

## Prerequisites

- [ ] Secured gateway, admin account, test tenant, and Studio Users page.

## Steps

| # | Action | Expected |
|---|---|---|
| S-042.1 | Create an invite for a new member in Studio Users | Admin flow returns an activation link with the selected member role. |
| S-042.2 | Activate the invite, then attempt to reuse its link | First activation succeeds; reuse is rejected. |
| S-042.3 | Run `kb auth reset-link --user-id <user-id>` as admin | Outputs a one-time reset URL and expiry. |
| S-042.4 | Redeem the reset URL with a new password and log in | New password works; old sessions are revoked. |
