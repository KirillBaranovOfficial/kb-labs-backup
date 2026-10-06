from __future__ import annotations

import sys
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))

from project_identity_steps import run_project_identity_steps, run_project_registry_steps
from service_steps import Journey


class ProjectIdentityStepsTest(unittest.TestCase):
    # case: S-204.7
    def test_unregistered_cwd_reports_unknown_project_and_add_hint(self):
        repo_root = Path(__file__).resolve().parents[2]
        journey = Journey("local", "project-identity")
        run_project_identity_steps(journey, repo_root)

        result = journey.result()
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(claims, {"S-204.7"})

    # case: S-043.1-.7, S-196.1-.4, S-199.1, S-199.6
    def test_project_registry_steps_reject_fake_registration_failure_then_claim_scope_checks(self):
        with tempfile.TemporaryDirectory(prefix="c07-project-identity-home-") as home:
            platform_root = Path(home) / "platform"
            (platform_root / ".kb").mkdir(parents=True)
            (platform_root / ".kb" / "marketplace.lock").write_text(json.dumps({
                "plugins": {"@kb-labs/workflow": {"version": "2.120.1"}},
            }))
            env = {"HOME": home, "KB_HOME": str(Path(home) / ".kb"), "KB_PLATFORM_ROOT": str(platform_root)}
            Path(env["KB_HOME"]).mkdir()
            calls = []
            registrations = {}
            locks = {}
            project_configs = {}

            def fake_runner(command, **kwargs):
                cwd = Path(kwargs["cwd"]).resolve()
                args = command[1:]
                calls.append((cwd, args))
                if args[:2] == ["project", "add"]:
                    project = Path(args[2]).resolve()
                    identifier = f"prj_{len(registrations) + 1}"
                    registrations[project] = identifier
                    return subprocess.CompletedProcess(command, 0, json.dumps({
                        "ok": True, "data": {"project": {"id": identifier, "path": str(project)}},
                    }), "")
                if args[:2] == ["project", "list"]:
                    rows = [{"id": identifier, "path": str(project)} for project, identifier in registrations.items()]
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"projects": rows}}), "")
                if args[:2] == ["marketplace", "install"]:
                    package = args[2].split("@2.120.1")[0]
                    manifest_id = {
                        "@kb-labs/workflow-entry": "@kb-labs/workflow",
                        "@kb-labs/commit-entry": "@kb-labs/commit",
                        "@kb-labs/mind-entry": "@kb-labs/mind",
                    }.get(package, package)
                    locks.setdefault(cwd, {})[manifest_id] = {"version": "2.120.1"}
                    lock = cwd / ".kb" / "marketplace.lock"
                    lock.parent.mkdir(parents=True, exist_ok=True)
                    lock.write_text(json.dumps({"plugins": locks[cwd]}))
                    return subprocess.CompletedProcess(command, 0, '{"ok":true}', "")
                if args[:2] == ["adapter", "list"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"adapters": []}}), "")
                if args[:2] == ["project", "show"]:
                    root = next((path for path in registrations if cwd == path or path in cwd.parents), cwd)
                    project = registrations.get(root)
                    row = {"id": project, "path": str(root)}
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"project": row}}), "")
                if args[:2] == ["config", "set"]:
                    root = next((path for path in registrations if cwd == path or path in cwd.parents), cwd)
                    project_configs[root] = json.loads(args[3])
                    return subprocess.CompletedProcess(command, 0, '{"ok":true}', "")
                if args[:2] == ["config", "show"]:
                    root = next((path for path in registrations if cwd == path or path in cwd.parents), None)
                    if root is None:
                        return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                            "code": "KB_PROJECT_UNKNOWN", "message": "Project is not registered.",
                            "hint": "Run kb project add <path> or cd into a registered project.",
                        }}), "")
                    return subprocess.CompletedProcess(command, 0, json.dumps({
                        "ok": True, "data": {"project": {"id": registrations[root]},
                            "rows": [{"field": "plugins.commit.scope.scopes", "source": "project",
                                      "value": project_configs[root]}]},
                    }), "")
                if args[:3] == ["mcp", "config", "codex"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {
                        "projectId": registrations[cwd],
                        "url": f"http://localhost:4000/api/v1/projects/{registrations[cwd]}/mcp",
                    }}), "")
                if args == ["--version"]:
                    return subprocess.CompletedProcess(command, 0, "2.120.1\n", "")
                return subprocess.CompletedProcess(command, 0, "command help\n", "")

            with patch("project_identity_steps._registry_version", return_value="2.120.1"):
                with patch("project_identity_steps.run_with_runner", side_effect=lambda _runner, command, **opts: fake_runner(command, **opts)):
                    red = Journey("local", "project-registry-red")
                    def fail_add(command, **kwargs):
                        if command[1:3] == ["project", "add"]:
                            return subprocess.CompletedProcess(command, 1, "", "fake registration failure")
                        return fake_runner(command, **kwargs)
                    with patch("project_identity_steps.run_with_runner", side_effect=lambda _runner, command, **opts: fail_add(command, **opts)):
                        run_project_registry_steps(red, "kb", env, "http://registry.test", runner=fake_runner)
                    red_claims = {claim["claimId"]: claim["status"] for claim in red.result().case_run["claims"]}
                    self.assertEqual(red_claims["S-043.1"], "FAIL")

                    registrations.clear()
                    locks.clear()
                    green = Journey("local", "project-registry-green")
                    run_project_registry_steps(green, "kb", env, "http://registry.test", runner=fake_runner)

        result = green.result()
        self.assertEqual(result.status, "PASS", result.first_failure)
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertTrue({"S-043.1", "S-043.2", "S-043.3", "S-043.4", "S-043.5", "S-043.6", "S-043.7",
                         "S-196.1", "S-196.2", "S-196.3", "S-196.4", "S-199.1", "S-199.6"} <= claims)


if __name__ == "__main__":
    unittest.main()
