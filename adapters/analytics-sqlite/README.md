# @kb-labs/adapters-analytics-sqlite

SQLite analytics adapter for the KB Labs platform. Stores and queries event data with concurrent writes and no lock contention, suitable for single-node deployments.

## Retention

SQLite analytics deletes events older than 90 days when the platform starts.
Set `platform.adapterOptions.analytics.retentionDays` in `.kb/kb.config.json`
to choose another positive age in days:

```json
{
  "platform": {
    "adapterOptions": {
      "analytics": {
        "retentionDays": 30
      }
    }
  }
}
```

The new policy applies on the next platform start. Other analytics adapters
own retention for their own storage backend.
