# Clean-machine CI checks

The Go suite command is the same entry point used by the `go-module-suite` kb-devkit check and local development. By default it discovers every module in the devkit `go-binary` registry and runs all non-e2e packages twice, with a fresh `HOME` and `TMPDIR` for each module and variant. Variant one is a direct short child of `/tmp`; variant two is one short child of the platform default temp directory. Before invoking Go, the suite probes the host's Unix socket pathname capacity and checks that each root can fit the TSX IPC socket path. An insufficient root fails with the named `GO_SUITE_TMPDIR_SOCKET_PATH_TOO_LONG` diagnostic. The suite clears `KB_PLATFORM_ROOT`, `KB_HOME`, `KB_NET_OFFSET`, and `GOFLAGS` for each run. It reuses the Go module and build caches outside the fresh `HOME` so clean-environment runs do not needlessly redownload dependencies or recompile every module.

Run the full registered suite from the repository root:

```sh
node scripts/ci/go-module-suite.mjs
```

For a focused local run, select registered modules by their workspace paths:

```sh
node scripts/ci/go-module-suite.mjs --module tools/clikit --module tools/kb-devkit
```

Run the clean-machine check to verify the runner and workflow contracts, and run `kb-devkit check` to include the registered Go modules:

```sh
kb-devkit check --only ci-clean-machine-lanes,go-module-suite
```

The reusable installer workflow builds one candidate from the triggering revision. Container journeys run on Linux; a separate native `macos-latest` job installs that candidate and checks the installed CLI. The nightly workflow updates the status issue selected by `NIGHTLY_STATUS_ISSUE`, or finds/creates the single issue labeled `nightly-status` when the value is `0`.
