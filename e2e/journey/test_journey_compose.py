from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[2]
COMPOSE = shutil.which("docker-compose")


@unittest.skipUnless(COMPOSE, "docker-compose is required to render the journey mount configuration")
class JourneyComposeMountTests(unittest.TestCase):
    def _config(self, workspace_source: str | None, *, ci: bool = False):
        env = dict(os.environ)
        env.pop("KB_JOURNEY_WORKSPACE_SOURCE", None)
        if workspace_source is not None:
            env["KB_JOURNEY_WORKSPACE_SOURCE"] = workspace_source
        compose_files = ["-f", "e2e/docker-compose.yml"]
        if ci:
            compose_files.extend(("-f", "e2e/docker-compose.ci.yml"))
        compose_files.extend(("-f", "e2e/journey/docker-compose.yml"))
        return subprocess.run(
            [COMPOSE, *compose_files, "config", "--format", "json"],
            cwd=ROOT, env=env, capture_output=True, text=True, check=False,
        )

    def test_workspace_source_is_required(self):
        result = self._config(None)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("KB_JOURNEY_WORKSPACE_SOURCE", result.stderr)

    def test_rendered_mounts_resolve_from_the_run_sh_compose_invocation(self):
        expected = {
            "/opt/kb-workspace": ROOT,
            "/summary": ROOT / ".journey" / "reports",
            "/e2e-oidc-certs": ROOT / "e2e" / "oidc-idp" / "certs",
            "/certs": ROOT / "e2e" / "oidc-idp" / "certs",
            "/fixture/mock_llm_server.py": ROOT / "e2e" / "journey" / "mock_llm_server.py",
            "/release": ROOT / ".journey" / "release",
            "/workspace/e2e-host": ROOT / "e2e",
            "/verdaccio/conf/config.yaml": ROOT / "e2e" / "verdaccio" / "config.yaml",
        }
        for ci in (False, True):
            result = self._config(str(ROOT), ci=ci)
            self.assertEqual(result.returncode, 0, result.stderr)
            services = json.loads(result.stdout)["services"]
            mounts = {
                mount["target"]: Path(mount["source"]).resolve()
                for service in services.values()
                for mount in service.get("volumes", [])
                if mount.get("type") == "bind"
            }
            configured = dict(expected)
            if ci:
                configured["/verdaccio/storage"] = ROOT / "e2e" / ".verdaccio-storage"
            for target, source in configured.items():
                with self.subTest(ci=ci, target=target):
                    self.assertEqual(mounts[target], source.resolve())


if __name__ == "__main__":
    unittest.main()
