from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

from service_steps import Journey
from invocations import invocation_context, run_command


def run_workspace_cli_steps(journey: Journey, repo_root: Path) -> None:
    cli = repo_root / "cli/bin/dist/bin.js"
    with tempfile.TemporaryDirectory(prefix="kb-workspace-cli-home-") as home:
        env = {**os.environ, "HOME": home}
        env["KB_PLATFORM_ROOT"] = str(repo_root)
        kb_home = Path(home) / ".kb"
        kb_home.mkdir()
        shutil.copy2(repo_root / ".kb/marketplace.lock", kb_home / "marketplace.lock")
        env["KB_HOME"] = str(kb_home)
        env.pop("KB_NET_OFFSET", None)

        def run(*args: str) -> subprocess.CompletedProcess[str]:
            return run_command(["node", str(cli), *args], cwd=repo_root, env=env, timeout=120)

        def register_checkout() -> str | None:
            result = run_command(
                [
                    "node", "--input-type=module", "-e",
                    "const { createProjectRegistry } = await import(process.argv[1]); "
                    "await createProjectRegistry({ env: process.env }).add(process.argv[2]);",
                    str(repo_root / "core/project-registry/dist/index.js"),
                    str(repo_root),
                ],
                cwd=repo_root,
                env=env,
                capture_output=True,
                text=True,
                check=False,
                timeout=120,
            )
            if result.returncode != 0:
                return f"could not register workspace checkout in isolated KB_HOME: {result.stderr or result.stdout}"
            return None

        def validate_cases() -> str | None:
            registration_error = register_checkout()
            if registration_error:
                return registration_error
            result = run("qa", "cases", "validate", "--json")
            if result.returncode != 0:
                return f"workspace QA case validation failed (exit {result.returncode}): {result.stderr or result.stdout}"
            try:
                payload = json.loads(result.stdout)
            except json.JSONDecodeError as error:
                return f"workspace QA case validation did not return JSON: {error}"
            if not isinstance(payload, dict) or payload.get("ok") is not True:
                return "workspace QA case validation did not return a successful JSON envelope"
            data = payload.get("data")
            if not isinstance(data, dict) or data.get("valid") is not True:
                return "workspace QA case validation envelope did not contain valid case data"
            return None

        journey.step("validate QA cases with the workspace CLI", "workspace-cli", validate_cases, cases=["S-179.2", "S-203.1"])

        def list_workspace_adapters() -> str | None:
            result = run("adapter", "list", "--json")
            if result.returncode != 0:
                return f"workspace adapter listing failed (exit {result.returncode}): {result.stderr or result.stdout}"
            try:
                payload = json.loads(result.stdout)
            except json.JSONDecodeError as error:
                return f"workspace adapter listing did not return JSON: {error}"
            data = payload.get("data") if isinstance(payload, dict) else None
            if not isinstance(payload, dict) or payload.get("ok") is not True or not isinstance(data, dict):
                return "workspace adapter listing did not return a successful JSON envelope"
            rows = data.get("adapters", [])
            implementations = {row.get("slot"): row.get("implementation") for row in rows if isinstance(row, dict)}
            expected = {
                "storage": "@kb-labs/adapters-fs",
                "cache": "@kb-labs/adapters-state-broker",
                "eventBus": "@kb-labs/adapters-eventbus-cache",
                "documentDatabase": "@kb-labs/adapters-sqlite",
                "kvStore": "@kb-labs/adapters-sqlite/kv",
                "logger": "@kb-labs/adapters-pino",
                "logRingBuffer": "@kb-labs/adapters-log-ringbuffer",
                "logPersistence": "@kb-labs/adapters-log-document",
                "analytics": "@kb-labs/adapters-analytics-sqlite",
                "serviceTransport": "@kb-labs/adapters-service-transport-http",
            }
            missing = [slot for slot, package in expected.items() if implementations.get(slot) != package]
            if missing:
                return f"workspace adapter listing did not resolve configured workspace packages for: {', '.join(missing)}"
            return None

        journey.step("list configured workspace adapters", "workspace-cli", list_workspace_adapters, cases=["S-179.5"])

        def list_plugin_commands() -> str | None:
            with invocation_context(step_id="S-179.3"):
                result = run("--help")
            if result.returncode != 0:
                return f"workspace CLI help failed (exit {result.returncode}): {result.stderr or result.stdout}"
            expected_groups = ("qa", "marketplace", "workflow", "commit", "release", "review", "mind", "agent")
            missing = [group for group in expected_groups if group not in result.stdout]
            if missing:
                return f"workspace CLI help is missing plugin groups: {', '.join(missing)}"
            return None

        journey.step("list workspace plugin command groups", "workspace-cli", list_plugin_commands, cases=["S-179.3"])

        def unavailable_locked_workspace_plugin() -> str | None:
            fixture_root = Path(home) / "unbuilt-workspace"
            kb_dir = fixture_root / ".kb"
            package_dir = fixture_root / "plugins/release/manager-cli"
            kb_dir.mkdir(parents=True)
            for source in repo_root.iterdir():
                if source.name in {".kb", ".git", "plugins"}:
                    continue
                (fixture_root / source.name).symlink_to(source, target_is_directory=source.is_dir())
            fixture_plugins = fixture_root / "plugins"
            fixture_plugins.mkdir()
            for source in (repo_root / "plugins").iterdir():
                if source.name == "release":
                    continue
                (fixture_plugins / source.name).symlink_to(source, target_is_directory=True)
            (fixture_plugins / "release").mkdir()
            for source in (repo_root / "plugins/release").iterdir():
                if source.name != "manager-cli":
                    (fixture_plugins / "release" / source.name).symlink_to(source, target_is_directory=True)
            package_dir.mkdir(parents=True)
            (package_dir / "package.json").write_text(json.dumps({
                "name": "@kb-labs/release-manager-cli",
                "kb": {"manifest": "./dist/manifest.js"},
            }), encoding="utf-8")
            locked = json.loads((repo_root / ".kb/marketplace.lock").read_text())
            locked["plugins"]["@kb-labs/release"]["link"] = "./plugins/release/manager-cli"
            config_source = repo_root / ".kb/kb.config.json"
            if not config_source.exists():
                config_source = repo_root / ".kb/kb.config.jsonc"
            (kb_dir / config_source.name).write_bytes(config_source.read_bytes())
            isolated_lock = locked
            (kb_dir / "marketplace.lock").write_text(json.dumps(isolated_lock), encoding="utf-8")
            isolated_env = {**env, "KB_PLATFORM_ROOT": str(fixture_root), "KB_HOME": str(kb_dir)}
            with invocation_context(step_id="S-179.13"):
                help_result = run_command(["node", str(cli), "--help"], cwd=fixture_root,
                                          env=isolated_env, timeout=120)
            help_text = "\n".join((help_result.stdout or "", help_result.stderr or ""))
            if help_result.returncode != 0 or "release" not in help_text or "pnpm build" not in help_text:
                return ("workspace help did not identify the unbuilt locked release plugin and pnpm build: "
                        f"exit={help_result.returncode} output={help_text[:3000]}")
            with invocation_context(step_id="S-179.13"):
                command_result = run_command(["node", str(cli), "release", "plan", "--json"],
                                             cwd=fixture_root, env=isolated_env, timeout=120)
            try:
                payload = json.loads(command_result.stdout or "")
            except json.JSONDecodeError:
                return "unbuilt release command did not return a typed JSON error"
            error = payload.get("error") if isinstance(payload, dict) else None
            if (command_result.returncode == 0 or not isinstance(error, dict)
                    or error.get("code") != "KB_PLUGIN_NOT_FOUND"
                    or "pnpm build" not in str(error.get("hint", ""))):
                return ("unbuilt release command did not return typed KB_PLUGIN_NOT_FOUND with pnpm build guidance: "
                        f"exit={command_result.returncode} stdout={(command_result.stdout or '')[:800]} "
                        f"stderr={(command_result.stderr or '')[:800]}")
            return None

        journey.step("diagnose an unbuilt locked workspace plugin", "workspace-cli",
                     unavailable_locked_workspace_plugin, cases=["S-179.13"])

        def verify_agent_command_namespaces() -> str | None:
            with invocation_context(step_id="S-142.5"):
                help_result = run("--help")
            help_text = "\n".join((help_result.stdout or "", help_result.stderr or ""))
            if help_result.returncode != 0:
                return "workspace CLI help failed while checking agent namespaces"
            if "tried to register \"agent register\"" in help_text or "namespace \"agent\" is owned" in help_text:
                return "workspace CLI reported an agent namespace collision"
            if "workspace" not in help_text or "agent" not in help_text:
                return "workspace or Agents command namespace is missing from help"
            with invocation_context(step_id="S-142.5"):
                workspace_help = run("workspace", "--help")
            workspace_text = "\n".join((workspace_help.stdout or "", workspace_help.stderr or ""))
            if workspace_help.returncode != 0 or any(name not in workspace_text for name in ("register", "status", "list")):
                return "Workspace Agent commands are not listed under the workspace namespace"
            with invocation_context(step_id="S-142.5"):
                agent_help = run("agent", "--help")
            agent_text = "\n".join((agent_help.stdout or "", agent_help.stderr or ""))
            if agent_help.returncode != 0 or not any(name in agent_text for name in ("run", "history")):
                return "Agents commands are not listed under the agent namespace"
            return None

        journey.step("keep host and project agent command namespaces distinct", "workspace-cli",
                     verify_agent_command_namespaces, cases=["S-142.5"])

        def verify_synced_workspace_lock() -> str | None:
            try:
                lock = json.loads((repo_root / ".kb/marketplace.lock").read_text())
            except (OSError, json.JSONDecodeError) as error:
                return f"workspace marketplace lock could not be read: {error}"
            expected_plugins = ("@kb-labs/release", "@kb-labs/review", "@kb-labs/mind", "@kb-labs/agents")
            missing_plugins = [plugin for plugin in expected_plugins if plugin not in lock.get("plugins", {})]
            adapters = lock.get("adapters", {})
            if lock.get("schema") != "kb.marketplace/3":
                return "workspace marketplace lock does not use schema kb.marketplace/3"
            if missing_plugins:
                return f"workspace marketplace lock is missing plugin entries: {', '.join(missing_plugins)}"
            if not adapters:
                return "workspace marketplace lock has no machine-level adapter entries"
            return None

        journey.step("verify synced workspace plugins and adapters in the v3 lock", "workspace-cli", verify_synced_workspace_lock, cases=["S-179.4"])
