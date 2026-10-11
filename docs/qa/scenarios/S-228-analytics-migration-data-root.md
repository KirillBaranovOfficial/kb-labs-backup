---
id: S-228
title: Analytics migration uses the platform data root
persona: platform-operator
area: analytics
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

Run the SQLite and DuckDB JSONL migration utilities against the data root owned
by a selected platform, regardless of the shell's current directory.

## Steps

| # | Action | Expected |
|---|---|---|
| S-228.1 | With a JSONL file under the platform data root, run each analytics migration utility with `--platform-root <platform-root> --dry-run` from outside the platform root | The reported source and database paths are resolved through the platform layout contract: `<workspace>/.kb/data` in development or the installed platform's shared `state/data` projection. |
| S-228.2 | Run either migration utility without `--platform-root` and without `KB_PLATFORM_ROOT` | It exits with a clear message that names both supported ways to provide the platform root and the exact next step. |

## Pass criteria

Both migration tools use the shared platform-layout resolver. Their relative
source and destination defaults do not depend on process cwd.
