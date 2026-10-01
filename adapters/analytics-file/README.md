# @kb-labs/adapters-analytics-file

> Part of [KB Labs](https://github.com/kb-labs-team/kb-labs) ecosystem. Works exclusively within KB Labs platform.

File-based analytics adapter for KB Labs platform. Writes events and metrics as JSONL into platform state.

## Usage

`kb.config.json`:
```json
"platform": {
  "adapters": {
    "analytics": "@kb-labs/adapters-analytics-file"
  },
  "adapterOptions": {
    "analytics": {
      "filenamePattern": "events-YYYYMMDD"
    }
  }
}
```

Options (all optional):
- `baseDir`: target directory (default `<KB_HOME>/state/<projectId>/analytics/buffer`).
- `filenamePattern`: filename without extension (`YYYYMMDD` will be replaced by date), default `events-YYYYMMDD`.

Record format: one JSON object per line.

## License

[KB Public License v1.1](../../LICENSE) - KB Labs Team
