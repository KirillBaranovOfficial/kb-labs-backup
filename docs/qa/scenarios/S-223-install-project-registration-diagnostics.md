---
id: S-223
title: Install — diagnose project registration failure after apply
persona: team-admin
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

When first-install project registration fails after the platform is applied, the
operator can identify the failed boundary and finish registration manually
without losing the running installation or exposing credentials.

## Prerequisites

- [ ] Isolated HOME, KB_HOME, platform root, existing selected project, and a
  fake Gateway that can return transport failures, catalogued login/API
  failures, or malformed project-registration results.

## Steps

| # | Action | Expected |
|---|---|---|
| S-223.1 | Run `kb-create install --json` for a fresh selected-project install against a Gateway that is unreachable, rejects the bootstrap administrator login, refuses the authenticated project request, or returns a malformed/unknown project result; inspect HOME, the install log, receipt, and diagnostic | The install exits non-zero with one `KB_INSTALL_PROJECT_REGISTRATION_FAILED` diagnostic stating the platform is installed and the project is not registered. `details.causeCode` preserves the catalogued boundary failure, or is `KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE` for a malformed response. The hint gives the complete mode-specific manual sequence: secured mode logs in with the Gateway URL and admin email before `kb project add` and `kb project show`; local mode gives add and show. The host remains installed and running. Passwords/tokens are absent from argv, child environment, progress, output, logs, receipt, diagnostic, and HOME session/credential files. |

## Pass criteria

Project registration failure is recoverable through the existing authenticated
Gateway API contract. The installer never creates an unauthenticated write path
or saves a human CLI session. The platform remains available while the operator
completes the explicit login and registration sequence.
