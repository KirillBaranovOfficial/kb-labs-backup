import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from adapter_defaults_steps import run_adapter_default_steps
from service_steps import Journey


class AdapterDefaultJourneyTests(unittest.TestCase):
    def test_default_pack_and_project_storage_override_are_observable(self):
        with tempfile.TemporaryDirectory(prefix="kb-adapter-defaults-") as directory:
            home = Path(directory)
            project_a = home / "project-a"
            (project_a / ".kb").mkdir(parents=True)
            (project_a / ".kb" / "kb.config.json").write_text("{}\n")
            (project_a / "README.md").write_text("adapter defaults fixture\n")
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project_a)}
            calls: list[tuple[str, ...]] = []
            run_counter = 0

            def config(root: Path) -> dict:
                config_file = root / ".kb" / "kb.config.json"
                return json.loads(config_file.read_text()) if config_file.exists() else {}

            def write_config(root: Path, value: dict) -> None:
                config_file = root / ".kb" / "kb.config.json"
                config_file.parent.mkdir(parents=True, exist_ok=True)
                config_file.write_text(json.dumps(value))

            def fake_run(command, *, cwd, env, capture_output, text, timeout):
                nonlocal run_counter
                calls.append(tuple(command))
                root = Path(cwd)
                args = command[1:]
                if command[0] == "git" and args == ["ls-files", "-z"]:
                    return subprocess.CompletedProcess(command, 0, b"README.md\0", b"")
                if command[0] == "kb-dev" and args == ["doctor", "--json"]:
                    result = {"ok": True, "adapters": [
                        {"slot": "storage", "mode": "real", "implementation": "@kb-labs/adapters-fs"},
                        {"slot": "cache", "mode": "real", "implementation": "@kb-labs/adapters-state-broker"},
                        *[{"slot": slot, "mode": "real", "implementation": slot}
                          for slot in ("logger", "analytics", "documentDatabase", "kvStore")],
                        {"slot": "logs", "mode": "real", "implementation": "HybridLogReader"},
                    ]}
                    return subprocess.CompletedProcess(command, 0, json.dumps(result), "")
                if args[:3] == ["adapter", "list", "--json"]:
                    value = config(root)
                    options = value.get("platform", {}).get("adapterOptions", {}).get("storage", {})
                    override = value.get("platform", {}).get("adapters", {}).get("storage")
                    rows = [
                        {"slot": "storage", "implementation": "@kb-labs/adapters-fs", "mode": "real",
                        "baseDir": str(root / options.get("baseDir", ".kb/data")), "networkRequired": False},
                        {"slot": "cache", "implementation": "@kb-labs/adapters-state-broker", "mode": "real", "networkRequired": False},
                        {"slot": "logger", "implementation": "@kb-labs/adapters-pino", "mode": "real", "networkRequired": False},
                        {"slot": "analytics", "implementation": "@kb-labs/adapters-analytics-sqlite", "mode": "real", "networkRequired": False},
                        {"slot": "documentDatabase", "implementation": "@kb-labs/adapters-sqlite", "mode": "real", "networkRequired": False},
                        {"slot": "kvStore", "implementation": "@kb-labs/adapters-sqlite", "mode": "real", "networkRequired": False},
                        {"slot": "eventBus", "implementation": "InMemoryEventBus", "mode": "inmemory", "networkRequired": False},
                        {"slot": "invoke", "implementation": "InMemoryInvoke", "mode": "inmemory", "networkRequired": False},
                        {"slot": "artifacts", "implementation": "InMemoryArtifacts", "mode": "inmemory", "networkRequired": False},
                        {"slot": "config", "implementation": "InMemoryConfig", "mode": "inmemory", "networkRequired": False},
                        {"slot": "logs", "implementation": "HybridLogReader", "mode": "real", "networkRequired": False},
                        {"slot": "notifier", "implementation": "InMemoryNotifier", "mode": "inmemory", "networkRequired": False},
                        {"slot": "llm", "implementation": None, "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                        {"slot": "embeddings", "implementation": "NoOpEmbeddings", "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                        {"slot": "vectorStore", "implementation": None, "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                        {"slot": "snapshotManager", "implementation": None, "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                    ]
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "adapters": rows, "storageOverride": override}), "")
                if args[:2] == ["project", "add"]:
                    target = Path(args[2])
                    write_config(target, {})
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["config", "set"]:
                    if args[2:4] == ["commit.llm.enabled", "true"]:
                        return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                    value = config(root)
                    value.setdefault("platform", {}).setdefault("adapterOptions", {})["storage"] = json.loads(args[3])
                    write_config(root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "scope": "project"}), "")
                if args[:2] == ["adapter", "bind"]:
                    value = config(root)
                    value.setdefault("platform", {}).setdefault("adapters", {})["storage"] = args[3]
                    write_config(root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["adapter", "unbind"]:
                    value = config(root)
                    value.setdefault("platform", {}).setdefault("adapters", {}).pop("storage", None)
                    value.setdefault("platform", {}).setdefault("adapterOptions", {}).pop("storage", None)
                    write_config(root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["workflow", "run"]:
                    run_counter += 1
                    run_id = f"run-{run_counter}"
                    value = config(root)
                    options = value.get("platform", {}).get("adapterOptions", {}).get("storage", {})
                    base_dir = options.get("baseDir", ".kb/data")
                    record = root / base_dir / "workflows" / f"{run_id}.json"
                    record.parent.mkdir(parents=True, exist_ok=True)
                    record.write_text(json.dumps({"runId": run_id, "status": "success"}))
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"runId": run_id}}), "")
                if args[:3] == ["workflow", "runs", "watch"]:
                    return subprocess.CompletedProcess(command, 0, "Run SUCCESS.\n", "")
                if args[:3] == ["workflow", "runs", "view"]:
                    run_id = args[3]
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"id": run_id, "status": "success"}}), "")
                if args[:2] == ["commit", "generate"]:
                    error = {"code": "KB_ADAPTER_NOT_CONFIGURED", "details": {"service": "llm"},
                             "actions": [{"command": "kb adapter bind llm @kb-labs/adapters-openai"}]}
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                if args[:2] == ["mind", "search"]:
                    error = {"code": "KB_ADAPTER_NOT_CONFIGURED", "details": {"service": "vectorStore"},
                             "actions": [{"command": "kb adapter bind vectorStore @kb-labs/adapters-qdrant"}]}
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                if args[:2] == ["project", "remove"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command}")

            journey = Journey("local", "adapter-defaults-fixture")
            run_adapter_default_steps(journey, project_a, env, fake_run)

            expected = {f"S-054.{number}" for number in range(1, 7)} | {f"S-048.{number}" for number in range(1, 7)}
            claims = {claim["claimId"] for claim in journey.result().case_run["claims"]}
            self.assertEqual(journey.result().status, "PASS", journey.result().first_failure)
            self.assertEqual(claims, expected)
            self.assertIn(("kb", "adapter", "list", "--json"), calls)
            self.assertIn(("kb", "config", "set", "commit.llm.enabled", "true", "--json"), calls)
            self.assertIn(("kb", "commit", "generate", "--json"), calls)
            self.assertIn(("kb", "mind", "search", "--text", "adapter defaults", "--json"), calls)
            self.assertNotIn(("kb", "marketplace", "install", "@kb-labs/adapters-fs", "--scope", "platform"), calls)
            self.assertFalse(any(item.name.startswith("kb-adapter-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
