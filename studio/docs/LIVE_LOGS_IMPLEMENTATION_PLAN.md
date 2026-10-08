# Live Logs: current implementation

The original proposal in this file described a Pino HTTP transport and a REST
ingestion route. Neither was shipped, so that proposal is retired.

Live log data stays in the platform logging adapters:

- `@kb-labs/adapters-log-ringbuffer` keeps recent records in process memory and
  supports live subscriptions.
- `@kb-labs/adapters-log-document` persists records through the configured
  document database.
- The platform log reader combines the available ring buffer and persistence
  adapters for API and plugin consumers.

Transport between processes is implemented by `core/ipc`. Remote plugin
execution uses the `IExecutionTransport` contract from
`core/contracts/src/execution-transport.ts`, with gateway dispatch wiring in
`core/plugin-execution-factory`.
