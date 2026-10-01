---
id: S-182
title: Studio Marketplace — find and install Commit in one project
persona: solo-developer
area: marketplace
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [studio, api]
---

## Goal

Find the Commit package, review its release identity and permissions, and
install the selected version for one project without changing another.

## Prerequisites

- [ ] Studio is connected to a Marketplace catalog containing `@kb-labs/commit`.
- [ ] Projects A and B are registered, with neither project initially containing Commit.

## Steps

| # | Action | Expected |
|---|---|---|
| S-182.1 | Open Studio **Marketplace** and search for `@kb-labs/commit` | The matching Commit package appears with its available version. |
| S-182.2 | Open the Commit package detail and select its **Overview** and **Permissions** tabs | The page identifies package `@kb-labs/commit`, the selected version, declared permissions and package details before installation. |
| S-182.3 | Choose project A and install the selected Commit version from its Marketplace detail page | Studio reports success; project A's installed plugin list shows the pinned version and its Commit page is available. |
| S-182.4 | Select project B and inspect its installed plugin list and Marketplace state | Project B remains unchanged and does not report Commit as installed. |
| S-182.5 | Request installation of `@kb-labs/unknown-package` for project A | Studio shows an actionable package-not-found error; project A's lock/config and installed list remain unchanged. |

## Pass criteria

Studio already has a searchable Marketplace, package detail and permissions
view, and a separate project plugin installation form. It does not currently
connect the Marketplace detail page to that installer, and the install form
accepts a package spec rather than a catalog selection. The case is planned
until the user can install from the Marketplace detail and the unknown-package
failure preserves project state. It is release true because Studio's
Marketplace and Commit are in the release scope.
