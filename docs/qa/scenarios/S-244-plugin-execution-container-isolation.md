---
id: S-244
title: Plugin execution — container permission boundaries and isolation
persona: team-admin
area: plugin-execution
priority: P1
status: planned
release: false
modes: [local, secured]
surfaces: [cli, api]
---

## Goal

Run a fixture plugin in paranoid container mode and verify that its manifest
permissions control access and resource limits at the isolated environment
boundary.

## Prerequisites

- [ ] `platform.execution.mode` is `container` and the container dispatch secret is configured.
- [ ] A fixture manifest declares selected permissions using `PermissionSpec`; a second fixture omits those permissions.
- [ ] The fixture can attempt filesystem, network, environment, platform service, shell, and plugin invocation operations, and report configured quotas.

## Steps

| # | Action | Expected |
|---|---|---|
| S-244.1 | Run the permission-declaring fixture with manifest `fs.read`, `fs.write`, `network.fetch`, `network.tcp.connect`, `network.ws.connect`, `env.read`, `platform.*`, `shell.allow`, and `invoke.allow` grants | Only declared operations succeed. Container mounts enforce filesystem grants; egress allowlists enforce declared network targets; only declared environment variables are present; platform, shell, and invoke permissions follow the manifest. An omitted permission remains denied. |
| S-244.2 | Run fixtures with `quotas.timeoutMs`, `memoryMb`, `cpuMs`, `maxProcesses`, and `maxOutputBytes` set, then exceed each quota | Each limit is enforced for the execution. Container memory, CPU, and process limits are enforced by the operating system. The violation returns its catalogued error, not `KB_RUNTIME_UNEXPECTED`. |
| S-244.3 | Omit all network declarations and try `fetch`, TCP, and WebSocket connections; then declare one allowed target and repeat | Undeclared network access is denied; the declared target is allowed and other targets remain denied. No adapter configuration grants network access in place of the plugin manifest. A denial has a catalogued error whose hint names the `manifest.permissions` field to declare. |
| S-244.4 | Stop the configured container runtime and run `kb fixture execution healthy --json` | The request fails with catalogued container-unavailable and its hint. No environment reservation or workspace attachment remains. |
| S-244.5 | Complete one successful and one failed fixture execution | The container environment and materialized workspace are released after both outcomes. |
| S-244.6 | Install an unsigned plugin from the internet without overriding the execution mode | The resolved mode defaults to container for this untrusted source. This is a planned trust-tier claim, not a release claim. |

## Decision notes

- D56: “container = the ‘paranoid’ mode.” It is “slow, expensive and maximally isolated.” It is the security mechanism for marketplace trust tiers: marketplace-signed KB Labs plugins, author-signed plugins, then unsigned internet plugins; it is intended to become the default for untrusted sources. This trust-tier behavior is a planned claim, not a release claim.
- D61: “container boundaries are derived from the plugin manifest `PermissionSpec`.” `DEFAULT_PERMISSIONS` denies by default. Network is available only when the manifest declares it: undeclared means denied, with no fallback or auto-grant. A denial is catalogued and its hint names the manifest field to declare.
- D61: Each declared container boundary is enforced at the OS level: mounts, egress allowlist, environment, and memory/CPU/process limits.

## Source

The permission contract and deny-by-default values are in
`core/plugin-contracts/src/permissions.ts`. Container provisioning is in
`core/runtime/src/loader.ts`; Docker launch options are in
`adapters/environment-docker/src/index.ts`. Known missing Docker limits are
recorded in the report findings.
