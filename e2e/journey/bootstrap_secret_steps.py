"""Journey assertions for one-time bootstrap credential storage."""
from __future__ import annotations

import os
from pathlib import Path

from service_steps import Journey


def bootstrap_password_file(install_root: Path,
                            outside_roots: tuple[tuple[str, Path], ...],
                            password: str) -> str | None:
    """Return a report-safe path when a disclosed password remains on disk."""
    secret = password.encode("utf-8")
    roots = (("install", install_root), *outside_roots)
    for label, root in roots:
        if not root.exists():
            continue
        paths = (root,) if root.is_file() or root.is_symlink() else sorted(root.rglob("*"))
        for path in paths:
            if not path.is_file():
                continue
            try:
                if secret in path.read_bytes():
                    relative = path.name if path == root else path.relative_to(root).as_posix()
                    return f"{label}/{relative}"
            except OSError:
                relative = path.name if path == root else path.relative_to(root).as_posix()
                return f"{label}/{relative} (unreadable)"
    return None


def password_absence_failure(install_root: Path,
                             outside_roots: tuple[tuple[str, Path], ...],
                             password: str | None) -> str | None:
    if not password:
        return "BLOCKED: bootstrap password was unavailable for the install-file scan"
    location = bootstrap_password_file(install_root, outside_roots, password)
    if location is not None:
        return f"bootstrap credential remains at {location}"
    return None


def register_install_password_absence_step(
        journey: Journey, install_root: Path,
        outside_roots: tuple[tuple[str, Path], ...], password: str) -> None:
    def check() -> str | None:
        return password_absence_failure(install_root, outside_roots, password)

    journey.step("verify disclosed bootstrap credential absent after install", "platform.install",
                 check, cases=["S-100.1"], runners=("service-secured:explore",))


def register_supplied_password_absence_step(
        journey: Journey, scan_root: Path, password: str | None) -> None:
    if journey.runner_selector not in {"agent-secured:commit", "agent:commit"}:
        return

    def check() -> str | None:
        return password_absence_failure(scan_root, (), password)

    journey.step("verify supplied bootstrap credential absent from install files", "platform.install",
                 check, cases=["S-181.3"],
                 runners=("agent-secured:commit", "agent:commit"))
