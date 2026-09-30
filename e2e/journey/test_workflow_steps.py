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
    def test_workflow_and_cron_cases_assert_outcomes_and_claim_steps(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            (project / ".kb").mkdir()
            clock = [0.0]
            run_list_calls = {"journey-hello": 0, "journey-fails": 0, "journey-cron": 0}
            cron_fired = [False]

            def fake_sleep(seconds):
                clock[0] += seconds

            def fake_run(args, *, env, cwd, timeout=120):
                args = list(args)
                output = {}
                if args[:2] == ["workflow", "run"]:
                    run_id = "ok-run" if any(arg.startswith("--workflow-id=journey-hello") for arg in args) else "failed-run"
                    output = {"ok": True, "data": {"runId": run_id, "status": "queued"}}
                elif args[:3] == ["workflow", "runs", "view"]:
                    run_id = args[3]
                    status = "success" if run_id in {"ok-run", "cron-run"} else "failed"
                    output = {"ok": True, "data": {
                        "id": run_id,
                        "status": status,
                        "inputs": {"message": "hello"} if run_id == "ok-run" else {},
                        "jobs": [{"steps": [{"id": "deliberate-failure", "status": "failed",
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
                    workflow = next(arg.split("=", 1)[1] for arg in args if arg.startswith("--workflow="))
                    run_list_calls[workflow] += 1
                    if workflow == "journey-hello":
                        runs = [{"id": "ok-run", "status": "success", "createdAt": "now"}]
                    elif workflow == "journey-fails":
                        runs = [{"id": "failed-run", "status": "failed", "createdAt": "now"}]
                    else:
                        schedule_file = cwd / ".kb" / "jobs" / "journey-cron.yml"
                        enabled = schedule_file.exists() and "enabled: true" in schedule_file.read_text()
                        if enabled:
                            cron_fired[0] = True
                        if cron_fired[0]:
                            runs = [{"id": "cron-run", "status": "success",
                                     "trigger": {"type": "schedule"}}]
                        else:
                            runs = []
                    output = {"ok": True, "data": runs}
                elif args[:2] == ["workflow", "list"]:
                    schedule_file = cwd / ".kb" / "jobs" / "journey-cron.yml"
                    enabled = schedule_file.exists() and "enabled: true" in schedule_file.read_text()
                    exists = schedule_file.exists()
                    output = {"ok": True, "data": {"crons": [{
                        "id": "user:journey-cron", "enabled": enabled, "schedule": "* * * * *",
                    }] if exists else []}}
                elif args[:2] == ["workflow", "lint"]:
                    output = {"ok": True, "data": {"valid": True}}
                elif args[:3] == ["workflow", "defs", "list"]:
                    output = {"ok": True, "data": [{"id": "journey-hello"}, {"id": "journey-fails"}]}
                else:
                    self.fail(f"unexpected CLI command: {args!r}")
                return CompletedProcess(args, 0, json.dumps(output))

            old_run_cli = workflow_steps.run_cli
            workflow_steps.run_cli = fake_run
            try:
                journey = Journey("local", "workflow-fixture")
                workflow_steps.run_workflow_journey(
                    journey,
                    env={"HOME": directory},
                    project=project,
                    clock=lambda: clock[0],
                    sleep=fake_sleep,
                )
            finally:
                workflow_steps.run_cli = old_run_cli

        result = journey.result()
        self.assertEqual(result.status, "PASS", result.first_failure)
        claims = {claim["claimId"] for claim in result.case_run["claims"]}
        self.assertEqual(claims, {
            "S-007.1", "S-007.2", "S-007.3", "S-009.1", "S-009.2", "S-009.3", "S-009.4",
            "S-029.1", "S-029.2", "S-029.3", "S-044.1", "S-044.2", "S-044.3", "S-044.4",
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
