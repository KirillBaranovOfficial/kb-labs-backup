"""Resolve repository-layout resources shared by tests and the runner image."""
from __future__ import annotations

import hashlib
import json
import shlex
import shutil
import sys
from pathlib import Path, PurePosixPath
from typing import NamedTuple


class RunnerImageResource(NamedTuple):
    """One repository input copied into the journey runner image."""

    source: str
    destination: str
    stage_source: str | None = None


RUNNER_IMAGE_RESOURCES = (
    RunnerImageResource("tools/kb-create/install.sh", "/usr/local/share/kb-create/install.sh"),
    RunnerImageResource("e2e/journey", "/opt/kb-journey/e2e/journey"),
    RunnerImageResource(
        ".journey/runner-inputs/project-service-path.js",
        "/opt/kb-journey/core/plugin-contracts/dist/project-service-path.js",
        "core/plugin-contracts/dist/project-service-path.js",
    ),
    RunnerImageResource("docs/qa/scenarios", "/opt/kb-journey/docs/qa/scenarios"),
    RunnerImageResource("e2e/platform/fixtures/installer-cases",
                        "/opt/kb-journey/e2e/platform/fixtures/installer-cases"),
    RunnerImageResource("e2e/platform/journey-update-packages.mjs",
                        "/opt/kb-journey/e2e/platform/journey-update-packages.mjs"),
    RunnerImageResource("e2e/platform/package-provenance.mjs",
                        "/opt/kb-journey/e2e/platform/package-provenance.mjs"),
    RunnerImageResource("e2e/platform/registry-manifest.json",
                        "/opt/kb-journey/e2e/platform/registry-manifest.json"),
    RunnerImageResource("e2e/publisher/publish.sh",
                        "/opt/kb-journey/e2e/publisher/publish.sh"),
    RunnerImageResource("contracts/ux-result", "/opt/kb-journey/contracts/ux-result"),
    RunnerImageResource("core/platform/src/error-envelope/error-envelope.schema.json",
                        "/opt/kb-journey/core/platform/src/error-envelope/error-envelope.schema.json"),
    RunnerImageResource("core/platform/src/error-envelope/errors.catalog.json",
                        "/opt/kb-journey/core/platform/src/error-envelope/errors.catalog.json"),
    RunnerImageResource("tools/kb-create/v2/contracts/platform-layout.json",
                        "/opt/kb-journey/tools/kb-create/v2/contracts/platform-layout.json"),
    RunnerImageResource("contracts/ux-result", "/opt/kb-contracts/ux-result"),
    RunnerImageResource("core/platform/src/error-envelope/error-envelope.schema.json",
                        "/opt/kb-contracts/error-envelope.schema.json"),
    RunnerImageResource("e2e/journey/entrypoint.py", "/usr/local/bin/kb-journey-runner"),
    RunnerImageResource("e2e/journey/studio-playwright.config.mjs",
                        "/opt/kb-studio-playwright/playwright.config.mjs"),
    RunnerImageResource(".journey/runner-inputs/studio-specs", "/opt/kb-studio-playwright/specs"),
    RunnerImageResource("tools/clikit/diag/gen/main.go", "/opt/kb-journey/tools/clikit/diag/gen/main.go"),
    RunnerImageResource("tools/kb-create/main.go", "/opt/kb-journey/tools/kb-create/main.go"),
    RunnerImageResource("tools/kb-create/v2/testsupport/wizard-pty/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/testsupport/wizard-pty/main.go"),
    RunnerImageResource("tools/kb-create/v2/testsupport/identity-supervisor/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/testsupport/identity-supervisor/main.go"),
    RunnerImageResource("tools/kb-create/v2/internal/fakehost/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/internal/fakehost/main.go"),
    RunnerImageResource("tools/kb-create/v2/cmd/gen-gateway-bootstrap/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/cmd/gen-gateway-bootstrap/main.go"),
    RunnerImageResource("tools/kb-create/v2/cmd/kb-create-release-index/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/cmd/kb-create-release-index/main.go"),
    RunnerImageResource("tools/kb-create/v2/cmd/kb-create-v2/main.go",
                        "/opt/kb-journey/tools/kb-create/v2/cmd/kb-create-v2/main.go"),
    RunnerImageResource("tools/kb-deploy/main.go", "/opt/kb-journey/tools/kb-deploy/main.go"),
    RunnerImageResource("tools/kb-devkit/main.go", "/opt/kb-journey/tools/kb-devkit/main.go"),
    RunnerImageResource("tools/kb-env/main.go", "/opt/kb-journey/tools/kb-env/main.go"),
    RunnerImageResource("tools/kb-dev/main.go", "/opt/kb-journey/tools/kb-dev/main.go"),
    RunnerImageResource("tools/kb-monitor/main.go", "/opt/kb-journey/tools/kb-monitor/main.go"),
)


def resource_root() -> Path:
    """Return the repository root mirrored by the runner image."""
    return Path(__file__).resolve().parents[2]


def workspace_resource(*parts: str) -> Path:
    """Return a resource using its path relative to the repository root."""
    return resource_root().joinpath(*parts)


def stage_runner_image_inputs() -> tuple[Path, ...]:
    """Stage declared runner inputs whose repository source is generated or ignored."""
    staged_paths = list(stage_studio_playwright_specs(
        workspace_resource(".journey", "runner-inputs", "studio-specs")))
    for resource in RUNNER_IMAGE_RESOURCES:
        if resource.stage_source is None:
            continue
        built_input = workspace_resource(*PurePosixPath(resource.stage_source).parts)
        if not built_input.is_file():
            raise FileNotFoundError(
                f"The journey runner needs {built_input.relative_to(resource_root())}. "
                "Run `corepack pnpm --filter @kb-labs/plugin-contracts... --workspace-concurrency=2 build` first."
            )
        staged_input = workspace_resource(*PurePosixPath(resource.source).parts)
        staged_input.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(built_input, staged_input)
        staged_paths.append(staged_input)
    if not staged_paths:
        raise RuntimeError("the journey runner resource set declares no staged runner inputs")
    return tuple(staged_paths)


def stage_studio_playwright_specs(output_dir: Path | None = None) -> tuple[Path, ...]:
    """Bundle the specs selected by Playwright's own config with workspace imports."""
    root = resource_root()
    config_path = workspace_resource("e2e", "studio", "playwright.config.ts")
    playwright = workspace_resource("e2e", "studio", "node_modules", ".bin", "playwright")
    if not playwright.is_file():
        raise FileNotFoundError(
            f"Studio Playwright CLI is missing at {playwright}. "
            "Restore workspace dependencies with `corepack pnpm install --frozen-lockfile`."
        )
    from invocations import run_harness_command

    listed = run_harness_command([
        str(playwright), "test", "--list", f"--config={config_path}", "--reporter=json",
    ])
    if listed.returncode:
        detail = listed.stderr.strip() or listed.stdout.strip() or f"exit {listed.returncode}"
        raise RuntimeError(f"Studio Playwright spec discovery failed: {detail}")
    inventory = json.loads(listed.stdout)
    config = inventory.get("config")
    if not isinstance(config, dict) or not isinstance(config.get("rootDir"), str):
        raise ValueError("Playwright spec discovery output omitted config.rootDir")
    source_dir = Path(config["rootDir"])
    entries: set[Path] = set()

    def visit_suites(suites: object) -> None:
        if not isinstance(suites, list):
            return
        for suite in suites:
            if not isinstance(suite, dict):
                raise ValueError("Playwright spec discovery returned an invalid suite")
            file = suite.get("file")
            if isinstance(file, str) and file:
                path = Path(file)
                entries.add(path if path.is_absolute() else source_dir / path)
            visit_suites(suite.get("suites"))

    visit_suites(inventory.get("suites"))
    if not entries:
        raise FileNotFoundError(f"Playwright config {config_path} selected no Studio spec files")
    for entry in entries:
        if not entry.is_file():
            raise FileNotFoundError(f"Playwright selected missing Studio spec {entry}")

    esbuild_candidates = sorted((root / "node_modules/.pnpm").glob(
        "esbuild@*/node_modules/esbuild/bin/esbuild"))
    if not esbuild_candidates:
        raise FileNotFoundError(
            "Studio Playwright specs cannot be bundled because workspace esbuild is missing. "
            "Build the workspace tooling before preparing the journey runner image."
        )

    destination = output_dir or workspace_resource(".journey", "runner-inputs", "studio-specs")
    shutil.rmtree(destination, ignore_errors=True)
    destination.mkdir(parents=True, exist_ok=True)
    command = [
        str(esbuild_candidates[-1]),
        *(str(entry) for entry in sorted(entries)),
        "--bundle",
        "--platform=node",
        "--format=cjs",
        "--external:@playwright/test",
        f"--outdir={destination}",
        f"--outbase={source_dir}",
    ]
    result = run_harness_command(command)
    if result.returncode:
        detail = result.stderr.strip() or result.stdout.strip() or f"exit {result.returncode}"
        raise RuntimeError(f"Studio Playwright spec dependency bundle failed: {detail}")
    return tuple(sorted(destination.rglob("*.spec.js")))


def main() -> int:
    try:
        stage_runner_image_inputs()
    except (FileNotFoundError, RuntimeError, ValueError, json.JSONDecodeError) as error:
        print(error, file=sys.stderr)
        return 2
    return 0


def workspace_tool_entrypoints() -> tuple[Path, ...]:
    """Resolve declared Go tool entrypoints from the workspace resource set."""
    return tuple(
        workspace_resource(*resource.source.split("/"))
        for resource in RUNNER_IMAGE_RESOURCES
        if resource.source.startswith("tools/") and resource.source.endswith("/main.go")
    )


def journey_module(name: str) -> Path:
    """Return a journey script by name; the image keeps the repository layout."""
    return workspace_resource("e2e", "journey", name)


def parse_shell_commands(value: str) -> list[list[str]]:
    """Parse the POSIX words and ``&&`` separators emitted by ShellQuote."""
    lexer = shlex.shlex(value, posix=True, punctuation_chars="&")
    lexer.whitespace_split = True
    commands: list[list[str]] = []
    current: list[str] = []
    for token in lexer:
        if token == "&&":
            if not current:
                raise ValueError("shell command sequence contains an empty command")
            commands.append(current)
            current = []
        elif token.startswith("&"):
            raise ValueError("shell command sequence contains an unsupported operator")
        else:
            current.append(token)
    if not current:
        raise ValueError("shell command sequence contains an empty command")
    commands.append(current)
    return commands


if __name__ == "__main__":
    raise SystemExit(main())


def installer_case_fixture_root() -> Path:
    return workspace_resource("e2e", "platform", "fixtures", "installer-cases")


def case_catalog_root() -> Path:
    return workspace_resource("docs", "qa", "scenarios")


def installed_platform_resource(*parts: str) -> Path:
    """Return a contract shipped separately with the installed platform."""
    return Path("/opt/kb-contracts").joinpath(*parts)


def _platform_layout() -> dict:
    value = json.loads(workspace_resource("tools", "kb-create", "v2", "contracts", "platform-layout.json").read_text())
    if not isinstance(value, dict):
        raise ValueError("platform layout contract is not an object")
    return value


def shared_runtime_file_fingerprints(install_root: Path) -> tuple[str | None, ...]:
    """Return opaque fingerprints for every file projection in the layout contract."""
    projections = _platform_layout().get("sharedRuntimePaths")
    if not isinstance(projections, list):
        raise ValueError("platform layout contract omits shared runtime projections")
    fingerprints = []
    for projection in projections:
        if not isinstance(projection, dict):
            raise ValueError("platform layout contains an invalid shared runtime projection")
        if projection.get("kind") != "file":
            continue
        state_path = projection.get("statePath")
        if not isinstance(state_path, str) or not state_path:
            raise ValueError("platform layout file projection omits its state path")
        try:
            contents = (Path(install_root) / state_path).read_bytes()
        except FileNotFoundError:
            fingerprints.append(None)
        else:
            fingerprints.append(hashlib.sha256(contents).hexdigest())
    return tuple(fingerprints)


def current_link_path(install_root: Path) -> Path:
    """Resolve the current link by its platform-layout identity."""
    from installed_fixture import installed_path
    return installed_path(install_root, "currentLink")


def version_roots_path(install_root: Path) -> Path:
    """Resolve the versions directory by its platform-layout identity."""
    from installed_fixture import installed_path
    return installed_path(install_root, "versionRoots.path")


def active_platform_root(install_root: Path) -> Path:
    """Resolve the active version through the installed-fixture path owner."""
    from installed_fixture import active_platform_root as resolve_active
    return resolve_active(install_root)


def installer_record_path(install_root: Path, record: str) -> Path:
    """Resolve an installer-owned record through the installed-root owner."""
    from installed_fixture import installed_path
    return installed_path(install_root, f"installerRecords.{record}")


def snapshot_tree(root: Path) -> dict[str, tuple[str, bytes | str | None]]:
    """Capture file content and links below root."""
    snapshot = {}
    for path in root.rglob("*"):
        relative = str(path.relative_to(root))
        if path.is_symlink():
            snapshot[relative] = ("symlink", str(path.readlink()))
        elif path.is_dir():
            snapshot[relative] = ("directory", None)
        elif path.is_file():
            snapshot[relative] = ("file", path.read_bytes())
    return snapshot


def snapshot_state_tree(root: Path) -> dict[str, tuple[str, int, int, bytes | str | None]]:
    """Capture shared state bytes and filesystem metadata below root."""
    snapshot = {}
    root_info = root.lstat()
    snapshot["."] = ("directory", root_info.st_mode, root_info.st_mtime_ns, None)
    for path in root.rglob("*"):
        relative = str(path.relative_to(root))
        info = path.lstat()
        if path.is_symlink():
            snapshot[relative] = ("symlink", info.st_mode, info.st_mtime_ns, str(path.readlink()))
        elif path.is_dir():
            snapshot[relative] = ("directory", info.st_mode, info.st_mtime_ns, None)
        elif path.is_file():
            snapshot[relative] = ("file", info.st_mode, info.st_mtime_ns, path.read_bytes())
    return snapshot


def read_installed_platform_resource(*parts: str) -> bytes:
    """Read an installed-platform contract and fail if that installed copy is missing."""
    return installed_platform_resource(*parts).read_bytes()


def installer_scenario_resources(host_os: str, host_arch: str) -> list[Path]:
    """List all repository and installed contract resources used by installer cases."""
    catalog = case_catalog_root()
    fixture_root = installer_case_fixture_root()
    return [
        catalog,
        *sorted(catalog.glob("*.md")),
        workspace_resource("contracts", "ux-result", "machine-result.schema.json"),
        workspace_resource("core", "platform", "src", "error-envelope", "error-envelope.schema.json"),
        workspace_resource("tools", "kb-create", "v2", "contracts", "platform-layout.json"),
        fixture_root / f"compatibility-matrix-{host_os}-{host_arch}.json",
        fixture_root / f"missing-host-binary-{host_os}-{host_arch}.json",
        installed_platform_resource("ux-result", "machine-result.schema.json"),
        installed_platform_resource("error-envelope.schema.json"),
    ]
