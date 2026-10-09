---
id: PC-001
title: Clean V2 install from released artifacts
persona: solo-developer
area: platform-core
priority: P0
status: active
release: true
modes: [local, secured]
surfaces: [cli]
---

## Goal

Verify the published launcher can turn the released, sealed V2 index into a
verified platform installation. This is not a source-tree or cache-assisted
test.

## Environment

- [ ] Released `kb-create` binary in `PATH`
- [ ] Node and pnpm supported by the release
- [ ] Empty `PLATFORM_ROOT` and no pre-existing `node_modules` below it
- [ ] Downloaded `release-index.json` from the candidate or stable release
- [ ] `kb-dev` supplied by the release (or an explicit test double only in CI)

## Steps

| # | Action | Expected |
|---|---|---|
| PC-001.1 | Run `kb-create --version` | Prints `kb-create <version>` and exits 0. |
| PC-001.2 | Run `kb-create install --index release-index.json --platform-root "$PLATFORM_ROOT"` in a terminal and complete the displayed prompts | The terminal prints `Set up KB Labs`, a `Plan: platform <version>, <count> artifacts` summary, `Applying installation…`, then a success/next-step message. The operation applies the selected plan; it does not write a request file. |
| PC-001.3 | Run `kb-create plan --index release-index.json --platform-root "$PLATFORM_ROOT" --service-profile default --scenario commit --json` | Exits 0; JSON has `ok: true`, `plan.request.platformRoot` equal to the isolated root, and the resolved `plan.artifacts` and `plan.serviceGraph`; no package, generated runtime config, or receipt is written. |
| PC-001.4 | Run `kb-create apply --index release-index.json --platform-root "$PLATFORM_ROOT" --service-profile default --scenario commit --json` | On success, exits 0; JSON has `ok: true`, `operation: "apply"`, non-empty `receipt` and `logPath`, and `shim: { linked: true, owner: "$PLATFORM_ROOT" }` when it takes or already owns the shim. If the launcher cannot create the `kb` shim, exits non-zero and reports `KB_INSTALL_APPLY_FAILED` with the shim path and filesystem reason; the platform files and receipt remain applied, and the hint gives `kb-create doctor --fix --platform-root "$PLATFORM_ROOT"` as the repair command. |
| PC-001.5 | Inspect `$PLATFORM_ROOT/.kb/kb.config.jsonc` and `.kb/v2/receipt.json` | The runtime config is valid JSONC, and the receipt's service graph contains the service declarations selected by the request's service profile. The install does not require a separate service-manager file. |
| PC-001.6 | Run `kb-create doctor --platform-root "$PLATFORM_ROOT" --json` after host start | Exits 0; JSON has `ok: true`, `installed: true`, `running: true`, `reachable: true`, and `platform.ok: true`. |
| PC-001.7 | Run `kb-create status --platform-root "$PLATFORM_ROOT" --json` | Exits 0; JSON has `ok: true`, `installed: true`, `running: true`, `reachable: true`, `host.state: "running"`, and `installation.receiptId` equal to the ID in `.kb/v2/receipt.json`. |
| PC-001.8 | With the installed host running, repeat `kb-create plan --index release-index.json --platform-root "$PLATFORM_ROOT" --service-profile default --scenario commit --json` | The returned plan has the same `planHash`, `artifacts`, and `serviceGraph` as the first plan; planning does not add, change, or remove installation-owned files. Runtime-state paths declared by the kb-create layout contract are outside this comparison. |
| PC-001.9 | While another process owns a port required by the selected service graph, run `kb-create apply --index release-index.json --platform-root "$PLATFORM_ROOT" --service-profile default --scenario commit --json` | Apply exits non-zero with catalogued `KB_INSTALL_PORT_IN_USE`; the error details identify the port, owning PID, and command, and the hint names the owning process and tells the operator to rerun `kb-create apply` after stopping it. |
| PC-001.10 | With root A installed at the release Gateway port 4000 and owning the `kb` shim, apply root B with no `--gateway-url`, then apply a second fresh secondary root with `--gateway-url http://127.0.0.1:4200`; inspect both receipts and run `kb --help` | B automatically receives the next free Gateway offset, port 4100, and its receipt records `http://127.0.0.1:4100/health`; the explicit secondary uses port 4200 and its receipt records `http://127.0.0.1:4200/health`. Both applies succeed, report A as shim owner, and the existing `kb` command still runs A. Human output gives each non-owner's direct command. |
| PC-001.11 | With root A owning the `kb` shim, run `kb-create uninstall --platform-root "$PLATFORM_ROOT_B" --yes --json` when B is also installed | Uninstall exits 0; JSON reports success, leaves the `kb` shim pointing to A, and does not relink it. |
| PC-001.12 | With root A running on the default Gateway port and root B installed with its own Gateway address, run `kb adapter list --json` through root B's direct `kb` command; then stop B and repeat | While B is running, the command reports B's adapter state and never A's, even when A owns the default port. With B stopped, it reports unknown host with the exact `kb-create start --platform-root "$PLATFORM_ROOT_B"` next command, or returns `KB_HOST_IDENTITY_MISMATCH` if A answers B's configured address; it never reports A's state. |
| PC-001.13 | Record the `installationId`, `verification.hostHealthUrl`, service graph ports, and service transport URL ports from `$PLATFORM_ROOT/.kb/v2/receipt.json`; run `kb-create update --platform-root "$PLATFORM_ROOT"`, then run `kb adapter list --json` through the installed `kb` command | Update exits 0 and restarts the host. The receipt retains the same non-empty `installationId`, Gateway URL, nonzero service ports, and service transport URL ports, and the CLI command succeeds against that installation. |
| PC-001.14 | Run `kb-create rollback --platform-root "$PLATFORM_ROOT" --snapshot "$SNAPSHOT_ID" --yes`, then run `kb adapter list --json` through the installed `kb` command | Rollback exits 0 and restarts the host. The restored receipt retains the same `installationId`, Gateway URL, nonzero service ports, and service transport URL ports as before update, and the CLI command succeeds against that installation. |
| PC-001.15 | Run `kb-create restart --platform-root "$PLATFORM_ROOT"`, then run `kb adapter list --json` through the installed `kb` command | Restart exits 0. The receipt retains the same non-empty `installationId`, Gateway URL, nonzero service ports, and service transport URL ports, and the CLI command succeeds against that installation. |
| PC-001.16 | With root A already installed at the release Gateway port, apply a fresh root B without `--gateway-url`, stop B, apply root C at `http://127.0.0.1:4200`, then plan a fresh root D and re-plan B | Every nonzero service port in B's service graph and service transport URLs uses B's single allocated 100-port offset. Stopping B does not release its allocation: C takes the explicit 4200 address, D receives the next 100-port offset, and planning B again retains B's Gateway and service ports. |

## Pass criteria

No artifact is selected from ambient `node_modules`; configuration and service
metadata come from the published manifests. Any failure has a stable error
code, a hint, a transcript under `.kb/logs/`, and a redacted diagnostic dossier
under `.kb/diagnostics/`.
