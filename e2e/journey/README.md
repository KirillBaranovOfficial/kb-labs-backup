# Fresh install journey runner

`run.sh` installs a candidate platform into a fresh container HOME, exercises
it, and uninstalls it before the container exits. The service modes also run
HTTP capability checks and a Studio browser journey while the gateway is still
running.

## Run local and secured service journeys

Run these from the monorepo root with Colima or Docker available:

```bash
KB_JOURNEY_ONLY=service-local:explore e2e/journey/run.sh --no-build
KB_JOURNEY_ONLY=service-secured:explore e2e/journey/run.sh --no-build
```

To run a selected claim, set `KB_JOURNEY_CASES`. The harness resolves every
runner that owns those claims for local and secured modes before it builds an
image or starts a container:

```bash
KB_JOURNEY_CASES="S-042,S-005.2" e2e/journey/run.sh --no-build
```

The selector resolves IDs from `qa cases coverage`, so a case ID expands to its
journey/e2e-claimed steps and a step ID selects that claim directly. A case with
any unclaimed step or a claim with no executable runner variant is refused with
the step ID before Docker work begins. To restrict execution, `KB_JOURNEY_ONLY`
filters the resolved runner set; it can contain comma-separated registered
runner selectors. If the filter omits an owner, the refusal names the missing
claims and the exact runner command.
Prerequisite steps declared by a selected step run first and are reported as
prerequisites; a failed prerequisite blocks the selected claim with its own
diagnosis. Unselected named steps appear as `SKIPPED (not selected)` in the
result. A filtered scoreboard lists the selection and marks itself partial;
partial evidence does not count toward case status or full-run deltas. Run the
normal mode without `KB_JOURNEY_CASES` to produce full scoreboard evidence.

The installed smoke covers successful fresh installation and the registered
project workflow and REST runtime health claims. Run these two commands to
prove both access modes:

```bash
e2e/journey/smoke.sh local --no-build
e2e/journey/smoke.sh secured --no-build
```

The script selects `PC-001.2`, `PC-002.3`, and `PC-002.8` and sets the matching
runner filter for each mode. Each mode uses its service runner for the install
and service claims, so the smoke performs one fresh install. It exits zero only
when all three claims pass; any FAIL or BLOCKED claim makes the command
nonzero. Preview the selected runner and prerequisite steps without building
or starting containers with:

```bash
e2e/journey/smoke.sh local --resolve-only
e2e/journey/smoke.sh secured --resolve-only
```

`--resolve-only` performs selection before report-directory setup and does not
write journey reports or start build/container work.

The local run verifies loopback access without login. The secured run creates
an isolated admin during installation, creates and activates a member through
the invite routes, and checks both roles in Studio. Each result is written to
`.journey/reports/service-local.json` or `service-secured.json` and includes the
Studio browser step for capability aggregation.

The runner image installs Playwright Test and Chromium because the journey runs
inside its own container and cannot use a browser installed on the host. The
fresh-host test is copied into that image at build time and targets the gateway
at `http://127.0.0.1:4000` from within the same container. The image is built by
`e2e/journey/run.sh`; no browser installation is needed on the host.

The runner image keeps `runner.py`, `service_runner.py`, `service_steps.py`,
`studio_steps.py`, and the other journey Python modules together in
`/opt/kb-journey`. The `kb-journey-runner` executable is a small bootstrap that
starts `runner.py` and records a `journey.runner` failure result if Python
cannot load or start it.

The batch gate prepares a fresh `journey-candidate` release from the worktree,
then runs these local and secured journeys after its build and checks. Preparing
the candidate is required even though each journey runner uses `--no-build`:
that flag skips the journey's own build and packaging, while the gate owns that
one-time setup.

```bash
scripts/local-gate.sh batch
```

The gate clears the two service result files before running. Its nightly
aggregator checks the local and secured profiles against every capability each
profile declares, records a non-zero journey process exit, and reports missing
or malformed result files as failures. A report is PASS only when all declared
capabilities have passing results.
