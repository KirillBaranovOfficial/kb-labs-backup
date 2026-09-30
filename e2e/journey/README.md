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

The batch gate performs these local and secured journeys after its build and
checks:

```bash
scripts/local-gate.sh batch
```
