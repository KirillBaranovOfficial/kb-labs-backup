---
id: S-225
title: Marketplace doctor reports a real store integrity mismatch
persona: solo-developer
area: platform-core
priority: P1
status: active
release: false
modes: [local, secured]
surfaces: [cli]
---

## Goal

As a platform operator, I can distinguish a real marketplace artifact pin mismatch from a different local directory digest and get a repair command that restores the pinned artifact.

## Prerequisites

- [ ] An isolated platform or project with a marketplace lock pin and an extension-store entry for the same package and version whose recorded integrity differs from the lock pin.

## Steps

| # | Action | Expected |
|---|---|---|
| S-225.1 | Run `kb marketplace plugins doctor --scope <scope> --json` for the isolated scope containing the mismatched store entry | The command exits non-zero with one JSON failure envelope. Its diagnostics identify the package with `INTEGRITY_MISMATCH`, show that the store integrity differs from the marketplace pin, and give `kb marketplace install <package>@<version>` as the remediation. |
