---
id: S-025
title: Platform — diagnose and safely repair a V2 setup
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

A broken composition is diagnosable from its receipt and installed manifests,
with one structured answer for both a person and an agent.

## Steps

| # | Action | Expected |
|---|---|---|
| S-025.1 | Remove a required non-secret config value in an isolated fixture and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Exits 1; JSON has `ok: false`; the matching entry in `findings` names the requirement `component`, JSON-pointer `path`, `code`, and remediation `hint`. |
| S-025.2 | Omit a required secret and run the same doctor command | The matching finding identifies the requirement path and says it is missing; output, logs, and telemetry contain no secret value. |
| S-025.3 | Replace `.kb/devservices.yaml` with a graph that differs from the active receipt, then run doctor | JSON has `ok: false`; `installation.checks` contains a failed `host-spec` or `devservices` check identifying the graph mismatch before a service command is attempted. |
| S-025.4 | Run `kb-create doctor --fix --platform-root "$PLATFORM_ROOT" --json` when the fixture has a manifest-declared safe default | Exits 0; JSON has `ok: true`, `operation: "doctor-fix"`, non-empty `snapshot.id`, and `hostRestarted`; the generated value matches the manifest default. |
| S-025.5 | Run doctor-fix when a required user or secret value is still missing | The result preserves that requirement in `findings` and `repair.requiredInput`; no value or provider is invented. **Product question:** whether doctor-fix should exit non-zero when safe fixes succeed but required input remains. |
| S-025.6 | Cause an apply or recovery operation failure and inspect its JSON result | Exits non-zero; JSON has `ok: false`, `error.code`, `error.stage`, `error.message`, `error.hint`, `logPath`, and `diagnosticPath`; the dossier omits secret values. |

## Pass criteria

Diagnosis compares effective configuration with the manifests of the artifacts
recorded in the active receipt. It does not rely on old marketplace lock files,
parsing terminal text, or raw stack traces.
