# Viewer contracts

`@kb-labs/viewer-contracts` contains the schemas and TypeScript types shared by the Viewer core and entry packages. The architecture index uses schema version 1; each node has a stable ID, a C1–C4 level, optional parent, diagram, documents, and case references.

```ts
import { architectureIndexSchema } from '@kb-labs/viewer-contracts';

const index = architectureIndexSchema.parse({
  schemaVersion: 1,
  id: 'kb-labs',
  title: 'KB Labs',
  root: 'system',
  nodes: [{ id: 'system', title: 'KB Labs', level: 'C1', children: [] }],
});
```

The package has no KB Labs runtime dependency. Build it with `pnpm --filter @kb-labs/viewer-contracts build`.
