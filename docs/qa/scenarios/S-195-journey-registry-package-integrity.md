---
id: S-195
title: Journey publisher — Detect stale registry tarballs
persona: developer
area: developer-tooling
priority: P1
status: planned
release: false
modes: [local]
surfaces: [cli]
---

## Goal

A journey maintainer can tell whether a persistent local registry already has the exact package bytes required by the current build, so the journey cannot silently install stale packages.

## Prerequisites

- [ ] The journey publisher has a packed package tarball and can reach its Verdaccio registry.
- [ ] The Verdaccio storage volume may contain a package version from an earlier run.

## Steps

| # | Action | Expected |
|---|---|---|
| S-195.1 | Run the journey publisher when a package version already exists in Verdaccio with the same tarball digest | The publisher reports that the package and version already exist with matching bytes, skips republishing, and exits successfully. |
| S-195.2 | Run the journey publisher when a package version already exists in Verdaccio with a different tarball digest | The publisher exits non-zero and names the package, version, registry digest, local digest, and next steps to remove `e2e_verdaccio-storage` or bump the package version. |
| S-195.3 | Start the journey once with fresh Verdaccio storage and once with package metadata from an earlier publish | Fresh storage does not produce a reuse warning; package metadata produces the one-line warning that the registry storage is being reused. |
| S-195.4 | Run the publisher with `/packages/pr` and `/packages/base` tarballs that have the same package name and version but different bytes | The publisher publishes the PR tarball once, reports `~ <name>@<version> (superseded by PR tarball)` for the base tarball, and exits successfully. |
| S-195.5 | Run the publisher with stable versions 2.120.1 and 2.120.2 and prerelease 2.120.3-canary.1 for one package, with 2.120.2 accepted before 2.120.1 | Publishing succeeds; 2.120.2 receives the `latest` tag, 2.120.1 receives `journey-2.120.1`, and 2.120.3-canary.1 receives `canary`. |
