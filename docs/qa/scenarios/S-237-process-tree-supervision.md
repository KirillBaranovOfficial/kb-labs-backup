---
id: S-237
title: Tooling — supervise complete process trees
persona: developer
area: tooling
priority: P1
status: active
release: false
modes: [local]
surfaces: [cli]
---

## Goal

As a tooling operator, inspect and clean up supervised command trees, including when the invoking supervisor exits unexpectedly, so hung or orphaned work is visible and does not outlive its owner.

## Preconditions

From the workspace root, set `TMP` to a fresh temporary directory, then build
`proc-helper` with `(cd tools/clikit && go build -o "$TMP/proc-helper"
./proc/testhelper)` and `kb-runner` with `(cd tools/kb-runner && go build -o
"$TMP/kb-runner" .)`. Set `STATE="$TMP/state"`, `OUT="$TMP/stdout.log"`, and
`ERR="$TMP/stderr.log"`. The fixture's `detached` and `double-fork` commands
create detached descendants and record their PID in a file when asked.

## Steps

| # | Action | Expected |
|---|---|---|
| S-237.1 | Run `"$TMP/kb-runner" run --tag normal --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- "$TMP/proc-helper" detached 30 "$STATE/descendant.pid"`; then run `"$TMP/kb-runner" ps --state "$STATE"`. For an interrupted run, start `"$TMP/kb-runner" run --tag interrupted --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sh -c '"$TMP/proc-helper" double-fork 300 & /bin/sleep 300 & wait'`, wait for `descendant-ready`, and SIGKILL that run process. | Normal completion and supervisor SIGKILL end descendants the supervisor can identify. Linux adopts orphaned descendants through child-subreaper ownership. On macOS, a detached same-user process is identified when `kern.procargs2` exposes its inherited run marker. If the query is denied or returns no environment, newer same-user processes appear as `unverified` in the tree and in the exit or stall event. They are not silently omitted or automatically signaled, because same-user processes may be unrelated. A detached process with a readable environment and no inherited marker is outside the macOS guarantee. Windows is unsupported. |
| S-237.2 | Run `"$TMP/kb-runner" run --tag silent --state "$STATE" --stall 1s --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; then run `"$TMP/kb-runner" events --state "$STATE"`. | One `stall` event appears with the leaf process command and elapsed time in the JSONL stream. |
| S-237.3 | Start `"$TMP/kb-runner" run --tag listed --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; from another process run `"$TMP/kb-runner" ps --state "$STATE"`, terminate the keeper, then run `"$TMP/kb-runner" ps --state "$STATE"` again. | The first listing includes the live run and tree. The second identifies the run as dead rather than live, using its PID and recorded process start time. |
| S-237.4 | Run commands that exit normally and by signal, then run `"$TMP/kb-runner" events --state "$STATE"`; start `"$TMP/kb-runner" events --state "$STATE" --follow` and observe a later run. Start N runs concurrently with distinct keepers and one shared state directory. | The append-only JSONL stream contains one structured `start`, `exit` (code or signal), `stall` when applicable, and `reap` event per lifecycle transition; concurrent records are complete JSON objects with no loss. `--follow` emits later records. |
| S-237.5 | Invoke `proc.Start` with a helper executable that does not dispatch the private `__keeper` command. | Start returns promptly with an actionable helper-handshake error and does not leave the invalid helper running. Replacing the helper file after a successful start affects future runs only; an already running keeper continues in its loaded process image. |

## Pass criteria

The shared supervisor owns the command tree independently from the invoking process. Live state and event records use the caller-selected state directory. Linux and macOS behavior is tested; Windows is explicitly unsupported. On macOS, process groups and the inherited run marker identify detached descendants when `kern.procargs2` returns an environment containing that marker. If the query is denied or hides the environment, newer same-user processes are surfaced as `unverified` in the tree and exit or stall event; the supervisor does not signal them because ownership is not established. A readable environment with no inherited marker is outside the macOS guarantee.
