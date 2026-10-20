# Shared CLI JSONL streams

`stream.Writer` is the JSONL result contract for long-running Cobra commands.
Each event is one machine-result record:

```json
{"ok":true,"data":{"event":"alive"}}
```

A command can emit its completed result as one marked event before the terminal
record. For example, `kb-create apply --json` includes its receipt this way:

```json
{"ok":true,"data":{"operation":"apply","receipt":{"id":"receipt-1"},"logPath":"/tmp/kb-create/apply.jsonl"},"meta":{"stream":{"result":true}}}
```

`Result` writes this record once and no further events or errors may follow it.
Progress-only streams can finish without a result record.

A failure is a normal typed result record with a correlated platform error:

```json
{"ok":false,"error":{"code":"KB_RUNTIME_UNEXPECTED","area":"runtime","stage":"run","severity":"error","retryable":false,"message":"An unexpected error occurred.","hint":"Re-run with --debug and include the correlationId in a report.","correlationId":"g52-stream-example"}}
```

Every completed stream ends with one terminal record carrying the process exit
code:

```json
{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":0}}}
```

The only terminal exit codes are `0` (success), `1` (operation failure), and
`2` (invalid usage or input). `Event`, `Result`, and `Fail` write records;
`Finish` writes the terminal record exactly once. A command returns
`result.StreamComplete` with the writer's exit code so the shared Cobra
dispatcher does not render a second result.
