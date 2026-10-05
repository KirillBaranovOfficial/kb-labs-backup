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
| S-182.3 | Choose project A in the detail page's **Project** selector and install Commit | Studio reports `Installed Commit for Project A`; project A's installed plugin list shows the pinned version and its Commit page is available. |
| S-182.4 | Choose project B in the detail page's **Project** selector and inspect its installed plugin list | Project B remains unchanged and does not report Commit as installed. |
| S-182.5 | Open project A's Plugins page and request installation of `@kb-labs/unknown-package` | Studio shows an actionable package-not-found alert using the error message and hint; project A's lock/config and installed list remain unchanged. |

## Pass criteria

Studio already has a searchable Marketplace, package detail and permissions
view, and a separate project plugin installation form. The Marketplace detail
page lets the user choose a registered project and install the selected catalog
package there. The project Plugins page continues to accept a package spec;
unknown-package failures show the shared error message and hint in an alert and
preserve project state. The case is planned until the install and failure
outcomes pass. It is release true because Studio's
Marketplace and Commit are in the release scope.
