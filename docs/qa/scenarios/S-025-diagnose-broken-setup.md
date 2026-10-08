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
| S-025.1 | Remove a required non-secret config value in an isolated fixture and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Doctor completes with exit 0; JSON has `ok: true`, `data.operation: "doctor"`, `data.status: "unhealthy"`, and structured `data.findings` plus `data.installation`. The matching finding names the requirement `component`, JSON-pointer `path`, `code`, and remediation `hint`. Human output shows the same finding and hint. |
| S-025.2 | Omit a required secret and run the same doctor command | Doctor completes with exit 0 and `data.status: "unhealthy"`; structured `data.findings` identifies the requirement path and says it is missing. Output, logs, and telemetry contain no secret value. Human output shows the same safe finding. |
| S-025.3 | In an isolated fixture, corrupt the active receipt's `plan.host` command and run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` | Doctor completes with exit 0; JSON has `ok: true`, `data.operation: "doctor"`, and `data.status: "unhealthy"`. `data.installation.checks` contains a failed `host-spec` check with code `KB_HOST_SPEC_INVALID` and the exact next command `kb-create apply --request-platform-root "$PLATFORM_ROOT"`. Doctor does not run the host or any service command. |
| S-025.4 | Run `kb-create doctor --fix --platform-root "$PLATFORM_ROOT" --json` when the fixture has a manifest-declared safe default | Exits 0; JSON has `ok: true`, `operation: "doctor-fix"`, non-empty `snapshot.id`, and `hostRestarted`; the generated value matches the manifest default. |
| S-025.5 | Run `kb-create doctor --fix --platform-root "$PLATFORM_ROOT" --json` when a safe projection repair is available and a required user or secret value is still missing | Exits 0 after the safe repair; shared clikit JSON has `ok: true`, and `data.findings` plus `data.repair.requiredInput` preserve the unresolved requirement. No value or provider is invented. |
| S-025.6 | Cause an apply or recovery operation failure and inspect its JSON result | Exits non-zero; JSON has `ok: false`, `error.code`, `error.stage`, `error.message`, `error.hint`, and `error.details.logPath` plus `error.details.diagnosticPath`; the dossier omits secret values. |
| S-025.7 | With platform A running, run `kb-create doctor --fix --platform-root "$PLATFORM_ROOT_B" --json`, then run `kb-create status --platform-root "$PLATFORM_ROOT_B" --json` and `kb-create status --platform-root "$PLATFORM_ROOT_A" --json` | The repair operates on B's receipt and configuration, reports `hostRestarted: false`, and reports B's host as `stopped`. A remains `running` with its original receipt; the operation on B never signals A's host. |
| S-025.8 | While platform A is running, run `kb-create start --platform-root "$PLATFORM_ROOT_B" --json` (or `kb-create apply` for B) | The operation fails before changing B with `KB_HOST_ALREADY_RUNNING`. The message identifies A's platform root and host pid; the hint says to stop A with `kb-create stop --platform-root <A>`. |
| S-025.9 | With live root A owning the shim and live root B installed, run `kb-create doctor --fix --platform-root "$PLATFORM_ROOT_B" --json`; then point the shim at a removed root and run `kb-create doctor --platform-root "$PLATFORM_ROOT_A" --json` and `kb-create doctor --fix --platform-root "$PLATFORM_ROOT_A" --json` | Fixing B exits 0 and leaves the shim with A. Doctor reports an unhealthy `KB_INSTALL_SHIM_DANGLING` finding with the exact fix command for A. Fixing A exits 0 and points the shim back to A. |

## Pass criteria

Diagnosis compares effective configuration with the manifests of the artifacts
recorded in the active receipt. It does not rely on old marketplace lock files,
parsing terminal text, or raw stack traces.
