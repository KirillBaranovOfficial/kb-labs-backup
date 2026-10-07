---
id: S-010
title: Marketplace — Install plugin
persona: solo-developer
area: marketplace
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal
Developer discovers a plugin in the marketplace and installs it. Plugin commands appear in CLI after install.

## Prerequisites
- [ ] Platform installed, `kb-dev start` done
- [ ] Internet access (marketplace registry)
- [ ] In secured mode, the CLI has a saved user session created by `kb auth login`.

---

## Steps

| # | Action | Expected |
|---|---|---|
| S-010.1 | From the project root, run `kb marketplace install @kb-labs/commit-entry@<version> --json`, then run `kb marketplace install @kb-labs/commit-entry@<version> --scope platform --json` | Both commands exit 0 and print JSON results whose `installed` list contains the canonical manifest ID `@kb-labs/commit` at the selected exact version. Each scope's schema v3 lock records that ID and version with a `store:` resolved path. The platform receipt and every installed package version under the platform's `node_modules` are identical before and after both installs. |
| S-010.2 | Run `kb marketplace plugins list` | The plugin is listed by its canonical manifest ID `@kb-labs/commit`, with its version and enabled state. |
| S-010.3 | Inspect `.kb/marketplace.lock` after installing `@kb-labs/commit-entry` | The schema v3 plugin entry is keyed by the canonical `kb.plugin/3` manifest ID (`@kb-labs/commit`) and records the npm package name as `package: "@kb-labs/commit-entry"`, alongside the exact version, integrity, source kind, and enabled state; its key equals the installed manifest's `id`. |
| S-010.4 | Run a documented command from the installed plugin | Command is discoverable and returns its documented result. |
| S-010.5 | Run `kb marketplace install @kb-labs/commit-entry@<version>` again | Installation remains pinned to the requested exact version without a duplicate lock entry. |
| S-010.6 | In secured mode, remove the saved CLI session and run `kb marketplace plugins list --scope platform --json` | The command exits non-zero with JSON error code `KB_AUTH_REQUIRED`, message `Authentication is required.`, and hint `Run "kb auth login --gateway-url <gateway-url> --email <email>".` with the configured gateway URL rendered when available; it does not report an internal error. |
| S-010.7 | From the project root, run `kb marketplace plugins doctor --scope project --json` against the running installed platform; repeat after stopping the host | With the host running, the CLI receives its gateway address from the installed platform and returns `{ "ok": true, "data": ... }`. With the host stopped, it exits non-zero with `KB_HOST_UNREACHABLE`, the catalogued `kb-create start` / `kb-create doctor` hint, and no `INTERNAL_ERROR`. |
