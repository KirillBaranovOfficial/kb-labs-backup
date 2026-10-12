---
id: S-222
title: Secured install — diagnose an internal bootstrap contract failure
persona: team-admin
area: security
priority: P1
status: active
release: false
modes: [secured]
surfaces: [cli]
---

## Goal

When the installer cannot read the Gateway's first-administrator health result,
the operator can identify the internal contract problem and the next diagnostic
command without being told to update one side of a sealed release.

## Prerequisites

- [ ] A secured V2 install with test Gateways that return invalid health data,
  standard failure envelopes, and transport failures from `/health/auth`.
- [ ] An isolated platform root.

## Steps

| # | Action | Expected |
|---|---|---|
| S-222.1 | Run `kb-create install --json` against test Gateways that omit a schema-required first-administrator field, return an unknown bootstrap enum, or return a valid health result with bootstrap status `failed` | A response missing any field marked required by the Gateway auth-health schema, or containing an unknown bootstrap enum, exits non-zero with `KB_INSTALL_APPLY_FAILED`, says this is an internal installer/Gateway contract mismatch, and gives `kb-create doctor --platform-root <platform-root>` as the next command. A response with all required fields and the valid `failed` status says Gateway did not prove first-administrator bootstrap and secured authentication are ready with a usable active administrator and directs the operator to inspect Gateway authentication readiness with that doctor command. Neither hint blames Gateway versions. |
| S-222.2 | Run `kb-create install --json` against a Gateway that returns a non-2xx `{ok:false,error}` result containing a catalogued diagnostic and a valid correlation ID | The installer surfaces the Gateway diagnostic's catalogued code, message, and hint, and carries its correlation ID as `details.remoteCorrelationId`; human output labels it as the remote correlation ID. The installer's own top-level `correlationId` remains distinct. It does not include credentials, tokens, response headers, or other private error fields in output or logs. |
| S-222.3 | Run `kb-create install --json` against a Gateway that returns a non-2xx body that is not a valid UX-result envelope | The installer exits non-zero with `KB_INSTALL_APPLY_FAILED`, identifies an internal installer/Gateway contract mismatch, and gives `kb-create doctor --platform-root <platform-root>` as the next command. Output and logs do not include credentials, tokens, response headers, or the response body. |
| S-222.4 | Run `kb-create install --json` against an unreachable Gateway | The existing transport-failure diagnostic remains in effect and does not include request or response credentials. |
| S-222.5 | Interrupt Gateway administrator bootstrap after each persisted user, password-credential, and tenant-admin-membership write; restart with the same bootstrap configuration | Each restart converges to exactly one usable tenant administrator. Login succeeds with the configured bootstrap password, and `/health/auth` reports `ok: true` and `activeAdmins: 1`. An already usable administrator's password credential hash remains unchanged. |
| S-222.6 | Run the installer against each valid `/health/auth` response that does not prove readiness `ok: true` and `activeAdmins >= 1` | The installer returns a typed failure naming incomplete first-administrator bootstrap, gives `kb-create doctor --platform-root <platform-root>` as the next command, and leaves the bootstrap-completion marker absent. |
| S-222.7 | Inspect install status after first-administrator bootstrap did not complete | The installation is not reported as installed-and-ready. Its typed diagnosis names incomplete administrator bootstrap and gives the exact `kb-create doctor --platform-root <platform-root>` command to inspect readiness and resolve the issue before rerunning install. |
