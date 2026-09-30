import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from marketplace_steps import run_marketplace_steps
from service_steps import Journey


class MarketplaceStepTests(unittest.TestCase):
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
                        "@kb-labs/workflow-entry": "2.120.1",
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

            expected = {f"S-{case}.{step}" for case, steps in (("010", range(1, 6)), ("047", range(1, 4)), ("048", (1, 2, 3, 4))) for step in steps}
            actual = {claim["claimId"] for claim in journey.result().case_run["claims"]}
            self.assertEqual(journey.result().status, "PASS", journey.result().first_failure)
            self.assertEqual(actual, expected)
            self.assertNotIn(("kb", "workflow", "run", "--workflow-id=e2e-hello", "--trigger-type=manual", "--json"), calls)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/commit-entry@2.120.1", "--json"), calls)
            self.assertIn(("kb", "adapter", "unbind", "storage", "--json"), calls)
            self.assertFalse(any(item.name.startswith("kb-marketplace-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
