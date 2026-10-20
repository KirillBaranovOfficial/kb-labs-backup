# @kb-labs/adapters-workspace-worktree

Git worktree implementation of `IWorkspaceProvider` for isolated workflow execution.

## Features

- Materialize a worktree from the owning registered project root supplied by the project runtime.
- Link configured CLI tool dist directories from the platform root, which may be separate from the project.
- Attach/release status for environment mount lifecycle.

Materialization requests must include `projectRoot`. Requests using the default CLI tool link must also include `platformRoot`.
