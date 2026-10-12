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
| S-222.7 | Inspect install status after first-administrator bootstrap did not complete | Status reports `bootstrapCompleted: false`; the installation is not reported as installed-and-ready. Its typed diagnosis names incomplete administrator bootstrap and gives the exact `kb-create doctor --platform-root <platform-root>` command to inspect readiness and resolve the issue before rerunning install. |
| S-222.8 | Interleave two Gateway bootstrap runs against the same persisted stores, first with the same config and then with different passwords; restart pending and already-active accounts with the original configured password | With the same config, both runs report success, one user and one tenant-admin membership exist, the configured password logs in, and readiness is ready; neither run reports bootstrap failure. With different passwords, exactly one configured password logs in, activation never changes the credential, and the losing participant reports a typed configuration conflict. A sequential restart may replace the credential while the record remains pending. Once active, a later password change does not make bootstrap fail or compare against the original bootstrap password. |
| S-222.9 | Install a secured SSO-only Gateway without a bootstrap email, then inspect readiness and `kb-create doctor`; evaluate the same tenant admins with configured login methods | The installer does not require first-admin bootstrap completion or a password when no bootstrap email was configured. An active tenant admin with a password credential is usable when email-password login is configured; an active tenant admin is usable with a configured OIDC/SSO provider without a password credential only while its provider reports operational; the same passwordless user is not usable when no provider can authenticate it. An unreachable or invalid OIDC discovery document makes readiness not ready with a typed issue naming the provider id and failure class, and doctor shows the same finding. Readiness counts each usable admin once. |
| S-222.10 | Complete installer bootstrap after Gateway health responses that do or do not prove a usable administrator | The installer records completion only after Gateway reports readiness `ok: true` and `activeAdmins >= 1`; every other valid response returns a typed failure and leaves completion absent. |
| S-222.11 | Change the bootstrap administrator password by password change, reset-by-link, and `kb-create doctor --recover`; restart the Gateway and inspect `/health/auth` and `kb-create doctor`; inspect the host environment before and after bootstrap completion; retry after a post-bootstrap apply failure or process interruption before output | Each restart returns `exists-active` without a bootstrap failure, `/health/auth` is ready, and doctor has no auth finding. Before the completion marker, the Gateway process receives the bootstrap password; after the marker it does not receive `GATEWAY_BOOTSTRAP_ADMIN_PASSWORD`. If a later project registration rejects the stored first-admin credential, the installer gives a typed refusal explaining that Gateway rejected it and the exact login, `kb project add`, and `kb project show` commands. A completed install does not disclose the stored bootstrap password again. If a post-bootstrap apply step fails before the disclosure marker is written, the marker remains absent and the retry returns a typed recovery diagnosis using the same doctor command. If the marker exists from an earlier run, a successful retry returns human and JSON results with state `previously-disclosed`, names the administrator email, says the credential was issued by an earlier run and is not shown again, and includes exact `kb-create stop --platform-root <root>`, `kb-create doctor --platform-root <root> --recover --email <email> --generate --yes`, and `kb-create start --platform-root <root>` commands in order. The doctor command is accepted by the real doctor flag set, and the notice contains no password. |
