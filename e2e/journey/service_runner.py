#!/usr/bin/env python3
"""Probe fresh-install service capabilities and emit machine-readable results."""
from __future__ import annotations

import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import subprocess
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone
from urllib.request import build_opener, HTTPCookieProcessor

from service_steps import Journey
from plugin_author_steps import run_plugin_author_flow
from marketplace_steps import run_marketplace_steps
from marketplace_steps import _registry_version, _output
from adapter_defaults_steps import run_adapter_default_steps
from mcp_project_steps import (
    apply_commit_plan,
    commit_tool_schema,
    invalid_arguments,
    local_auth,
    project_isolation,
    secured_auth,
    unknown_tool,
)


BASE_URL = os.getenv("KB_JOURNEY_BASE_URL", "http://127.0.0.1:4000").rstrip("/")
PROJECT_ID = os.getenv("KB_JOURNEY_PROJECT_ID", "")
ADMIN_EMAIL = os.getenv("KB_JOURNEY_ADMIN_EMAIL", "admin@example.com")
ADMIN_PASSWORD = os.getenv("KB_JOURNEY_ADMIN_PASSWORD", "")
HTTP = build_opener(HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path: str, *, method="GET", payload=None, token="", accept="application/json") -> tuple[int, object, dict[str, str]]:
    body = json.dumps(payload).encode() if payload is not None else None
    headers = {"Accept": accept}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(BASE_URL + path, data=body, headers=headers, method=method)
    try:
        with HTTP.open(req, timeout=15) as response:
            raw = response.read()
            status = response.status
            response_headers = dict(response.headers.items())
    except urllib.error.HTTPError as error:
        raw, status, response_headers = error.read(), error.code, dict(error.headers.items())
    except (urllib.error.URLError, TimeoutError) as error:
        raise RuntimeError(f"gateway is unreachable at {BASE_URL}: {error}; start it with `kb-create start`") from error
    try:
        parsed = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        parsed = raw.decode(errors="replace")
    return status, parsed, response_headers


def expect(path: str, expected: set[int], capability: str, *, method="GET", payload=None, token=""):
    status, body, _ = request(path, method=method, payload=payload, token=token)
    if status not in expected:
        detail = body.get("message", body.get("error", "")) if isinstance(body, dict) else ""
        reason = f"{capability} unavailable: {method} {path} returned HTTP {status}"
        if detail:
            reason += f" ({detail})"
        reason += "; build and start the capability, then rerun `python3 /opt/kb-journey/service_runner.py`"
        return reason
    return None


def expect_json(path: str, capability: str, *, token="") -> str | None:
    status, body, _ = request(path, token=token)
    if status != 200:
        detail = body.get("message", body.get("error", "")) if isinstance(body, dict) else ""
        suffix = f" ({detail})" if detail else ""
        return f"{capability} unavailable: GET {path} returned HTTP {status}{suffix}"
    if not isinstance(body, dict):
        return f"{capability} returned HTTP 200 without a JSON object"
    return None


def checks(mode: str):
    yield ("gateway readiness", "gateway", lambda: expect_json("/ready", "gateway readiness"))
    if mode == "secured":
        def admin_login():
            if not ADMIN_PASSWORD:
                return "BLOCKED: secured profile needs KB_JOURNEY_ADMIN_PASSWORD; provide test credentials in the isolated journey container"
            status, body, _ = request("/auth/login/cli", method="POST",
                                      payload={"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
            if status not in {200, 201}:
                return f"secured admin CLI login unavailable: POST /auth/login/cli returned HTTP {status}; check the isolated secured install credentials"
            if not isinstance(body, dict):
                return "secured admin CLI login returned a non-JSON response"
            token = body.get("accessToken") or body.get("token")
            if not token:
                return "secured admin CLI login returned no access token from /auth/login/cli"
            os.environ["KB_JOURNEY_SERVICE_TOKEN"] = str(token)
            me_status, _, _ = request("/auth/me", token=str(token))
            if me_status != 200:
                return f"secured admin CLI token did not authenticate /auth/me (HTTP {me_status})"
            return None
        yield ("secured admin CLI login", "auth.secured", admin_login)

    token = os.getenv("KB_JOURNEY_SERVICE_TOKEN", "")
    if not PROJECT_ID:
        project = lambda: "BLOCKED: no project is registered; run `kb project add <path> --json` and set KB_JOURNEY_PROJECT_ID"
    else:
        project = lambda: expect(f"/api/v1/projects/{PROJECT_ID}", {200}, "project registry lookup", token=os.getenv("KB_JOURNEY_SERVICE_TOKEN", token))
    yield ("project registration", "projects", project)
    pid = PROJECT_ID or "{projectId}"
    auth_token = lambda: os.getenv("KB_JOURNEY_SERVICE_TOKEN", token)
    yield ("workflow health", "workflow", lambda: expect_json(
        f"/api/v1/projects/{pid}/workflow/health", "project-aware workflow health", token=auth_token()))
    yield ("workflow discovery", "workflow", lambda: discover_workflow(pid, auth_token()))
    yield ("workflow execution endpoint", "workflow", lambda: expect(
        f"/api/v1/projects/{pid}/workflow/api/v1/workflows/e2e-hello/runs", {200, 201, 202},
        "project-aware workflow run endpoint", method="POST", payload={"trigger": {"type": "manual"}}, token=auth_token()))
    yield ("project-scoped MCP initialize", "mcp", lambda: mcp_initialize(pid, auth_token()))
    yield ("project-scoped MCP tools/list", "mcp", lambda: mcp_probe(pid, auth_token()))
    yield ("project-scoped MCP diagnostics", "mcp", lambda: mcp_diagnostics(pid, auth_token()))
    if mode == "local":
        yield ("project MCP tokenless local access", "mcp", lambda: local_auth(pid))
    else:
        yield ("project MCP secured missing and invalid token rejection", "mcp", lambda: secured_auth(pid))
    yield ("project MCP commit schema", "mcp", lambda: commit_tool_schema(pid, auth_token()))
    project_root = os.getenv("KB_JOURNEY_PROJECT_ROOT", "")
    if project_root:
        yield ("project MCP commit mutation", "mcp", lambda: apply_commit_plan(pid, auth_token(), project_root))
    else:
        yield ("project MCP commit mutation", "mcp", lambda: "BLOCKED: provide KB_JOURNEY_PROJECT_ROOT for the disposable commit fixture")
    yield ("project MCP unknown tool error", "mcp", lambda: unknown_tool(pid, auth_token()))
    yield ("project MCP invalid argument error", "mcp", lambda: invalid_arguments(pid, auth_token()))
    project_b_id = os.getenv("KB_JOURNEY_PROJECT_B_ID", "")
    if project_b_id:
        yield ("project MCP cross-project isolation", "mcp", lambda: project_isolation(pid, project_b_id, auth_token()))
    else:
        yield ("project MCP cross-project isolation", "mcp", lambda: "BLOCKED: provide KB_JOURNEY_PROJECT_B_ID for the isolated second project")
    yield ("Studio shell", "studio", lambda: expect("/", {200}, "Studio shell"))
    yield ("Studio project API", "studio", lambda: expect("/api/v1/projects", {200}, "Studio project API", token=auth_token()))
    yield ("marketplace registry", "marketplace", lambda: expect(f"/api/v1/projects/{pid}/rest/api/v1/plugins/registry", {200}, "marketplace registry", token=auth_token()))
def declared_capabilities(mode: str) -> set[str]:
    """Return every capability the selected service profile promises to check."""
    capabilities = {capability for _, capability, _ in checks(mode)}
    capabilities.add("auth.local" if mode == "local" else "auth.invite-reset")
    capabilities.add("plugin-author")
    return capabilities


def discover_workflow(project_id: str, token: str) -> str | None:
    path = f"/api/v1/projects/{project_id}/workflow/api/v1/workflows"
    status, body, _ = request(path, token=token)
    if status != 200:
        return f"project-aware workflow discovery unavailable: GET {path} returned HTTP {status}"
    data = body.get("data", body) if isinstance(body, dict) else body
    workflows = data.get("workflows", []) if isinstance(data, dict) else data
    if not isinstance(workflows, list) or not any(
        isinstance(item, dict) and (item.get("name") == "e2e-hello" or item.get("id") == "e2e-hello")
        for item in workflows
    ):
        return "project-aware workflow catalog does not include the e2e-hello fixture from .kb/workflows/e2e-hello.yml"
    return None


def _mcp_response(project_id: str, token: str, payload: dict) -> tuple[int, object]:
    status, body, _ = request(f"/api/v1/projects/{project_id}/mcp", method="POST", token=token,
                              accept="application/json, text/event-stream", payload=payload)
    if isinstance(body, str) and any(line.startswith("data:") for line in body.splitlines()):
        data = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
        try:
            body = json.loads(data)
        except json.JSONDecodeError:
            return status, None
    return status, body


def mcp_initialize(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-03-26", "capabilities": {},
                   "clientInfo": {"name": "kb-labs-journey", "version": "1.0.0"}},
    })
    if status != 200:
        return f"project-scoped MCP initialize unavailable: POST /api/v1/projects/{project_id}/mcp returned HTTP {status}; enable the project MCP runtime and rerun the service journey"
    result = body.get("result") if isinstance(body, dict) else None
    if not isinstance(result, dict) or result.get("protocolVersion") != "2025-03-26":
        return "project-scoped MCP initialize returned no result for protocol version 2025-03-26"
    return None


def mcp_probe(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {},
    })
    if status != 200:
        return f"project-scoped MCP tools/list unavailable: POST /api/v1/projects/{project_id}/mcp returned HTTP {status}; enable the project MCP runtime and rerun the service journey"
    if not isinstance(body, dict) or not isinstance(body.get("result"), dict):
        return "project-scoped MCP tools/list returned no JSON-RPC result"
    tools = body["result"].get("tools")
    if not isinstance(tools, list) or not tools:
        return "project-scoped MCP tools/list returned no project tools"
    names = {tool.get("name") for tool in tools if isinstance(tool, dict)}
    expected = {"kb-labs-commit__commit", "kb-labs-workflow__workflow_run", "kb-labs__mcp_diagnostics"}
    if not expected <= names:
        return f"project-scoped MCP tools/list omitted expected tools: {sorted(expected - names)}"
    if any(not isinstance(tool, dict) or not isinstance(tool.get("name"), str)
           or not isinstance(tool.get("inputSchema"), dict) for tool in tools):
        return "project-scoped MCP tools/list returned a tool without its name or input schema"
    return None


def mcp_diagnostics(project_id: str, token: str) -> str | None:
    if project_id == "{projectId}":
        return "BLOCKED: MCP project scope needs KB_JOURNEY_PROJECT_ID from project registration"
    status, body = _mcp_response(project_id, token, {
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": {"name": "kb-labs__mcp_diagnostics", "arguments": {}},
    })
    if status != 200:
        return f"project-scoped MCP diagnostics unavailable: HTTP {status}"
    result = body.get("result") if isinstance(body, dict) else None
    content = result.get("content", []) if isinstance(result, dict) else []
    text = "\n".join(item.get("text", "") for item in content if isinstance(item, dict))
    if not isinstance(result, dict) or result.get("isError") or "loaded cleanly" not in text:
        return "project-scoped MCP diagnostics did not report that the project loaded cleanly"
    return None


def _case_claims(name: str) -> list[str]:
    if name in {"gateway readiness", "workflow health"}:
        return ["PC-002.3"]
    case_by_name = {
        "project-scoped MCP initialize": "S-045.1",
        "project-scoped MCP tools/list": "S-045.2",
        "project-scoped MCP diagnostics": "S-045.3",
        "project MCP tokenless local access": "S-045.4",
        "project MCP secured missing and invalid token rejection": "S-045.5",
        "project MCP commit schema": "S-140.1",
        "project MCP commit mutation": "S-140.2",
        "project MCP unknown tool error": "S-140.3",
        "project MCP invalid argument error": "S-140.4",
        "project MCP cross-project isolation": "S-140.5",
    }
    case_id = case_by_name.get(name)
    if name == "project MCP secured missing and invalid token rejection":
        return ["S-045.5", "S-140.6"]
    if not case_id:
        return []
    return [case_id]


def run_service_steps(journey: Journey, steps) -> None:
    """Run capability checks through Journey so raised checks are recorded."""
    for name, capability, check in steps:
        journey.step(name, capability, check, cases=_case_claims(name))


def configure_installed_cli_path() -> None:
    """Expose the isolated installed platform binaries to subprocess flows."""
    bin_dir = str(Path.home() / ".local" / "bin")
    current = os.environ.get("PATH", "").split(os.pathsep)
    if bin_dir not in current:
        os.environ["PATH"] = os.pathsep.join([bin_dir, *current])


def restore_secured_cli_login(kb_path: str | Path, env: dict[str, str], *, runner=subprocess.run) -> None:
    """Restore the CLI session after the auth journey intentionally logs out."""
    email = env.get("KB_JOURNEY_ADMIN_EMAIL", "").strip()
    password = env.get("KB_JOURNEY_ADMIN_PASSWORD", "")
    if not email or not password:
        raise RuntimeError("secured service journey needs admin credentials; rerun with KB_JOURNEY_ADMIN_EMAIL and KB_JOURNEY_ADMIN_PASSWORD")
    command = [str(kb_path), "auth", "login", "--gateway-url",
               env.get("KB_JOURNEY_BASE_URL", BASE_URL), "--email", email,
               "--password", password]
    result = runner(command, cwd=env.get("HOME"), env=env, capture_output=True,
                    text=True, timeout=90, check=False)
    if result.returncode != 0:
        detail = " ".join((result.stdout + " " + result.stderr).split())
        suffix = f"; {detail}" if detail else ""
        raise RuntimeError(f"`kb auth login` failed while restoring the secured service journey session{suffix}")


def _run_installed_flows(journey: Journey, project_root: str, registry: str) -> None:
    env = os.environ.copy()
    journey.step("adapter defaults flow", "adapter",
                 lambda: run_adapter_default_steps(journey, project_root, env))
    journey.step("plugin author flow", "plugin-author",
                 lambda: run_plugin_author_flow(journey, Path(project_root),
                                                Path.home() / ".local/bin/kb", registry,
                                                base_env=env))


def _find_project_id(payload: object) -> str:
    if isinstance(payload, dict):
        for key in ("projectId", "project_id", "id"):
            value = payload.get(key)
            if isinstance(value, str) and value.startswith("prj_"):
                return value
        for value in payload.values():
            found = _find_project_id(value)
            if found:
                return found
    elif isinstance(payload, list):
        for value in payload:
            found = _find_project_id(value)
            if found:
                return found
    return ""


def _write_mcp_commit_plan(project_root: Path, env: dict[str, str]) -> Path:
    """Prepare the staged note commit required by S-140.2 in the isolated KB state."""
    subprocess.run(["git", "-C", str(project_root), "config", "user.name", "KB Labs Journey"],
                   check=True, capture_output=True, text=True)
    subprocess.run(["git", "-C", str(project_root), "config", "user.email", "journey@example.com"],
                   check=True, capture_output=True, text=True)

    def git_files(*args: str) -> list[str]:
        result = subprocess.run(["git", "-C", str(project_root), *args], capture_output=True,
                               check=True, text=True)
        return [line for line in result.stdout.splitlines() if line]

    status = {
        "staged": git_files("diff", "--cached", "--name-only"),
        "unstaged": git_files("diff", "--name-only"),
        "untracked": git_files("ls-files", "--others", "--exclude-standard"),
    }
    files = [*status["staged"], *status["unstaged"], *status["untracked"]]
    if not status["staged"]:
        raise RuntimeError("S-140.2 requires a staged project A change before preparing the Commit plan")

    project_id = "prj_" + hashlib.sha256(str(project_root.resolve()).encode("utf-8")).hexdigest()[:16]
    kb_home_override = env.get("KB_HOME", "").strip()
    kb_home = Path(kb_home_override) if kb_home_override else Path(env["HOME"]) / ".kb"
    kb_home = kb_home.expanduser().resolve()
    plan_path = (kb_home / "state" / project_id / "commit" / "plans" / "root" /
                 "current" / "plan.json")
    plan_path.parent.mkdir(parents=True, exist_ok=True)
    plan = {
        "schemaVersion": "1.0",
        "createdAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "repoRoot": str(project_root.resolve()),
        "gitStatus": status,
        "commits": [{
            "id": "journey-commit",
            "type": "chore",
            "message": "Add journey fixture",
            "files": files,
            "releaseHint": "none",
            "breaking": False,
        }],
        "metadata": {"totalFiles": len(files), "totalCommits": 1, "llmUsed": False},
    }
    plan_path.write_text(json.dumps(plan, indent=2) + "\n")
    return plan_path


def prepare_mcp_projects(project_root: str, registry: str, env: dict[str, str],
                         runner=subprocess.run) -> str:
    """Install Commit into project A and register a clean project B for isolation."""
    project_a = Path(project_root).resolve()
    package = "@kb-labs/commit-entry"
    version = _registry_version(package, registry, env, runner)
    install = ["kb", "marketplace", "install", f"{package}@{version}", "--json"]
    installed = runner(install, cwd=str(project_a), env=env, capture_output=True,
                       text=True, timeout=300)
    _output(installed, install)
    _write_mcp_commit_plan(project_a, env)

    home = Path(env["HOME"])
    project_b = home / "project-b-mcp"
    project_b.mkdir(parents=True, exist_ok=False)
    subprocess.run(["git", "init", "-q", str(project_b)], check=True)
    register = ["kb", "project", "add", str(project_b), "--json"]
    result = runner(register, cwd=str(project_b), env=env, capture_output=True,
                    text=True, timeout=120)
    raw = _output(result, register)
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as error:
        raise RuntimeError("registering MCP isolation project B returned invalid JSON") from error
    project_b_id = _find_project_id(payload)
    if not project_b_id:
        raise RuntimeError("registering MCP isolation project B returned no project ID")
    lock_path = project_b / ".kb" / "marketplace.lock"
    if lock_path.exists():
        lock = json.loads(lock_path.read_text())
        if package in lock.get("plugins", {}):
            raise RuntimeError("MCP isolation project B unexpectedly has the Commit plugin installed")
    return project_b_id


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["local", "secured"], required=True)
    parser.add_argument("--output", default="service-journey.json")
    parser.add_argument("--run-id")
    args = parser.parse_args()
    journey = Journey(args.mode, args.run_id or str(uuid.uuid4()))
    try:
        configure_installed_cli_path()
        if args.mode == "secured":
            journey.step("restore secured CLI login", "auth.secured",
                         lambda: restore_secured_cli_login(
                             Path.home() / ".local/bin/kb", os.environ.copy()))
        project_root = os.getenv("KB_JOURNEY_PROJECT_ROOT")
        registry = os.getenv("KB_JOURNEY_REGISTRY", "http://verdaccio:4873")
        if project_root:
            env = os.environ.copy()
            journey.step("marketplace flow", "marketplace",
                         lambda: run_marketplace_steps(journey, project_root, registry, env))
            def prepare_projects() -> None:
                os.environ["KB_JOURNEY_PROJECT_B_ID"] = prepare_mcp_projects(project_root, registry, env)
            journey.step("prepare MCP project fixtures", "mcp", prepare_projects)
        run_service_steps(journey, checks(args.mode))
        if project_root:
            _run_installed_flows(journey, project_root, registry)
        else:
            reason = "journey runner did not receive KB_JOURNEY_PROJECT_ROOT from the installed project setup"
            journey.blocked("marketplace and adapter user flows", "marketplace", reason)
            journey.blocked("plugin author flow", "plugin-author", reason)
    except Exception as error:
        journey.step("service runner", "journey.runner", lambda: str(error) or error.__class__.__name__)
    finally:
        result = journey.write_json(args.output)
    print(json.dumps(result.__dict__, indent=2))
    return 0 if result.status == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
