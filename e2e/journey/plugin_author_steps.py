#!/usr/bin/env python3
"""Installed-project plugin scaffold, link, edit, and unlink journey."""
from __future__ import annotations

import json
import os
import shutil
import subprocess
from pathlib import Path
from typing import Callable

from service_steps import Journey


PLUGIN_NAME = "demo"
PLUGIN_DIR = Path(".kb/plugins/demo")
ENTRY_DIR = PLUGIN_DIR / "packages/demo-entry"
CORE_SOURCE = PLUGIN_DIR / "packages/demo-core/src/hello.ts"
ORIGINAL_GREETING = "Hello, ${who} from demo!"
EDITED_GREETING = "Edited demo greeting for ${who}!"


def _run(command: list[str], *, cwd: Path, env: dict[str, str],
         command_runner: Callable[..., subprocess.CompletedProcess[str]]) -> str:
    result = command_runner(command, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            check=False)
    if result.returncode != 0:
        detail = " ".join((result.stdout or "").split())
        raise RuntimeError(f"{' '.join(command)} exited {result.returncode}; {detail or 'no output'}")
    return result.stdout or ""


def _plugins(kb: Path, *, cwd: Path, env: dict[str, str], command_runner) -> list[dict]:
    output = _run([str(kb), "marketplace", "plugins", "list", "--json"],
                  cwd=cwd, env=env, command_runner=command_runner)
    try:
        data = json.loads(output)
    except json.JSONDecodeError as error:
        raise RuntimeError("kb marketplace plugins list --json did not return JSON") from error
    entries = data.get("entries") if isinstance(data, dict) else None
    if not isinstance(entries, list):
        raise RuntimeError("kb marketplace plugins list --json returned no entries array")
    return entries


def _lock_entry(project: Path) -> tuple[dict, dict]:
    lock_path = project / ".kb/marketplace.lock"
    if not lock_path.is_file():
        raise RuntimeError("project .kb/marketplace.lock was not created by marketplace link")
    try:
        lock = json.loads(lock_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise RuntimeError("project .kb/marketplace.lock is not valid JSON") from error
    plugins = lock.get("plugins") if isinstance(lock, dict) else None
    entry = plugins.get(PLUGIN_NAME) if isinstance(plugins, dict) else None
    if not isinstance(entry, dict):
        raise RuntimeError("project .kb/marketplace.lock does not contain demo")
    return lock, entry


def run_plugin_author_flow(
    journey: Journey,
    project_root: Path,
    kb_path: Path,
    registry: str,
    *,
    command_runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
    base_env: dict[str, str] | None = None,
) -> None:
    """Run S-003 in an installed project and record each catalog step."""
    project = project_root.resolve()
    plugin_root = project / PLUGIN_DIR
    entry = project / ENTRY_DIR
    core_source = project / CORE_SOURCE
    child_env = (base_env or os.environ).copy()
    child_env["NPM_CONFIG_REGISTRY"] = registry
    linked = False

    def record(name: str, *, cases: list[str], check: Callable[[], None]) -> bool:
        return journey.step(name, "plugin-author", lambda: check() or None,
                            cases=cases).status == "PASS"

    def _scaffold() -> None:
        _run([str(kb_path), "scaffold", "run", "plugin", PLUGIN_NAME,
              "--mode", "standalone", "--yes"],
             cwd=project, env=child_env, command_runner=command_runner)
        if not entry.is_dir() or not (entry / "package.json").is_file() or not core_source.is_file():
            raise RuntimeError("kb scaffold run did not create .kb/plugins/demo with entry and core packages")

    def _build() -> None:
        _run(["pnpm", "install"], cwd=plugin_root, env=child_env, command_runner=command_runner)
        _run(["pnpm", "-w", "build"], cwd=plugin_root, env=child_env, command_runner=command_runner)
        if not (entry / "dist/manifest.js").is_file():
            raise RuntimeError("pnpm -w build did not emit the plugin entry manifest")

    def _link() -> None:
        nonlocal linked
        _run([str(kb_path), "marketplace", "link", "--link", ".kb/plugins/demo/packages/demo-entry"],
             cwd=project, env=child_env, command_runner=command_runner)
        linked = True
        listed = _plugins(kb_path, cwd=project, env=child_env, command_runner=command_runner)
        if not any(item.get("id") == PLUGIN_NAME and item.get("source") == "local"
                   and item.get("scope") == "project" for item in listed if isinstance(item, dict)):
            raise RuntimeError("demo is not listed as a local project plugin after marketplace link")
        _, lock_entry = _lock_entry(project)
        if lock_entry.get("source") != "local" or lock_entry.get("resolvedPath") != str(ENTRY_DIR):
            raise RuntimeError("project marketplace.lock does not record demo's local relative link path")

    def _invoke_original() -> None:
        help_output = _run([str(kb_path), "--help"], cwd=project, env=child_env,
                           command_runner=command_runner)
        if "demo" not in help_output:
            raise RuntimeError("kb --help did not list the demo command group")
        output = _run([str(kb_path), "demo", "hello"], cwd=project, env=child_env,
                      command_runner=command_runner)
        if "Hello, World from demo!" not in output:
            raise RuntimeError("kb demo hello did not print its documented greeting")

    def _edit_build_invoke() -> None:
        source = core_source.read_text(encoding="utf-8")
        if ORIGINAL_GREETING not in source:
            raise RuntimeError("generated demo greeting source no longer matches the scaffold template")
        core_source.write_text(source.replace(ORIGINAL_GREETING, EDITED_GREETING, 1), encoding="utf-8")
        _run(["pnpm", "-w", "build"], cwd=plugin_root, env=child_env, command_runner=command_runner)
        output = _run([str(kb_path), "demo", "hello"], cwd=project, env=child_env,
                      command_runner=command_runner)
        if "Edited demo greeting for World!" not in output:
            raise RuntimeError("kb demo hello did not show the greeting after rebuilding the edited source")

    def _unlink() -> None:
        _run([str(kb_path), "marketplace", "plugins", "unlink", PLUGIN_NAME],
             cwd=project, env=child_env, command_runner=command_runner)
        if any(item.get("id") == PLUGIN_NAME for item in _plugins(
                kb_path, cwd=project, env=child_env, command_runner=command_runner)
               if isinstance(item, dict)):
            raise RuntimeError("demo remains in the project plugin list after unlink")
        lock_path = project / ".kb/marketplace.lock"
        if lock_path.exists():
            try:
                plugins = json.loads(lock_path.read_text(encoding="utf-8")).get("plugins", {})
            except json.JSONDecodeError as error:
                raise RuntimeError("project .kb/marketplace.lock became invalid after unlink") from error
            if PLUGIN_NAME in plugins:
                raise RuntimeError("demo remains in project .kb/marketplace.lock after unlink")

    def _record_unlink() -> bool:
        return record("unlink plugin", cases=["S-003.6"], check=_unlink)

    scaffolded = record("scaffold plugin", cases=["S-003.1"], check=_scaffold)
    if not scaffolded:
        for name in ("build generated plugin", "link plugin to project", "invoke generated plugin",
                     "rebuild edited plugin", "unlink plugin"):
            journey.blocked(name, "plugin-author", "blocked because plugin scaffolding failed")
        shutil.rmtree(plugin_root, ignore_errors=True)
        return

    built = record("build generated plugin", cases=["S-003.2"], check=_build)
    if not built:
        for name in ("link plugin to project", "invoke generated plugin",
                     "rebuild edited plugin", "unlink plugin"):
            journey.blocked(name, "plugin-author", "blocked because the generated plugin did not build")
        shutil.rmtree(plugin_root, ignore_errors=True)
        return

    link_passed = record("link plugin to project", cases=["S-003.3"], check=_link)
    if not link_passed:
        for name in ("invoke generated plugin", "rebuild edited plugin"):
            journey.blocked(name, "plugin-author", "blocked because marketplace link verification failed")
        if linked:
            _record_unlink()
        else:
            journey.blocked("unlink plugin", "plugin-author", "plugin was not linked")
        shutil.rmtree(plugin_root, ignore_errors=True)
        return

    try:
        record("invoke generated plugin", cases=["S-003.4"], check=_invoke_original)
        record("rebuild edited plugin", cases=["S-003.5"], check=_edit_build_invoke)
    finally:
        if linked:
            _record_unlink()
        shutil.rmtree(plugin_root, ignore_errors=True)
