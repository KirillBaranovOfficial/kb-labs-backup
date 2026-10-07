from __future__ import annotations

import subprocess
import tempfile
import unittest
import json
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

from installer_case_steps import (_seed_doctor_fixture, assert_default_service_ports_free,
                                  cleanup_installer_fixture_root,
                                  run_doctor_root_isolation_step,
                                  run_analytics_invalid_retention_step, run_installer_case_steps,
                                  run_offline_plan_step, run_repeat_plan_readonly_step,
                                  run_platform_cli, run_secondary_root_gateway_isolation_step,
                                  run_gateway_allocation_step,
                                  assert_root_offset, run_json_install_project_step,
                                  run_uninstall_decline_step, _service_ids_match,
                                  _snapshot_installation_owned_tree)
from journey_resources import case_catalog_root
from service_steps import Journey, record_candidate_provenance
from service_port_assertions import assert_service_ports_preserved


class InstallerCaseStepsTest(unittest.TestCase):
    def test_root_offset_assertion_checks_services_and_transport_urls(self):
        plan = {"serviceGraph": {"services": [{"id": "gateway", "port": 4100},
                                               {"id": "marketplace", "port": 5170}]},
                "configPatches": [{"path": "/platform/adapterOptions", "json": json.dumps({
                    "serviceTransport": {"services": {
                        "gateway": {"url": "http://127.0.0.1:4100"},
                        "marketplace": {"url": "http://127.0.0.1:5170"},
                    }},
                })}]}
        assert_root_offset(plan, {"gateway": 4000, "marketplace": 5070},
                           {"gateway": 4000, "marketplace": 5070}, 100, "secondary root")
        plan["serviceGraph"]["services"][1]["port"] = 5270
        with self.assertRaisesRegex(RuntimeError, "service ports"):
            assert_root_offset(plan, {"gateway": 4000, "marketplace": 5070},
                               {"gateway": 4000, "marketplace": 5070}, 100, "secondary root")

    # case: PC-001.13, PC-001.14, PC-001.15
    def test_lifecycle_port_assertion_rejects_service_or_transport_drift(self):
        before = {"serviceGraph": {"services": [{"id": "gateway", "port": 4100},
                                                {"id": "marketplace", "port": 5170}]},
                  "configPatches": [{"path": "/platform/adapterOptions", "json": json.dumps({
                      "serviceTransport": {"services": {
                          "gateway": {"url": "http://127.0.0.1:4100"},
                          "marketplace": {"url": "http://127.0.0.1:5170"},
                      }},
                  })}]}
        assert_service_ports_preserved(before, before, "update")
        changed_service = deepcopy(before)
        changed_service["serviceGraph"]["services"][1]["port"] += 1
        with self.assertRaisesRegex(RuntimeError, "service graph ports"):
            assert_service_ports_preserved(before, changed_service, "rollback")
        changed_transport = deepcopy(before)
        transport = json.loads(changed_transport["configPatches"][0]["json"])
        transport["serviceTransport"]["services"]["marketplace"]["url"] = "http://127.0.0.1:5171"
        changed_transport["configPatches"][0]["json"] = json.dumps(transport)
        with self.assertRaisesRegex(RuntimeError, "service transport ports"):
            assert_service_ports_preserved(before, changed_transport, "restart")

    def test_json_custom_scenario_install_claim_checks_project_registry_and_cleans_host(self):
        with tempfile.TemporaryDirectory(prefix="journey-json-install-project-") as temp:
            root = Path(temp)
            platform = root / "platform"
            project = root / "project"
            index = root / "index.json"
            index.write_text("{}")
            calls = []

            def run_command(args):
                calls.append(args)
                if args[1] == "install":
                    (platform / ".kb/v2/bin").mkdir(parents=True)
                    (platform / ".kb/v2/receipt.json").write_text("{}")
                    (platform / ".kb/v2/bin/kb").write_text("fixture")
                    return subprocess.CompletedProcess(args, 0, '{"ok":true,"data":{"operation":"install"}}', "")
                if args[1] == "project":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "project": {"id": "prj_test", "path": str(project.resolve())}}}), "")
                if args[1] == "status":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "host": {"state": "running"}}}), "")
                return subprocess.CompletedProcess(args, 0, '{"ok":true,"data":{}}', "")

            journey = run_json_install_project_step(mode="local", launcher="kb-create", stable_index=index,
                                                    platform_root=platform, project_root=project,
                                                    run_command=run_command)
            self.assertEqual(journey.steps[0].cases, ["S-187.6"])
            self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
            install_args = calls[0]
            self.assertIn("--json", install_args)
            self.assertEqual(install_args[install_args.index("--scenario") + 1], "custom")
            self.assertEqual(install_args[install_args.index("--project-root") + 1], str(project))
            self.assertEqual([call[1] for call in calls[1:]], ["project", "status", "stop", "uninstall"])

    def test_installer_fixture_cleanup_stops_running_host_before_removing_root(self):
        with tempfile.TemporaryDirectory(prefix="journey-installer-cleanup-") as temp:
            root = Path(temp) / "platform"
            receipt = root / ".kb/v2/receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text("{}")
            calls = []

            def run_command(args):
                calls.append(args[1])
                payload = {"host": {"state": "running"}} if args[1] == "status" else {}
                return subprocess.CompletedProcess(args, 0,
                    json.dumps({"ok": True, "data": payload}), "")

            cleanup_installer_fixture_root(root, "kb-create", run_command)

        self.assertEqual(calls, ["status", "stop", "uninstall"])
        self.assertFalse(root.exists())

    def test_default_service_port_check_names_leftover_listener_owner(self):
        with tempfile.TemporaryDirectory(prefix="journey-default-port-check-") as temp:
            root = Path(temp)
            receipt_path = root / ".kb/v2/receipt.json"
            receipt_path.parent.mkdir(parents=True)
            receipt_path.write_text(json.dumps({"plan": {
                "host": {"healthUrl": "http://127.0.0.1:4000/health"},
                "serviceGraph": {"services": [{"id": "gateway", "port": 4000},
                                                {"id": "marketplace", "port": 5070}]},
            }}))

            calls = []

            def lsof(args):
                calls.append(args)
                if "-iTCP:4000" in args:
                    return subprocess.CompletedProcess(args, 0, "p363\ncMainThread\n", "")
                return subprocess.CompletedProcess(args, 1, "", "")

            with self.assertRaisesRegex(RuntimeError, r"port 4000 .* pid 363 \(MainThread\)"):
                assert_default_service_ports_free(root, lsof)
            self.assertEqual(len(calls), 2)
            self.assertIn("-iTCP:4000", calls[0])

    def test_offline_plan_step_primes_and_uses_its_own_root_cache(self):
        with tempfile.TemporaryDirectory(prefix="journey-offline-plan-") as temp:
            root = Path(temp)
            index = root / "release-index.json"
            index.write_text("{}")
            calls = []

            def run_command(args):
                calls.append(args)
                if args[1] == "status":
                    payload = {"ok": True, "data": {"host": {"state": "stopped"},
                              "running": False, "reachable": False}}
                elif args[1] == "apply":
                    payload = {"ok": True, "data": {"operation": "apply"}}
                else:
                    payload = {"ok": True, "data": {"plan": {"request": {"source": "offline"},
                              "artifacts": [{"resolvedFrom": "cache"}]}}}
                return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

            journey = run_offline_plan_step(mode="local", launcher="kb-create", stable_index=index,
                                            platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["S-181.4"])
        self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
        self.assertEqual([call[1] for call in calls], ["status", "apply", "plan"])
        self.assertNotIn("--offline", calls[1])
        self.assertIn("--offline", calls[2])
        cache_root = root.parent / "s181-offline-root"
        self.assertEqual(calls[1][calls[1].index("--request-platform-root") + 1], str(cache_root))
        self.assertEqual(calls[2][calls[2].index("--request-platform-root") + 1], str(cache_root))

    def test_platform_cli_resolves_the_requested_roots_executable(self):
        with tempfile.TemporaryDirectory(prefix="journey-platform-cli-") as temp:
            root = Path(temp)
            executable = root / ".kb/v2/bin/kb"
            executable.parent.mkdir(parents=True)
            executable.touch()
            calls = []
            run_platform_cli(root, ["adapter", "list", "--json"],
                             lambda args: calls.append(args) or subprocess.CompletedProcess(args, 0, "", ""))

        self.assertEqual(calls, [[str(executable), "adapter", "list", "--json"]])

    def test_secondary_root_gateway_isolation_checks_running_and_stopped_legs(self):
        with tempfile.TemporaryDirectory(prefix="journey-secondary-gateway-") as temp:
            parent = Path(temp)
            primary, secondary = parent / "root-a", parent / "root-b"
            running = {primary: False, secondary: False}
            for root, installation, port in ((primary, "installation-a", 4000),
                                             (secondary, "installation-b", 4100)):
                (root / ".kb/v2/bin").mkdir(parents=True)
                (root / ".kb/v2/bin/kb").touch()
                (root / ".kb/v2/receipt.json").write_text(json.dumps({
                    "installationId": installation,
                    "verification": {"hostHealthUrl": f"http://127.0.0.1:{port}/health"},
                }))
            calls = []

            def run_command(args):
                calls.append(args)
                if args[0] == "kb-create":
                    command = args[1]
                    root = Path(args[args.index("--platform-root") + 1])
                    if command == "status":
                        is_running = running[root]
                        data = {"host": {"state": "running" if is_running else "stopped"},
                                "running": is_running, "reachable": is_running}
                    elif command == "start":
                        running[root] = True
                        data = {"operation": "start"}
                    else:
                        running[root] = False
                        data = {"operation": "stop"}
                    return subprocess.CompletedProcess(args, 0,
                        json.dumps({"ok": True, "data": data}), "")
                root = secondary
                if running[root]:
                    payload = {"ok": True, "data": {"adapters": [{"id": "analytics-sqlite"}]}}
                    return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")
                payload = {"ok": False, "error": {
                    "code": "KB_HOST_UNREACHABLE", "message": "secondary Gateway is stopped",
                    "hint": f'Run "kb-create start --platform-root {secondary}" and retry the command.',
                    "details": {"address": "http://127.0.0.1:4100"},
                }}
                return subprocess.CompletedProcess(args, 1, json.dumps(payload), "")

            journey = run_secondary_root_gateway_isolation_step(
                mode="local", launcher="kb-create", primary_root=primary,
                secondary_root=secondary, run_command=run_command,
            )

        self.assertEqual(journey.steps[0].cases, ["PC-001.12"])
        self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
        self.assertFalse(running[primary])
        self.assertFalse(running[secondary])
        adapter_calls = [args for args in calls if args[0].endswith("/.kb/v2/bin/kb")]
        self.assertEqual(len(adapter_calls), 2)

    def test_gateway_allocation_step_checks_default_allocation_and_explicit_override(self):
        with tempfile.TemporaryDirectory(prefix="journey-gateway-allocation-") as temp:
            parent = Path(temp)
            primary, automatic, explicit = parent / "root-a", parent / "root-b", parent / "root-c"
            index = parent / "release-index.json"
            index.write_text("{}")
            for root, port, service_port in ((primary, 4000, 5070), (automatic, 4100, 5170)):
                (root / ".kb/v2").mkdir(parents=True)
                (root / ".kb/v2/receipt.json").write_text(json.dumps({
                    "verification": {"hostHealthUrl": f"http://127.0.0.1:{port}/health"},
                    "plan": {"serviceGraph": {"services": [
                        {"id": "gateway", "port": port}, {"id": "marketplace", "port": service_port},
                    ]}, "configPatches": [{"path": "/platform/adapterOptions", "json": json.dumps({
                        "serviceTransport": {"services": {
                            "gateway": {"url": f"http://127.0.0.1:{port}"},
                            "marketplace": {"url": f"http://127.0.0.1:{service_port}"},
                        }},
                    })}]},
                }))
            calls = []

            def run_command(args):
                calls.append(args)
                command = args[1]
                if command == "status":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "host": {"state": "running"},
                    }}), "")
                if command == "stop":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {}}), "")
                if command == "uninstall":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {}}), "")
                if command == "plan":
                    root = Path(args[args.index("--request-platform-root") + 1])
                    port = 4300 if root.name == "root-d" else 4100
                    payload = {"plan": {"request": {"gatewayUrl": f"http://127.0.0.1:{port}"},
                                        "host": {"healthUrl": f"http://127.0.0.1:{port}/health"},
                                        "serviceGraph": {"services": [
                                            {"id": "gateway", "port": port},
                                            {"id": "marketplace", "port": port + 1070},
                                        ]}, "configPatches": [{"path": "/platform/adapterOptions", "json": json.dumps({
                                            "serviceTransport": {"services": {
                                                "gateway": {"url": f"http://127.0.0.1:{port}"},
                                                "marketplace": {"url": f"http://127.0.0.1:{port + 1070}"},
                                            }},
                                        })}]}}
                    return subprocess.CompletedProcess(args, 0,
                        json.dumps({"ok": True, "data": payload}), "")
                root = Path(args[args.index("--request-platform-root") + 1])
                gateway = args[args.index("--gateway-url") + 1]
                port = gateway.rsplit(":", 1)[1]
                (root / ".kb/v2").mkdir(parents=True)
                receipt = {"verification": {"hostHealthUrl": f"http://127.0.0.1:{port}/health"},
                           "plan": {"serviceGraph": {"services": [
                               {"id": "gateway", "port": int(port)},
                               {"id": "marketplace", "port": int(port) + 1070},
                           ]}, "configPatches": [{"path": "/platform/adapterOptions", "json": json.dumps({
                               "serviceTransport": {"services": {
                                   "gateway": {"url": f"http://127.0.0.1:{port}"},
                                   "marketplace": {"url": f"http://127.0.0.1:{int(port) + 1070}"},
                               }},
                           })}]}}
                (root / ".kb/v2/receipt.json").write_text(json.dumps(receipt))
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                    "receipt": receipt,
                }}), "")

            journey = run_gateway_allocation_step(mode="local", launcher="kb-create", stable_index=index,
                                                  primary_root=primary, automatic_root=automatic,
                                                  explicit_root=explicit, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["PC-001.10", "PC-001.16"])
        self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
        self.assertEqual([call[1] for call in calls], ["stop", "apply", "plan", "plan", "status", "stop"])
        self.assertEqual(calls[1][calls[1].index("--gateway-url") + 1], "http://127.0.0.1:4200")
        self.assertEqual(calls[2][calls[2].index("--request-platform-root") + 1], str(explicit.parent / "root-d"))

    def test_repeat_plan_compares_install_owned_files_while_host_stays_running(self):
        with tempfile.TemporaryDirectory(prefix="journey-repeat-plan-") as temp:
            root = Path(temp) / "platform"
            marker = root / ".kb/receipt.json"
            marker.parent.mkdir(parents=True)
            marker.write_text('{"receipt":"stable"}\n')
            index = Path(temp) / "release-index.json"
            index.write_text("{}")
            calls = []
            status_calls = 0

            def run_command(args):
                nonlocal status_calls
                calls.append(args)
                command = args[1]
                if command == "status":
                    status_calls += 1
                    running = status_calls > 1
                    data = {"host": {"state": "running" if running else "stopped"},
                            "running": running, "reachable": running}
                elif command == "start":
                    data = {"operation": "start"}
                elif command == "plan":
                    data = {"plan": {"planHash": "same", "artifacts": [{"id": "platform"}],
                                     "serviceGraph": {"platformVersion": "2.0.0"}}}
                elif command == "stop":
                    data = {"operation": "stop"}
                else:
                    data = {"operation": command}
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": data}), "")

            journey = run_repeat_plan_readonly_step(mode="local", launcher="kb-create", stable_index=index,
                                                    platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["PC-001.8"])
        self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
        self.assertEqual([args[1] for args in calls], ["status", "start", "plan", "plan", "status", "stop"])

    def test_repeat_plan_allows_runtime_state_changes_from_running_host(self):
        with tempfile.TemporaryDirectory(prefix="journey-repeat-plan-runtime-state-") as temp:
            root = Path(temp) / "platform"
            log_dir = root / ".kb/logs"
            log_dir.mkdir(parents=True)
            index = Path(temp) / "release-index.json"
            index.write_text("{}")
            calls = []

            def run_command(args):
                calls.append(args[1])
                command = args[1]
                if command == "status":
                    data = {"host": {"state": "running"}, "running": True, "reachable": True}
                elif command == "plan":
                    (log_dir / "host-runtime.log").write_text("runtime update\n")
                    data = {"plan": {"planHash": "same", "artifacts": [], "serviceGraph": {}}}
                else:
                    data = {"operation": command}
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": data}), "")

            journey = run_repeat_plan_readonly_step(mode="local", launcher="kb-create", stable_index=index,
                                                    platform_root=root, run_command=run_command)
            installed_files = _snapshot_installation_owned_tree(root)

        self.assertEqual(journey.steps[0].status, "PASS", journey.steps[0].message)
        self.assertEqual(calls, ["status", "plan", "plan", "status"])
        self.assertEqual(set(installed_files), {".kb"})

    def test_missing_installer_fixture_is_claimless_preflight_failure(self):
        with tempfile.TemporaryDirectory(prefix="journey-missing-installer-fixture-") as temp:
            root = Path(temp)
            stable, canary = root / "stable.json", root / "canary.json"
            stable.write_text("{}")
            canary.write_text("{}")
            calls = []
            with patch("installer_case_steps.installer_scenario_resources",
                       return_value=[root / "missing-host-binary-linux-amd64.json"]):
                journey = run_installer_case_steps(
                    mode="local", launcher="kb-create", stable_index=stable, canary_index=canary,
                    platform_root=root, secret_value="secret", run_command=lambda args: calls.append(args),
                )

        self.assertEqual(len(journey.steps), 1)
        self.assertEqual(journey.steps[0].capability, "journey.orchestration")
        self.assertEqual(journey.steps[0].cases, [])
        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertIn("missing-host-binary", journey.steps[0].message)
        self.assertIn("node_modules/@kb-labs/cli-bin/dist/bin.js", journey.steps[0].message)
        self.assertEqual(calls, [])

    def test_repeat_plan_reports_changed_added_and_removed_paths(self):
        with tempfile.TemporaryDirectory(prefix="journey-repeat-plan-diff-") as temp:
            root = Path(temp) / "platform"
            root.mkdir()
            (root / "changed.txt").write_text("before")
            (root / "removed.txt").write_text("remove")
            index = Path(temp) / "release-index.json"
            index.write_text("{}")

            def run_command(args):
                command = args[1]
                if command == "status":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "host": {"state": "running"}, "running": True, "reachable": True}}), "")
                if command == "plan" and not (root / "added.txt").exists():
                    (root / "changed.txt").write_text("after")
                    (root / "removed.txt").unlink()
                    (root / "added.txt").write_text("new")
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                    "plan": {"planHash": "same", "artifacts": [], "serviceGraph": {}}}}), "")

            journey = run_repeat_plan_readonly_step(mode="local", launcher="kb-create", stable_index=index,
                                                    platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertIn("changed (1): changed.txt", journey.steps[0].message)
        self.assertIn("added (1): added.txt", journey.steps[0].message)
        self.assertIn("removed (1): removed.txt", journey.steps[0].message)

    def test_repeat_plan_fails_when_restarted_host_is_unhealthy(self):
        with tempfile.TemporaryDirectory(prefix="journey-repeat-plan-unhealthy-") as temp:
            root = Path(temp) / "platform"
            root.mkdir()
            index = Path(temp) / "release-index.json"
            index.write_text("{}")
            status_calls = 0

            def run_command(args):
                nonlocal status_calls
                command = args[1]
                if command == "status":
                    status_calls += 1
                    healthy = status_calls == 1
                    data = {"host": {"state": "running" if healthy else "stopped"},
                            "running": healthy, "reachable": healthy}
                elif command == "plan":
                    data = {"plan": {"planHash": "same", "artifacts": [], "serviceGraph": {}}}
                else:
                    data = {"operation": command}
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": data}), "")

            journey = run_repeat_plan_readonly_step(mode="local", launcher="kb-create", stable_index=index,
                                                    platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertIn("host health", journey.steps[0].message)

    def test_repeat_plan_limits_displayed_paths_and_reports_total(self):
        from installer_case_steps import _format_snapshot_changes

        message = _format_snapshot_changes({"changed": [f"file-{index:02d}" for index in range(22)],
                                            "added": [], "removed": []})

        self.assertIn("changed (22):", message)
        self.assertIn("22 path(s) total, showing 20 of 22", message)
        self.assertNotIn("file-20", message)

    def test_repeat_plan_snapshot_excludes_layout_runtime_state(self):
        with tempfile.TemporaryDirectory(prefix="journey-layout-runtime-state-") as temp:
            root = Path(temp)
            (root / ".kb/logs").mkdir(parents=True)
            (root / ".kb/logs/host.log").write_text("runtime\n")
            (root / ".kb/kb.config.jsonc").write_text("{}\n")
            snapshot = _snapshot_installation_owned_tree(root)

        self.assertEqual(set(snapshot), {".kb", ".kb/kb.config.jsonc"})

    def test_receipt_service_graph_must_match_selected_plan_services(self):
        planned = {"services": [{"id": "api"}, {"id": "worker"}]}
        declared = {"services": [{"id": "api"}]}

        self.assertFalse(_service_ids_match(planned, declared))

    def test_offline_plan_requires_cache_resolution_for_every_artifact(self):
        with tempfile.TemporaryDirectory(prefix="journey-offline-plan-provenance-") as temp:
            root = Path(temp)
            index = root / "release-index.json"
            index.write_text("{}")

            def run_command(args):
                payload = {"ok": True, "data": {"plan": {"request": {"source": "offline"}, "artifacts": [
                    {"id": "platform", "resolvedFrom": "cache"},
                    {"id": "kb-dev", "resolvedFrom": "registry", "resolvedUrl": "https://example.test/kb-dev"},
                ]}}}
                return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")

            journey = run_offline_plan_step(mode="local", launcher="kb-create", stable_index=index,
                                            platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertIn("network resolution", journey.steps[0].message)

    def test_candidate_mismatch_is_typed_orchestration_failure_without_claims(self):
        with tempfile.TemporaryDirectory(prefix="journey-candidate-provenance-") as temp:
            report = Path(temp) / "candidate.json"
            journey = Journey("local", "candidate-provenance-test")
            step = record_candidate_provenance(journey, "abc1234", {
                "version": "2.0.0", "commit": "def5678",
            })
            journey.write_json(report)
            payload = json.loads(report.read_text())

        self.assertEqual(step.status, "FAIL")
        self.assertEqual(journey.result().case_run["claims"], [])
        self.assertEqual(len(payload["steps"]), 1)
        step = payload["steps"][0]
        self.assertEqual(step["capability"], "journey.orchestration")
        self.assertEqual(step["details"]["code"], "KB_JOURNEY_CANDIDATE_MISMATCH")
        self.assertIn("abc1234", step["message"])
        self.assertIn("def5678", step["message"])

    def test_candidate_short_commit_matches_checkout_revision(self):
        with tempfile.TemporaryDirectory(prefix="journey-candidate-provenance-") as temp:
            report = Path(temp) / "candidate.json"
            journey = Journey("local", "candidate-provenance-test")
            step = record_candidate_provenance(journey, "abc1234567890", {
                "version": "2.0.0", "commit": "abc1234",
            })
            journey.write_json(report)

        self.assertEqual(step.status, "PASS")
        self.assertEqual(journey.result().case_run["claims"], [])

    def test_invalid_analytics_retention_claim_requires_catalogued_start_error(self):
        with tempfile.TemporaryDirectory(prefix="journey-invalid-analytics-retention-") as temp:
            root = Path(temp)
            config_path = root / ".kb/kb.config.jsonc"
            config_path.parent.mkdir(parents=True)
            config_path.write_text('{"platform":{"adapterOptions":{"analytics":{"retentionDays":90}}}}\n')
            calls = []

            def run_command(args):
                calls.append(args)
                if args[1] == "start":
                    self.assertEqual(json.loads(config_path.read_text())["platform"]["adapterOptions"]["analytics"]["retentionDays"], 0)
                    payload = {"ok": True, "data": {"operation": "start", "host": {"pid": 363}}}
                    return subprocess.CompletedProcess(args, 0, json.dumps(payload), "")
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"operation": "stop"}}), "")

            journey = run_analytics_invalid_retention_step(mode="local", launcher="kb-create",
                                                           platform_root=root, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["S-191.7"])
        self.assertEqual(journey.steps[0].status, "FAIL")
        self.assertEqual(len(calls), 2)
        self.assertEqual(calls[0][1], "start")
        self.assertEqual(calls[1][1], "stop")
        self.assertEqual(calls[1][calls[1].index("--platform-root") + 1], str(root))

    def test_doctor_root_isolation_claim_checks_repair_and_main_status(self):
        with tempfile.TemporaryDirectory(prefix="journey-doctor-isolation-") as temp:
            root = Path(temp)
            source = root / "source"
            receipt = source / ".kb/v2/receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text(json.dumps({"plan": {"artifacts": [
                {"kind": "platform", "id": "platform", "package": "@kb/platform", "version": "1.0.0"},
            ]}}))
            calls = []

            def run_command(args):
                calls.append(args)
                if args[1:3] == ["doctor", "--fix"]:
                    output = {"ok": True, "data": {"operation": "doctor-fix", "hostRestarted": False}}
                elif args[1] == "start":
                    output = {"ok": False, "error": {"code": "KB_HOST_ALREADY_RUNNING",
                               "message": f"The KB Labs host for platform root {source.resolve()} is already running (pid 42).",
                               "hint": f"Stop that platform first with `kb-create stop --platform-root {source}`.",
                               "meta": {"pid": "42"}}}
                    return subprocess.CompletedProcess(args, 1, json.dumps(output), "")
                elif args[1] == "status" and str(source) not in " ".join(args):
                    output = {"ok": True, "data": {"host": {"state": "stopped"}}}
                else:
                    output = {"ok": True, "data": {"host": {"state": "running"}}}
                return subprocess.CompletedProcess(args, 0, json.dumps(output), "")

            journey = run_doctor_root_isolation_step(mode="local", launcher="kb-create",
                                                     platform_root=source, run_command=run_command)

        self.assertEqual(journey.steps[0].cases, ["S-025.7"])
        self.assertEqual(journey.steps[0].status, "PASS")
        self.assertEqual(journey.steps[1].cases, ["S-025.8"])
        self.assertEqual(journey.steps[1].status, "PASS")
        self.assertEqual(len(calls), 5)

    def test_doctor_fixture_requirement_matches_receipt_and_manifest_projection(self):
        with tempfile.TemporaryDirectory(prefix="journey-doctor-fixture-") as temp:
            root = Path(temp)
            source = root / "source"
            source_receipt = source / ".kb/v2/receipt.json"
            source_receipt.parent.mkdir(parents=True)
            source_receipt.write_text(json.dumps({"plan": {"artifacts": [
                {"kind": "platform", "id": "platform", "package": "@kb/platform", "version": "1.0.0"},
            ]}}))
            target = root / "fixture"
            requirement = {"id": "fixture.required", "path": "/fixture/required", "required": True,
                           "secret": False, "hint": "configure fixture"}

            _seed_doctor_fixture(source, target, requirement=requirement)

            receipt = json.loads((target / ".kb/v2/receipt.json").read_text())
            manifest = json.loads((target / "node_modules/@kb-labs/journey-doctor-fixture/kb-create.manifest.json").read_text())
            fixture_artifact = next(item for item in receipt["plan"]["artifacts"]
                                    if item["id"] == "journey-doctor-fixture")
            self.assertEqual(fixture_artifact["config"], [requirement])
            self.assertEqual(manifest["requirements"], [requirement])

    def test_doctor_fixture_includes_installed_cli_entry_needed_by_launcher_shim(self):
        with tempfile.TemporaryDirectory(prefix="journey-doctor-cli-fixture-") as temp:
            root = Path(temp)
            source = root / "source"
            source_package = source / "node_modules/@kb-labs/cli-bin"
            source_package.joinpath("dist").mkdir(parents=True)
            source_package.joinpath("dist/bin.js").write_text("export {};\n")
            receipt = source / ".kb/v2/receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text(json.dumps({"plan": {"artifacts": [
                {"kind": "cli", "id": "cli", "package": "@kb-labs/cli-bin", "version": "1.0.0"},
            ]}}))
            target = root / "target"

            _seed_doctor_fixture(source, target)

            self.assertEqual((target / "node_modules/@kb-labs/cli-bin/dist/bin.js").read_text(), "export {};\n")

    def test_uninstall_decline_claim_fails_for_a_failed_fake_response(self):
        with tempfile.TemporaryDirectory(prefix="journey-uninstall-decline-") as temp:
            receipt = Path(temp) / "receipt.json"
            receipt.write_text('{"id":"unchanged"}')

            def fake_pty(args, *, on_output):
                self.assertEqual(on_output("Continue? [y/N] "), "n\n")
                return subprocess.CompletedProcess(args, 2, "KB_INSTALL_CONFIRMATION_REQUIRED", ""), 0.0

            journey = run_uninstall_decline_step(mode="local", args=["kb-create", "uninstall"],
                                                 receipt_path=receipt, run_pty=fake_pty)
            self.assertEqual(journey.steps[0].cases, ["S-050.3"])
            self.assertEqual(journey.steps[0].status, "FAIL")

    def test_each_claim_fails_when_the_cli_returns_a_failed_envelope(self):
        with tempfile.TemporaryDirectory(prefix="journey-installer-cases-") as temp:
            root = Path(temp)
            stable = root / "stable.json"
            canary = root / "canary.json"
            stable.write_text('{"platforms": []}')
            canary.write_text('{"platforms": []}')
            installed = root / "installed"
            installed.mkdir()
            installed_cli_entry = installed / "node_modules/@kb-labs/cli-bin/dist/bin.js"
            installed_cli_entry.parent.mkdir(parents=True)
            installed_cli_entry.write_text("export {};\n")

            def fail_every_command(args):
                return subprocess.CompletedProcess(args, 2, '{"ok":false,"error":{"code":"FAKE_FAILURE"}}', "")

            with patch("installer_case_steps.installer_scenario_resources",
                       return_value=[case_catalog_root()]):
                journey = run_installer_case_steps(mode="local", launcher="kb-create", stable_index=stable,
                                                   canary_index=canary, platform_root=installed,
                                                   secret_value="journey-test-secret",
                                                   run_command=fail_every_command)
            claims = {claim: status for step in journey.steps for claim in step.cases
                      for status in [step.status]}
            expected = {"PC-001.4", "PC-001.5", "PC-001.8", "PC-001.9", "PC-001.10", "PC-001.11", "PC-001.12", "PC-001.16", "S-025.9", "S-181.1", "S-181.2",
                        "S-181.3", "S-181.4", "S-181.5", "S-025.1", "S-025.2",
                        "S-025.3", "S-025.4", "S-025.5", "S-025.6", "S-191.7",
                        "S-053.2", "S-053.3", "S-053.4", "S-053.5", "S-053.6", "S-053.7",
                        "S-187.4", "S-187.5", "S-187.6"}
            self.assertEqual(set(claims), expected)
            self.assertTrue(all(claims[claim] == "FAIL" for claim in expected))


if __name__ == "__main__":
    unittest.main()
