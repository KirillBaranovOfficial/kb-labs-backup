from pathlib import Path
from journey_resources import current_link_path, version_roots_path


def create_test_install_container(install_root: Path) -> Path:
    """Create a minimal valid container for journey unit fixtures."""
    install_root = Path(install_root)
    current = current_link_path(install_root)
    if current.is_symlink():
        return current.resolve()
    active = version_roots_path(install_root) / "fixture"
    active.mkdir(parents=True, exist_ok=True)
    current.symlink_to(Path("versions") / "fixture")
    return active


def move_test_platform_into_container(install_root: Path) -> Path:
    """Wrap an existing platform-root fixture in the install-container layout."""
    install_root = Path(install_root)
    active = create_test_install_container(install_root)
    owned_layout_entries = {version_roots_path(install_root).name, current_link_path(install_root).name}
    for entry in list(install_root.iterdir()):
        if entry.name in owned_layout_entries:
            continue
        entry.rename(active / entry.name)
    return active
