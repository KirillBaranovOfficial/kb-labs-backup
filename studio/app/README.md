# KB Labs Studio App

`@kb-labs/studio-app` is the KB Labs web UI. The machine gateway serves its
static bundle and owns browser access to host APIs and project runtimes.

## What it does

- Lists registered projects and manages each project's plugins and adapters.
- Shows gateway health and project-scoped runtime logs and events.
- Loads enabled plugin pages through the Studio federation layer.
- Uses gateway sessions in secured mode and no login in loopback-only local mode.

## Project marketplace

Select a project from the shared project context, then use the Plugins page to
install or uninstall plugin package specs and enable or disable project pins.
The Adapters page installs adapters at machine scope, binds them to slots for
the selected project, and edits project-scoped adapter options. These actions
go through host APIs; Studio does not edit marketplace locks or config files.

## Development

Run from the monorepo root:

```bash
pnpm --filter @kb-labs/studio-app dev
pnpm --filter @kb-labs/studio-app build
pnpm --filter @kb-labs/studio-app test
```

The rspack dev server supports frontend work. API requests require a reachable
gateway. Installed platforms serve the built bundle from the gateway.

## Access and API routes

Local installs bind the gateway to loopback and open Studio without a login.
Secured installs require a gateway session. Members can use projects and their
own account; administrators can manage users and invites.

Browser requests use the gateway origin. Machine health uses `/api/health`,
`/api/ready`, and `/api/observability/health`; host project registration uses
`/api/v1/projects`. Project runtime calls include the active project id, such as
`/api/v1/projects/{projectId}/logs`.

Plugin pages must use `@kb-labs/sdk` and public contracts. They must not import
Studio implementation packages such as `@kb-labs/studio-data-client` or
`@kb-labs/studio-federation`.
