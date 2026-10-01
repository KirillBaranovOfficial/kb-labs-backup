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
| S-010.1 | From the project root, run `kb marketplace install @kb-labs/commit-entry@<version>` | Selected exact version is installed into the shared extension store and pinned in the selected scope. The platform receipt and every installed package version under the platform's `node_modules` are identical before and after the install. |
| S-010.2 | Run `kb marketplace plugins list` | Installed plugin, version, and enabled state are listed. |
| S-010.3 | Inspect `.kb/marketplace.lock` | Schema v3 entry has exact version, integrity, source kind, and enabled state. |
| S-010.4 | Run a documented command from the installed plugin | Command is discoverable and returns its documented result. |
| S-010.5 | Run `kb marketplace install @kb-labs/commit-entry@<version>` again | Installation remains pinned to the requested exact version without a duplicate lock entry. |
| S-010.6 | In secured mode, remove the saved CLI session and run `kb marketplace plugins list --scope platform --json` | The command exits non-zero with JSON error code `KB_AUTH_REQUIRED`, message `Authentication is required.`, and hint `Run "kb auth login".`; it does not report an internal error. |
