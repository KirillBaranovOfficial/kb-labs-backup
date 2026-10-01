import json
import ast
import subprocess
import sys
import tempfile
import unittest
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from marketplace_steps import (_output, _platform_snapshot, authenticate_secured_marketplace,
                               run_marketplace_steps, wait_for_marketplace_ready)
from service_steps import Journey
from invocations import invocation_context, open_http, run_command


class MarketplaceStepTests(unittest.TestCase):
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
                return subprocess.CompletedProcess(command, 0, '{"ok":true}', "")

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
                        return subprocess.CompletedProcess(command, 0, json.dumps({"entries": []}), "")
                    return subprocess.CompletedProcess(command, 1, json.dumps({
                        "ok": False,
                        "error": {
                            "code": "KB_AUTH_REQUIRED",
                            "message": "Authentication is required.",
                            "hint": 'Run "kb auth login".',
                        },
                    }), "")
                if args[:2] == ["auth", "login"]:
                    logged_in = True
                    return subprocess.CompletedProcess(command, 0, '{"ok":true}', "")
                if args[:2] == ["auth", "status"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({
                        "ok": True, "session": {"authenticated": logged_in},
                    }), "")
                if args[:3] == ["marketplace", "plugins", "list"] and logged_in:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"entries": []}), "")
                return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command[1:3]}")

            journey = Journey("secured", "marketplace-auth-fixture")
            authenticate_secured_marketplace(journey, project, env, fake_run)

            self.assertEqual(journey.result().status, "PASS")
            self.assertEqual(journey.result().case_run["claims"], [
                {"claimId": "S-041.7", "kind": "journey", "status": "PASS"},
            ])
            self.assertIn(("kb", "auth", "login", "--gateway-url", "http://gateway.test",
                           "--email", "admin@example.test", "--password", "fixture-password", "--json"), calls)
            self.assertIn(("kb", "auth", "status", "--json"), calls)

    def test_marketplace_and_adapter_flows_claim_only_asserted_case_steps(self):
        with tempfile.TemporaryDirectory(prefix="kb-marketplace-journey-") as directory:
            home = Path(directory)
            project = home / "project-a"
            (project / ".kb").mkdir(parents=True)
            (project / ".kb" / "kb.config.json").write_text("{}\n")
            platform_root = home / "kb-platform"
            receipt = platform_root / ".kb" / "v2" / "receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text('{"version":"2.120.1"}\n')
            package = platform_root / "node_modules" / "@kb-labs" / "workflow-daemon" / "package.json"
            package.parent.mkdir(parents=True)
            package.write_text('{"name":"@kb-labs/workflow-daemon","version":"2.120.1"}\n')
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project),
                   "KB_JOURNEY_PLATFORM_ROOT": str(platform_root)}
            calls: list[tuple[str, ...]] = []

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
                calls.append(tuple(command))
                cwd_path = Path(cwd)
                if command[0] == "npm":
                    package_id = command[2]
                    versions = {
                        "@kb-labs/commit-entry": "2.120.1",
                        "@kb-labs/qa-entry": "2.120.1",
                        "@kb-labs/adapters-fs": "2.107.0",
                    }
                    return subprocess.CompletedProcess(command, 0, json.dumps(versions[package_id]), "")

                args = command[1:]
                stdout = "{}"
                if args[:3] == ["marketplace", "plugins", "doctor"]:
                    stdout = json.dumps({"ok": True, "total": 0, "issues": []})
                elif args[:2] == ["marketplace", "install"]:
                    package_id, version = args[2].rsplit("@", 1)
                    platform = "--scope" in args and args[args.index("--scope") + 1] == "platform"
                    lock_root = platform_root if platform else cwd_path
                    value = lock(lock_root, platform=platform)
                    value.setdefault("adapters", {})
                    collection = value["plugins"]
                    collection[package_id] = {
                        "version": version, "integrity": "sha512-fixture", "source": "marketplace",
                        "enabled": True, "resolvedPath": f"store:{package_id}@{version}",
                    }
                    save_lock(lock_root, value)
                    stdout = json.dumps({
                        "installed": [{"id": package_id, "version": version}],
                        "warnings": [],
                        "scope": "platform" if platform else "project",
                    })
                elif args[:3] == ["marketplace", "plugins", "list"]:
                    value = lock(cwd_path)
                    entries = [{"id": package_id, **entry} for package_id, entry in value["plugins"].items()]
                    stdout = json.dumps({"entries": entries})
                elif args[:2] == ["commit", "open"]:
                    value = lock(cwd_path)
                    if "@kb-labs/commit-entry" not in value["plugins"]:
                        return subprocess.CompletedProcess(command, 1, "Command not found: commit open", "")
                    stdout = json.dumps({"hasPlan": False})
                elif args[:2] == ["marketplace", "uninstall"]:
                    value = lock(cwd_path)
                    value["plugins"].pop(args[2], None)
                    save_lock(cwd_path, value)
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
                return subprocess.CompletedProcess(command, 0, stdout, "")

            journey = Journey("local", "marketplace-fixture")
            run_marketplace_steps(journey, project, "http://registry.test", env, fake_run)

            expected = {f"S-{case}.{step}" for case, steps in (("010", range(1, 6)), ("047", range(1, 4))) for step in steps}
            actual = {claim["claimId"] for claim in journey.result().case_run["claims"]}
            self.assertEqual(journey.result().status, "PASS", journey.result().first_failure)
            self.assertEqual(actual, expected)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1", "--json"), calls)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1",
                           "--scope", "platform", "--json"), calls)
            self.assertNotIn(("npm", "view", "@kb-labs/adapters-fs", "version", "--json", "--registry=http://registry.test"), calls)
            self.assertNotIn(("kb", "marketplace", "install", "@kb-labs/adapters-fs@2.107.0", "--scope", "platform", "--json"), calls)
            self.assertIn(("kb", "qa", "cases", "list", "--json"), calls)
            self.assertFalse(any(item.name.startswith("kb-marketplace-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
