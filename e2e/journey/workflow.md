# Workflow service journey

Run this on a freshly installed platform in both local and secured gateway
modes. The journey runner currently installs and starts the platform but does
not yet have a service scenario; this file is the step contract for wiring it
into that runner.

## Local mode

1. Start from a fresh project directory and register it:

   ```bash
   kb project add
   ```

2. Create and lint the no-network sample:

   ```bash
   kb workflow create
   kb workflow lint
   ```

   Assert `.kb/workflows/hello.yml` exists and lint exits successfully.

3. Run it from the CLI and poll the returned run ID until `success`:

   ```bash
   RUN_JSON=$(kb workflow run --workflow-id=hello --json)
   RUN_ID=$(printf '%s' "$RUN_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["runId"])')
   kb workflow runs status "$RUN_ID" --json
   ```

   Assert the step output or run logs contain `hello-workflow`:

   ```bash
   kb workflow runs logs "$RUN_ID"
   kb workflow runs list --json
   ```

4. Submit another run through the gateway's project route, then poll and
   inspect it with the CLI. Set `PROJECT_ID` to the ID returned by project
   registration and `KB_GATEWAY_URL` to the installed gateway origin:

   ```bash
   HTTP_JSON=$(curl --fail-with-body --silent --show-error \
     -H 'Content-Type: application/json' \
     -d '{}' \
     "$KB_GATEWAY_URL/api/v1/projects/$PROJECT_ID/workflow/workflows/hello/runs")
   HTTP_RUN_ID=$(printf '%s' "$HTTP_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["runId"])')
   ```

   Assert the response has a run ID, poll `HTTP_RUN_ID` to `success`, and
   assert its logs contain `hello-workflow`.

## Secured mode

Install a fresh platform with secured access and a seeded administrator, then
register the project and create/lint the workflow as above. Authenticate the
CLI using the seeded administrator:

```bash
kb auth login --gateway-url "$KB_GATEWAY_URL" \
  --email "$KB_JOURNEY_ADMIN_EMAIL" \
  --password "$KB_JOURNEY_ADMIN_PASSWORD"
```

Assert an anonymous request to the HTTP run route returns `401`. Read the
access token from the isolated journey HOME's `~/.kb/session.json` without
printing it, repeat the HTTP run with `Authorization: Bearer $TOKEN`, and
assert it succeeds. Run and poll through the CLI as in local mode; history and
logs must contain the run and the `hello-workflow` marker.

The runner must create a new HOME for each mode and remove it after the
journey, as it does for the install journeys.
