from __future__ import annotations

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from platform_update_variant_steps import _install_vibeproxy_adapter, run_platform_update_variant_steps
from service_steps import Journey


def _versioned_platform(root: Path, version: str) -> Path:
    platform = root / "platform"
    version_root = platform / "versions" / "v1"
    (version_root / ".kb/v2").mkdir(parents=True)
    (version_root / ".kb/kb.config.jsonc").write_text("{}\n")
    (version_root / ".kb/marketplace.lock").write_text('{"plugins":[]}\n')
    (version_root / ".kb/v2/receipt.json").write_text(json.dumps({
        "plan": {"serviceGraph": {"platformVersion": version}},
    }))
    platform.mkdir(exist_ok=True)
    (platform / "state").mkdir()
    (platform / "state" / "opaque-runtime-state.bin").write_bytes(b"fixture state")
    (platform / "current").symlink_to(Path("versions/v1"), target_is_directory=True)
    return platform


class PlatformUpdateVariantStepsTests(unittest.TestCase):
    def test_update_fixture_installs_vibeproxy_through_platform_marketplace(self) -> None:
        calls = []

        def run(args, cwd):
            calls.append((args, cwd))
            return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"package": "@kb-labs/adapters-vibeproxy"}}), "")

        with tempfile.TemporaryDirectory() as directory:
            cwd = Path(directory)
            _install_vibeproxy_adapter("/platform/bin/kb", run, cwd)

        self.assertEqual(len(calls), 1)
        args, called_cwd = calls[0]
        self.assertEqual(called_cwd, cwd)
        self.assertEqual(args[1:], ["marketplace", "install", "@kb-labs/adapters-vibeproxy",
                                   "--scope", "platform", "--json"])

    def test_installed_steps_register_each_update_variant_claim(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            platform = _versioned_platform(root, "3.2.0")
            project_a, project_b = root / "project-a", root / "project-b"
            for project in (project_a, project_b):
                project.mkdir()
            indexes = {name: root / f"{name}.json" for name in
                       ("base", "update", "patch", "minor", "jump", "next", "bad", "config", "doctor", "failed", "healthy")}
            registered = []

            def capture_step(journey, name, capability, check, cases=(), details=None, *, requires=()):
                registered.append((name, list(cases), check, tuple(requires)))

            def fake_runner(args, cwd):
                if args[1] == "update":
                    return subprocess.CompletedProcess(args, 1, json.dumps({"ok": False, "error": {
                        "code": "KB_UPDATE_CHECKSUM_MISMATCH", "message": "Artifact checksum mismatch.",
                        "hint": "Replace the artifact and rerun the update."}}), "")
                if args[1] == "status":
                    return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {
                        "runningVersion": "3.2.0", "installedVersion": "3.2.0"}}), "")
                raise AssertionError(f"unexpected fake product command: {args[1]}")

            request_file = root / "request.json"
            request_file.write_text("{}\n")
            for index in indexes.values():
                index.write_text(json.dumps({"channels": {"stable": "3.2.1"}}))

            with patch.object(Journey, "step", capture_step), patch.dict(
                    os.environ, {"KB_JOURNEY_SELECTED_STEPS": "S-023.12"}):
                run_platform_update_variant_steps(
                    mode="local", launcher="kb-create", kb_binary="kb",
                    platform_root=platform, project_a=project_a, project_b=project_b,
                    request_file=request_file, base_index=indexes["base"], update_index=indexes["update"],
                    patch_index=indexes["patch"],
                    minor_index=indexes["minor"], jump_index=indexes["jump"],
                    next_index=indexes["next"], bad_checksum_index=indexes["bad"],
                    config_failure_index=indexes["config"], doctor_failure_index=indexes["doctor"],
                    failed_host_index=indexes["failed"], healthy_host_index=indexes["healthy"],
                    run=fake_runner, run_interactive=fake_runner,
                )
            claims = {case for _, cases, _, _ in registered for case in cases}
            self.assertTrue({"S-023.9", "S-023.10", "S-023.11", "S-023.12", "S-023.13",
                             "S-023.14", "S-023.15", "S-024.6", "S-194.7"}.issubset(claims))
            self.assertEqual(len(registered), 12)
            fixture_step = next(requires for name, _, _, requires in registered
                                if name == "prepare update preservation baseline")
            self.assertEqual(fixture_step, ("state:project-registered",))
            for claim in ("S-023.10", "S-023.15"):
                dependencies = next(requires for _, cases, _, requires in registered if claim in cases)
                self.assertIn("step:prepare update preservation baseline", dependencies)
            checksum_step = next(check for name, _, check, _ in registered
                                 if name == "reject corrupted sealed binary without changing N")
            checksum_step()

    def test_n_plus_two_plan_requires_every_intervening_sealed_note(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            platform = _versioned_platform(root, "3.0.0")
            project_a, project_b = root / "project-a", root / "project-b"
            project_a.mkdir()
            project_b.mkdir()
            indexes = {name: root / f"{name}.json" for name in
                       ("base", "update", "patch", "minor", "jump", "next", "bad", "config", "doctor", "failed", "healthy")}
            versions = {"base": "3.0.0", "update": "3.0.1", "patch": "3.0.1", "minor": "3.1.0", "jump": "3.0.2",
                        "next": "3.0.1", "bad": "3.0.2", "config": "3.1.1", "doctor": "3.1.2", "failed": "3.2.0",
                        "healthy": "3.2.1"}
            for name, index in indexes.items():
                version = versions[name]
                index.write_text(json.dumps({"channels": {"stable": version}, "platforms": [
                    {"version": version, "releaseNotes": f"Release {version} notes."}]}))
            request_file = root / "request.json"
            request_file.write_text("{}\n")
            registered = []

            def capture_step(journey, name, capability, check, cases=(), details=None, *, requires=()):
                registered.append((name, list(cases), check))

            def fake_runner(args, cwd):
                if args[1] == "plan":
                    response = {"ok": True, "data": {"updateSummary": {"releaseNotes": [
                        {"version": "3.0.0", "notes": "Release 3.0.0 notes."},
                        {"version": "3.0.1", "notes": "Release 3.0.1 notes."},
                        {"version": "3.0.2", "notes": "Release 3.0.2 notes."},
                    ]}}}
                    return subprocess.CompletedProcess(args, 0, json.dumps(response), "")
                raise AssertionError(f"unexpected fake product command: {args[1]}")

            with patch.object(Journey, "step", capture_step), patch.dict(
                    os.environ, {"KB_JOURNEY_SELECTED_STEPS": "S-023.2"}):
                run_platform_update_variant_steps(
                    mode="local", launcher="kb-create", kb_binary="kb",
                    platform_root=platform, project_a=project_a, project_b=project_b,
                    request_file=request_file, base_index=indexes["base"], update_index=indexes["update"],
                    patch_index=indexes["patch"], minor_index=indexes["minor"], jump_index=indexes["jump"],
                    next_index=indexes["next"], bad_checksum_index=indexes["bad"],
                    config_failure_index=indexes["config"], doctor_failure_index=indexes["doctor"],
                    failed_host_index=indexes["failed"], healthy_host_index=indexes["healthy"],
                    run=fake_runner, run_interactive=fake_runner,
                )
            plan_step = next(check for name, cases, check in registered if "S-023.2" in cases)
            plan_step()


if __name__ == "__main__":
    unittest.main()
