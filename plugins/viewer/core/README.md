# Viewer core

`@kb-labs/viewer-core` validates `architecture.yaml`, resolves the selected architecture source, lists package files, and reads supported text content. Workspace sources are rooted at `docs/architecture`; imported sources live in the project's `.kb/viewer/sources` directory. File resolution rejects absolute paths, traversal, and symlink escapes.

```ts
import { loadArchitecture, resolveSourceRoot } from '@kb-labs/viewer-core';

const root = await resolveSourceRoot(projectRoot, 'workspace');
const architecture = await loadArchitecture(root);
```

The core package is used by the Viewer REST handlers. Build it with `pnpm --filter @kb-labs/viewer-core build`.
