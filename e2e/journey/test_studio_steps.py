from __future__ import annotations

import io
import json
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))

from studio_steps import (
    PLAYWRIGHT_HEADLESS_SHELL,
    create_studio_environment,
    create_studio_member,
    record_studio_browser_step,
    run_studio_browser_step,
    _record_playwright_case_claims,
)


class FakeResponse:
    def __init__(self, body: dict, status: int = 200):
        self.status = status
        self.body = json.dumps(body).encode()

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def read(self):
        return self.body


class StudioJourneyTests(unittest.TestCase):
    def test_runner_playwright_version_matches_studio_e2e_lockfile(self):
        repository = Path(__file__).parents[2]
        studio_lock = (repository / "pnpm-lock.yaml").read_text().split("\n  e2e/studio:\n", 1)[1]
        studio_lock = studio_lock.split("\n  e2e/tests:\n", 1)[0]
        playwright_lock = studio_lock.split("      '@playwright/test':\n", 1)[1]
        expected_version = playwright_lock.splitlines()[1].strip().removeprefix("version: ")
        dockerfile = (Path(__file__).parent / "Dockerfile.runner").read_text()

        self.assertIn("FROM node:24-bookworm", dockerfile)
        self.assertIn(f"@playwright/test@{expected_version}", dockerfile)
        self.assertIn("ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright", dockerfile)

        playwright_core = repository / f"node_modules/.pnpm/playwright-core@{expected_version}/node_modules/playwright-core"
        browsers = json.loads((playwright_core / "browsers.json").read_text())
        revision = next(browser["revision"] for browser in browsers["browsers"]
                        if browser["name"] == "chromium-headless-shell")
        self.assertEqual(PLAYWRIGHT_HEADLESS_SHELL.parts[0], f"chromium_headless_shell-{revision}")

    def test_studio_environment_preserves_playwright_browser_path(self):
        studio_env = create_studio_environment({
            "HOME": "/tmp/isolated-home",
            "PLAYWRIGHT_BROWSERS_PATH": "/ms-playwright",
        }, mode="local", project_id="project-1", project_name="Journey",
           project_root="/tmp/project", kb_bin="/tmp/bin/kb")

        self.assertEqual(studio_env["HOME"], "/tmp/isolated-home")
        self.assertEqual(studio_env["PLAYWRIGHT_BROWSERS_PATH"], "/ms-playwright")
        self.assertEqual(studio_env["KB_ACCESS_MODE"], "local")

    # case: S-020.3
    def test_secured_studio_environment_exports_admin_before_member_provisioning(self):
        studio_env = create_studio_environment({
            "KB_JOURNEY_ADMIN_EMAIL": "admin@example.test",
            "KB_JOURNEY_ADMIN_PASSWORD": "isolated-password",
        }, mode="secured", project_id="project-1", project_name="Journey",
           project_root="/tmp/project", kb_bin="/tmp/bin/kb")

        self.assertEqual(studio_env["KB_STUDIO_ADMIN_EMAIL"], "admin@example.test")
        self.assertEqual(studio_env["KB_STUDIO_ADMIN_PASSWORD"], "isolated-password")
        self.assertEqual(studio_env["KB_ACCESS_MODE"], "secured")
        self.assertNotIn("KB_STUDIO_MEMBER_EMAIL", studio_env)
        self.assertNotIn("KB_STUDIO_MEMBER_PASSWORD", studio_env)

    def test_missing_playwright_browser_fails_preflight_without_launch(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))
            browsers_path = Path(directory) / "browsers"
            env = {"PLAYWRIGHT_BROWSERS_PATH": str(browsers_path)}

            with patch("studio_steps.subprocess.Popen") as popen:
                passed, elapsed, message = run_studio_browser_step(
                    ["playwright"], env, report, "local", True)
            saved = json.loads(report.read_text())

        expected_executable = browsers_path / PLAYWRIGHT_HEADLESS_SHELL
        self.assertFalse(passed)
        self.assertEqual(elapsed, 0.0)
        self.assertEqual(
            message,
            f"Playwright Chromium headless shell is missing at {expected_executable}; "
            "rebuild the runner image with `e2e/journey/run.sh --no-build`.",
        )
        popen.assert_not_called()
        self.assertEqual(saved["steps"][0]["status"], "FAIL")
        self.assertEqual(saved["steps"][0]["message"], message)

    def test_playwright_subprocess_receives_browser_path(self):
        with tempfile.TemporaryDirectory() as directory:
            browsers_path = Path(directory) / "browsers"
            executable = browsers_path / PLAYWRIGHT_HEADLESS_SHELL
            executable.parent.mkdir(parents=True)
            executable.write_text("fake executable for preflight")
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))
            studio_env = create_studio_environment(
                {"HOME": "/tmp/isolated-home", "PLAYWRIGHT_BROWSERS_PATH": str(browsers_path)},
                mode="local", project_id="project-1", project_name="Journey",
                project_root="/tmp/project", kb_bin="/tmp/bin/kb")

            passed, _elapsed, message = run_studio_browser_step(
                [sys.executable, "-c", "import os; print(os.environ['PLAYWRIGHT_BROWSERS_PATH'])"],
                studio_env, report, "local", True)
            log = (Path(directory) / "studio-playwright-local.log").read_text()

        self.assertTrue(passed, message)
        self.assertIn(str(browsers_path), log)

    def test_secured_member_is_created_through_invite_and_activation(self):
        responses = [
            FakeResponse({"accessToken": "admin-token"}),
            FakeResponse({"activationUrl": "http://localhost/activate/one-time-token"}, 201),
            FakeResponse({}, 200),
        ]
        with patch("studio_steps.urllib.request.urlopen", side_effect=responses) as open_url:
            email, password = create_studio_member("http://127.0.0.1:4000", "admin@example.test", "secret")

        self.assertEqual(email, "journey-member@example.test")
        self.assertEqual(password, "JourneyMember123!")
        invite_request = open_url.call_args_list[1].args[0]
        self.assertEqual(invite_request.get_header("Authorization"), "Bearer admin-token")
        self.assertEqual(json.loads(invite_request.data), {"email": email, "role": "member"})
        activation_request = open_url.call_args_list[2].args[0]
        self.assertEqual(json.loads(activation_request.data), {"token": "one-time-token", "password": password})

    def test_browser_result_is_added_to_capability_report(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))

            record_studio_browser_step(report, "local", True, 1.25, "Studio browser journey passed")

            saved = json.loads(report.read_text())
        self.assertEqual(saved["steps"][0]["capability"], "studio")
        self.assertEqual(saved["steps"][0]["status"], "PASS")
        self.assertEqual(saved["steps"][0]["duration_seconds"], 1.25)

    def test_playwright_timeout_streams_and_records_last_output(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "PASS", "steps": []}))
            browsers_path = Path(directory) / "browsers"
            executable = browsers_path / PLAYWRIGHT_HEADLESS_SHELL
            executable.parent.mkdir(parents=True)
            executable.write_text("fake executable for preflight")
            command = [sys.executable, "-u", "-c",
                       "import time; [print(f'playwright line {i}', flush=True) for i in range(45)]; time.sleep(5)"]
            streamed = io.StringIO()
            with patch("studio_steps.subprocess.Popen", wraps=subprocess.Popen) as popen:
                with redirect_stdout(streamed):
                    passed, _elapsed, message = run_studio_browser_step(
                        command, {"PLAYWRIGHT_BROWSERS_PATH": str(browsers_path)},
                        report, "local", True, timeout=0.2)
            saved = json.loads(report.read_text())
            log = (Path(directory) / "studio-playwright-local.log").read_text()

        popen.assert_called_once()
        self.assertFalse(passed)
        self.assertIn("timed out after 0.2s", message)
        self.assertIn("playwright line 44", message)
        self.assertNotIn("playwright line 4\n", message)
        self.assertIn("[studio-playwright] playwright line 44", streamed.getvalue())
        self.assertIn("playwright line 44", log)
        self.assertEqual(saved["steps"][0]["capability"], "studio")
        self.assertEqual(saved["steps"][0]["status"], "FAIL")

    def test_failed_studio_shell_blocks_playwright(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            report.write_text(json.dumps({"status": "FAIL", "steps": []}))
            with patch("studio_steps.subprocess.run") as run:
                passed, elapsed, message = run_studio_browser_step(
                    ["playwright"], {}, report, "local", False)
            saved = json.loads(report.read_text())

        run.assert_not_called()
        self.assertFalse(passed)
        self.assertEqual(elapsed, 0.0)
        self.assertIn("shell check failed", message)
        self.assertEqual(saved["steps"][0]["status"], "BLOCKED")

    def test_playwright_tagged_cases_are_merged_into_release_claims(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "service-local.json"
            playwright = Path(directory) / "playwright.json"
            report.write_text(json.dumps({"case_run": {"claims": []}}))
            playwright.write_text(json.dumps({"suites": [{"specs": [{
                "title": "Studio schedules @S-055.2 toggles a project cron",
                "tests": [{"results": [{"status": "passed"}]}],
            }]}]}))

            _record_playwright_case_claims(report, str(playwright))
            saved = json.loads(report.read_text())

        self.assertEqual(saved["case_run"]["claims"], [
            {"claimId": "S-055.2", "kind": "journey", "status": "PASS"},
        ])


if __name__ == "__main__":
    unittest.main()
