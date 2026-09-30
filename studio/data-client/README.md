# KB Labs Studio Data Client

`@kb-labs/studio-data-client` provides typed data sources and TanStack Query
hooks for Studio. HTTP sources target the machine gateway; project runtime data
can be routed through the active project context.

## Gateway routes

The system source calls gateway-native endpoints at `/api/health`, `/api/ready`,
and `/api/observability/health`. Projects, plugins, and adapters use host APIs
under `/api/v1`.

Observability sources accept an optional `projectId`. When it is set, logs and
event streams use the gateway's project route. For example:

```ts
import { createDataSources } from '@kb-labs/studio-data-client';

const sources = createDataSources({
  mode: 'http',
  baseUrl: '/api/v1',
  projectId: 'project-01',
});

await sources.observability.queryLogs({ limit: 50 });
```

The client requests `/api/v1/projects/project-01/logs`; the gateway starts the
project runtime on demand. Studio must not send project log requests to an
unscoped `/api/v1/logs` route.

## Development

From the monorepo root:

```bash
pnpm --filter @kb-labs/studio-data-client type-check
pnpm --filter @kb-labs/studio-data-client test
```
