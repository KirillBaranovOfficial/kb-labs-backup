---
id: S-192
title: Marketplace — call Commit REST routes after installation
persona: platform-developer
area: marketplace
priority: P0
status: planned
release: true
modes: [local, secured]
surfaces: [cli, api]
---

## Goal

Install the release Commit plugin, call a declared project REST route, then
uninstall it and verify that its route is no longer available.

## Prerequisites

- [ ] A registered project and authenticated project API client.
- [ ] A published `@kb-labs/commit` marketplace version compatible with the platform.

## Steps

| # | Action | Expected |
|---|---|---|
| S-192.1 | Run `kb marketplace install @kb-labs/commit-entry@<version>` in the registered project | The selected Commit version is installed and enabled in that project's marketplace state. |
| S-192.2 | GET `/api/v1/projects/<project-id>/rest/api/v1/plugins/commit/status` for the registered project | The Commit handler answers successfully with its status response. |
| S-192.3 | Run `kb marketplace uninstall @kb-labs/commit-entry` in the project, then GET `/api/v1/projects/<project-id>/rest/api/v1/plugins/commit/status` again | The request returns HTTP 404 with the gateway UX envelope `{ok:false,error:{code:"KB_RUNTIME_UNEXPECTED",message:"An unexpected error occurred.",hint:"Re-run with --debug and include the correlationId in a report.",correlationId:<non-empty>}}`. The code is the catalogued gateway fallback for an unrecognized 404. |

## Pass criteria

The installable artifact is `@kb-labs/commit-entry` and its manifest ID is
`@kb-labs/commit` (`plugins/commit/entry/package.json:2`;
`plugins/commit/entry/src/manifest.ts:63`). Commit declares
`GET /v1/plugins/commit/status` in its manifest and response
contract (`plugins/commit/entry/src/manifest.ts:227-247`;
`plugins/commit/contracts/src/routes.ts:7-24`). REST route mounting replaces
the manifest `/v1` prefix with the configured `/api/v1` base path
(`services/rest-api/app/src/routes/plugins.ts:404-450`), giving the request
path in this case. Gateway UX boundary maps unrecognized HTTP 404 responses to
the catalogued `KB_RUNTIME_UNEXPECTED` error envelope
(`services/gateway/app/src/ux-boundary.ts:16-38`,
`services/gateway/app/src/errors.catalog.json:1050`). However, after the server
starts listening, a marketplace registry change cannot remount plugin routes;
the implementation logs that a restart is required
(`services/rest-api/app/src/routes/index.ts:209-237`). Keep the case planned
until install and uninstall make the Commit route appear and disappear without
an undocumented manual router change or restart. The case is release-scoped
because Commit is the release plugin and REST routes are part of its advertised
plugin surface.
