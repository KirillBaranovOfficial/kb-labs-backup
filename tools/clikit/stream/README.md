# Shared CLI JSONL streams

`stream.Writer` is the JSONL result contract for long-running Cobra commands.
Each event is one machine-result record:

```json
{"ok":true,"data":{"event":"alive"}}
```

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
`2` (invalid usage or input). `Event` and `Fail` write records; `Finish` writes
the terminal record exactly once. A command returns `result.StreamComplete`
with the writer's exit code so the shared Cobra dispatcher does not render a
second result.
