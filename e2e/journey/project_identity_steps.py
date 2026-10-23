from __future__ import annotations

from kb_invocation import kb_command
from installed_fixture import configure_hermetic_cli_fixture, layout_relative, layout_value

from project_service_path import project_service_path

import json
import os
import tempfile
from pathlib import Path

from service_steps import Journey
from invocations import (
    CompletedProcess,
    decode_ux_error_result,
    decode_ux_success_result,
    invocation_context,
    read_ux_cli_result,
    run_command,
    run_with_runner,
)
from marketplace_steps import _output, _registry_version


def _cli_success(result: CompletedProcess[str]):
    return decode_ux_success_result(read_ux_cli_result(result))


def _cli_error(result: CompletedProcess[str]):
    return decode_ux_error_result(read_ux_cli_result(result))


def run_project_registry_steps(journey: Journey, cli: str, env: dict[str, str], registry: str,
                               runner=run_command) -> None:
    """Register two disposable repositories and prove their CLI scope boundaries."""
    command_runner = runner

    def run(cwd: Path, *args: str, step_id: str) -> CompletedProcess[str]:
        with invocation_context(step_id=step_id):
            return run_with_runner(command_runner, kb_command([ *args], executable=cli), cwd=cwd, env=env, timeout=180)

    with tempfile.TemporaryDirectory(prefix="kb-project-registry-", dir=env.get("HOME")) as directory:
        fixture_root = Path(directory)
        projects = (fixture_root / "project-a", fixture_root / "project-b")
        for project in projects:
            project.mkdir()
            initialized = run_command(["git", "init", "--quiet", str(project)], cwd=fixture_root,
                                      env=env, timeout=30)
            if initialized.returncode != 0:
                journey.step("prepare project registry fixtures", "projects",
                             lambda: f"could not initialize {project.name}: {initialized.stderr.strip()}")
                return
        ids: dict[Path, str] = {}

        def registration() -> str | None:
            for project in projects:
                if (project / ".kb").exists():
                    return f"{project.name} fixture unexpectedly contains .kb before registration"
            step_ids = ("S-043.1", "S-196.1")
            if len(projects) != len(step_ids):
                raise RuntimeError("project registry fixture step mapping is incomplete")
            for project, step_id in zip(projects, step_ids):
                result = run(project, "project", "add", str(project), "--json", step_id=step_id)
                if result.returncode != 0:
                    return f"kb project add failed for {project.name}: {(result.stderr or result.stdout).strip()}"
                try:
                    payload = _cli_success(result)
                except ValueError as error:
                    return f"kb project add returned an invalid UX result for {project.name}: {error}"
                row = payload.get("project", {}) if isinstance(payload, dict) else {}
                identifier = row.get("id") if isinstance(row, dict) else None
                path = row.get("path") if isinstance(row, dict) else None
                if not isinstance(identifier, str) or not identifier or path != str(project.resolve()):
                    return f"kb project add omitted the canonical ID or path for {project.name}"
                ids[project] = identifier
            if ids[projects[0]] == ids[projects[1]] or (projects[1] / ".kb").exists():
                return "project registrations were not distinct or project B was modified"
            return None

        journey.step("register isolated project A and B", "projects", registration,
                     cases=["S-043.1", "S-196.1"])

        def list_projects() -> str | None:
            result = run(fixture_root, "project", "list", "--json", step_id="S-043.2")
            if result.returncode != 0:
                return f"kb project list failed: {(result.stderr or result.stdout).strip()}"
            try:
                payload = _cli_success(result)
            except ValueError as error:
                return f"kb project list returned an invalid UX result: {error}"
            rows = payload.get("projects", []) if isinstance(payload, dict) else []
            found = {(row.get("id"), row.get("path")) for row in rows if isinstance(row, dict)}
            expected = {(ids.get(project), str(project.resolve())) for project in projects}
            if not expected <= found:
                return "kb project list omitted one or both registered roots"
            return None

        journey.step("list both registered projects", "projects", list_projects, cases=["S-043.2"],
                     requires=("step:register isolated project A and B",))

        def install_scoped_plugins() -> str | None:
            for project, package in ((projects[0], "@kb-labs/qa"),
                                     (projects[1], "@kb-labs/commit")):
                version = _registry_version(package, registry, project, env, command_runner)
                args = ["marketplace", "install", f"{package}@{version}", "--json"]
                result = run(project, *args, step_id="S-043.3")
                if result.returncode != 0:
                    return f"Marketplace install of {package} failed: {(result.stderr or result.stdout).strip()}"
            locks = []
            for project in projects:
                lock_path = project / ".kb" / "marketplace.lock"
                try:
                    locks.append(json.loads(lock_path.read_text()).get("plugins", {}))
                except (OSError, json.JSONDecodeError):
                    return f"{project.name} marketplace lock was missing or invalid after installation"
            if "@kb-labs/qa" not in locks[0] or "@kb-labs/commit" in locks[0]:
                return "Project A's marketplace lock did not contain only its QA install"
            if "@kb-labs/commit" not in locks[1] or "@kb-labs/qa" in locks[1]:
                return "Project B's marketplace lock did not contain only its Commit install"
            return None

        journey.step("install distinct plugins into project A and B", "marketplace", install_scoped_plugins,
                     cases=["S-043.3"], requires=("step:register isolated project A and B",))

        def mind_plugin_scope() -> str | None:
            package = "@kb-labs/mind"
            version = _registry_version(package, registry, projects[0], env, command_runner)
            result = run(projects[0], "marketplace", "install", f"{package}@{version}", "--json",
                         step_id="S-199.1")
            if result.returncode != 0:
                return f"Mind Marketplace install failed: {(result.stderr or result.stdout).strip()}"
            locks = []
            for project in projects:
                try:
                    locks.append(json.loads((project / layout_value('versionStatePaths.kb_marketplace_lock')).read_text()).get("plugins", {}))
                except (OSError, json.JSONDecodeError):
                    return f"{project.name} marketplace lock could not be inspected"
            if "@kb-labs/mind" not in locks[0] or "@kb-labs/commit" in locks[0]:
                return "Project A did not retain Mind without Project B's Commit plugin"
            if "@kb-labs/commit" not in locks[1] or "@kb-labs/mind" in locks[1]:
                return "Project B did not retain Commit without Project A's Mind plugin"
            return None

        journey.step("install Mind into project A and verify plugin lock isolation", "marketplace",
                     mind_plugin_scope, cases=["S-199.1"],
                     requires=("step:register isolated project A and B",))

        def adapter_bindings() -> str | None:
            outputs = []
            for project in projects:
                result = run(project, "adapter", "list", "--json", step_id="S-043.4")
                if result.returncode != 0:
                    return f"adapter list failed in {project.name}: {(result.stderr or result.stdout).strip()}"
                try:
                    payload = _cli_success(result)
                except ValueError as error:
                    return f"adapter list returned an invalid UX result in {project.name}: {error}"
                outputs.append(payload)
                for config in (project / ".kb" / "kb.config.json", project / ".kb" / "kb.config.jsonc"):
                    if config.exists() and '"adapters"' in config.read_text():
                        return f"{project.name} config contains project-scoped adapter settings"
            if outputs[0] != outputs[1]:
                return "registered projects resolved different platform adapter bindings"
            return None

        journey.step("resolve shared platform adapters in both projects", "adapters", adapter_bindings,
                     cases=["S-043.4"], requires=("step:register isolated project A and B",))

        def show_projects() -> str | None:
            for project in projects:
                result = run(project, "project", "show", "--json", step_id="S-043.5")
                if result.returncode != 0:
                    return f"project show failed in {project.name}: {(result.stderr or result.stdout).strip()}"
                try:
                    payload = _cli_success(result)
                except ValueError as error:
                    return f"project show returned an invalid UX result in {project.name}: {error}"
                row = payload.get("project", {}) if isinstance(payload, dict) else {}
                if not isinstance(row, dict) or row.get("id") != ids[project] or row.get("path") != str(project.resolve()):
                    return f"project show did not identify {project.name}'s registered root"
            return None

        journey.step("resolve project show from each repository", "projects", show_projects,
                     cases=["S-043.5"], requires=("step:register isolated project A and B",))

        def current_project_identity() -> str | None:
            listed = run(fixture_root, "project", "list", "--json", step_id="S-196.2")
            if listed.returncode != 0:
                return f"project list failed: {(listed.stderr or listed.stdout).strip()}"
            try:
                payload = _cli_success(listed)
            except ValueError as error:
                return f"project list returned an invalid UX result: {error}"
            rows = payload.get("projects", []) if isinstance(payload, dict) else []
            expected = {(ids[project], str(project.resolve())) for project in projects}
            found = {(row.get("id"), row.get("path")) for row in rows if isinstance(row, dict)}
            if not expected <= found:
                return "project list omitted a registered project ID or root"
            for project in projects:
                result = run(project, "project", "show", "--json", step_id="S-196.2")
                try:
                    current = _cli_success(result).get("project", {})
                except ValueError as error:
                    return f"project show returned an invalid UX result in {project.name}: {error}"
                if (result.returncode != 0 or current.get("id") != ids[project]
                        or current.get("path") != str(project.resolve())):
                    return f"project show selected the wrong registered root from {project.name}"
            return None

        journey.step("list and select each current project", "projects", current_project_identity,
                     cases=["S-196.2"], requires=("step:register isolated project A and B",))

        def project_plugin_commands() -> str | None:
            commands = ((projects[0], ("qa", "cases", "list", "--help"), "@kb-labs/qa"),
                        (projects[1], ("commit", "open", "--help"), "@kb-labs/commit"))
            for project, args, package in commands:
                result = run(project, *args, step_id="S-043.6")
                if result.returncode != 0:
                    return f"project-scoped {package} command failed in {project.name}: {(result.stderr or result.stdout).strip()}"
            return None

        journey.step("resolve project scoped plugin commands", "plugins", project_plugin_commands,
                     cases=["S-043.6"], requires=("step:register isolated project A and B",))

        def platform_plugin_is_available() -> str | None:
            version = _registry_version("@kb-labs/qa", registry, projects[0], env, command_runner)
            install = run(projects[0], "marketplace", "install", f"@kb-labs/qa@{version}",
                          "--scope", "platform", "--json", step_id="S-043.7")
            if install.returncode != 0:
                return f"Platform-scope install of @kb-labs/qa failed: {(install.stderr or install.stdout).strip()}"
            project_lock_path = projects[0] / ".kb" / "marketplace.lock"
            platform_lock_path = Path(env.get("KB_PLATFORM_ROOT", "")) / ".kb" / "marketplace.lock"
            try:
                project_plugins = json.loads(project_lock_path.read_text()).get("plugins", {})
                platform_plugins = json.loads(platform_lock_path.read_text()).get("plugins", {})
            except (OSError, json.JSONDecodeError):
                return "could not inspect the project and platform plugin scopes"
            if "@kb-labs/qa" not in project_plugins or "@kb-labs/qa" not in platform_plugins:
                return "the same QA plugin ID was not installed at both project and platform scope"
            result = run(projects[0], "qa", "cases", "list", "--help", step_id="S-043.7")
            if result.returncode != 0:
                return f"the shared QA command was unavailable from project A: {(result.stderr or result.stdout).strip()}"
            return None

        journey.step("resolve platform plugin alongside project plugins", "plugins",
                     platform_plugin_is_available, cases=["S-043.7"],
                     requires=("step:register isolated project A and B",))

        def project_mcp_config() -> str | None:
            for project in projects:
                result = run(project, "mcp", "config", "codex", "--json", step_id="S-199.6")
                if result.returncode != 0:
                    return f"mcp config codex failed in {project.name}: {(result.stderr or result.stdout).strip()}"
                try:
                    payload = _cli_success(result)
                except ValueError as error:
                    return f"mcp config codex returned an invalid UX result in {project.name}: {error}"
                expected_url = project_service_path(ids[project], "mcp")
                if (payload.get("projectId") != ids[project]
                        or not isinstance(payload.get("url"), str)
                        or expected_url not in payload["url"]):
                    return f"mcp config codex did not return {project.name}'s registered project endpoint"
            return None

        journey.step("render MCP client config for both projects", "mcp", project_mcp_config,
                     cases=["S-199.6"], requires=("step:register isolated project A and B",))

        def current_project_config() -> str | None:
            for project in projects:
                value = json.dumps([{
                    "id": f"journey-{project.name}", "label": project.name, "path": ".",
                }])
                with invocation_context(step_id="journey.setup"):
                    configured = run_with_runner(command_runner,
                        kb_command([ "config", "set", "plugins.commit.scope.scopes", value, "--json"], executable=cli),
                        cwd=project, env=env, timeout=180)
                if configured.returncode != 0:
                    return f"could not prepare a project-specific config fixture for {project.name}"
                nested = project / "nested"
                nested.mkdir()
                result = run(nested, "config", "show", "--json", step_id="S-196.3")
                if result.returncode != 0:
                    return f"config show failed from {project.name} subdirectory: {(result.stderr or result.stdout).strip()}"
                try:
                    payload = _cli_success(result)
                except ValueError as error:
                    return f"config show returned an invalid UX result from {project.name} subdirectory: {error}"
                rows = payload.get("rows", []) if isinstance(payload, dict) else []
                scopes = next((row for row in rows if isinstance(row, dict)
                               and row.get("field") == "plugins.commit.scope.scopes"), None)
                if (not isinstance(scopes, dict) or scopes.get("source") not in ("project", "both")
                        or scopes.get("value") != json.loads(value)):
                    return f"config show did not return {project.name}'s project-scoped config from its subdirectory"
            return None

        journey.step("resolve effective config from each project subdirectory", "projects", current_project_config,
                     cases=["S-196.3"], requires=("step:register isolated project A and B",))

        def unknown_project_and_local_command() -> str | None:
            result = run(fixture_root, "config", "show", "--json", step_id="S-196.4")
            try:
                error = _cli_error(result)
            except ValueError as error:
                return f"config show outside registered projects returned an invalid UX result: {error}"
            if result.returncode == 0 or error.get("code") != "KB_PROJECT_UNKNOWN":
                return "project-scoped config show outside registered roots did not return KB_PROJECT_UNKNOWN"
            hint = error.get("hint", "")
            if "kb project add" not in hint and "cd" not in hint:
                return "KB_PROJECT_UNKNOWN did not explain the project registration or cd recovery"
            local = run(fixture_root, "--version", step_id="S-196.4")
            if local.returncode != 0:
                return "local-only --version failed outside registered projects"
            return None

        journey.step("reject unknown project context and keep local CLI commands available", "projects",
                     unknown_project_and_local_command, cases=["S-196.4"],
                     requires=("step:register isolated project A and B",))


def run_project_identity_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"

    def project_show_outside_registry() -> str | None:
        with tempfile.TemporaryDirectory(prefix="kb-project-identity-home-") as home, \
                tempfile.TemporaryDirectory(prefix="kb-project-identity-cwd-") as cwd:
            env = configure_hermetic_cli_fixture(
                Path(home), env=os.environ,
                source_config=repo_root / ".kb" / "kb.config.jsonc")
            result = run_command(
                ["node", str(cli), "project", "show", "--json"],
                cwd=cwd,
                env=env,
                capture_output=True,
                text=True,
                check=False,
                timeout=120,
            )
            try:
                error = _cli_error(result)
            except ValueError as error:
                return f"project show returned an invalid UX result: {error}"
            payload = error
            if result.returncode == 0 or error.get("code") != "KB_PROJECT_UNKNOWN":
                return f"unregistered cwd returned unexpected result: {payload}"
            if "kb project add" not in error.get("hint", ""):
                return f"unknown project hint did not explain registration: {error}"
            if (Path(cwd) / ".kb").exists():
                return "project show created a .kb directory in the unregistered repository"
            return None

    journey.step(
        "reject project identity outside the registry",
        "project",
        project_show_outside_registry,
        cases=["S-204.7"],
    )
