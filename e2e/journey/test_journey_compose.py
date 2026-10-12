from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


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
        compose = bin_dir / "docker-compose"
        compose.write_text("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$COMPOSE_ARGS_OUT\"\n")
        compose.chmod(0o755)
        env = dict(os.environ)
        env["PATH"] = str(bin_dir) + os.pathsep + "/usr/bin:/bin"
        env["COMPOSE_ARGS_OUT"] = str(compose_args)
        return env, compose_args

    def _config(self, *, ci: bool) -> dict:
        env = dict(os.environ)
        env["KB_JOURNEY_CI"] = "1" if ci else "0"
        result = subprocess.run(
            ["bash", str(RUNNER), "--render-compose-config"],
            cwd=ROOT, env=env, capture_output=True, text=True, check=False,
        )
        self.assertEqual(
            result.returncode, 0,
            "journey Compose rendering failed; install Docker Compose v2 (`docker compose`) or "
            f"the standalone `docker-compose` command and rerun. Output: {result.stderr.strip()}",
        )
        return json.loads(result.stdout)

    def test_rendered_bind_set_matches_runner_compose_inputs_for_local_and_ci(self):
        expected = {
            ("platform", "/workspace/e2e-host"): ROOT / "e2e",
            ("runner", "/summary"): ROOT / ".journey" / "reports",
            ("runner", "/opt/kb-workspace"): ROOT,
            ("runner", "/e2e-oidc-certs"): ROOT / "e2e" / "oidc-idp" / "certs",
            ("origin", "/release"): ROOT / ".journey" / "release",
            ("mock-llm", "/fixture/mock_llm_server.py"): ROOT / "e2e" / "journey" / "mock_llm_server.py",
            ("idp-stub", "/certs"): ROOT / "e2e" / "oidc-idp" / "certs",
            ("verdaccio", "/verdaccio/conf/config.yaml"): ROOT / "e2e" / "verdaccio" / "config.yaml",
        }
        for ci in (False, True):
            rendered = self._config(ci=ci)
            binds = {
                (service_name, mount["target"]): Path(mount["source"]).resolve()
                for service_name, service in rendered["services"].items()
                for mount in service.get("volumes", [])
                if mount.get("type") == "bind"
            }
            configured = dict(expected)
            if ci:
                configured[("verdaccio", "/verdaccio/storage")] = ROOT / "e2e" / ".verdaccio-storage"
            self.assertEqual(binds, {key: source.resolve() for key, source in configured.items()})

    def test_ci_teardown_uses_the_runner_owned_compose_file_order(self):
        with tempfile.TemporaryDirectory(prefix="journey-compose-down-") as temp:
            env, compose_args = self._fake_compose_environment(temp)
            env["KB_JOURNEY_CI"] = "1"
            result = subprocess.run(
                ["bash", str(RUNNER), "--down"], cwd=ROOT, env=env,
                capture_output=True, text=True, check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(compose_args.read_text().splitlines(), [
                "-f", "e2e/docker-compose.yml",
                "-f", "e2e/docker-compose.ci.yml",
                "-f", "e2e/journey/docker-compose.yml",
                "down", "--volumes", "--remove-orphans",
            ])

    def test_missing_compose_binary_fails_with_an_actionable_message(self):
        with tempfile.TemporaryDirectory(prefix="journey-compose-missing-") as temp:
            env, _ = self._fake_compose_environment(temp)
            env["PATH"] = str(Path(temp) / "bin") + os.pathsep + "/usr/bin:/bin"
            # The fake compose binary exists to support the teardown test only.
            (Path(temp) / "bin" / "docker-compose").unlink()
            result = subprocess.run(
                ["bash", str(RUNNER), "--render-compose-config"], cwd=ROOT, env=env,
                capture_output=True, text=True, check=False,
            )

            self.assertEqual(result.returncode, 127)
            self.assertIn("install Docker Compose v2", result.stderr)
            self.assertIn("standalone 'docker-compose' command", result.stderr)


if __name__ == "__main__":
    unittest.main()
