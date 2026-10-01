---
id: S-202
title: Projects — Unregister one project and preserve tenant access boundaries
persona: team-member
area: security
priority: P1
status: planned
release: false
modes: [secured]
surfaces: [cli, studio, api]
---

## Goal

Remove one repository from the platform registry without affecting other
projects, and understand which secured users can access the remaining projects.

## Prerequisites

- [ ] Secured platform with an admin and a member account.
- [ ] Projects A and B are registered and have separate files, runtime state, and history.

## Steps

| # | Action | Expected |
|---|---|---|
| S-202.1 | In Studio as an administrator, open Projects, choose Remove for Project A, and confirm Unregister; then inspect the registry and Project A's repository and state directory | A is absent from the registry; B remains registered and usable. Unregistering leaves A's repository, `.kb/` files, and runtime state directory untouched. |
| S-202.2 | In Studio, select Project B and inspect its plugins, runs, analytics, and logs | B's views continue to show B's data and no longer offer A as a project selection. |
| S-202.3 | As a member, send JSON-RPC `tools/list` to `POST /api/v1/projects/<B_ID>/mcp`, then send the same request to `POST /api/v1/projects/<A_ID>/mcp` after unregistering A | The member receives B's tool catalog because secured members have tenant-wide project access. A returns the typed unknown-project error. No per-project member grants are currently defined. |
## Product boundary

The current secured product decision grants members access to projects,
plugins, and workflows tenant-wide; it does not define per-project grants.
This case records that current truth. If project-level permissions are
required, the owner must decide that model before implementation.

## Dependencies

- S-124 covers project registration/removal and explicitly states that files
  and runtime state remain after unregistering.
- S-104 defines secured admin and member authorization.
