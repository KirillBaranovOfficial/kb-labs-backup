# @kb-labs/infra-worker

Core package for the KB Labs infra worker plugin.

Exports:
- V3 plugin manifest (`./plugin-manifest`)
- CLI command handlers under `src/cli/commands`

## Commands

- `infra-worker:prepare`
- `infra-worker:capture-snapshot`
- `infra-worker:restore-snapshot`

## Build

```bash
pnpm --filter @kb-labs/infra-worker build
```
