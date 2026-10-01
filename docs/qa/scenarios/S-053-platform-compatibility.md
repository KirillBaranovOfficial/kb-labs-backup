---
id: S-053
title: Platform — refuse incompatible release selections before mutation
persona: solo-developer
area: platform-core
priority: P1
status: planned
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Resolve platform, SDK, plugin, adapter and binary compatibility before an
installation or update can change the active receipt or installed files.

## Prerequisites

- [ ] A sealed compatibility matrix with SDK and binary relations, and fixture plugin/adapter artifacts whose declared ranges exclude the candidate.
- [ ] An installed platform receipt with a selected plugin for the update compatibility check.

## Steps

| # | Action | Expected |
|---|---|---|
| S-053.1 | Run `kb-create plan --platform-version VERSION --sdk-version VERSION --policy strict --json` with an SDK outside the platform's `SDKRange` or release-index compatibility relation | Planning fails with `KB_INSTALL_INCOMPATIBLE_COMPONENTS`, identifies the rejected release set or SDK, includes a recovery hint, and leaves the active receipt unchanged. |
| S-053.2 | Run `kb-create plan --plugins fixture-plugin@VERSION --policy strict --json` and `kb-create plan --adapters fixture-adapter@VERSION --policy strict --json` where each manifest range excludes the candidate | Each plan fails with the typed incompatibility code and hint, names the plugin or adapter and rejected range, and leaves the active receipt unchanged. |
| S-053.3 | Resolve a platform bundle that lacks a binary for the runner's OS/architecture | Planning fails with `KB_INSTALL_INCOMPATIBLE_COMPONENTS`, names the missing target and leaves the active receipt unchanged. |
| S-053.4 | Update an installation containing the fixture plugin to a platform outside that plugin's declared range | Update is refused during resolution, names the plugin and range, and preserves the active receipt and host. |

## Pass criteria

The resolver returns a structured incompatibility error with a generic safe
hint before apply/update side effects. `--policy` is normalized and serialized,
but the resolver currently does not change behavior for `strict` versus
`compatible`; the range checker also accepts only `*`, exact versions, or a
major-matching caret range. This case stays planned until the canary candidate
includes the requested incompatible fixture artifacts and policy behavior is
specified and implemented. Update checks an explicitly supplied plugin request;
it does not independently reconstruct compatibility from plugins omitted by
the update request.
