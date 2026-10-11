import json
import hashlib
import io
import subprocess
import sys
import tempfile
import unittest
import unicodedata
from unittest.mock import patch
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from adapter_defaults_steps import _assert_required_upstreams_ready, run_adapter_default_steps
from service_steps import Journey
from test_install_layout import create_test_install_container


class AdapterDefaultJourneyTests(unittest.TestCase):
    def test_workflow_readiness_claim_fails_when_required_upstream_is_down(self):
        with self.assertRaisesRegex(RuntimeError, "required upstreams not ready: marketplace"):
            _assert_required_upstreams_ready({
                "ready": True,
                "components": {
                    "upstream:marketplace": {"ready": False, "status": "down"},
                    "projectRuntimes": {"ready": True, "status": "1/4 active"},
                },
            })

    def test_default_pack_and_platform_adapter_scope_are_observable(self):
        with tempfile.TemporaryDirectory(prefix="kb-adapter-defaults-") as directory:
            home = Path(directory)
            project_a = home / "project-a"
            (project_a / ".kb").mkdir(parents=True)
            (project_a / ".kb" / "kb.config.json").write_text("{}\n")
            (project_a / "README.md").write_text("adapter defaults fixture\n")
            project_id = "prj_" + hashlib.sha256(unicodedata.normalize("NFC", str(project_a.resolve())).encode()).hexdigest()[:16]
            registry_home = (home / ".kb").resolve()
            registry_home.mkdir()
            (registry_home / "projects.json").write_text(json.dumps({"schemaVersion": 1, "projects": [
                {"id": project_id, "path": str(project_a.resolve()), "name": "project-a", "status": "active",
                 "addedAt": "2026-10-01T00:00:00.000Z", "lastUsedAt": None}
            ]}))
            storage_default = registry_home / "state" / project_id / "storage"
            env = {"HOME": str(home), "KB_JOURNEY_PROJECT_ROOT": str(project_a)}
            calls: list[tuple[str, ...]] = []
            run_counter = 0
            host_running = True
            install_root = (home / "kb-platform").resolve()
            platform_root = create_test_install_container(install_root)
            receipt = platform_root / ".kb" / "v2" / "receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text(json.dumps({"id": "sealed", "plan": {"artifacts": [
                {"id": "workflow", "kind": "service", "package": "@kb-labs/workflow-daemon",
                 "version": "2.120.1", "sha256": "workflow-digest", "tarball": "registry/workflow.tgz"},
                {"id": "openai", "kind": "adapter", "package": "@kb-labs/adapters-openai", "config": [
                    {"id": "openai.apiKey", "optional": True, "severity": "warning",
                     "action": "set OPENAI_API_KEY to enable LLM features"}]},
            ]}}) + "\n")
            sealed_package = platform_root / "node_modules" / "@kb-labs" / "workflow-daemon" / "package.json"
            sealed_package.parent.mkdir(parents=True)
            sealed_package.write_text('{"name":"@kb-labs/workflow-daemon","version":"2.120.1"}\n')
            openai_package = platform_root / "node_modules" / "@kb-labs" / "adapters-openai" / "package.json"
            openai_package.parent.mkdir(parents=True)
            openai_package.write_text('{"name":"@kb-labs/adapters-openai","version":"2.120.1"}\n')
            for package_name in (
                "core-platform", "core-runtime", "core-sys", "perm-presets",
                "shared-cli-ui", "shared-command-kit", "shared-tool-kit",
            ):
                package_path = platform_root / "node_modules" / "@kb-labs" / package_name / "package.json"
                package_path.parent.mkdir(parents=True)
                package_path.write_text(json.dumps({"name": f"@kb-labs/{package_name}", "version": "2.120.1"}) + "\n")
            (platform_root / "package.json").write_text(json.dumps({
                "name": "kb-platform",
                "dependencies": {},
            }))
            (platform_root / "pnpm-workspace.yaml").write_text(
                'overrides:\n  "@kb-labs/workflow-daemon": "file:.kb/v2/cache/packages/workflow-digest.tgz"\n')
            platform_config = platform_root / ".kb" / "kb.config.jsonc"
            platform_config.parent.mkdir(parents=True, exist_ok=True)
            platform_config.write_text(json.dumps({"platform": {
                "adapters": {
                    "analytics": "@kb-labs/adapters-analytics-sqlite",
                    "eventBus": "@kb-labs/adapters-eventbus-cache",
                },
                "adapterOptions": {"analytics": {"filename": ".kb/analytics/analytics.sqlite"}},
            }}))
            gateway_manifest = platform_root / "node_modules" / "@kb-labs" / "gateway-app" / "kb-create.manifest.json"
            gateway_manifest.parent.mkdir(parents=True)
            gateway_manifest.write_text(json.dumps({"schema": "kb.create.artifact-manifest/v2", "id": "gateway-app",
                "package": "@kb-labs/gateway-app", "version": "1.0.0"}))

            def config(root: Path) -> dict:
                jsonc_file = root / ".kb" / "kb.config.jsonc"
                config_file = jsonc_file if jsonc_file.exists() else root / ".kb" / "kb.config.json"
                return json.loads(config_file.read_text()) if config_file.exists() else {}

            def write_config(root: Path, value: dict) -> None:
                jsonc_file = root / ".kb" / "kb.config.jsonc"
                config_file = jsonc_file if jsonc_file.exists() else root / ".kb" / "kb.config.json"
                config_file.parent.mkdir(parents=True, exist_ok=True)
                config_file.write_text(json.dumps(value))

            def fake_run(command, *, cwd, env, capture_output, text, timeout):
                nonlocal run_counter, host_running
                calls.append(tuple(command))
                root = Path(cwd)
                args = command[1:]
                if command[0] == "kb-create" and args[:2] == ["status", "--platform-root"]:
                    return subprocess.CompletedProcess(command, 0,
                        json.dumps({"ok": True, "host": {"healthUrl": "http://health.test/health"}}), "")
                if command[0] == "git" and args == ["ls-files", "-z"]:
                    return subprocess.CompletedProcess(command, 0, b"README.md\0", b"")
                if command[0] == "kb-create" and args[:3] == ["doctor", "--platform-root", str(install_root)]:
                    if "--fix" in args:
                        gateway_manifest.parent.mkdir(parents=True, exist_ok=True)
                        gateway_manifest.write_text(json.dumps({"schema": "kb.create.artifact-manifest/v2", "id": "gateway-app",
                            "package": "@kb-labs/gateway-app", "version": "1.0.0"}))
                        return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {
                            "operation": "doctor-fix", "findings": []}}), "")
                    if not gateway_manifest.exists():
                        result = {"ok": True, "data": {"operation": "doctor", "status": "unhealthy",
                            "findings": [{"code": "KB_INSTALL_MANIFEST_PROJECTION_INVALID",
                            "component": "@kb-labs/gateway-app", "path": "node_modules/@kb-labs/gateway-app/kb-create.manifest.json",
                            "hint": "Run `kb-create doctor --fix`."}], "platform": {"ok": True, "checks": []}}}
                        return subprocess.CompletedProcess(command, 0, json.dumps(result), "")
                    if not host_running:
                        result = {"ok": True, "data": {"operation": "doctor", "status": "unhealthy",
                            "findings": [], "platform": {"checks": [
                            {"id": "adapters", "detail": "Adapter status cannot be checked while the host is stopped.",
                             "hint": f'Run `kb-create start --platform-root "{install_root}"`.'}
                        ]}}}
                        if args[-1] == "--json":
                            result["data"]["findings"] = [{"code": "KB_ADAPTER_CREDENTIALS_MISSING", "component": "openai",
                                "path": "/platform/adapterOptions/llm/apiKey", "hint": "set OPENAI_API_KEY"}]
                            return subprocess.CompletedProcess(command, 0, json.dumps(result), "")
                        return subprocess.CompletedProcess(command, 0,
                            f'Finding KB_ADAPTER_CREDENTIALS_MISSING: package=openai path=/platform/adapterOptions/llm/apiKey\nAdapters: Adapter status cannot be checked while the host is stopped.\nNext: Run `kb-create start --platform-root "{install_root}"`.\n', "")
                    analytics_provider = config(platform_root).get("platform", {}).get("adapters", {}).get(
                        "analytics", "@kb-labs/adapters-analytics-sqlite")
                    statuses = [
                        {"slot": "storage", "mode": "real", "implementation": "@kb-labs/adapters-fs"},
                        {"slot": "cache", "mode": "real", "implementation": "@kb-labs/adapters-state-broker"},
                        *[{"slot": slot, "mode": "real", "implementation": slot}
                          for slot in ("logger", "documentDatabase")],
                        {"slot": "kvStore", "mode": "real", "implementation": "@kb-labs/adapters-sqlite/kv"},
                        {"slot": "analytics", "mode": "real", "implementation": analytics_provider},
                        {"slot": "eventBus", "mode": "real", "implementation": "@kb-labs/adapters-eventbus-cache"},
                        {"slot": "logs", "mode": "real", "implementation": "HybridLogReader"},
                        *[{"slot": slot, "mode": "inmemory", "implementation": slot}
                          for slot in ("config", "invoke", "artifacts", "notifier")],
                        *[{"slot": slot, "mode": "noop", "implementation": slot}
                          for slot in ("vectorStore", "embeddings")],
                        {"slot": "llm", "mode": "real", "implementation": "@kb-labs/adapters-openai"},
                    ]
                    if args[-1] == "--json":
                        findings = [{"code": "KB_ADAPTER_CREDENTIALS_MISSING", "severity": "warning", "component": "openai", "path": "/platform/adapterOptions/llm/apiKey", "action": "set OPENAI_API_KEY to enable LLM features", "hint": "set OPENAI_API_KEY"}]
                        result = {"ok": True, "data": {"findings": findings, "platform": {"ok": True, "checks": statuses}}}
                        return subprocess.CompletedProcess(command, 0, json.dumps(result), "")
                    return subprocess.CompletedProcess(command, 0,
                        "Warnings:\nKB_ADAPTER_CREDENTIALS_MISSING: set OPENAI_API_KEY to enable LLM features\nAdapter storage: mode=real implementation=@kb-labs/adapters-fs\n", "")
                if command[0] == "kb-create" and args[:2] in (["stop", "--platform-root"], ["start", "--platform-root"]):
                    host_running = args[0] == "start"
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if command[0] == "kb-create" and args[:2] == ["restart", "--platform-root"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:3] == ["adapter", "list", "--json"]:
                    value = config(root)
                    project_platform = value.get("platform", {})
                    if ("adapters" in project_platform or "adapterOptions" in project_platform or
                            "adapters" in value or "adapterOptions" in value):
                        error = {"code": "KB_ADAPTER_PROJECT_SCOPE", "message": "Project adapter configuration is unsupported.",
                                 "hint": "Move `platform.adapters` and `platform.adapterOptions` to the platform config."}
                        return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                    platform_value = config(platform_root)
                    platform_config = platform_value.get("platform", {})
                    options = platform_config.get("adapterOptions", {}).get("storage", {})
                    analytics_provider = platform_config.get("adapters", {}).get(
                        "analytics", "@kb-labs/adapters-analytics-sqlite")
                    storage_base = Path(options.get("baseDir", storage_default))
                    if not storage_base.is_absolute():
                        storage_base = root / storage_base
                    rows = [
                        {"slot": "storage", "implementation": "@kb-labs/adapters-fs", "mode": "real",
                        "baseDir": str(storage_base), "networkRequired": False},
                        {"slot": "cache", "implementation": "@kb-labs/adapters-state-broker", "mode": "real", "networkRequired": False},
                        {"slot": "logger", "implementation": "@kb-labs/adapters-pino", "mode": "real", "networkRequired": False},
                        {"slot": "analytics", "implementation": analytics_provider, "mode": "real", "networkRequired": False},
                        {"slot": "documentDatabase", "implementation": "@kb-labs/adapters-sqlite", "mode": "real", "networkRequired": False},
                        {"slot": "kvStore", "implementation": "@kb-labs/adapters-sqlite/kv", "mode": "real", "networkRequired": False},
                        {"slot": "eventBus", "implementation": "@kb-labs/adapters-eventbus-cache", "mode": "real", "networkRequired": False},
                        {"slot": "invoke", "implementation": "InMemoryInvoke", "mode": "inmemory", "networkRequired": False},
                        {"slot": "artifacts", "implementation": "InMemoryArtifacts", "mode": "inmemory", "networkRequired": False},
                        {"slot": "config", "implementation": "InMemoryConfig", "mode": "inmemory", "networkRequired": False},
                        {"slot": "logs", "implementation": "HybridLogReader", "mode": "real", "networkRequired": False},
                        {"slot": "notifier", "implementation": "InMemoryNotifier", "mode": "inmemory", "networkRequired": False},
                        {"slot": "llm", "implementation": "@kb-labs/adapters-openai", "mode": "real", "networkRequired": True,
                         "credentialsStatus": "missing", "missingCredentials": ["OPENAI_API_KEY"]},
                        {"slot": "embeddings", "implementation": "NoOpEmbeddings", "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                        {"slot": "vectorStore", "implementation": None, "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                        {"slot": "snapshotManager", "implementation": None, "mode": "unconfigured", "networkRequired": False,
                         "requiresConfiguration": True},
                    ]
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "adapters": rows}), "")
                if args[:2] == ["project", "add"]:
                    target = Path(args[2])
                    write_config(target, {})
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["config", "set"]:
                    if args[2:4] == ["commit.llm.enabled", "true"]:
                        return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                    if args[2:4] == ["--scope", "platform"]:
                        key, raw = args[4], args[5]
                    else:
                        key, raw = args[2], args[3]
                    if key != "platform.adapterOptions.storage":
                        return subprocess.CompletedProcess(command, 2, "", f"unexpected config key: {key}")
                    value = config(platform_root)
                    value.setdefault("platform", {}).setdefault("adapterOptions", {})["storage"] = json.loads(raw)
                    write_config(platform_root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "scope": "platform"}), "")
                if args[:2] == ["adapter", "bind"]:
                    value = config(platform_root)
                    value.setdefault("platform", {}).setdefault("adapters", {})[args[2]] = args[3]
                    write_config(platform_root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["adapter", "unbind"]:
                    value = config(platform_root)
                    value.setdefault("platform", {}).setdefault("adapters", {}).pop("storage", None)
                    value.setdefault("platform", {}).setdefault("adapterOptions", {}).pop("storage", None)
                    write_config(platform_root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["workflow", "run"]:
                    run_counter += 1
                    run_id = f"run-{run_counter}"
                    value = config(platform_root)
                    options = value.get("platform", {}).get("adapterOptions", {}).get("storage", {})
                    base_dir = Path(options.get("baseDir", storage_default))
                    if not base_dir.is_absolute():
                        base_dir = root / base_dir
                    record = base_dir / "workflows" / f"{run_id}.json"
                    record.parent.mkdir(parents=True, exist_ok=True)
                    record.write_text(json.dumps({"runId": run_id, "status": "success"}))
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"runId": run_id}}), "")
                if args[:3] == ["workflow", "runs", "watch"]:
                    return subprocess.CompletedProcess(command, 0, "Run SUCCESS.\n", "")
                if args[:3] == ["workflow", "runs", "view"]:
                    run_id = args[3]
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "data": {"id": run_id, "status": "success"}}), "")
                if args[:2] == ["commit", "generate"]:
                    error = {"code": "KB_ADAPTER_CREDENTIALS_MISSING", "message": "Adapter llm is missing OPENAI_API_KEY.",
                             "hint": "Set OPENAI_API_KEY or run `kb config set --scope platform platform.adapterOptions.llm.apiKey '${OPENAI_API_KEY}' --allow-missing-env`. No request was sent."}
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                if args[:2] == ["mind", "search"]:
                    error = {"code": "KB_ADAPTER_NOT_CONFIGURED", "message": "Adapter is not configured.",
                             "details": {"service": "vectorStore"},
                             "actions": [{"command": "kb adapter bind vectorStore @kb-labs/adapters-qdrant"}]}
                    return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                if args[:2] == ["marketplace", "install"] and args[2] == "@kb-labs/mind-entry":
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:3] == ["marketplace", "install", "@kb-labs/commit-entry"]:
                    value = config(root)
                    value.setdefault("plugins", {})["@kb-labs/commit-entry"] = True
                    write_config(root, value)
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:3] == ["marketplace", "plugins", "list"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True, "plugins": list(config(root).get("plugins", {}))}), "")
                if args[:3] == ["marketplace", "install", "@kb-labs/adapters-analytics-file"]:
                    workspace = (platform_root / "pnpm-workspace.yaml").read_text()
                    if '"@kb-labs/workflow-daemon": "file:.kb/v2/cache/packages/workflow-digest.tgz"' not in workspace:
                        error = {
                            "code": "KB_MARKETPLACE_PLATFORM_ARTIFACT_PINS_MISSING",
                            "message": "The installed platform is missing kb-create's sealed artifact overrides. No package was installed.",
                            "hint": f'Run "kb-create update --platform-root {install_root}" and retry "kb marketplace install @kb-labs/adapters-analytics-file@2.120.1 --scope platform".',
                        }
                        return subprocess.CompletedProcess(command, 1, json.dumps({"ok": False, "error": error}), "")
                    lock_path = platform_root / ".kb" / "marketplace.lock"
                    lock_path.parent.mkdir(parents=True, exist_ok=True)
                    lock = {"schema": "kb.marketplace/3", "plugins": {}, "adapters": {
                        "@kb-labs/adapters-analytics-file": {
                            "version": "2.120.1", "integrity": "sha512-fixture", "resolvedPath": "node_modules/@kb-labs/adapters-analytics-file",
                            "enabled": True, "source": "platform",
                        },
                    }}
                    lock_path.write_text(json.dumps(lock))
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                if args[:2] == ["project", "remove"]:
                    return subprocess.CompletedProcess(command, 0, json.dumps({"ok": True}), "")
                return subprocess.CompletedProcess(command, 2, "", f"unexpected command: {command}")

            raw_fake_run = fake_run

            def fake_run(command, *, cwd, env, capture_output, text, timeout):
                result = raw_fake_run(command, cwd=cwd, env=env, capture_output=capture_output,
                                      text=text, timeout=timeout)
                if result.returncode == 0 and "--json" in command and result.stdout:
                    try:
                        envelope = json.loads(result.stdout)
                    except json.JSONDecodeError:
                        return result
                    if isinstance(envelope, dict) and envelope.get("ok") is True and "data" not in envelope:
                        envelope = {"ok": True, "data": {key: value for key, value in envelope.items() if key != "ok"}}
                        return subprocess.CompletedProcess(command, result.returncode, json.dumps(envelope), result.stderr)
                return result

            journey = Journey("local", "adapter-defaults-fixture")
            runtime_adapters = [
                {"slot": "storage", "mode": "real", "implementation": "@kb-labs/adapters-fs"},
                {"slot": "cache", "mode": "real", "implementation": "@kb-labs/adapters-state-broker"},
                *[{"slot": slot, "mode": "real", "implementation": slot}
                  for slot in ("logger", "documentDatabase")],
                {"slot": "kvStore", "mode": "real", "implementation": "@kb-labs/adapters-sqlite/kv"},
                {"slot": "analytics", "mode": "real", "implementation": None},
                {"slot": "eventBus", "mode": "real", "implementation": "@kb-labs/adapters-eventbus-cache"},
                {"slot": "logs", "mode": "real", "implementation": "HybridLogReader"},
                *[{"slot": slot, "mode": "inmemory", "implementation": slot}
                  for slot in ("config", "invoke", "artifacts", "notifier")],
                *[{"slot": slot, "mode": "noop", "implementation": slot}
                  for slot in ("vectorStore", "embeddings")],
                {"slot": "llm", "mode": "real", "implementation": "@kb-labs/adapters-openai"},
            ]

            def health_adapters() -> list[dict]:
                # The host reports the provider currently bound in the platform config.
                bound = config(platform_root).get("platform", {}).get("adapters", {})
                provider = bound.get("analytics", "@kb-labs/adapters-analytics-sqlite")
                return [{**row, "implementation": provider} if row["slot"] == "analytics" else row
                        for row in runtime_adapters]

            def fake_urlopen(request, *args, **kwargs):
                url = request.full_url if hasattr(request, "full_url") else str(request)
                if url.endswith("/ready"):
                    return io.BytesIO(json.dumps({"ok": True, "data": {
                        "ready": True,
                        "components": {
                            "upstream:marketplace": {"ready": True, "status": "up"},
                        },
                    }}).encode())
                if url.endswith("/health"):
                    return io.BytesIO(json.dumps({"ok": True, "data": {
                        "status": "healthy",
                        "upstreams": {"marketplace": {"status": "up"}},
                        "projectRuntimes": {"limit": 4, "active": 1, "runtimes": [
                            {"projectId": project_id, "state": "ready"}]}}}).encode())
                return io.BytesIO(json.dumps({"ok": True, "data": health_adapters()}).encode())

            with patch("adapter_defaults_steps.urllib.request.urlopen", side_effect=fake_urlopen):
                run_adapter_default_steps(journey, project_a, env, fake_run)

            expected = {f"S-054.{number}" for number in range(1, 9)} | {f"S-054.{number}" for number in range(10, 17)} | {f"S-048.{number}" for number in range(1, 7)}
            claims = {claim["claimId"] for claim in journey.result().case_run["claims"]}
            self.assertEqual(journey.result().status, "PASS", journey.result().first_failure)
            self.assertEqual(claims, expected)
            self.assertIn(("kb", "adapter", "list", "--json"), calls)
            self.assertIn(("kb", "adapter", "unbind", "storage", "--json"), calls)
            self.assertIn(("kb", "config", "set", "commit.llm.enabled", "true", "--json"), calls)
            self.assertIn(("kb", "commit", "generate", "--json"), calls)
            self.assertIn(("kb", "mind", "search", "--text", "adapter defaults", "--json"), calls)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/mind-entry", "--json"), calls)
            self.assertIn(("kb", "marketplace", "install", "@kb-labs/adapters-analytics-file", "--scope", "platform", "--json"), calls)
            self.assertIn(("kb-create", "doctor", "--platform-root", str(install_root), "--json"), calls)
            self.assertIn(("kb-create", "doctor", "--platform-root", str(install_root)), calls)
            self.assertIn(("kb-create", "doctor", "--platform-root", str(install_root), "--fix", "--json"), calls)
            self.assertIn(("kb-create", "status", "--platform-root", str(install_root), "--json"), calls)
            self.assertFalse(any(call[0] == "kb-dev" and "doctor" in call for call in calls))
            self.assertNotIn(("kb", "marketplace", "install", "@kb-labs/adapters-fs", "--scope", "platform"), calls)
            self.assertFalse(any(item.name.startswith("kb-adapter-project-b-") for item in home.iterdir()))


if __name__ == "__main__":
    unittest.main()
