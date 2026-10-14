from __future__ import annotations

import json
import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


JOURNEY_DIR = Path(__file__).resolve().parent


class JourneyTeardownTests(unittest.TestCase):
    def test_collector_failure_still_removes_compose_resources(self):
        with tempfile.TemporaryDirectory(prefix="journey-teardown-") as temporary:
            root = Path(temporary)
            state_path = root / "docker-state.json"
            state_path.write_text(json.dumps({
                "containers": ["runner-kept", "platform-kept"],
                "volumes": ["journey-data"],
                "calls": [],
            }), encoding="utf-8")
            docker = root / "docker"
            docker.write_text(
                "#!/usr/bin/env python3\n"
                "import json, pathlib, sys\n"
                f"state_path = pathlib.Path({str(state_path)!r})\n"
                "state = json.loads(state_path.read_text())\n"
                "args = sys.argv[1:]\n"
                "if args == ['compose', 'ps', '-aq', '--all']:\n"
                "    state['calls'].append('compose ps')\n"
                "    state_path.write_text(json.dumps(state))\n"
                "    print('\\n'.join(state['containers']))\n"
                "elif args == ['compose', 'down', '--volumes', '--remove-orphans']:\n"
                "    state['calls'].append('compose down')\n"
                "    state['containers'] = []\n"
                "    state['volumes'] = []\n"
                "    state_path.write_text(json.dumps(state))\n"
                "else:\n"
                "    raise SystemExit(f'unmodeled Docker command: {args!r}')\n",
                encoding="utf-8",
            )
            docker.chmod(docker.stat().st_mode | stat.S_IXUSR)
            invalid_report = root / "report-path-is-a-file"
            invalid_report.write_text("forces the real outcome collector to fail", encoding="utf-8")
            environment = os.environ.copy()
            environment["PATH"] = f"{root}{os.pathsep}{environment['PATH']}"
            result = subprocess.run(
                [
                    "bash",
                    str(JOURNEY_DIR / "teardown-compose.sh"),
                    "0",
                    str(invalid_report),
                    "2026-10-14T00:00:00Z",
                    "docker",
                    "compose",
                ],
                cwd=JOURNEY_DIR.parents[1],
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("Journey container outcome capture failed", result.stderr)
            self.assertIn("Next: e2e/journey/run.sh --down", result.stderr)
            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["calls"], ["compose ps", "compose down"])
            self.assertEqual(state["containers"], [])
            self.assertEqual(state["volumes"], [])


if __name__ == "__main__":
    unittest.main()
