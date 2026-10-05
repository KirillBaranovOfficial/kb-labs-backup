---
id: S-053
title: Platform — refuse incompatible release selections before mutation
persona: solo-developer
area: platform-core
priority: P1
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Resolve platform, SDK, plugin, adapter and binary compatibility before an
installation or update can change the active receipt or installed files.

## Prerequisites

- [ ] Sealed release entries for the stable platform, canary prerelease, an incompatible SDK, and a platform without the runner's OS/architecture binary.
- [ ] An installed plugin fixture whose declared platform range accepts version N and excludes N+1.
- [ ] The installer runs on a supported Linux or macOS host.

## Steps

| # | Action | Expected |
|---|---|---|
| S-053.1 | Plan with `--policy strict` for a platform/SDK pair present as an exact relation in the sealed matrix | The exact selected pair resolves and appears in the immutable plan. |
| S-053.2 | Plan the same platform against an indexed SDK admitted by the matrix range but without an exact relation, once with `--policy strict` and once with `--policy compatible` | Strict returns `KB_INSTALL_INCOMPATIBLE_COMPONENTS` with `details.component=SDK`, `details.version`, `details.range`, and `details.policy=strict`; the hint is `Use an exact platform/SDK pair from the release matrix or choose --policy compatible.` Compatible resolves only if the matrix range admits that exact SDK version. |
| S-053.3 | Install plugin `fixture-compat@N`, then run `kb-create update --platform-version N+1 --policy compatible --json` where the installed manifest range excludes N+1 | Update refuses before mutation with `KB_UPDATE_INCOMPATIBLE`; details contain `component=fixture-compat`, `kind=plugin`, `version=N+1`, and the declared `range`; the hint is `Update or remove plugin fixture-compat first, then rerun \`kb-create update\`.` The active receipt and host remain unchanged. |
| S-053.4 | Request an adapter whose declared platform or SDK range excludes the selected release | Planning fails with `KB_INSTALL_INCOMPATIBLE_COMPONENTS`; details contain the adapter in `component`, the rejected `version`, and declared `range`; the hint is `Choose compatible versions or pass an explicit supported provider.` No installation files change. |
| S-053.5 | Request an SDK outside the platform SDK range or a platform without a binary for the runner OS/architecture | Planning fails with `KB_INSTALL_INCOMPATIBLE_COMPONENTS`; an SDK rejection names `component=platform SDK`, its `version`, and `range`, while a missing binary identifies `details.subject=binary` and the OS/architecture in `details.value`. The error includes a hint and leaves the active receipt unchanged. |
| S-053.6 | Attempt an update that violates an installed plugin or adapter range | `KB_UPDATE_INCOMPATIBLE` identifies the installed component, kind, target version and range, and says to update or remove that component first. The receipt and running host are preserved. |
| S-053.7 | On the Linux or macOS runner, plan from a sealed release fixture that omits the runner OS/architecture binary | Planning returns `KB_INSTALL_INCOMPATIBLE_COMPONENTS` with `details.subject=binary` and the missing OS/architecture; the isolated install root is unchanged. |

## Pass criteria

Strict compatibility means an exact relationship in the sealed matrix;
compatible compatibility may use a valid semantic-version range declared by
that matrix. Update evaluates every plugin and adapter in the active receipt,
even when the new update request omits them. Incompatible updates fail before
the snapshot or any package/config/receipt mutation.
