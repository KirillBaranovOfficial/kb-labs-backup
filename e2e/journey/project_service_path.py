"""Project service URL paths built by the shared TypeScript contracts package."""

from functools import lru_cache
from pathlib import Path
from invocations import run_command
from journey_values import journey_state


def require_project_service_path_builder() -> Path:
    repo_root = Path(__file__).resolve().parents[2]
    builder = repo_root / "core/plugin-contracts/dist/project-service-path.js"
    if not builder.is_file():
        raise RuntimeError(
            "The shared project service URL builder is not built. Run "
            "`corepack pnpm --filter @kb-labs/plugin-contracts... --workspace-concurrency=2 build` first."
        )
    return repo_root


@lru_cache(maxsize=None)
def project_service_path(project_id: str, service_id: str, path: str = "") -> str:
    project_id = journey_state("KB_JOURNEY_PROJECT_ID", {"KB_JOURNEY_PROJECT_ID": project_id})
    repo_root = require_project_service_path_builder()
    script = (
        "import { projectServicePath } from './core/plugin-contracts/dist/project-service-path.js'; "
        "process.stdout.write(projectServicePath(process.argv[1], process.argv[2], process.argv[3]));"
    )
    result = run_command(
        ["node", "--input-type=module", "-e", script, project_id, service_id, path],
        cwd=repo_root,
    )
    if result.returncode != 0:
        raise RuntimeError(
            "The shared project service URL builder failed. Rebuild it with "
            "`corepack pnpm --filter @kb-labs/plugin-contracts... --workspace-concurrency=2 build`."
        )
    return result.stdout
