from __future__ import annotations

import json
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
case "$*" in
  *' config --format json') printf '{}\\n'; exit 0 ;;
esac
printf '%s\\n' "$@" > "$COMPOSE_ARGS_OUT"
""")
        compose.chmod(0o755)
        env = isolated_environment(Path(temp) / "home", bin_dir, required_tools=("bash", "git", "dirname"),
                                   overrides={"COMPOSE_ARGS_OUT": str(compose_args)})
        return env, compose_args

    def _config(self, *, hosted: bool) -> dict:
        with tempfile.TemporaryDirectory(prefix="journey-compose-render-") as temp:
            bin_dir = Path(temp) / "bin"
            bin_dir.mkdir()
            compose_tools = tuple(tool for tool in ("docker", "docker-compose") if shutil.which(tool))
            self.assertTrue(compose_tools, "journey Compose tests require Docker Compose v2 or docker-compose on PATH")
            env = isolated_environment(Path(temp) / "home", bin_dir,
                                       required_tools=("bash", "git", "dirname", *compose_tools))
            if hosted:
                env["CI"] = "true"
                env["GITHUB_ACTIONS"] = "true"
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

    # case: S-262.1
    def test_local_and_hosted_journeys_render_the_same_compose_bind_set(self):
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
        for hosted in (False, True):
            rendered = self._config(hosted=hosted)
            binds = {
                (service_name, mount["target"]): Path(mount["source"]).resolve()
                for service_name, service in rendered["services"].items()
                for mount in service.get("volumes", [])
                if mount.get("type") == "bind"
            }
            self.assertEqual(binds, {key: source.resolve() for key, source in expected.items()})

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
