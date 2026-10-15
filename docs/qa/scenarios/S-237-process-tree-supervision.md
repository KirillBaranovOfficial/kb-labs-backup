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
Every command that needs a temp file, output path, state directory, or minimum
free-space value obtains those values from the fresh `TMP` directory. Linux and
macOS are supported; Windows is unsupported.

## Steps

| # | Action | Expected |
|---|---|---|
| S-237.1 | Run `"$TMP/kb-runner" run --tag normal --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- "$TMP/proc-helper" detached 30 "$STATE/descendant.pid"`; then run `"$TMP/kb-runner" ps --state "$STATE"`. For an interrupted run, start `"$TMP/kb-runner" run --tag interrupted --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sh -c '"$TMP/proc-helper" double-fork 300 & /bin/sleep 300 & wait'`, wait for `descendant-ready`, and SIGKILL that run process. | Normal completion and supervisor SIGKILL end descendants the supervisor can identify. After a keeper is lost, teardown repeatedly observes and signals newly discovered owned descendants until consecutive observations find none, within a bounded cleanup period; if the bound expires, the teardown event reports `error` and the surviving `remaining` identities. Linux adopts orphaned descendants through child-subreaper ownership. On macOS, a detached same-user process is identified when `kern.procargs2` exposes its inherited run marker. If process-table observation fails, teardown reports the observation error and does not claim clean cleanup. If an environment/marker query is denied or returns no environment, a newer same-user process that could belong to the run appears as `unverified` in the tree and in exit/stall/teardown records; teardown reports that cleanup is not verified. Such a process is not silently omitted or automatically signaled, because same-user processes may be unrelated. A detached process with a readable environment and no inherited marker is outside the macOS guarantee. Windows is unsupported. |
| S-237.2 | Run `"$TMP/kb-runner" run --tag silent --state "$STATE" --stall 1s --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; then run `"$TMP/kb-runner" events --state "$STATE"`. | One `stall` event appears with the leaf process command and elapsed time in the JSONL stream. |
| S-237.3 | Start `"$TMP/kb-runner" run --tag listed --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`; from another process run `"$TMP/kb-runner" ps --state "$STATE"`, terminate the keeper, then run `"$TMP/kb-runner" ps --state "$STATE"` again. | The first listing includes the live run and tree. The second identifies the run as dead rather than live, using its PID and recorded process start time. |
| S-237.4 | Run commands that exit normally and by signal, then run `"$TMP/kb-runner" events --state "$STATE"`; start `"$TMP/kb-runner" events --state "$STATE" --follow` and observe a later run. Start N runs concurrently with distinct keepers and one shared state directory. | The append-only JSONL stream contains one structured `start`, `exit` (code or signal), `stall` when applicable, and `teardown` event per lifecycle transition; concurrent records are complete JSON objects with no loss. `--follow` emits later records. |
| S-237.5 | Invoke `proc.Start` with a helper executable that does not dispatch the private `__keeper` command. | Start returns promptly with an actionable helper-handshake error and does not leave the invalid helper running. Replacing the helper file after a successful start affects future runs only; an already running keeper continues in its loaded process image. |
| S-237.6 | Run `"$TMP/kb-runner" run --tag input --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" --stdin "$TMP/prompt.txt" --env MODEL=local --env TASK_ID=42 -- /bin/sh -c 'printf "prompt="; cat; printf "model=%s task=%s marker=%s\n" "$MODEL" "$TASK_ID" "$KBLABS_CLIPROC_RUN"'`. | The supervised command reads the prompt bytes from stdin and receives both explicit environment entries. `KBLABS_CLIPROC_RUN` is also present and has the active run ID. |
| S-237.7 | On macOS, start a helper that creates a detached `/bin/sleep` grandchild and keeps its intermediate ancestor alive for at least 300 ms; let the supervised root finish after the intermediate exits. | The 100 ms keeper sampler observes and records the grandchild's PID and process-start identity while it is in the ancestry tree. After reparenting, it remains verified and is signalled at teardown. A descendant created and reparented entirely between sampling instants, with no readable inherited marker, remains outside this guarantee and is reported `unverified` when observed. |
| S-237.8 | Start four `"$TMP/kb-runner" run --tag worker-N --state "$STATE" --stall 1h --stdout "$STATE/worker-N.out" --stderr "$STATE/worker-N.err" --slot-group workers --slots 2 -- /bin/sh -c 'echo $$ > "$1"; exec /bin/sleep 30' sh "$STATE/worker-N.pid"` commands concurrently in one state directory, substituting N with 1–4. Release the two active PIDs, then the next two. In a separate run with one slot, SIGKILL the keeper PID of the active run while its child is still alive. | At most two commands are running at once. The other two are visible as queued with positions 1 and 2, and are admitted in FIFO order as slots open. Admission is atomic across runner processes. In the one-slot run, shared identity pruning marks the dead keeper `lost`, records a named lost-run event, tears down its surviving tree, and admits the queued run. |
| S-237.9 | Run `kb-runner gate hold maintenance --state "$STATE" --tag holder --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`, then start `kb-runner run --wait-gate maintenance --state "$STATE" --tag waiter --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/sleep 30`. SIGKILL the gate-holding client process. | The waiting run is visible as `blocked: maintenance` and does not start while the keeper-owned gate is live. The keeper ends its tree after the client dies; the gate is released after the recorded holder PID/start identity is no longer live, and the waiting run starts. |
| S-237.10 | Start `"$TMP/kb-runner" run --min-free 18446744073709551615 --tag disk --state "$STATE" --stall 1h --stdout "$OUT" --stderr "$ERR" -- /bin/true` on a filesystem below that threshold. | The command does not start. `kb-runner ps` shows a visible disk-budget wait, and the event stream contains exactly one blocked event for that wait. |
| S-237.11 | Start `kb-runner events --state "$STATE" --follow --filter exit,stall,blocked,teardown` while a tagged command is active; let it exit with code 7; run `kb-runner wait --tag result --state "$STATE"`, then stop the follower. | `wait` blocks until the run ends and exits with code 7. JSONL contains start, wait transitions as applicable, exit code/signal and duration, and teardown inventory including reaped and unverified processes. The filtered event stream emits only requested event types and follows later records. |
| S-237.12 | While one run is active, queue another behind its slot, block a third behind a held gate, and run a silent command past its stall threshold; query `kb-runner ps --state "$STATE"` from another process once. | In this single listing, each `running`, `queued`, `blocked`, and `stalled` entry includes its own tree and elapsed time; queued entries include their queue position and blocked entries include their gate or disk reason. A wait is never displayed as an indistinguishable hang. |

## Pass criteria

The shared supervisor owns the command tree independently from the invoking process. Live state and event records use the caller-selected state directory. Linux and macOS behavior is tested; Windows is explicitly unsupported. On macOS, the keeper samples ancestry every 100 ms and remembers observed descendants by PID and start identity; it also uses the inherited run marker when `kern.procargs2` exposes it. If a descendant is created and reparented entirely between samples and its marker is unreadable, it remains outside the macOS guarantee and is surfaced as `unverified` if observed later. Unverified processes are never signalled. Slot admission uses the run registry and a shared state lock; gates are held by keeper identity; disk waits and queue states remain visible in `ps` and JSONL.
