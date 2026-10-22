from __future__ import annotations

import json
import os
import shutil
from pathlib import Path
import subprocess
import tempfile
import unittest

from test_environment import isolated_environment


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "e2e" / "journey" / "run.sh"


class JourneyComposeMountTests(unittest.TestCase):
    def _fake_compose_environment(self, temp: str) -> tuple[dict[str, str], Path]:
        bin_dir = Path(temp) / "bin"
        bin_dir.mkdir()
        git = bin_dir / "git"
        git.write_text("#!/bin/sh\nprintf 'test-revision\\n'\n")
        git.chmod(0o755)
        compose_args = Path(temp) / "compose-args.txt"
        compose = bin_dir / "docker"
        compose.write_text("""#!/bin/sh
if [ "$1 $2" = 'compose version' ]; then exit 0; fi
shift
printf '%s\\n' "$@" > "$COMPOSE_ARGS_OUT"
case "$*" in
  *' config --format json') printf '{}\\n'; exit 0 ;;
esac
printf '%s\\n' "$@" > "$COMPOSE_ARGS_OUT"
""")
        compose.chmod(0o755)
        env = isolated_environment(Path(temp) / "home", bin_dir,
                                   required_tools=("bash", "git", "dirname", "id", "node", "mktemp", "rm"),
                                   overrides={"COMPOSE_ARGS_OUT": str(compose_args)})
        return env, compose_args

    def _config(self, *, hosted: bool) -> dict:
        with tempfile.TemporaryDirectory(prefix="journey-compose-render-") as temp:
            bin_dir = Path(temp) / "bin"
            bin_dir.mkdir()
            compose_tools = tuple(tool for tool in ("docker", "docker-compose") if shutil.which(tool))
            self.assertTrue(compose_tools, "journey Compose tests require Docker Compose v2 or docker-compose on PATH")
            env = isolated_environment(Path(temp) / "home", bin_dir,
                                       required_tools=("bash", "git", "dirname", "id", "node", "mktemp", "rm", *compose_tools),
                                       overrides={
                                           "KB_JOURNEY_WORKSPACE_SOURCE": str(ROOT),
                                           "KB_JOURNEY_GIT_REVISION": "test-revision",
                                           "KB_JOURNEY_RUNNER_UID": str(os.getuid()),
                                           "KB_JOURNEY_RUNNER_GID": str(os.getgid()),
                                       })
            compose_command = ["docker", "compose"] if shutil.which("docker") and subprocess.run(
                ["docker", "compose", "version"], capture_output=True, check=False,
            ).returncode == 0 else ["docker-compose"]
            composition = Path(temp) / "marketplace-registry-composition"
            producer = ROOT / "services" / "marketplace-registry" / "app" / "scripts" / "render-journey-composition.mjs"
            rendered = subprocess.run(
                ["node", str(producer), "--output", str(composition),
                 "--registry-url", "http://marketplace-registry:5071"],
                cwd=ROOT, env=env, capture_output=True, text=True, check=False,
            )
            self.assertEqual(rendered.returncode, 0, rendered.stderr)
            producer_output = json.loads(rendered.stdout)
            overlay = Path(producer_output["composeOverlayPath"])
            self.assertTrue((composition / ".kb" / "kb.config.jsonc").is_file())
            self.assertTrue((composition / ".kb" / "marketplace.lock").is_file())
            self.assertTrue(overlay.is_file())
            validation = subprocess.run(
                ["node", "--input-type=module", "-e", """
import fs from 'node:fs';
import { validateProductConfig } from './core/config/dist/index.js';
import { validateMarketplaceLock } from './core/discovery/dist/index.js';
const root = process.argv[1];
const config = JSON.parse(fs.readFileSync(`${root}/.kb/kb.config.jsonc`, 'utf8'));
const lock = JSON.parse(fs.readFileSync(`${root}/.kb/marketplace.lock`, 'utf8'));
const configResult = validateProductConfig('marketplace', config.marketplace);
const lockResult = validateMarketplaceLock(lock, 'platform');
if (!configResult.ok || !lockResult.valid) {
  process.stderr.write(JSON.stringify({ config: configResult.errors, lock: lockResult }));
  process.exitCode = 1;
}
""", str(composition)],
                cwd=ROOT, env=env, capture_output=True, text=True, check=False,
            )
            self.assertEqual(validation.returncode, 0, validation.stderr)
            if hosted:
                env["CI"] = "true"
                env["GITHUB_ACTIONS"] = "true"
            env["COMPOSE_PROFILES"] = "marketplace-registry"
            result = subprocess.run(
                ["bash", str(RUNNER), "--render-compose-config"],
                cwd=ROOT, env=env, capture_output=True, text=True, check=False,
            )
        self.assertEqual(
            result.returncode, 0,
            "journey Compose rendering failed; install Docker Compose v2 (`docker compose`) or "
            f"the standalone `docker-compose` command and rerun. Output: {result.stderr.strip()}",
        )
        rendered = json.loads(result.stdout)
        mounts = {
            mount["target"]: mount
            for mount in rendered["services"]["marketplace-registry"].get("volumes", [])
            if mount.get("type") == "bind"
        }
        self.assertEqual(set(mounts), {"/app/.kb/kb.config.jsonc", "/app/.kb/marketplace.lock"})
        config_source = Path(mounts["/app/.kb/kb.config.jsonc"]["source"])
        lock_source = Path(mounts["/app/.kb/marketplace.lock"]["source"])
        self.assertEqual(config_source.name, "kb.config.jsonc")
        self.assertEqual(lock_source.name, "marketplace.lock")
        self.assertEqual(config_source.parent, lock_source.parent)
        self.assertEqual(config_source.parent.name, ".kb")
        self.assertTrue(all(mount.get("read_only") is True for mount in mounts.values()))
        return rendered

    def test_default_compose_configuration_does_not_require_registry_lane_inputs(self):
        with tempfile.TemporaryDirectory(prefix="journey-compose-default-") as temp:
            compose_command = ["docker", "compose"] if shutil.which("docker") and subprocess.run(
                ["docker", "compose", "version"], capture_output=True, check=False,
            ).returncode == 0 else ["docker-compose"]
            env = isolated_environment(
                Path(temp) / "home", Path(temp) / "bin", required_tools=tuple(compose_command),
                overrides={
                    "KB_JOURNEY_WORKSPACE_SOURCE": str(ROOT),
                    "KB_JOURNEY_GIT_REVISION": "test-revision",
                    "KB_JOURNEY_RUNNER_UID": str(os.getuid()),
                    "KB_JOURNEY_RUNNER_GID": str(os.getgid()),
                },
            )
            result = subprocess.run(
                [*compose_command, "-f", "e2e/docker-compose.yml", "-f",
                 "e2e/journey/docker-compose.yml", "config", "--quiet"],
                cwd=ROOT, env=env, capture_output=True, text=True, check=False,
            )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_registry_lane_adds_producer_overlay_through_compose_owner(self):
        for action, suffix in (("--render-compose-config", ["config", "--format", "json"]),
                               ("--down", ["down", "--volumes", "--remove-orphans"])):
            with tempfile.TemporaryDirectory(prefix="journey-compose-registry-owner-") as temp:
                env, compose_args = self._fake_compose_environment(temp)
                env["COMPOSE_PROFILES"] = "marketplace-registry"
                result = subprocess.run(
                    ["bash", str(RUNNER), action], cwd=ROOT, env=env,
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                args = compose_args.read_text().splitlines()
            self.assertEqual(args.count("-f"), 3)
            self.assertTrue(any(argument.endswith("docker-compose.registry.yml") for argument in args))
            self.assertEqual(args[-len(suffix):], suffix)

    def test_default_compose_owner_uses_only_default_files(self):
        with tempfile.TemporaryDirectory(prefix="journey-compose-default-owner-") as temp:
            env, compose_args = self._fake_compose_environment(temp)
            result = subprocess.run(
                ["bash", str(RUNNER), "--render-compose-config"], cwd=ROOT, env=env,
                capture_output=True, text=True, check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            args = compose_args.read_text().splitlines()
        self.assertNotIn("docker-compose.registry.yml", " ".join(args))

    # case: S-262.1
    def test_local_and_hosted_journeys_render_the_same_compose_bind_set(self):
        expected = {
            ("platform", "/workspace/e2e-host"): ROOT / "e2e",
            ("runner", "/summary"): ROOT / ".journey" / "reports",
            ("runner", "/opt/kb-workspace"): ROOT,
            ("runner", "/e2e-oidc-certs"): ROOT / "e2e" / "oidc-idp" / "certs",
            ("marketplace-tamper", "/fixture/marketplace_tamper_proxy.py"): ROOT / "e2e" / "journey" / "marketplace_tamper_proxy.py",
            ("origin", "/release"): ROOT / ".journey" / "release",
            ("mock-llm", "/fixture/mock_llm_server.py"): ROOT / "e2e" / "journey" / "mock_llm_server.py",
            ("idp-stub", "/certs"): ROOT / "e2e" / "oidc-idp" / "certs",
            ("verdaccio", "/verdaccio/conf/config.yaml"): ROOT / "e2e" / "verdaccio" / "config.yaml",
        }
        for hosted in (False, True):
            rendered = self._config(hosted=hosted)
            binds = {
                (service_name, mount["target"]): Path(mount["source"]).resolve()
                for service_name, service in rendered["services"].items()
                for mount in service.get("volumes", [])
                if mount.get("type") == "bind"
            }
            expected_binds = {key: source.resolve() for key, source in expected.items()}
            non_registry_binds = {key: source for key, source in binds.items()
                                  if key[0] != "marketplace-registry"}
            self.assertEqual(non_registry_binds, expected_binds)
            self.assertEqual(rendered["services"]["runner"]["user"], f"{os.getuid()}:{os.getgid()}")
            self.assertEqual(rendered["services"]["marketplace-registry"]["profiles"], ["marketplace-registry"])
            self.assertEqual(rendered["services"]["marketplace-state"]["profiles"], ["marketplace-registry"])
            self.assertEqual(rendered["services"]["marketplace-registry"]["build"]["dockerfile"],
                             "services/marketplace-registry/app/Dockerfile")
            self.assertEqual(rendered["services"]["marketplace-state"]["build"]["dockerfile"],
                             "services/state/daemon/Dockerfile")
            self.assertEqual(rendered["services"]["marketplace-tamper"]["profiles"], ["marketplace-registry"])

    # case: S-262.1
    def test_local_and_hosted_journey_use_the_same_compose_file_list(self):
        invocations = []
        for hosted in (False, True):
            with tempfile.TemporaryDirectory(prefix="journey-compose-down-") as temp:
                env, compose_args = self._fake_compose_environment(temp)
                if hosted:
                    env["CI"] = "true"
                    env["GITHUB_ACTIONS"] = "true"
                result = subprocess.run(
                    ["bash", str(RUNNER), "--down"], cwd=ROOT, env=env,
                    capture_output=True, text=True, check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                invocations.append(compose_args.read_text().splitlines())

        self.assertEqual(invocations[0], invocations[1])
        self.assertEqual(invocations[0], [
            "-f", "e2e/docker-compose.yml",
            "-f", "e2e/journey/docker-compose.yml",
            "down", "--volumes", "--remove-orphans",
        ])

    def test_missing_compose_binary_fails_with_an_actionable_message(self):
        with tempfile.TemporaryDirectory(prefix="journey-compose-missing-") as temp:
            env, _ = self._fake_compose_environment(temp)
            # Remove the fake Docker CLI so the real host Compose plugin cannot satisfy this test.
            (Path(temp) / "bin" / "docker").unlink()
            result = subprocess.run(
                ["bash", str(RUNNER), "--render-compose-config"], cwd=ROOT, env=env,
                capture_output=True, text=True, check=False,
            )

            self.assertEqual(result.returncode, 127)
            self.assertIn("install Docker Compose v2", result.stderr)
            self.assertIn("standalone 'docker-compose' command", result.stderr)


if __name__ == "__main__":
    unittest.main()
