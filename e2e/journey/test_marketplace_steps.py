import json
import ast
import subprocess
import sys
import tempfile
import unittest
import urllib.request
from unittest.mock import patch
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from marketplace_steps import (_output, _platform_snapshot, authenticate_secured_marketplace,
                               run_plugin_policy_diagnostic_step,
                               run_marketplace_steps, validate_commit_status_response,
                               validate_shared_failure, validate_unmounted_commit_response,
                               wait_for_marketplace_ready)
from service_steps import Journey
from invocations import invocation_context, open_http, run_command


class MarketplaceStepTests(unittest.TestCase):
    def test_policy_diagnostic_step_restores_config_and_proves_blocked_fixture_and_first_party_cli(self):
        with tempfile.TemporaryDirectory(prefix="kb-policy-journey-unit-") as directory:
            project = Path(directory)
            config = project / ".kb" / "kb.config.json"
            config.parent.mkdir()
            original = b'{"preserved": true}\n'
            config.write_bytes(original)
            calls = []

            def runner(command, **_kwargs):
                calls.append(command)
                if command[0] == "kb" and command[1:3] == ["marketplace", "install"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {}}), "")
                if command[0] == "kb" and command[1:4] == ["marketplace", "plugins", "doctor"]:
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_RUNTIME_COMMAND_FAILED", "message": "Marketplace doctor found issues",
                        "hint": "Run doctor", "diagnostics": [{
                            "code": "PLUGIN_POLICY_TYPE_INVALID", "severity": "error",
                            "path": str(config), "message": "@kb-labs/e2e-analytics-emitter is unavailable: plugins.block must be an array",
                            "hint": f"Repair the policy; kb marketplace plugins doctor --scope project",
                        }],
                    }}), "")
                if command[0] == "kb" and command[1:3] == ["journey", "analytics"]:
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_RUNTIME_COMMAND_NOT_FOUND", "message": "Unknown command journey analytics emit",
                        "hint": "Run kb help",
                    }}), "")
                if command[0] == "kb" and command[1] == "version":
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"version": "1.0.0"}}), "")
                raise AssertionError(f"unexpected command: {command}")

            journey = Journey("local", "policy-test")
            with patch("marketplace_steps._registry_version", return_value="1.0.0"):
                run_plugin_policy_diagnostic_step(journey, project, "http://registry.test", {"HOME": directory}, runner)

            self.assertEqual(config.read_bytes(), original)
            self.assertEqual(journey.steps[-1].status, "PASS")
            self.assertEqual(journey.steps[-1].cases, ["S-203.24"])
            self.assertTrue(any(command[0] == "kb" and command[1] == "version" for command in calls))

    def test_commit_route_http_claims_accept_documented_success_and_not_found(self):
        response = {"ok": False, "error": {"code": "KB_PLUGIN_NOT_INSTALLED",
            "message": "Plugin is not installed in this project.",
            "hint": "Run `kb marketplace install @kb-labs/commit-entry` in this project.",
            "correlationId": "request-fixture"}}
        validate_commit_status_response(200, {"ok": True, "data": {"scope": "project"}})
        validate_unmounted_commit_response(404, response)

    def test_commit_route_claims_fail_for_fake_unmounted_and_still_mounted_responses(self):
        with self.assertRaisesRegex(RuntimeError, "unexpected response: HTTP 404"):
            validate_commit_status_response(404, {"ok": False, "error": {"code": "KB_RUNTIME_UNEXPECTED"}})
        with self.assertRaisesRegex(RuntimeError, "unexpected gateway failure: HTTP 200"):
            validate_unmounted_commit_response(200, {"ok": True, "data": {"scope": "project"}})
        with self.assertRaisesRegex(RuntimeError, "unexpected gateway failure: HTTP 404"):
            validate_unmounted_commit_response(404, {"ok": False, "error": {
                "code": "KB_RUNTIME_UNEXPECTED", "message": "An unexpected error occurred.",
                "hint": "Re-run with --debug and include the correlationId in a report.",
                "correlationId": "request-fixture"}})

    def test_fake_failures_are_rejected_for_both_shared_error_renderings(self):
        for json_mode in (False, True):
            with self.subTest(json_mode=json_mode), self.assertRaises(AssertionError):
                validate_shared_failure(subprocess.CompletedProcess(
                    ["kb", "marketplace", "install"], 0, "{}", ""), json_mode=json_mode,
                    code="KB_PLUGIN_NOT_FOUND", message="missing", hint="kb marketplace search")

    def test_shared_error_validator_accepts_json_envelope_and_human_recovery_hint(self):
        message = "Adapter package x is not installed on this platform."
        hint = "kb marketplace install x --scope platform"
        validate_shared_failure(subprocess.CompletedProcess(["kb"], 1, json.dumps({
            "ok": False, "error": {"code": "KB_ADAPTER_NOT_INSTALLED", "message": message, "hint": hint},
        }), ""), json_mode=True, code="KB_ADAPTER_NOT_INSTALLED", message=message, hint=hint)
        validate_shared_failure(subprocess.CompletedProcess(["kb"], 1,
            f"{message} [KB_ADAPTER_NOT_INSTALLED]\n  Hint:  {hint}\n", ""),
            json_mode=False, code="KB_ADAPTER_NOT_INSTALLED", message=message, hint=hint)

    def test_command_and_http_steps_do_not_bypass_the_invocation_helper(self):
        root = Path(__file__).parent
        guarded = [source for source in root.rglob("*.py")
                   if source.name != "invocations.py" and not source.name.startswith("test_")]
        violations = []

        class CallGuard(ast.NodeVisitor):
            def __init__(self, source):
                self.source = source

            def visit_Call(self, node):
                function = node.func
                if (isinstance(function, ast.Attribute)
                        and function.attr in {"run", "Popen", "check_output", "call"}
                        and isinstance(function.value, ast.Name)
                        and function.value.id == "subprocess"):
                    violations.append(f"{self.source.name}:{node.lineno} calls subprocess.{function.attr} directly")
                if (isinstance(function, ast.Attribute) and function.attr == "urlopen"
                        and isinstance(function.value, ast.Attribute)
                        and function.value.attr == "request"):
                    violations.append(f"{self.source.name}:{node.lineno} calls urllib.request.urlopen directly")
                self.generic_visit(node)

            def visit_FunctionDef(self, node):
                self._check_defaults(node.args.defaults, node.lineno)
                self._check_defaults([value for value in node.args.kw_defaults if value is not None], node.lineno)
                self.generic_visit(node)

            visit_AsyncFunctionDef = visit_FunctionDef

            def _check_defaults(self, values, line):
                for value in values:
                    if (isinstance(value, ast.Attribute) and value.attr == "run"
                            and isinstance(value.value, ast.Name) and value.value.id == "subprocess"):
                        violations.append(f"{self.source.name}:{line} defaults to subprocess.run")

        for source in guarded:
            tree = ast.parse(source.read_text(encoding="utf-8"), filename=str(source))
            CallGuard(source).visit(tree)
        self.assertEqual(violations, [])

    def test_invocation_helper_records_case_command_outputs_and_redacts_secrets(self):
        with tempfile.TemporaryDirectory(prefix="kb-invocation-evidence-") as directory:
            report_root = Path(directory)
            env = {"HOME": "/isolated", "KB_JOURNEY_HOME_LABEL": "anonymous",
                   "KB_JOURNEY_ADMIN_PASSWORD": "never-write-this"}

            def runner(command, **_kwargs):
                return subprocess.CompletedProcess(command, 7, "failure never-write-this", "stderr detail")

            with invocation_context(step_id="S-041.7", report_root=report_root):
                result = run_command(["kb", "marketplace", "plugins", "list", "--json"],
                                     cwd="/isolated/project", env=env, runner=runner)

            record = json.loads((report_root / "invocations" / "calls.jsonl").read_text().splitlines()[0])
            self.assertEqual(result.returncode, 7)
            self.assertEqual(record["step_id"], "S-041.7")
            self.assertEqual(record["home_label"], "anonymous")
            self.assertEqual(record["cwd"], "/isolated/project")
            self.assertEqual(record["exit_code"], 7)
            self.assertEqual(record["stdout"], "failure [REDACTED]")
            self.assertEqual(record["stderr"], "stderr detail")
            self.assertGreaterEqual(record["duration_seconds"], 0)
            self.assertNotIn("never-write-this", json.dumps(record))

    def test_invocation_helper_records_redacted_http_request_and_response(self):
        class Response:
            status = 201
            headers = {}

            def __enter__(self):
                return self

            def __exit__(self, *_exc):
                return None

            def read(self):
                return b'{"ok":true}'

        with tempfile.TemporaryDirectory(prefix="kb-invocation-http-evidence-") as directory:
            report_root = Path(directory)
            secret = "studio-member-password"
            request = urllib.request.Request("https://gateway.test/auth/invites", data=json.dumps({
                "email": "member@example.test", "password": secret,
            }).encode(), method="POST")
            with invocation_context(step_id="S-106.2", report_root=report_root):
                status, body, _ = open_http(lambda *_args, **_kwargs: Response(), request,
                                            timeout=3, env={"KB_STUDIO_MEMBER_PASSWORD": secret})
            record = json.loads((report_root / "invocations" / "calls.jsonl").read_text().splitlines()[0])
            self.assertEqual(status, 201)
            self.assertEqual(body, b'{"ok":true}')
            self.assertEqual(record["argv"], ["POST"])
            self.assertEqual(record["url"], "https://gateway.test/auth/invites")
            self.assertEqual(record["request_body"], '{"email": "member@example.test", "password": "[REDACTED]"}')
            self.assertEqual(record["stdout"], '{"ok":true}')
            self.assertNotIn(secret, json.dumps(record))

    def test_marketplace_product_health_check_runs_through_invocation_helper(self):
        with tempfile.TemporaryDirectory(prefix="kb-marketplace-health-") as directory:
            calls = []

            def fake_run(command, **kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(command, 0, '{"ok":true,"data":{}}', "")

            wait_for_marketplace_ready(Path(directory), {"HOME": directory}, fake_run)
            self.assertEqual(calls, [["kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"]])

    def test_platform_snapshot_detects_receipt_and_package_version_changes(self):
        with tempfile.TemporaryDirectory(prefix="kb-marketplace-platform-snapshot-") as directory:
            root = Path(directory)
            receipt = root / ".kb" / "v2" / "receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text('{"version":"1"}\n')
            package = root / "node_modules" / "@kb-labs" / "workflow-daemon" / "package.json"
            package.parent.mkdir(parents=True)
            package.write_text('{"name":"@kb-labs/workflow-daemon","version":"2.120.1"}\n')

            before = _platform_snapshot(root)
            receipt.write_text('{"version":"2.120.2"}\n')
            self.assertNotEqual(before, _platform_snapshot(root))
            receipt.write_text('{"version":"2.120.1"}\n')

            before = _platform_snapshot(root)
            package.write_text('{"name":"@kb-labs/workflow-daemon","version":"2.120.2"}\n')

            self.assertNotEqual(before, _platform_snapshot(root))

    def test_marketplace_failure_detail_keeps_error_start_and_end(self):
        command = ["kb", "marketplace", "install", "@kb-labs/qa-entry"]
        raw = "ERR_PNPM_RESOLUTION_FAILURE: root cause " + ("detail " * 400) + "final context"
        result = subprocess.CompletedProcess(command, 1, raw, "")

        with self.assertRaisesRegex(RuntimeError, "ERR_PNPM_RESOLUTION_FAILURE: root cause") as raised:
            _output(result, command)

        self.assertIn("final context", str(raised.exception))

    def test_marketplace_failure_detail_uses_stderr_when_stdout_is_empty(self):
        command = ["kb", "marketplace", "install", "@kb-labs/adapters-analytics-file", "--json"]
        result = subprocess.CompletedProcess(command, 1, "", '{"error":{"code":"MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING"}}')

        with self.assertRaisesRegex(RuntimeError, "MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING"):
            _output(result, command)

    def test_setup_command_failure_preserves_typed_code_message_and_next_step(self):
        command = ["kb", "project", "add", "/work/missing-project", "--json"]
        result = subprocess.CompletedProcess(command, 1, json.dumps({
            "ok": False,
            "error": {
                "code": "KB_PROJECT_PATH_INVALID",
                "message": "Project path does not exist.",
                "hint": "Create it, then run `kb project add /work/missing-project`.",
            },
        }), "")

        with self.assertRaises(RuntimeError) as raised:
            _output(result, command)

        self.assertIn("`kb project add /work/missing-project --json` exited 1", str(raised.exception))
        self.assertIn("KB_PROJECT_PATH_INVALID", str(raised.exception))
        self.assertIn("Project path does not exist.", str(raised.exception))
        self.assertIn("hint: Create it, then run `kb project add /work/missing-project`.", str(raised.exception))

    def test_secured_marketplace_claims_typed_missing_login_then_persists_admin_session(self):
        with tempfile.TemporaryDirectory(prefix="kb-marketplace-auth-journey-") as directory:
            root = Path(directory)
            project = root / "project"
            project.mkdir()
            env = {
                "HOME": str(root),
                "KB_JOURNEY_BASE_URL": "http://gateway.test",
                "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
                "KB_JOURNEY_ADMIN_PASSWORD": "fixture-password",
            }
            calls: list[tuple[str, ...]] = []
            logged_in = False

            def fake_run(command, *, cwd, env, capture_output, text, timeout):
                nonlocal logged_in
                calls.append(tuple(command))
                args = command[1:]
                if args[:3] == ["marketplace", "plugins", "list"]:
                    if logged_in:
                        return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"entries": []}}), "")
                    return subprocess.CompletedProcess(command, 1, json.dumps({
                        "ok": False,
                        "error": {
                            "code": "KB_AUTH_REQUIRED",
                            "message": "Authentication is required.",
                            "hint": 'Run "kb auth login --gateway-url http://gateway.test --email <email>".',
                        },
                    }), "")
                if args[:2] == ["auth", "login"]:
                    logged_in = True
                    return subprocess.CompletedProcess(command, 0, '{"ok":true}', "")
                if args[:2] == ["auth", "status"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({
                        "ok": True, "data": {"session": {"authenticated": logged_in}},
                    }), "")
                if args[:3] == ["marketplace", "plugins", "list"] and logged_in:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"entries": []}}), "")
                return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command[1:3]}")

            journey = Journey("secured", "marketplace-auth-fixture")
            authenticate_secured_marketplace(journey, project, env, fake_run)

            self.assertEqual(journey.result().status, "PASS")
            self.assertEqual(journey.result().case_run["claims"], [
                {"claimId": "S-010.6", "kind": "journey", "status": "PASS"},
                {"claimId": "S-041.7", "kind": "journey", "status": "PASS"},
            ])
            self.assertIn(("kb", "marketplace", "plugins", "list", "--scope", "platform", "--json"), calls)
            self.assertIn(("kb", "auth", "login", "--gateway-url", "http://gateway.test",
                           "--email", "admin@example.test", "--password-stdin", "--json"), calls)
            self.assertIn(("kb", "auth", "status", "--json"), calls)

    def test_marketplace_and_adapter_flows_claim_only_asserted_case_steps(self):
        with tempfile.TemporaryDirectory(prefix="kb-marketplace-journey-") as directory:
            home = Path(directory)
            project = home / "project-a"
            (project / ".kb").mkdir(parents=True)
            (project / ".kb" / "kb.config.json").write_text("{}\n")
            platform_root = home / "kb-platform"
            platform_config = platform_root / ".kb" / "kb.config.jsonc"
            platform_config.parent.mkdir(parents=True)
            platform_config.write_text(json.dumps({"gateway": {"access": {"mode": "local"}}}))
            receipt = platform_root / ".kb" / "v2" / "receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text('{"version":"2.120.1"}\n')
            package = platform_root / "node_modules" / "@kb-labs" / "workflow-daemon" / "package.json"
            package.parent.mkdir(parents=True)
            package.write_text('{"name":"@kb-labs/workflow-daemon","version":"2.120.1"}\n')
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project),
                   "KB_JOURNEY_PLATFORM_ROOT": str(platform_root),
                   "KB_JOURNEY_PROJECT_ID": "prj_marketplace_fixture",
                   "KB_JOURNEY_BASE_URL": "http://gateway.test",
                   "KB_JOURNEY_SERVICE_TOKEN": "fixture-token"}
            calls: list[tuple[str, ...]] = []
            host_running = True

            def lock(path: Path, *, platform: bool = False) -> dict:
                lock_file = path / ".kb" / "marketplace.lock"
                try:
                    return json.loads(lock_file.read_text())
                except FileNotFoundError:
                    return {"schema": "kb.marketplace/3", "plugins": {}, "adapters": {}} if platform else {
                        "schema": "kb.marketplace/3", "plugins": {}
                    }

            def save_lock(path: Path, value: dict) -> None:
                lock_file = path / ".kb" / "marketplace.lock"
                lock_file.parent.mkdir(parents=True, exist_ok=True)
                lock_file.write_text(json.dumps(value))

            def fake_run(command, *, cwd, env, capture_output, text, timeout):
                nonlocal host_running
                calls.append(tuple(command))
                cwd_path = Path(cwd)
                if command[0] == "kb-create":
                    host_running = command[1] == "start"
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if command[0] == "npm":
                    package_id = command[2]
                    versions = {
                        "@kb-labs/commit-entry": "2.120.1",
                        "@kb-labs/qa-entry": "2.120.1",
                        "@kb-labs/e2e-analytics-emitter": "1.0.0",
                        "@kb-labs/adapters-fs": "2.107.0",
                    }
                    return subprocess.CompletedProcess(command, 0, json.dumps(versions[package_id]), "")

                args = command[1:]
                stdout = "{}"
                if args[:2] == ["marketplace", "install"] and args[2] == "@kb-labs/not-published":
                    payload = {"ok": False, "error": {"code": "KB_PLUGIN_NOT_FOUND",
                        "message": "Plugin @kb-labs/not-published was not found in the registry.",
                        "hint": "kb marketplace search"}}
                    if "--json" in args:
                        return subprocess.CompletedProcess(command, 1, json.dumps(payload), "")
                    error = payload["error"]
                    return subprocess.CompletedProcess(command, 1,
                        f"{error['message']} [{error['code']}]\n  Hint:  {error['hint']}\n", "")
                elif args[:2] == ["adapter", "bind"] and args[3] == "@kb-labs/adapters-not-installed":
                    payload = {"ok": False, "error": {"code": "KB_ADAPTER_NOT_INSTALLED",
                        "message": "Adapter package @kb-labs/adapters-not-installed is not installed on this platform.",
                        "hint": "kb marketplace install @kb-labs/adapters-not-installed --scope platform"}}
                    if "--json" in args:
                        return subprocess.CompletedProcess(command, 1, json.dumps(payload), "")
                    error = payload["error"]
                    return subprocess.CompletedProcess(command, 1,
                        f"{error['message']} [{error['code']}]\n  Hint:  {error['hint']}\n", "")
                elif args[:3] == ["marketplace", "plugins", "doctor"]:
                    policy_config = json.loads((cwd_path / ".kb" / "kb.config.json").read_text())
                    if policy_config.get("plugins", {}).get("block") == 7:
                        return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                            "code": "KB_RUNTIME_COMMAND_FAILED", "message": "Marketplace doctor found issues", "hint": "Run doctor",
                            "diagnostics": [{"code": "PLUGIN_POLICY_TYPE_INVALID", "severity": "error",
                                "path": str(cwd_path / ".kb" / "kb.config.json"),
                                "message": "@kb-labs/e2e-analytics-emitter is unavailable: plugins.block must be an array",
                                "hint": "Repair config; kb marketplace plugins doctor --scope project"}],
                        }}), "")
                    if env.get("GATEWAY_PORT") == "not-a-port":
                        return subprocess.CompletedProcess(command, 1, json.dumps({
                            "ok": False,
                            "error": {
                                "code": "KB_CONFIG_INVALID",
                                "message": 'Environment variable "GATEWAY_PORT" must be an integer from 1 through 65535.',
                                "hint": 'Run "unset GATEWAY_PORT", then retry the command to use the service default port.',
                            },
                        }), "")
                    if host_running:
                        stdout = json.dumps({"ok": True, "data": {"total": 0, "issues": []}})
                    else:
                        return subprocess.CompletedProcess(command, 1, json.dumps({
                            "ok": False,
                            "error": {
                                "code": "KB_HOST_UNREACHABLE",
                                "message": "The platform host is unreachable.",
                                "hint": 'Run "kb-create start", or "kb-create doctor" if the host should already be running.',
                            },
                        }), "")
                elif args[:3] == ["journey", "analytics", "emit"]:
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_RUNTIME_COMMAND_NOT_FOUND", "message": "Unknown command journey analytics emit", "hint": "Run kb help",
                    }}), "")
                elif args[:1] == ["version"]:
                    stdout = json.dumps({"version": "1.0.0"})
                elif args[:2] == ["marketplace", "install"]:
                    package_id, version = args[2].rsplit("@", 1)
                    platform = "--scope" in args and args[args.index("--scope") + 1] == "platform"
                    lock_root = platform_root if platform else cwd_path
                    value = lock(lock_root, platform=platform)
                    value.setdefault("adapters", {})
                    collection = value["plugins"]
                    plugin_id = "@kb-labs/commit" if package_id == "@kb-labs/commit-entry" else package_id
                    collection[plugin_id] = {
                        "version": version, "integrity": "sha512-fixture", "source": "marketplace",
                        "enabled": True, "resolvedPath": f"store:{package_id}@{version}", "package": package_id,
                    }
                    save_lock(lock_root, value)
                    stdout = json.dumps({
                        "installed": [{"id": plugin_id, "version": version}],
                        "warnings": [],
                        "scope": "platform" if platform else "project",
                    })
                elif args[:3] == ["marketplace", "plugins", "list"]:
                    value = lock(cwd_path)
                    entries = [{"id": package_id, **entry} for package_id, entry in value["plugins"].items()]
                    if "--json" in args:
                        stdout = json.dumps({"entries": entries})
                    else:
                        enabled_count = sum(1 for entry in entries if entry.get("enabled", True))
                        disabled_count = len(entries) - enabled_count
                        rows = [f"{'✅' if entry.get('enabled', True) else '⏸'} {entry['id']} {entry.get('version', '')}"
                                for entry in entries]
                        stdout = (f"{len(entries)} plugins ({enabled_count} enabled, {disabled_count} disabled) — scope=project\n"
                                  + "\n".join(rows))
                elif args[:4] in (["marketplace", "plugins", "disable", "@kb-labs/commit"],
                                  ["marketplace", "plugins", "enable", "@kb-labs/commit"]):
                    value = lock(cwd_path)
                    entry = value["plugins"].get("@kb-labs/commit")
                    if not isinstance(entry, dict):
                        return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False}), "")
                    enabled = args[2] == "enable"
                    entry["enabled"] = enabled
                    save_lock(cwd_path, value)
                    stdout = f"{'Enabled' if enabled else 'Disabled'} @kb-labs/commit (project)\n"
                elif args[:3] == ["marketplace", "plugins", "disable"]:
                    return subprocess.CompletedProcess(command, 1, "", "Plugin @kb-labs/not-installed was not found.\n")
                elif args[:3] == ["marketplace", "update", "@kb-labs/commit"]:
                    value = lock(cwd_path)
                    entry = value["plugins"]["@kb-labs/commit"]
                    entry["version"] = "2.120.2"
                    entry["integrity"] = "sha512-updated-fixture"
                    entry["resolvedPath"] = "store:@kb-labs/commit-entry@2.120.2"
                    save_lock(cwd_path, value)
                    stdout = json.dumps({"scope": "project", "warnings": [],
                        "installed": [{"id": "@kb-labs/commit", "version": "2.120.2", "primaryKind": "plugin"}]})
                elif args[:3] == ["marketplace", "update", "@kb-labs/not-published"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"scope": "project",
                        "warnings": ["Plugin @kb-labs/not-published was not found in the registry."]}}), "")
                elif args[:2] == ["commit", "--help"]:
                    if lock(cwd_path)["plugins"].get("@kb-labs/commit", {}).get("enabled") is False:
                        return subprocess.CompletedProcess(command, 1, "", "Command not found: commit")
                    stdout = "Commands: commit open"
                elif args[:2] == ["commit", "open"]:
                    value = lock(cwd_path)
                    if "@kb-labs/commit" not in value["plugins"]:
                        return subprocess.CompletedProcess(command, 2, json.dumps({"ok": False, "error": {
                            "code": "KB_RUNTIME_INPUT_INVALID",
                            "message": "Unknown command: commit open",
                            "hint": 'Check the command name or run "kb --help" to list available commands.',
                        }}), "")
                    stdout = json.dumps({"hasPlan": False})
                elif args[:2] == ["marketplace", "uninstall"]:
                    platform = "--scope" in args and args[args.index("--scope") + 1] == "platform"
                    lock_root = platform_root if platform else cwd_path
                    value = lock(lock_root, platform=platform)
                    value["plugins"].pop("@kb-labs/commit" if args[2] == "@kb-labs/commit-entry" else args[2], None)
                    save_lock(lock_root, value)
                    stdout = json.dumps({"ok": True})
                elif args[:2] == ["workflow", "health"]:
                    stdout = json.dumps({"ok": True, "data": {"status": "healthy"}})
                elif args[:3] == ["qa", "cases", "list"]:
                    stdout = json.dumps({"schema": "kb.qa-case-list/v1", "cases": [], "diagnostics": []})
                elif args[:2] == ["project", "add"]:
                    root = Path(args[2])
                    (root / ".kb").mkdir(parents=True, exist_ok=True)
                    (root / ".kb" / "kb.config.json").write_text("{}\n")
                    stdout = json.dumps({"ok": True})
                elif args[:2] == ["adapter", "bind"]:
                    if "--dry-run" not in args:
                        config_file = cwd_path / ".kb" / "kb.config.json"
                        config = json.loads(config_file.read_text())
                        config.setdefault("platform", {}).setdefault("adapters", {})["storage"] = args[3]
                        config_file.write_text(json.dumps(config))
                    stdout = json.dumps({"ok": True, "dryRun": "--dry-run" in args})
                elif args[:2] == ["config", "set"]:
                    config_file = cwd_path / ".kb" / "kb.config.json"
                    config = json.loads(config_file.read_text())
                    config.setdefault("platform", {}).setdefault("adapterOptions", {})["storage"] = json.loads(args[3])
                    config_file.write_text(json.dumps(config))
                    stdout = json.dumps({"ok": True, "scope": "project"})
                elif args[:2] == ["workflow", "run"]:
                    stdout = json.dumps({"ok": True, "data": {"runId": "run-fixture"}})
                elif args[:3] == ["workflow", "runs", "watch"]:
                    stdout = "Run SUCCESS\n"
                elif args[:2] == ["adapter", "unbind"]:
                    config_file = cwd_path / ".kb" / "kb.config.json"
                    config = json.loads(config_file.read_text())
                    del config["platform"]["adapters"]["storage"]
                    config_file.write_text(json.dumps(config))
                    stdout = json.dumps({"ok": True})
                elif args[:2] == ["project", "remove"]:
                    stdout = json.dumps({"ok": True})
                else:
                    return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command}")
                if "--json" in args and stdout.lstrip().startswith("{"):
                    envelope = json.loads(stdout)
                    if envelope.get("ok") is True and "data" not in envelope:
                        envelope = {"ok": True, "data": {key: value for key, value in envelope.items() if key != "ok"}}
                    elif "ok" not in envelope:
                        envelope = {"ok": True, "data": envelope}
                    stdout = json.dumps(envelope)
                return subprocess.CompletedProcess(command, 0, stdout, "")

            http_calls = []
            commit_case_hook_calls = []

            class FakeResponse:
                headers = {}

                def __init__(self, status, payload):
                    self.status = status
                    self.payload = payload

                def __enter__(self):
                    return self

                def __exit__(self, *_args):
                    return None

                def read(self):
                    return json.dumps(self.payload).encode()

            def fake_http(request, timeout):
                http_calls.append((request.full_url, request.get_header("Authorization"), timeout))
                if len(http_calls) in (1, 3):
                    return FakeResponse(200, {"ok": True, "data": {"scope": "project", "hasPlan": False}})
                return FakeResponse(404, {"ok": False, "error": {
                    "code": "KB_PLUGIN_NOT_INSTALLED",
                    "message": "Plugin is not installed in this project.",
                    "hint": "Run `kb marketplace install @kb-labs/commit-entry` in this project.",
                    "correlationId": "request-fixture"}})

            def fake_commit_case_hook():
                self.assertIn("@kb-labs/commit", lock(project)["plugins"])
                commit_case_hook_calls.append(True)

            journey = Journey("local", "marketplace-fixture")
            run_marketplace_steps(journey, project, "http://registry.test", env, fake_run, fake_http,
                                  commit_case_hook=fake_commit_case_hook)

            expected = {f"S-{case}.{step}" for case, steps in (("010", range(1, 6)), ("011", range(1, 7)), ("012", range(1, 5)), ("047", range(1, 4)), ("192", range(1, 5))) for step in steps}
            expected.update({
                "S-010.7",
                "S-051.2", "S-051.3", "S-051.4",
                "S-203.14", "S-203.15", "S-203.16",
                "S-203.24",
            })
            actual = {claim["claimId"] for claim in journey.result().case_run["claims"]}
            self.assertEqual(journey.result().status, "PASS", journey.result().first_failure)
            self.assertEqual(actual, expected)
            self.assertEqual(commit_case_hook_calls, [True])
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1", "--json"), calls)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1",
                           "--scope", "platform", "--json"), calls)
            self.assertIn(("kb", "marketplace", "uninstall", "@kb-labs/commit",
                           "--scope", "platform", "--json"), calls)
            self.assertEqual([call[0] for call in http_calls], [
                "http://gateway.test/api/v1/projects/prj_marketplace_fixture/rest/api/v1/plugins/commit/status",
                "http://gateway.test/api/v1/projects/prj_marketplace_fixture/rest/api/v1/plugins/commit/status",
                "http://gateway.test/api/v1/projects/prj_marketplace_fixture/rest/api/v1/plugins/commit/status",
            ])
            self.assertTrue(all(call[1] == "Bearer fixture-token" for call in http_calls))
            self.assertNotIn(("npm", "view", "@kb-labs/adapters-fs", "version", "--json", "--registry=http://registry.test"), calls)
            self.assertNotIn(("kb", "marketplace", "install", "@kb-labs/adapters-fs@2.107.0", "--scope", "platform", "--json"), calls)
            self.assertIn(("kb", "qa", "cases", "list", "--json"), calls)
            self.assertIn(("kb", "marketplace", "plugins", "doctor", "--scope", "project", "--json"), calls)
            self.assertNotIn("port", json.loads(platform_config.read_text())["gateway"])
            self.assertFalse(any(item.name.startswith("kb-marketplace-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
