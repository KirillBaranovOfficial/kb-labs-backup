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

To confirm only the journey claims for selected case IDs or step IDs, add
`KB_JOURNEY_CASES` to a single-mode run:

```bash
KB_JOURNEY_ONLY=service-secured:explore \
  KB_JOURNEY_CASES="S-042,S-005.2" \
  e2e/journey/run.sh --no-build
```

The selector resolves IDs from `qa cases coverage`, so a case ID expands to its
journey/e2e-claimed steps and a step ID selects that claim directly. Unknown or
unclaimed IDs stop the run with a usage error. Unselected named steps appear as
`SKIPPED (not selected)` in the result. A filtered scoreboard lists the
selection and marks itself partial; partial evidence does not count toward case
status or full-run deltas. Run the normal mode without `KB_JOURNEY_CASES` to
produce full scoreboard evidence.

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
