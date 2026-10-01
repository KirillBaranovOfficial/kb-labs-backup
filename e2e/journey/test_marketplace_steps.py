import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from marketplace_steps import _output, authenticate_secured_marketplace, run_marketplace_steps
from service_steps import Journey


class MarketplaceStepTests(unittest.TestCase):
    def test_marketplace_failure_detail_keeps_error_start_and_end(self):
        command = ["kb", "marketplace", "install", "@kb-labs/qa-entry"]
        raw = "ERR_PNPM_RESOLUTION_FAILURE: root cause " + ("detail " * 400) + "final context"
        result = subprocess.CompletedProcess(command, 1, raw, "")

        with self.assertRaisesRegex(RuntimeError, "ERR_PNPM_RESOLUTION_FAILURE: root cause") as raised:
            _output(result, command)

        self.assertIn("final context", str(raised.exception))

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
                return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command[1:3]}")

            journey = Journey("secured", "marketplace-auth-fixture")
            authenticate_secured_marketplace(journey, project, env, fake_run)

            self.assertEqual(journey.result().status, "PASS")
            self.assertEqual(journey.result().case_run["claims"], [
                {"claimId": "S-010.6", "kind": "journey", "status": "PASS"},
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
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project)}
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
                if args[:2] == ["marketplace", "install"]:
                    package_id, version = args[2].rsplit("@", 1)
                    platform = "--scope" in args and args[args.index("--scope") + 1] == "platform"
                    value = lock(cwd_path, platform=platform)
                    value.setdefault("adapters", {})
                    collection = value["adapters"] if platform else value["plugins"]
                    collection[package_id] = {
                        "version": version, "integrity": "sha512-fixture", "source": "platform" if platform else "marketplace",
                        "enabled": True,
                    }
                    save_lock(cwd_path, value)
                    stdout = json.dumps({"ok": True})
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
            self.assertNotIn(("npm", "view", "@kb-labs/adapters-fs", "version", "--json", "--registry=http://registry.test"), calls)
            self.assertNotIn(("kb", "marketplace", "install", "@kb-labs/adapters-fs@2.107.0", "--scope", "platform", "--json"), calls)
            self.assertIn(("kb", "qa", "cases", "list", "--json"), calls)
            self.assertFalse(any(item.name.startswith("kb-marketplace-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
