---
id: S-224
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
| S-224.1 | Run the installed-project registration client against a Gateway that refuses login, returns a malformed login failure, refuses registration, returns a malformed registration failure, or closes the transport; include the bootstrap password and access token in remote messages, hints, response bodies, and headers; inspect the returned failure, human/JSON output, HOME, and install root | Each failure is local and typed: the registration diagnostic carries the catalogued remote cause code (or `KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE` for malformed/unknown results), HTTP status when available, and request stage (`login` or `register`). It contains no remote message, hint, body, headers, URL query, password, or token. The platform remains installed and running and the hint gives the complete mode-specific login/add/show sequence. Passwords and tokens do not appear in human output, the JSON envelope, logs, receipt, or new/changed files under HOME/install root; the pre-existing bootstrap secret store is unchanged. A transport failure has no HTTP status and identifies its request stage. |

## Pass criteria

Project registration failure is recoverable through the existing authenticated
Gateway API contract. The installer never creates an unauthenticated write path
or saves a human CLI session. The platform remains available while the operator
completes the explicit login and registration sequence.
