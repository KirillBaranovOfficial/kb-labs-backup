"""Resolve repository-layout resources shared by tests and the runner image."""
from __future__ import annotations

import json
import shlex
from pathlib import Path


def resource_root() -> Path:
    """Return the repository root mirrored by the runner image."""
    return Path(__file__).resolve().parents[2]


def workspace_resource(*parts: str) -> Path:
    """Return a resource using its path relative to the repository root."""
    return resource_root().joinpath(*parts)


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


def current_link_path(install_root: Path) -> Path:
    current = _platform_layout().get("currentLink")
    if not isinstance(current, str) or not current:
        raise ValueError("platform layout contract omits currentLink")
    return Path(install_root) / current


def version_roots_path(install_root: Path) -> Path:
    versions = _platform_layout().get("versionRoots", {}).get("path")
    if not isinstance(versions, str) or not versions:
        raise ValueError("platform layout contract omits versionRoots.path")
    return Path(install_root) / versions


def active_platform_root(install_root: Path) -> Path:
    """Resolve the version selected by the install container's stable link."""
    install_root = Path(install_root)
    current = current_link_path(install_root)
    if not current.is_symlink():
        if not install_root.exists() or (install_root.is_dir() and not any(install_root.iterdir())):
            raise FileNotFoundError(f"no installed platform at {install_root}")
        raise ValueError(f"unsupported platform layout at {install_root}: current is not a symlink")
    target = current.readlink()
    if target.is_absolute():
        raise ValueError(f"unsupported platform layout at {install_root}: current link is not relative")
    try:
        active = current.resolve(strict=True)
        active.relative_to(version_roots_path(install_root).resolve(strict=True))
    except (OSError, ValueError) as error:
        raise ValueError(f"unsupported platform layout at {install_root}: current does not select a complete version") from error
    if not active.is_dir():
        raise ValueError(f"unsupported platform layout at {install_root}: current does not select a directory")
    return active


def installer_record_path(install_root: Path, record: str) -> Path:
    """Resolve an installer-owned record from the shipped layout contract."""
    layout = _platform_layout()
    relative = layout.get("installerRecords", {}).get(record)
    if not isinstance(relative, str) or not relative:
        raise ValueError(f"platform layout does not declare installer record {record!r}")
    return install_root / relative


def create_fixture_install_container(install_root: Path, version_key: str = "fixture") -> Path:
    """Create an empty container for deterministic journey fixture contents."""
    contract = _platform_layout()
    versions = contract.get("versionRoots", {}).get("path")
    current = contract.get("currentLink")
    if not isinstance(versions, str) or not versions or not isinstance(current, str) or not current:
        raise ValueError("platform layout contract omits its container paths")
    install_root = Path(install_root)
    active = install_root / versions / version_key
    active.mkdir(parents=True, exist_ok=True)
    install_root.joinpath(current).symlink_to(Path(versions) / version_key)
    return active


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
