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
| S-237.1 | Run `"$TMP/kb-runner" run --tag normal --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- "$TMP/proc-helper" detached 30 "$STATE/descendant.pid"`; then run `"$TMP/kb-runner" ps --state "$STATE"`. For an interrupted run, start `"$TMP/kb-runner" run --tag interrupted --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sh -c '"$TMP/proc-helper" double-fork 300 & /bin/sleep 300 & wait'`, wait for `descendant-ready`, and SIGKILL that run process. | Normal completion and supervisor SIGKILL both end the command's complete tree, including descendants that left the process group. Linux uses child-subreaper ownership; macOS uses process groups plus the inherited run marker. Windows is unsupported. |
| S-237.2 | Run `"$TMP/kb-runner" run --tag silent --state "$STATE" --stall 1s --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; then run `"$TMP/kb-runner" events --state "$STATE"`. | One `stall` event appears with the leaf process command and elapsed time in the JSONL stream. |
| S-237.3 | Start `"$TMP/kb-runner" run --tag listed --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; from another process run `"$TMP/kb-runner" ps --state "$STATE"`, terminate the keeper, then run `"$TMP/kb-runner" ps --state "$STATE"` again. | The first listing includes the live run and tree. The second identifies the run as dead rather than live, using its PID and recorded process start time. |
| S-237.4 | Run commands that exit normally and by signal, then run `"$TMP/kb-runner" events --state "$STATE"`; start `"$TMP/kb-runner" events --state "$STATE" --follow` and observe a later run. | The append-only JSONL stream contains one structured `start`, `exit` (code or signal), `stall` when applicable, and `reap` event per lifecycle transition. `--follow` emits later records. |

## Pass criteria

The shared supervisor owns the command tree independently from the invoking process. Live state and event records use the caller-selected state directory. Linux and macOS behavior is tested; Windows is explicitly unsupported. On macOS, a descendant that deliberately removes the inherited run marker and reparents outside the command tree is beyond the platform guarantee.
