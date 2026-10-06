---
id: S-214
title: Installer — diagnose invalid release indexes
persona: solo-developer
area: platform-core
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

Identify why a release index cannot be used before installation or update
changes the active platform.

## Prerequisites

- [ ] A missing index path, malformed JSON index, unsupported schema index, digest-mismatched index, and structurally invalid index.
- [ ] A staged package with a secret manifest requirement missing its environment variable or target services.
- [ ] An isolated platform root that is not modified by these commands.

## Steps

| # | Action | Expected |
|---|---|---|
| S-214.1 | Run `kb-create plan --index MISSING_INDEX --request-platform-root ROOT --json` | Returns `KB_INSTALL_RELEASE_INDEX_INVALID`; `details.gate` is `read`, `details.path` names `MISSING_INDEX`, and the cause names the OS read failure. Human output includes a `Why:` line with the gate and path. |
| S-214.2 | Run the same plan command with an index containing malformed JSON | Returns `KB_INSTALL_RELEASE_INDEX_INVALID`; `details.gate` is `parse`, `details.path` names the index, and the cause describes the JSON parse failure. |
| S-214.3 | Run the same plan command with an unsupported index schema | Returns `KB_INSTALL_INDEX_SCHEMA_UNSUPPORTED`; details name the `schema version` gate, index path, supported schema, and actual schema. |
| S-214.4 | Run the same plan command with a validly structured index whose digest does not match | Returns `KB_INSTALL_RELEASE_INDEX_INVALID`; `details.gate` is `digest`, and `details.expected` and `details.actual` contain the digest values. |
| S-214.5 | Run the same plan command with a correctly sealed index that violates a validation rule | Returns `KB_INSTALL_RELEASE_INDEX_INVALID`; `details.gate` is `validation rule`, `details.path` names the index, and the cause names the failed rule. |
| S-214.6 | Run `node tools/kb-create/scripts/prepare-release-index.mjs --flow platform --artifacts-dir <stage> --binary-manifest <binary-manifest> --output <index>` with a staged package whose `kb-create.requirements.json` has a secret requirement without `env` and `services` | The sealer prints `KB_RELEASE_INDEX_SEAL_FAILED` with the exact manifest rule in `cause`, `details.gate: "manifest validation"`, `details.rule` naming the failed requirement, `retryable: false`, and a hint to declare `env` and `services` for that requirement. |
| S-214.7 | Run the same sealer command with a secret manifest requirement that declares `env` and `services` but also declares a default | The sealer prints `KB_RELEASE_INDEX_SEAL_FAILED` with the exact manifest rule in `cause` and `details.rule`, `retryable: false`, and a hint to remove the secret default; it does not suggest adding `env` or `services`. |

## Pass criteria

Every loader rejection identifies its gate and source file. When the gate has a
meaningful comparison, the expected and actual values are available in the
machine error details and the human reason explains the rejection.
