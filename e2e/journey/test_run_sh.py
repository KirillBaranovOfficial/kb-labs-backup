from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path


class RunShellOptionTests(unittest.TestCase):
    def test_resolve_only_and_no_build_are_order_independent_and_do_not_start_compose(self):
        root = Path(__file__).resolve().parents[2]
        script = root / "e2e/journey/run.sh"
        with tempfile.TemporaryDirectory(prefix="journey-run-options-") as temporary:
            home = Path(temporary) / "home"
            home.mkdir()
            bin_dir = Path(temporary) / "bin"
            bin_dir.mkdir()
            docker_log = Path(temporary) / "docker.log"
            docker = bin_dir / "docker"
            docker.write_text(
                "#!/bin/sh\n"
                f"printf '%s\\n' \"$*\" >> {docker_log}\n"
                "if [ \"$*\" = 'compose version' ]; then exit 0; fi\n"
                "case \"$*\" in *' config --format json') printf '{}\\n'; exit 0 ;; *' down --volumes --remove-orphans') exit 0 ;; esac\n"
                "exit 97\n",
                encoding="utf-8",
            )
            docker.chmod(0o755)
            env = {
                **os.environ,
                "HOME": str(home),
                "PATH": f"{bin_dir}:/Users/kirillbaranov/.nvm/versions/node/v24.18.1/bin:{os.environ['PATH']}",
                "KB_JOURNEY_ONLY": "agent:commit",
            }
            for options in (("--no-build", "--resolve-only"), ("--resolve-only", "--no-build")):
                result = subprocess.run(["bash", str(script), *options], cwd=root, env=env,
                                        text=True, capture_output=True, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('"selector": "agent:commit"', result.stdout)
            self.assertFalse(docker_log.exists(), "resolve-only must not probe or start Docker")

            for options, expected in (
                    (("--no-build", "--render-compose-config"), "config"),
                    (("--render-compose-config", "--no-build"), "config"),
                    (("--no-build", "--down"), "down"),
                    (("--down", "--no-build"), "down")):
                result = subprocess.run(["bash", str(script), *options], cwd=root, env=env,
                                        text=True, capture_output=True, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(expected, docker_log.read_text(encoding="utf-8").splitlines()[-1])
            self.assertEqual(docker_log.read_text(encoding="utf-8").splitlines(), [
                "compose version", "compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml config --format json",
                "compose version", "compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml config --format json",
                "compose version", "compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml down --volumes --remove-orphans",
                "compose version", "compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml down --volumes --remove-orphans",
            ])

    def test_unknown_option_is_refused_before_journey_work(self):
        root = Path(__file__).resolve().parents[2]
        script = root / "e2e/journey/run.sh"
        with tempfile.TemporaryDirectory(prefix="journey-run-options-unknown-") as temporary:
            home = Path(temporary) / "home"
            home.mkdir()
            result = subprocess.run(["bash", str(script), "--not-a-journey-option"], cwd=root,
                                    env={**os.environ, "HOME": str(home)}, text=True,
                                    capture_output=True, check=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("Unknown journey option", result.stderr)

    def test_conflicting_actions_are_refused_before_docker(self):
        root = Path(__file__).resolve().parents[2]
        script = root / "e2e/journey/run.sh"
        with tempfile.TemporaryDirectory(prefix="journey-run-options-conflict-") as temporary:
            home = Path(temporary) / "home"
            home.mkdir()
            result = subprocess.run(["bash", str(script), "--resolve-only", "--prepare-only"], cwd=root,
                                    env={**os.environ, "HOME": str(home)}, text=True,
                                    capture_output=True, check=False)
        self.assertEqual(result.returncode, 2)
        self.assertIn("cannot be combined", result.stderr)


if __name__ == "__main__":
    unittest.main()
