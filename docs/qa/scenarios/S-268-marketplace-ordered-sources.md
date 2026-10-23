---
id: S-268
title: Marketplace — resolve identities through ordered trusted sources
persona: solo-developer
area: marketplace
priority: P0
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

A developer installs a Marketplace entity by its identity from the KB Registry first, sees which configured source served it and what trust was verified, and uses npm only as an explicit fallback for an identity the Registry does not own.

## Prerequisites

- [ ] A platform install configured with the Registry URL, its Ed25519 public key and optional auth token through environment references, plus an optional npm fallback URL.
- [ ] A published Commit plugin, a Registry-owned identity, an npm-only fixture identity, and a deliberately tampered Registry mirror response.
- [ ] The installed journey Registry service, its State dependency, and Verdaccio are reachable through their configured service addresses.

## Steps

| # | Action | Expected |
|---|---|---|
| S-268.1 | Run `kb marketplace install @kb-labs/commit --json`, then inspect its lock entry, install output, and `kb marketplace plugins doctor --json` | Registry resolves the canonical plugin identity; installation reports source `registry` and signature `verified`; the lock records Registry source, tarball integrity, and verified signature status; doctor reports the same provenance. The Registry signature is verified with the configured Ed25519 public key before extraction. |
| S-268.2 | Run `kb marketplace install journey-npm-only-fixture --json` when Registry reports `not-found-unowned` and npm fallback contains the identity | Installation succeeds from npm only after that structured Registry result; output, lock, and doctor identify source `npm` and signature `unsigned`. |
| S-268.3 | Attempt the same fallback identity while the Registry returns an authentication or transport failure | The install fails with the catalogued source error code and its exact next command; npm is not queried and no package or lock entry is created. |
| S-268.4 | Attempt installation when Registry signature verification fails or tarball bytes differ from the Registry integrity | The install fails with the catalogued signature or integrity error code and exact next command; npm is not queried, no package is extracted, and the lock remains unchanged. |
| S-268.5 | Attempt an npm fallback for an identity whose namespace or handle is reserved by the Registry | Registry returns `not-found-owned`; installation fails with the catalogued ownership error code and exact next command. npm is not queried and no package or lock entry is created. |
| S-268.6 | Install using the Marketplace source URL, key reference, token reference, and optional npm URL supplied to `kb-create install`; update the platform; inspect `kb-create status` and `kb-create doctor` | The resolved ordered source configuration persists across update. Both status reports show effective source kinds and URLs with credentials redacted; the referenced signing key and token values are absent from config, status, and doctor output. |
| S-268.7 | Install through a Registry mirror whose metadata is valid but whose tarball has been altered | The mirror uses the same signature and integrity policy as the primary Registry; installation fails with the catalogued integrity error code and exact next command, does not fall through to npm, and leaves package files and lock unchanged. |
| S-268.8 | Run `kb marketplace plugins doctor --json` with a Marketplace lock entry that has no source provenance | Doctor fails with the catalogue-owned provenance error and the exact `kb marketplace install <identity>` command needed to record provenance; it does not infer or fill missing source fields. |
| S-268.9 | Install the platform with `kb-create install` and no Marketplace source flags, then inspect the rendered config and Marketplace health | The installer renders the release-declared default `marketplace.sources` list (`[npm]` for Release 1); the Marketplace service boots and reports ready with those sources. |
| S-268.10 | Put an installed Marketplace plugin entry in a project lock whose `resolvedPath` uses parent segments to reach a plugin outside the project, including normalized and backslash traversal forms, then run discovery; separately add a user-created local link whose absolute `link` points to a developer plugin outside the scope and run discovery | The installed entry is rejected with structured reason `invalid-lock`; discovery does not load or register it. The local link remains the recorded user-owned pointer and discovery loads the external developer plugin from that exact path. |
