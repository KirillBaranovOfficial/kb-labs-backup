# kb-labs-studio-app

KB Labs Studio application — web UI for interacting with KB Labs plugins and products.

## Vision & Purpose

**kb-labs-studio-app** is the main Studio frontend.  
It renders dashboards, plugin widgets, workflow runs, analytics views, and release/audit results powered by the KB Labs REST API and Mind.

### Core Goals

- Provide a unified web UI for all KB Labs products (Audit, Release, Mind, DevLink, AI tools)
- Render plugin-provided Studio widgets (via `@kb-labs/plugin-adapter-studio`)
- Offer dashboards, gallery, and per-product pages on top of `@kb-labs/data-client`

## Package Status

- **Version**: 0.1.0  
- **Stage**: Stable (internal)  
- **Status**: Production Ready for internal use ✅

## Architecture

### Stack

- **Framework**: React 18 + React Router
- **UI**: `@kb-labs/ui-core`, `@kb-labs/ui-react`, Ant Design, Lucide
- **Data**: `@tanstack/react-query` with `@kb-labs/data-client`

### Structure

```
apps/studio/
├── src/
│   ├── App.tsx              # Root React tree and providers
│   ├── router.tsx           # Route definitions
│   ├── modules/             # Feature modules (analytics, dashboard, mind, workflows, settings)
│   ├── components/          # Shared layout and widget host components
│   ├── layouts/             # Layouts (TwoPane, DashboardGrid, etc.)
│   ├── providers/           # Auth, data sources, registry providers
│   ├── registry/            # Studio registry loader and types
│   ├── plugins/             # Plugin discovery and registry integration
│   ├── hooks/               # Custom hooks (polling, widget data)
│   └── styles/              # Theme and CSS variables
└── vite.config.ts           # Vite configuration
```

The app uses a plugin registry to dynamically render Studio widgets exposed by plugins.

## Project marketplace

The project Plugins page installs and uninstalls plugin package specs through
the host API and manages each project's enabled state. The Adapters page
installs adapter packages at machine scope, binds an installed package to a
slot in the selected project, and edits project-scoped adapter options. Both
pages use the host's `/api/v1/plugins` and `/api/v1/adapters` routes; they do
not edit marketplace locks or config files directly.

## Scripts

From `kb-labs-studio` repo root:

```bash
pnpm install
pnpm --filter kb-labs-studio-app dev      # Start Vite dev server
pnpm --filter kb-labs-studio-app build    # Build for production
pnpm --filter kb-labs-studio-app preview  # Preview built bundle
pnpm --filter kb-labs-studio-app test     # Run Vitest tests
```

## Integration Points

### Accounts and API tokens

In secured mode, Studio uses Gateway sessions for account and team access. The
account pages include password changes, active sessions, and `/account/tokens`
for creating, listing, and revoking personal agent/CI tokens. Admin-only user
and invite pages create reset or activation links for out-of-band sharing.

- **REST API**: uses `@kb-labs/data-client` with `VITE_API_BASE_URL` (usually `/api/v1`)
- **Plugins**: consumes Studio manifests via `@kb-labs/plugin-adapter-studio` and registry loader
- **Mind**: uses `@kb-labs/mind-cli` and Mind surfaces for advanced views

## Environment

Key environment variables (via Vite):

- `VITE_API_BASE_URL` — base URL of REST API (defaults to `/api/v1`)

