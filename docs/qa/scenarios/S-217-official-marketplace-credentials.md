---
id: S-217
title: Marketplace — install from and publish to the official KB Labs marketplace
persona: plugin-author
area: marketplace
priority: P2
status: planned
release: false
modes: [local, secured]
surfaces: [cli, studio]
---

## Goal

Users install public plugins from the official KB Labs marketplace without any token. Teams get private or paid plugins through their platform's own marketplace credential. Plugin authors manage their plugins with a personal author credential. A credential for one role is never needed or used for another.

## Prerequisites

- [ ] The official marketplace is a hosted KB Labs Gateway in secured mode (S-216).
- [ ] A plugin author account owning the scope `@author-scope`.
- [ ] A team platform in secured mode with an administrator and a member.

## Steps

| # | Action | Expected |
|---|---|---|
| S-217.1 | On a fresh installation with no marketplace credential, run `kb marketplace install @author-scope/public-plugin@<version> --json` | Installation succeeds without any login. The lock entry is keyed by the plugin's manifest ID, records its package name, exact version, integrity, and the marketplace origin as its source, and installation verifies the integrity before the plugin loads. |
| S-217.2 | As the team administrator, register the platform with the marketplace so that the host holds an installation credential; as a member signed in only to the team platform, install a private plugin the team is entitled to | The member's command is authorized by the team platform. The host fetches the package from the marketplace with the installation credential, so the member never holds or sees a marketplace token. A member without the platform permission receives `KB_AUTH_FORBIDDEN` from the team platform. |
| S-217.3 | As the author, sign in to the marketplace (S-216.1) and publish a new version of `@author-scope/public-plugin` | Publishing succeeds only for scopes the author owns; publishing to another owner's scope returns `KB_AUTH_FORBIDDEN` naming the scope. The published manifest ID must equal the scope/name being published. |
| S-217.4 | Create a CI publish credential limited to `@author-scope` with an expiry, publish with it from CI, then revoke it | The credential can publish only to its scope until it expires or is revoked; after revocation, publishing returns `KB_AUTH_REQUIRED`. A CI provider identity token may be exchanged for a short-lived publish credential instead of storing a long-lived secret. |
| S-217.5 | As the author, deprecate one version and yank another | Existing locks pinned to the deprecated version still install it with a deprecation warning; a yanked version is no longer offered for new installs, and an existing pin to it fails with a catalogued error and a hint to choose another version. |

Planned. The plugin identity contract (manifest ID as lock key, package name recorded in the lock) is already in place; the store, author accounts, and installation registration are not.
