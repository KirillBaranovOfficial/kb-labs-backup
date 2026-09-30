# KB Labs Studio Data Client

`@kb-labs/studio-data-client` provides typed data sources and TanStack Query
hooks for Studio. HTTP sources target the machine gateway; project runtime data
is routed through the selected project context.

## Gateway and project routes

The system source calls gateway-native endpoints at `/api/health`, `/api/ready`,
and `/api/observability/health`. Host APIs such as projects, plugins, and
adapters use `/api/v1`. Observability requests use a project route when a
project id is supplied:

```ts
import { createDataSources } from '@kb-labs/studio-data-client';

const sources = createDataSources({
  mode: 'http',
  baseUrl: '/api/v1',
  projectId: 'project-01',
});

await sources.observability.queryLogs({ limit: 50 });
```

The request is routed through `/api/v1/projects/project-01/logs`; the gateway
starts the project runtime on demand. Project plugin sources list pins and
support install, enable/disable, and uninstall through host routes. Project
adapter sources install packages at machine scope, bind them to project slots,
and configure project-scoped options. Their TanStack Query hooks invalidate
the selected project's list after mutations.

## Development

Run from the monorepo root:

```bash
pnpm --filter @kb-labs/studio-data-client type-check
pnpm --filter @kb-labs/studio-data-client test
```
