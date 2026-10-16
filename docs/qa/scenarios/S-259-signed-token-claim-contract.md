---
id: S-259
title: Authentication — signed access token outside the current claim contract
persona: platform-developer
area: security
priority: P1
status: active
release: false
modes: [secured]
surfaces: [api]
---

## Goal

Give machine clients a truthful recovery path when the Gateway can verify an access token's signature but the token no longer satisfies the current claim contract.

## Prerequisites

- [ ] A secured Gateway test fixture with its signing secret.
- [ ] A machine access token signed with the fixture secret that omits a currently required claim.

## Steps

| # | Action | Expected |
|---|---|---|
| S-259.1 | Send the correctly signed machine access token that no longer satisfies the current claim contract to a secured Gateway route and the registered project MCP endpoint | Both requests return HTTP 401 with the catalogued `KB_AUTH_TOKEN_REISSUE_REQUIRED` envelope and a correlation ID. The catalogue hint gives the machine token exchange or refresh recovery path; it does not tell a machine caller to use interactive user login. |

## Pass criteria

The verifier distinguishes a valid signature with claims outside the current schema from malformed, forged, or expired tokens. HTTP-boundary regressions cover both Gateway routes and project MCP. This scenario is non-release because production token issuance cannot create the deliberately stale signed claim shape; it is verified with a signed test fixture and is not an installed journey claim.
