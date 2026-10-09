from __future__ import annotations

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from platform_update_variant_steps import run_platform_update_variant_steps
from service_steps import Journey


class PlatformUpdateVariantStepsTests(unittest.TestCase):
    def test_installed_steps_register_each_update_variant_claim(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            platform = root / "platform"
            receipt = platform / ".kb/v2/receipt.json"
            receipt.parent.mkdir(parents=True)
            receipt.write_text(json.dumps({"plan": {"serviceGraph": {"platformVersion": "3.2.0"}}}))
            project_a, project_b = root / "project-a", root / "project-b"
            for project in (project_a, project_b):
                project.mkdir()
            indexes = {name: root / f"{name}.json" for name in
                       ("patch", "minor", "jump", "next", "bad", "config", "doctor", "failed")}
            registered = []

            def capture_step(journey, name, capability, check, cases=(), details=None, *, requires=()):
                registered.append((name, list(cases), check))

            def fake_runner(args, cwd):
                if args[1] == "update":
                    return subprocess.CompletedProcess(args, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_UPDATE_CHECKSUM_MISMATCH", "message": "Artifact checksum mismatch.",
                        "hint": "Replace the artifact and rerun the update."}}), "")
                if args[1] == "status":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "runningVersion": "3.2.0", "installedVersion": "3.2.0"}}), "")
                raise AssertionError(f"unexpected fake product command: {args[1]}")

            (platform / ".kb/kb.config.jsonc").write_text("{}\n")
            (platform / ".kb/v2/secrets.env").write_text("secret=sentinel\n")
            request_file = root / "request.json"
            request_file.write_text("{}\n")
            for index in indexes.values():
                index.write_text(json.dumps({"channels": {"stable": "3.2.1"}}))

            with patch.object(Journey, "step", capture_step), patch.dict(
                    os.environ, {"KB_JOURNEY_SELECTED_STEPS": "S-023.12"}):
                run_platform_update_variant_steps(
                    mode="local", launcher="kb-create", kb_binary="kb",
                    platform_root=platform, project_a=project_a, project_b=project_b,
                    request_file=request_file, patch_index=indexes["patch"],
                    minor_index=indexes["minor"], jump_index=indexes["jump"],
                    next_index=indexes["next"], bad_checksum_index=indexes["bad"],
                    config_failure_index=indexes["config"], doctor_failure_index=indexes["doctor"],
                    failed_host_index=indexes["failed"], run=fake_runner, run_interactive=fake_runner,
                )
            claims = {case for _, cases, _ in registered for case in cases}
            self.assertTrue({"S-023.9", "S-023.10", "S-023.11", "S-023.12", "S-023.13",
                             "S-023.14", "S-023.15", "S-024.6", "S-194.7"}.issubset(claims))
            self.assertEqual(len(registered), 11)
            checksum_step = next(check for name, _, check in registered
                                 if name == "reject corrupted sealed binary without changing N")
            checksum_step()


if __name__ == "__main__":
    unittest.main()
