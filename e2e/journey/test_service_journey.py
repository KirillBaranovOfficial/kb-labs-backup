import ast
import hashlib
import io
import json
import os
import shlex
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from nightly_report import aggregate, fingerprint, load_results, render_summary, sync_issues
from entrypoint import run_runner
from service_runner import (checks, configure_installed_cli_path, declared_capabilities,
                            discover_workflow, expect_json, mcp_diagnostics, mcp_initialize,
                            prepare_mcp_projects,
                            main as service_main, mcp_probe, run_service_steps)
from service_steps import Journey, record_runner_failure
from steps.auth import run_secured_auth_steps


class FakeGitHub:
    def __init__(self):
        self.issues = [{"number": 10, "title": "Nightly status", "state": "open", "labels": []}]
        self.created = 20

    def list_open(self):
        return self.issues

    def create(self, title, body, labels):
        issue = {"number": self.created, "title": title, "body": body, "labels": labels, "state": "open"}
        self.created += 1
        self.issues.append(issue)
        return issue

    def update(self, number, **fields):
        issue = next(item for item in self.issues if item["number"] == number)
        issue.update(fields)
        return issue


class ServiceJourneyTests(unittest.TestCase):
    def test_first_failure_and_json_result(self):
        journey = Journey("local", "fixture")
        journey.step("health", "gateway", lambda: None)
        journey.step("workflow", "workflow", lambda: "workflow route returned 404")
        result = journey.result()
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.first_failure["name"], "workflow")
        self.assertEqual(len(result.steps), 2)
        self.assertEqual(result.steps[0]["cases"], [])
        self.assertEqual(result.case_run["claims"], [])
        with tempfile.TemporaryDirectory() as directory:
            saved = journey.write_json(Path(directory) / "journey.json")
            self.assertEqual(saved.schema, "kb.service-journey/v1")

    def test_service_step_exception_is_saved_and_later_steps_continue(self):
        journey = Journey("local", "raised-step")

        def broken_check():
            raise RuntimeError("marketplace CLI is unavailable")

        run_service_steps(journey, [
            ("marketplace registry", "marketplace", broken_check),
            ("gateway readiness", "gateway", lambda: None),
        ])
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "service-local.json"
            result = journey.write_json(path)
            saved = json.loads(path.read_text())

        self.assertEqual(result.status, "FAIL")
        self.assertEqual(saved["steps"][0]["status"], "FAIL")
        self.assertEqual(saved["steps"][0]["message"], "marketplace CLI is unavailable")
        self.assertEqual(saved["steps"][1]["status"], "PASS")

    def test_service_step_preserves_blocked_result_without_case_claim(self):
        journey = Journey("local", "blocked-step")
        run_service_steps(journey, [
            ("gateway readiness", "gateway", lambda: "BLOCKED: gateway fixture is missing"),
        ])
        self.assertEqual(journey.steps[0].status, "BLOCKED")
        self.assertEqual(journey.result().case_run["claims"], [])

    def test_installed_cli_bin_is_added_to_service_path(self):
        with tempfile.TemporaryDirectory() as directory, patch("pathlib.Path.home", return_value=Path(directory)), \
                patch.dict("os.environ", {"PATH": "/usr/bin"}):
            configure_installed_cli_path()
            self.assertEqual(os.environ["PATH"].split(os.pathsep)[0], str(Path(directory) / ".local" / "bin"))

    def test_runner_writes_result_json_when_orchestration_raises(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "service-local.json"
            stdout = io.StringIO()
            with patch("service_runner.checks", side_effect=RuntimeError("simulated orchestration error")), \
                    patch("sys.argv", ["service_runner.py", "--mode", "local", "--output", str(output)]), \
                    redirect_stdout(stdout):
                status = service_main()
            result = json.loads(output.read_text())

        self.assertEqual(status, 1)
        self.assertEqual(result["steps"][0]["name"], "service runner")
        self.assertEqual(result["steps"][0]["status"], "FAIL")
        self.assertEqual(result["steps"][0]["message"], "simulated orchestration error")
        self.assertNotIn("Traceback", stdout.getvalue())

    def test_pre_service_failure_is_saved_with_step_and_message(self):
        with tempfile.TemporaryDirectory() as directory:
            result = record_runner_failure(Path(directory) / "service-local.json", "local",
                                           "install.sh: download returned HTTP 404")
        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.steps[0]["capability"], "journey.install")
        self.assertEqual(result.steps[0]["name"], "install.sh")
        self.assertEqual(result.steps[0]["message"], "download returned HTTP 404")

    def test_runner_startup_exception_writes_failure_result_without_traceback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            broken_runner = root / "broken_runner.py"
            broken_runner.write_text('raise RuntimeError("simulated import failure")\n')
            summary = root / "journey-summary.md"
            stderr = io.StringIO()
            with redirect_stderr(stderr):
                status = run_runner(broken_runner, summary, "local")
            result = load_results(root)[0]
        self.assertEqual(status, 1)
        self.assertEqual(result["steps"][0]["capability"], "journey.runner")
        self.assertEqual(result["steps"][0]["status"], "FAIL")
        self.assertEqual(result["steps"][0]["name"], "runner startup")
        self.assertIn("simulated import failure", result["steps"][0]["message"])
        self.assertNotIn("Traceback", stderr.getvalue())

    def test_runner_crash_is_labeled_separately_from_startup(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            crashing_runner = root / "crashing_runner.py"
            crashing_runner.write_text('def main():\n    raise RuntimeError("simulated run crash")\n')
            with redirect_stderr(io.StringIO()):
                status = run_runner(crashing_runner, root / "journey-summary.md", "local")
            result = load_results(root)[0]
        self.assertEqual(status, 1)
        self.assertEqual(result["steps"][0]["name"], "runner crash")
        self.assertIn("simulated run crash", result["steps"][0]["message"])

    def test_mcp_probe_sends_streamable_http_accept_and_parses_sse(self):
        import json
        tools = [{"name": name, "inputSchema": {"type": "object"}}
                 for name in ("kb-labs-commit__commit", "kb-labs-workflow__workflow_run", "kb-labs__mcp_diagnostics")]
        event = 'event: message\ndata: ' + json.dumps({"jsonrpc": "2.0", "id": 2, "result": {"tools": tools}}) + '\n\n'
        with patch("service_runner.request", return_value=(200, event, {})) as request:
            failure = mcp_probe("prj_test", "token")
        self.assertIsNone(failure)
        self.assertEqual(request.call_args.kwargs["accept"], "application/json, text/event-stream")

    def test_mcp_probe_uses_sanitized_platform_member_manifest_id(self):
        tools = [{"name": name, "inputSchema": {"type": "object"}}
                 for name in ("kb-labs-commit__commit", "workflow__workflow_run", "kb-labs__mcp_diagnostics")]
        with patch("service_runner.request", return_value=(200, {"result": {"tools": tools}}, {})):
            failure = mcp_probe("prj_test", "token")
        self.assertIn("kb-labs-workflow__workflow_run", failure)

    def test_prepare_mcp_projects_installs_commit_and_registers_project_b(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            home = root / "home"
            home.mkdir()
            project_a = root / "project-a"
            project_a.mkdir()
            subprocess.run(["git", "init", "-q", str(project_a)], check=True)
            (project_a / "note.txt").write_text("journey note\n")
            subprocess.run(["git", "-C", str(project_a), "add", "note.txt"], check=True)
            env = {"HOME": str(home)}
            calls = []
            registered_project_exists = []

            def fake_runner(command, **kwargs):
                calls.append(command)
                if command[:3] == ["kb", "project", "add"]:
                    registered_project_exists.append(
                        Path(kwargs["cwd"]).is_dir() and (Path(kwargs["cwd"]) / ".git").is_dir())
                    return subprocess.CompletedProcess(
                        command, 0, '{"data":{"projectId":"prj_b"}}', "")
                return subprocess.CompletedProcess(command, 0, "{}", "")

            with patch("service_runner._registry_version", return_value="2.120.1"):
                project_b_id = prepare_mcp_projects(str(project_a), "http://registry.test", env, fake_runner)
            project_id = "prj_" + hashlib.sha256(
                str(project_a.resolve()).encode("utf-8")).hexdigest()[:16]
            plan_path = home / ".kb" / "state" / project_id / "commit" / "plans" / "root" / "current" / "plan.json"
            plan = json.loads(plan_path.read_text())

        self.assertEqual(project_b_id, "prj_b")
        self.assertEqual(calls[0], ["kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1", "--json"])
        self.assertEqual(calls[1][0:3], ["kb", "project", "add"])
        self.assertEqual(registered_project_exists, [True])
        self.assertEqual(plan["gitStatus"]["staged"], ["note.txt"])
        self.assertEqual(plan["commits"][0]["files"], ["note.txt"])

    def test_mcp_initialize_checks_protocol_response(self):
        response = {"result": {"protocolVersion": "2025-03-26"}}
        with patch("service_runner.request", return_value=(200, response, {})) as request:
            self.assertIsNone(mcp_initialize("prj_test", "token"))
        self.assertEqual(request.call_args.kwargs["payload"]["method"], "initialize")
        with patch("service_runner.request", return_value=(200, {"result": {}}, {})):
            self.assertIn("no result", mcp_initialize("prj_test", "token"))

    def test_mcp_diagnostics_checks_clean_project_result(self):
        result = {"result": {"isError": False, "content": [{"text": "Project loaded cleanly"}]}}
        with patch("service_runner.request", return_value=(200, result, {})) as request:
            self.assertIsNone(mcp_diagnostics("prj_test", "token"))
        self.assertEqual(request.call_args.kwargs["payload"]["params"]["name"], "kb-labs__mcp_diagnostics")
        with patch("service_runner.request", return_value=(200, {"result": {"isError": True, "content": []}}, {})):
            self.assertIn("did not report", mcp_diagnostics("prj_test", "token"))

    def test_health_claim_requires_json_object(self):
        with patch("service_runner.request", return_value=(200, {"ready": True}, {})):
            self.assertIsNone(expect_json("/ready", "gateway readiness"))
        with patch("service_runner.request", return_value=(200, "ready", {})):
            self.assertIn("without a JSON object", expect_json("/ready", "gateway readiness"))

    # case: S-041.4
    def test_secured_auth_journey_preserves_session_until_logout_after_restart(self):
        calls = []
        state = {"admin_revoked": False}

        def fake_request(_base, path, *, method="GET", token=None, body=None, expected=200):
            calls.append((method, path, token, body, expected))
            if path == "/auth/permissions":
                return {"permissions": ["users:read"]}
            if path == "/auth/users" and expected == 200:
                return {"users": [{"email": "member@example.test", "userId": "member-1"}]}
            if path == "/auth/invites":
                return {"activationUrl": "http://localhost/activate/invite-token"}
            if path.endswith("password-reset-link"):
                return {"resetUrl": "http://localhost/reset-password/reset-token"}
            if path == "/auth/me" and token == "admin-access":
                self.assertEqual(expected, 401 if state["admin_revoked"] else 200)
                return {} if state["admin_revoked"] else {"email": "admin@example.test"}
            if path == "/auth/me" and token == "fresh-admin-token":
                self.assertEqual(expected, 200)
                return {"email": "admin@example.test"}
            if path == "/auth/logout/cli" and body == {"refreshToken": "admin-refresh"}:
                state["admin_revoked"] = True
                return {"ok": True}
            if path == "/auth/refresh/cli" and body == {"refreshToken": "admin-refresh"}:
                return {"error": "invalid_session"}
            return {}

        logins = iter([
            ("admin-access", "admin-refresh"),
            ("member-access", "member-refresh"),
            ("member-replacement-access", "member-replacement-refresh"),
            ("fresh-admin-token", "fresh-admin-refresh"),
        ])

        def fake_login(_base, email, password):
            calls.append(("LOGIN", email, password, None, None))
            return next(logins)

        def restart_host():
            calls.append(("RESTART", "", None, None, None))
            return "http://gateway.test/restarted"

        with patch("steps.auth._request", side_effect=fake_request), \
             patch("steps.auth._login", side_effect=fake_login):
            run_secured_auth_steps("http://gateway.test", "admin@example.test", "secret",
                                   "member@example.test", restart_host)

        restart = calls.index(("RESTART", "", None, None, None))
        restart_check = calls.index(("GET", "/auth/me", "admin-access", None, 200), restart)
        logout = calls.index(("POST", "/auth/logout/cli", None, {"refreshToken": "admin-refresh"}, 200))
        revoked_check = calls.index(("GET", "/auth/me", "admin-access", None, 401))
        fresh_login = calls.index(("LOGIN", "admin@example.test", "secret", None, None), restart)
        fresh_check = calls.index(("GET", "/auth/me", "fresh-admin-token", None, 200))
        self.assertLess(restart_check, logout)
        self.assertLess(logout, revoked_check)
        self.assertLess(revoked_check, fresh_login)
        self.assertLess(fresh_login, fresh_check)

    def test_dockerfile_copies_modules_imported_by_journey_scripts_together(self):
        root = Path(__file__).resolve().parents[2]
        journey_dir = Path(__file__).resolve().parent
        copied_into_package = set()
        wrapper_copied = False
        for line in (journey_dir / "Dockerfile.runner").read_text().splitlines():
            if not line.lstrip().startswith("COPY "):
                continue
            tokens = shlex.split(line)
            destination = tokens[-1]
            for source in tokens[1:-1]:
                for path in root.glob(source):
                    target = Path(destination)
                    if destination.endswith("/"):
                        target /= path.name
                    if str(target) == "/usr/local/bin/kb-journey-runner" and path.name == "entrypoint.py":
                        wrapper_copied = True
                    if str(target.parent) == "/opt/kb-journey":
                        copied_into_package.add(path.name)

        local_modules = {path.stem for path in journey_dir.glob("*.py")}
        imported_modules = set()
        for filename in ("runner.py", "service_runner.py", "studio_steps.py"):
            tree = ast.parse((journey_dir / filename).read_text(), filename=filename)
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    imported_modules.update(alias.name.split(".", 1)[0] for alias in node.names)
                elif isinstance(node, ast.ImportFrom) and node.module:
                    imported_modules.add(node.module.split(".", 1)[0])
        required_modules = {f"{name}.py" for name in imported_modules & local_modules}
        required_modules.update({"runner.py", "service_runner.py", "studio_steps.py"})
        self.assertTrue(wrapper_copied, "Dockerfile.runner must install entrypoint.py as kb-journey-runner")
        self.assertTrue(required_modules <= copied_into_package,
                        f"Dockerfile.runner misses journey modules: {sorted(required_modules - copied_into_package)}")
        self.assertIn("COPY e2e/journey/steps /opt/kb-journey/steps",
                      (journey_dir / "Dockerfile.runner").read_text(),
                      "Dockerfile.runner must copy the auth step package imported by runner.py")

    def test_aggregate_compares_previous_status_and_uses_stable_fingerprint(self):
        bad = {"capability": "workflow", "mode": "local", "name": "run", "status": "FAIL",
               "message": "HTTP 404 at host A", "duration_seconds": 1}
        report = aggregate([{"steps": [bad]}])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(fingerprint(bad), fingerprint({**bad, "message": "HTTP 500 at host B"}))
        good = {**bad, "status": "PASS", "message": "passed"}
        next_report = aggregate([{"steps": [good]}], [{"steps": [bad]}])
        self.assertEqual(next_report["delta"]["fixed"], ["workflow:local"])

    def test_yesterday_today_fixtures_report_fixed_and_new_failures(self):
        fixtures = Path(__file__).parent / "fixtures" / "nightly"
        report = aggregate(load_results(fixtures / "today"), load_results(fixtures / "yesterday"))
        self.assertEqual(report["delta"]["fixed"], ["workflow:local"])
        self.assertEqual(report["delta"]["new"], ["mcp:local"])
        self.assertEqual(report["status"], "FAIL")

    def test_empty_results_cannot_pass(self):
        report = aggregate([])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["capabilities"][0]["first_failure"]["name"], "result files")

    def test_release_case_coverage_is_included_in_nightly_status(self):
        capability_result = {"steps": [{"capability": "gateway", "mode": "local", "name": "ready",
                                        "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        coverage = {"schema": "kb.qa-case-coverage/v1", "mode": "local", "releasePassed": False,
                    "cases": [{"id": "S-045", "release": True, "status": "active", "statusResult": "NOT COVERED"}]}
        report = aggregate([capability_result], case_coverage=[coverage])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["case_coverage"], [coverage])
        self.assertIn("Case coverage (local): **FAIL**", render_summary(report))

    def test_result_with_no_capability_steps_cannot_pass(self):
        report = aggregate([{"steps": []}])
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(report["capabilities"][0]["first_failure"]["name"], "capability results")

    def test_profile_missing_declared_capabilities_is_blocked(self):
        result = {"steps": [{"capability": "gateway", "mode": "local", "name": "ready",
                            "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        report = aggregate([result], profiles={"local": {"gateway", "workflow"}})
        workflow = next(item for item in report["capabilities"] if item["capability"] == "workflow")
        self.assertEqual(workflow["status"], "BLOCKED")
        self.assertIn("profile local declares workflow", workflow["first_failure"]["message"])
        self.assertEqual(report["status"], "BLOCKED")

    def test_profile_passes_only_when_each_declared_capability_passes(self):
        steps = [{"capability": capability, "mode": "local", "name": capability,
                  "status": "PASS", "duration_seconds": 0, "message": "passed"}
                 for capability in ("gateway", "workflow")]
        report = aggregate([{"steps": steps}], profiles={"local": {"gateway", "workflow"}})
        self.assertEqual(report["status"], "PASS")
        self.assertEqual({item["capability"] for item in report["capabilities"]}, {"gateway", "workflow"})

    def test_nonzero_journey_exit_fails_even_when_result_steps_pass(self):
        result = {"steps": [{"capability": "gateway", "mode": "secured", "name": "ready",
                            "status": "PASS", "duration_seconds": 0, "message": "passed"}]}
        report = aggregate([result], journey_exits={"secured": 7})
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(failure["first_failure"]["name"], "journey process exit")
        self.assertIn("status 7", failure["first_failure"]["message"])

    def test_unparseable_result_file_is_reported_as_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text("{broken")
            report = aggregate(load_results(directory))
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertEqual(failure["first_failure"]["name"], "result file parse")
        self.assertIn("service-local.json", failure["first_failure"]["message"])

    def test_result_loader_skips_case_coverage_documents(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text('{"steps":[]}')
            Path(directory, "case-coverage-local.json").write_text('{"schema":"kb.qa-case-coverage/v1"}')
            Path(directory, "report.json").write_text('{"schema":"kb.nightly-report/v1"}')
            results = load_results(directory)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0], {"steps": []})

    def test_invalid_step_status_cannot_be_treated_as_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "service-local.json").write_text(
                '{"steps":[{"capability":"workflow","mode":"local","name":"run",'
                '"status":"SKIP","message":"missing"}]}'
            )
            report = aggregate(load_results(directory))
        failure = next(item for item in report["capabilities"] if item["capability"] == "journey.runner")
        self.assertEqual(report["status"], "FAIL")
        self.assertIn("status must be", failure["first_failure"]["message"])

    def test_declared_profile_capabilities_match_service_checks(self):
        for mode, auth_capability in (("local", "auth.local"), ("secured", "auth.invite-reset")):
            self.assertEqual(declared_capabilities(mode),
                             {capability for _, capability, _ in checks(mode)}
                             | {auth_capability, "plugin-author"})

    def test_issue_sync_is_idempotent_updates_existing_and_closes_fixed(self):
        client = FakeGitHub()
        failure = {"capability": "mcp", "mode": "secured", "name": "tools/list",
                   "status": "FAIL", "message": "HTTP 404", "duration_seconds": 2}
        report = aggregate([{"steps": [failure]}])
        first = sync_issues(client, report, 10)
        self.assertEqual(len(first["created"]), 1)
        self.assertEqual(len(client.issues), 2)
        second = sync_issues(client, report, 10)
        self.assertEqual(second["updated"], first["created"])
        self.assertEqual(len(client.issues), 2)
        green = aggregate([{"steps": [{**failure, "status": "PASS", "message": "passed"}]}])
        closed = sync_issues(client, green, 10)
        self.assertEqual(len(closed["closed"]), 1)
        self.assertEqual(client.issues[1]["state"], "closed")

    def test_issue_sync_creates_one_pinned_status_issue_when_missing(self):
        client = FakeGitHub()
        client.issues.clear()
        passed = {"capability": "gateway", "mode": "local", "name": "ready",
                  "status": "PASS", "message": "passed", "duration_seconds": 0}
        changes = sync_issues(client, aggregate([{"steps": [passed]}]), 0)
        self.assertEqual(len(client.issues), 1)
        self.assertIn("nightly-status", client.issues[0]["labels"])
        self.assertEqual(changes, {"created": [], "updated": [], "closed": []})

    def test_required_mode_steps_and_workflow_fixture_are_explicit(self):
        local = {capability for _, capability, _ in checks("local")}
        secured = {capability for _, capability, _ in checks("secured")}
        self.assertNotIn("auth.invites", local | secured)
        self.assertNotIn("scaffold", local | secured)
        with patch("service_runner.request", return_value=(200, {"data": {"workflows": []}}, {})):
            failure = discover_workflow("prj_fixture", "")
        self.assertIn("e2e-hello fixture", failure)


if __name__ == "__main__":
    unittest.main()
