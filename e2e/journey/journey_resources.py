"""Resolve repository-layout resources shared by tests and the runner image."""
from __future__ import annotations

from pathlib import Path


def resource_root() -> Path:
    """Return the repository root mirrored by the runner image."""
    return Path(__file__).resolve().parents[2]


def workspace_resource(*parts: str) -> Path:
    """Return a resource using its path relative to the repository root."""
    return resource_root().joinpath(*parts)


def installer_case_fixture_root() -> Path:
    return workspace_resource("e2e", "platform", "fixtures", "installer-cases")


def case_catalog_root() -> Path:
    return workspace_resource("docs", "qa", "scenarios")


def installed_platform_resource(*parts: str) -> Path:
    """Return a contract shipped separately with the installed platform."""
    return Path("/opt/kb-contracts").joinpath(*parts)


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
