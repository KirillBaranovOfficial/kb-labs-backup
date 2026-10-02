import json
import sys
import tempfile
import unittest
from pathlib import Path
from subprocess import CompletedProcess

sys.path.insert(0, str(Path(__file__).parent))

import workflow_steps
from service_steps import Journey


class WorkflowJourneyTests(unittest.TestCase):
    def test_secured_plugin_commands_are_gated_and_admin_login_recovers(self):
        authenticated = [False]
        calls = []
        call_homes = []
        call_envs = []

        def fake_run(args, *, env, cwd, timeout=120):
            args = list(args)
            calls.append(args)
            call_homes.append(env["HOME"])
            call_envs.append({key: env.get(key) for key in ("KB_JOURNEY_CLI_PATH", "KB_PLATFORM_ROOT", "KB_PROJECT_ROOT")})
            if args == ["auth", "status", "--json"]:
                return CompletedProcess(args, 0, json.dumps({
                    "ok": True, "authenticated": False, "session": {"authenticated": False},
                }))
            if args in (["auth", "login", "--help"], ["version"], ["health", "--json"]):
                return CompletedProcess(args, 0, json.dumps({"ok": True}))
            if args[:3] in (["config", "show", "--json"], ["workflow", "defs", "list"],
                            ["marketplace", "plugins", "list"], ["qa", "cases", "validate"]):
                if not authenticated[0]:
                    return CompletedProcess(args, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_AUTH_REQUIRED", "message": "Authentication is required.",
                        "hint": 'Run "kb auth login".',
                    }}))
                return CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"id": "hello"}}))
            if args[:2] == ["auth", "login"]:
                self.assertIn("admin@example.test", args)
                self.assertIn("admin-password", args)
                authenticated[0] = True
                return CompletedProcess(args, 0, "Logged in")
            self.fail(f"unexpected CLI command: {args!r}")

        old_run_cli = workflow_steps.run_cli
        workflow_steps.run_cli = fake_run
        try:
            with tempfile.TemporaryDirectory() as home:
                shared_home = home
                journey = Journey("secured", "workflow-auth")
                workflow_steps.run_secured_cli_auth_step(
                    journey,
                    env={"HOME": home,
                         "KB_JOURNEY_CLI_PATH": "/installed/bin/kb",
                         "KB_JOURNEY_PLATFORM_ROOT": "/installed/platform",
                         "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                         "KB_JOURNEY_ADMIN_PASSWORD": "admin-password"},
                    project=Path("/tmp/project"),
                )
        finally:
            workflow_steps.run_cli = old_run_cli

        result = journey.result()
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(calls[:4], [["auth", "status", "--json"], ["auth", "login", "--help"], ["version"], ["health", "--json"]])
        self.assertEqual(calls[4:8], [
            ["config", "show", "--json"],
            ["workflow", "defs", "list", "--json"],
            ["marketplace", "plugins", "list", "--json"],
            ["qa", "cases", "validate", "--json"],
        ])
        self.assertEqual(calls[8][:2], ["auth", "login"])
        self.assertEqual(calls[9:], calls[4:8])
        self.assertTrue(all(call_home != shared_home for call_home in call_homes[:8]))
        self.assertTrue(all(call_home == shared_home for call_home in call_homes[8:]))
        self.assertTrue(all(call_env["KB_JOURNEY_CLI_PATH"] == "/installed/bin/kb" for call_env in call_envs[:8]))
        self.assertTrue(all(call_env["KB_PLATFORM_ROOT"] == "/installed/platform"
                            and call_env["KB_PROJECT_ROOT"] == "/tmp/project" for call_env in call_envs[:8]))
        self.assertEqual(result.case_run["claims"], [
            {"claimId": "S-007.4", "kind": "journey", "status": "PASS"},
            {"claimId": "S-041.12", "kind": "journey", "status": "PASS"},
            {"claimId": "S-041.7", "kind": "journey", "status": "PASS"},
            {"claimId": "S-041.8", "kind": "journey", "status": "PASS"},
        ])

    def test_workflow_and_cron_cases_assert_outcomes_and_claim_steps(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            (project / ".kb").mkdir()
            clock = [0.0]
            run_list_calls = {"journey-hello": 0, "journey-fails": 0, "journey-cron-target": 0}
            cron_fired = [False]
            restart_calls = []
            marketplace_install_calls = []

            def fake_sleep(seconds):
                clock[0] += seconds

            def fake_run(args, *, env, cwd, timeout=120):
                args = list(args)
                output = {}
                if args[:2] == ["marketplace", "install"]:
                    marketplace_install_calls.append(args)
                    output = {"ok": True, "data": {"installed": [{"id": "@kb-labs/steward-entry"}]}}
                elif args[:2] == ["workflow", "run"]:
                    run_id = "ok-run" if any(arg.startswith("--workflow-id=journey-hello") for arg in args) else "failed-run"
                    output = {"ok": True, "data": {"runId": run_id, "status": "queued"}}
                elif args[:3] == ["workflow", "runs", "view"]:
                    run_id = args[3]
                    status = "success" if run_id in {"ok-run", "cron-run"} else "failed"
                    output = {"ok": True, "data": {
                        "id": run_id,
                        "status": status,
                        "trigger": {"type": "schedule", "metadata": {"cronId": "journey-cron"}}
                        if run_id == "cron-run" else {"type": "manual"},
                        "metadata": {"cronId": "journey-cron"} if run_id == "cron-run" else {},
                        "inputs": {"message": "hello"} if run_id == "ok-run" else {},
                        "jobs": [{"steps": [{"id": "failed-run:smoke:0", "name": "Deliberate failure",
                                              "spec": {"id": "deliberate-failure"}, "status": "failed",
                                              "error": {"message": "journey-failure-diagnostic"}}]}]
                        if run_id == "failed-run" else [],
                    }}
                elif args[:3] == ["workflow", "runs", "logs"]:
                    run_id = args[3]
                    message = "hello" if run_id == "ok-run" else "journey-failure-diagnostic"
                    if run_id == "cron-run":
                        message = "journey-cron-fired"
                    output = {"ok": True, "data": {"logs": [{"message": message}]}}
                elif args[:3] == ["workflow", "runs", "list"]:
                    workflow_arg = next((arg.split("=", 1)[1] for arg in args if arg.startswith("--workflow=")), None)
                    if workflow_arg is None:
                        runs = [
                            {"id": "ok-run", "status": "success", "createdAt": "now"},
                            {"id": "failed-run", "status": "failed", "createdAt": "now"},
                        ]
                    elif workflow_arg == "journey-hello":
                        run_list_calls[workflow_arg] += 1
                        runs = [{"id": "ok-run", "status": "success", "createdAt": "now"}]
                    elif workflow_arg == "journey-fails":
                        run_list_calls[workflow_arg] += 1
                        runs = [{"id": "failed-run", "status": "failed", "createdAt": "now"}]
                    else:
                        run_list_calls[workflow_arg] += 1
                        schedule_file = cwd / ".kb" / "crons" / "journey-cron.yml"
                        enabled = schedule_file.exists() and "enabled: true" in schedule_file.read_text()
                        if enabled:
                            cron_fired[0] = True
                        if cron_fired[0]:
                            runs = [{"id": "cron-run", "status": "success",
                                     "trigger": {"type": "schedule", "metadata": {"cronId": "journey-cron"}}}]
                        else:
                            runs = []
                    output = {"ok": True, "data": runs}
                elif args[:3] == ["workflow", "crons", "list"]:
                    schedule_file = cwd / ".kb" / "crons" / "journey-cron.yml"
                    enabled = schedule_file.exists() and "enabled: true" in schedule_file.read_text()
                    exists = schedule_file.exists()
                    content = schedule_file.read_text() if exists else ""
                    errors = []
                    if "not a cron" in content:
                        errors = [".kb/crons/journey-cron.yml: invalid cron expression"]
                    elif "missing-workflow" in content:
                        errors = [".kb/crons/journey-cron.yml: unknown workflow missing-workflow"]
                    output = {"ok": True, "data": {"crons": [{
                        "id": "journey-cron", "source": "user", "enabled": enabled,
                        "schedule": "* * * * *", "target": {"workflow": "journey-cron-target"},
                        "input": {"message": "journey-cron-fired"},
                        "nextFireAt": "next",
                    }, {"id": "plugin:test:read-only", "source": "plugin", "enabled": True,
                        "schedule": "0 0 * * *", "target": {"handler": "test:cleanup"}}
                    ] if exists and not errors else [{"id": "plugin:test:read-only", "source": "plugin",
                        "enabled": True, "schedule": "0 0 * * *", "target": {"handler": "test:cleanup"}}], "errors": errors}}
                elif args[:3] == ["workflow", "crons", "disable"]:
                    schedule_file = cwd / ".kb" / "crons" / "journey-cron.yml"
                    schedule_file.write_text(schedule_file.read_text().replace("enabled: true", "enabled: false"))
                    output = {"ok": True, "data": {"enabled": False}}
                elif args[:3] == ["workflow", "crons", "enable"]:
                    if args[3].startswith("plugin:"):
                        return CompletedProcess(args, 1, "CronMutationRefused: plugin crons are read-only")
                    schedule_file = cwd / ".kb" / "crons" / "journey-cron.yml"
                    schedule_file.write_text(schedule_file.read_text().replace("enabled: false", "enabled: true"))
                    output = {"ok": True, "data": {"enabled": True}}
                elif args[:2] == ["workflow", "lint"]:
                    output = {"ok": True, "data": {"valid": True}}
                elif args[:3] == ["workflow", "defs", "list"]:
                    output = {"ok": True, "data": [{"id": "journey-hello"}, {"id": "journey-fails"},
                                                       {"id": "journey-cron-target"}, {"id": "hello"}]}
                else:
                    self.fail(f"unexpected CLI command: {args!r}")
                return CompletedProcess(args, 0, json.dumps(output))

            old_run_cli = workflow_steps.run_cli
            workflow_steps.run_cli = fake_run
            try:
                journey = Journey("local", "workflow-fixture")
                journey_env = {"HOME": directory}
                workflow_steps.run_workflow_journey(
                    journey,
                    env=journey_env,
                    project=project,
                    restart_host=lambda: restart_calls.append("kb-create restart --platform-root <platform>"),
                    clock=lambda: clock[0],
                    sleep=fake_sleep,
                )
            finally:
                workflow_steps.run_cli = old_run_cli

        self.assertEqual(journey_env.get("KB_STUDIO_DIAGNOSTIC_RUN_ID"), "failed-run")

        result = journey.result()
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual(marketplace_install_calls, [["marketplace", "install", "@kb-labs/steward-entry", "--json"]])
        self.assertEqual(restart_calls, [
            "kb-create restart --platform-root <platform>",
            "kb-create restart --platform-root <platform>",
        ])
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(claims, {
            "S-006.3", "S-007.1", "S-007.2", "S-007.3", "S-009.1", "S-009.2", "S-009.3", "S-009.4", "PC-002.5",
            "S-029.1", "S-029.2", "S-029.3", "S-044.1", "S-044.2", "S-044.3", "S-044.4",
            "S-044.5", "S-044.6", "S-044.7", "S-044.8",
            "S-056.1", "S-056.2", "S-056.3",
        })

        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({
                "steps": [],
                "case_run": {"schema": "kb.qa-case-run/v1", "runId": "base", "mode": "local",
                             "claims": [{"claimId": "S-001.1", "kind": "journey", "status": "PASS"}]},
            }))
            merged = workflow_steps.merge_workflow_result(report, result.__dict__)
            merged_ids = {claim["claimId"] for claim in merged["case_run"]["claims"]}
            self.assertEqual(merged_ids, claims | {"S-001.1"})


if __name__ == "__main__":
    unittest.main()
