from __future__ import annotations

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
JOURNEY = ROOT / "e2e/journey"


class HostBash32Tests(unittest.TestCase):
    def test_empty_monitor_and_container_lists_reach_cleanup_under_host_bash(self):
        version = subprocess.run(["/bin/bash", "--version"], text=True, capture_output=True, check=True)
        self.assertIn("version 3.2.", version.stdout)

        with tempfile.TemporaryDirectory(prefix="journey-bash32-") as temporary:
            sandbox = Path(temporary)
            repo = sandbox / "repo"
            journey = repo / "e2e/journey"
            journey.mkdir(parents=True)
            for name in ("run.sh", "candidate-services.sh", "teardown-compose.sh", "storage_has_packages.sh"):
                shutil.copy2(JOURNEY / name, journey / name)

            bin_dir = sandbox / "bin"
            bin_dir.mkdir()
            (bin_dir / "bash").symlink_to("/bin/bash")
            calls_path = sandbox / "docker-calls.jsonl"
            self._install_docker(bin_dir / "docker")
            self._write_stub(bin_dir / "python3", "#!/bin/sh\nexit 0\n")
            self._write_stub(bin_dir / "git", "#!/bin/sh\nprintf 'test-revision\\n'\n")

            environment = os.environ.copy()
            environment.update({
                "PATH": f"{bin_dir}{os.pathsep}{environment['PATH']}",
                "HOME": str(sandbox),
                "FAKE_DOCKER_CALLS": str(calls_path),
                "KB_JOURNEY_ONLY": "service-local:explore",
            })
            for key in ("KB_PLATFORM_ROOT", "KB_HOME", "KB_NET_OFFSET", "GOFLAGS"):
                environment.pop(key, None)

            result = subprocess.run(
                ["/bin/bash", str(journey / "run.sh"), "--no-build"],
                cwd=repo,
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn("Journey package publisher container exited 17", result.stderr)
            docker_calls = [json.loads(line) for line in calls_path.read_text(encoding="utf-8").splitlines()]
            ps_calls = [call for call in docker_calls if "ps" in call]
            self.assertEqual(len(ps_calls), 2, f"expected empty monitor and teardown listings: {docker_calls}")
            self.assertIn(["wait", "publisher-container-id"], docker_calls, f"publisher wait did not use the created id: {docker_calls}")
            self.assertIn(["rm", "-f", "publisher-container-id"], docker_calls, f"publisher container cleanup did not use the created id: {docker_calls}")
            self.assertTrue(
                any("down" in call and "--volumes" in call and "--remove-orphans" in call for call in docker_calls),
                f"Compose teardown was not called: {docker_calls}",
            )

    @staticmethod
    def _install_docker(path: Path) -> None:
        path.write_text(
            f"#!{sys.executable}\n"
            "import json, os, sys\n"
            "args = sys.argv[1:]\n"
            "with open(os.environ['FAKE_DOCKER_CALLS'], 'a', encoding='utf-8') as stream:\n"
            "    stream.write(json.dumps(args) + '\\n')\n"
            "action = next((item for item in args[1:] if item in {'version', 'ps', 'run', 'exec', 'down', 'build', 'up', 'stop', 'rm'}), None)\n"
            "if action in {'version', 'ps', 'down', 'build', 'up', 'stop', 'rm'}: raise SystemExit(0)\n"
            "if action == 'run':\n    print('publisher-container-id')\n    raise SystemExit(0)\n"
            "if action == 'exec': raise SystemExit(1)\n"
            "if args[:1] == ['wait']:\n"
            "    print('17')\n"
            "    raise SystemExit(0)\n"
            "raise SystemExit(f'unmodeled Docker command: {args!r}')\n",
            encoding="utf-8",
        )
        path.chmod(path.stat().st_mode | stat.S_IXUSR)

    @staticmethod
    def _write_stub(path: Path, contents: str) -> None:
        path.write_text(contents, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR)


if __name__ == "__main__":
    unittest.main()
