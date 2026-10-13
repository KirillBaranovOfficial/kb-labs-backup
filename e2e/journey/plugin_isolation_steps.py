from __future__ import annotations

from kb_invocation import kb_command

import json
import os
from pathlib import Path
import tempfile

from invocations import (decode_ux_success_result, invocation_context, read_ux_cli_result,
                         run_command)
from journey_resources import active_platform_root, create_fixture_install_container
from service_steps import Journey


def run_project_plugin_availability_steps(journey: Journey, cli: str, project_a: Path,
                                         project_b: Path, env: dict[str, str], *, runner=run_command) -> None:
    """Check that each repository reports its other project's plugin as unavailable (S-197.5)."""
    def unavailable(project: Path, args: list[str], package: str, step_id: str,
                    command_env: dict[str, str]) -> str | None:
        with invocation_context(step_id=step_id):
            result = run_command(kb_command([ *args], executable=cli), cwd=project, env=command_env, runner=runner, timeout=180)
        if result.returncode == 0:
            return f"{package} command resolved from {project.name} although it is not installed in that project"
        output = "\n".join((result.stdout or "", result.stderr or ""))
        if package not in output or "marketplace install" not in output.lower():
            return f"{project.name} did not report {package} as unavailable with its install command"
        return None

    def check(command_env: dict[str, str]) -> str | None:
        failure = unavailable(
            project_a, ["review", "run", "--mode=heuristic", "--json"],
            "@kb-labs/review-entry", "S-197.5", command_env,
        )
        if failure:
            return failure
        return unavailable(
            project_b, ["commit", "commit", "--dry-run", "--json"],
            "@kb-labs/commit-entry", "S-197.5", command_env,
        )

    def isolated_platform_environment() -> tuple[tempfile.TemporaryDirectory[str] | None, dict[str, str]]:
        platform_root = Path(env.get("KB_PLATFORM_ROOT", ""))
        if not platform_root.is_dir():
            return None, env
        temp = tempfile.TemporaryDirectory(prefix="kb-project-plugin-scope-", dir=env.get("HOME"))
        fixture_root = Path(temp.name) / "platform"
        fixture_version = create_fixture_install_container(fixture_root, "fixture")
        source_version = active_platform_root(platform_root)
        for entry in source_version.iterdir():
            if entry.name in {".kb", "pnpm-workspace.yaml"}:
                continue
            (fixture_version / entry.name).symlink_to(entry, target_is_directory=entry.is_dir())
        source_kb = source_version / ".kb"
        target_kb = fixture_version / ".kb"
        target_kb.mkdir()
        for filename in ("kb.config.json", "kb.config.jsonc"):
            source = source_kb / filename
            if source.exists():
                (target_kb / filename).write_bytes(source.read_bytes())
        source_lock = source_kb / "marketplace.lock"
        if source_lock.exists():
            lock = json.loads(source_lock.read_text())
            lock.get("plugins", {}).pop("@kb-labs/commit", None)
            (target_kb / "marketplace.lock").write_text(json.dumps(lock, indent=2) + "\n")
        isolated_env = {**env, "KB_PLATFORM_ROOT": str(fixture_root)}
        return temp, isolated_env

    def check_in_isolated_platform() -> str | None:
        temp, isolated_env = isolated_platform_environment()
        try:
            return check(isolated_env)
        finally:
            if temp is not None:
                temp.cleanup()

    journey.step("reject cross-project plugin commands with install guidance", "plugins",
                 check_in_isolated_platform, cases=["S-197.5"])

    def review_project_b() -> str | None:
        fixture = project_b / "journey-review-fixture.txt"
        if fixture.exists():
            return "Project B already contains the review fixture path"
        fixture.write_text("Project B review fixture.\n", encoding="utf-8")
        try:
            with invocation_context(step_id="S-197.3"):
                result = run_command(
                    kb_command([ "review", "run", "--mode=heuristic", "--json"], executable=cli),
                    cwd=project_b, env=env, runner=runner, timeout=180,
                )
            if result.returncode != 0:
                return f"heuristic Review failed in Project B: {(result.stderr or result.stdout).strip()}"
            try:
                payload = decode_ux_success_result(read_ux_cli_result(result.stdout, result.returncode))
            except ValueError as error:
                return f"heuristic Review returned an invalid UX result: {error}"
            result_data = payload
            metadata = result_data.get("metadata") if isinstance(result_data, dict) else None
            analyzed = metadata.get("analyzedFiles") if isinstance(metadata, dict) else None
            if (not isinstance(metadata, dict) or metadata.get("mode") != "heuristic"
                    or not isinstance(analyzed, int) or analyzed < 1):
                return "heuristic Review did not report an analyzed Project B change"
            findings = result_data.get("findings", [])
            if not isinstance(findings, list):
                return "heuristic Review returned an invalid findings list"
            for finding in findings:
                file_path = finding.get("file") if isinstance(finding, dict) else None
                if isinstance(file_path, str) and Path(file_path).is_absolute():
                    if not Path(file_path).is_relative_to(project_b.resolve()):
                        return f"Review included a finding outside Project B: {file_path}"
            return None
        finally:
            fixture.unlink(missing_ok=True)

    journey.step("review changed files from Project B with the heuristic engine", "review",
                 review_project_b, cases=["S-197.3"])
