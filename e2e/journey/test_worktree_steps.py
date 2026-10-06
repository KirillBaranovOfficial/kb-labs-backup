from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from service_steps import Journey
from worktree_steps import run_worktree_steps


class WorktreeStepsTest(unittest.TestCase):
    # case: S-198.5, S-198.6, S-198.7
    def test_registered_and_unregistered_git_worktree_responses(self):
        commands = []

        def fake_run(args, *, cwd, env, **kwargs):
            commands.append((args, Path(cwd), env))
            if args[1:3] == ["project", "add"]:
                stdout = json.dumps({"ok": True, "data": {"project": {"id": "prj_fixture"}}})
                return subprocess.CompletedProcess(args, 0, stdout, "")
            if args[1:3] == ["workflow", "lint"]:
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True}), "")
            if args[1:3] == ["workflow", "run"]:
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True, "data": {"runId": "run-fixture"}}), "")
            if args[1:3] == ["workflow", "runs"]:
                return subprocess.CompletedProcess(args, 0, "Run SUCCESS.\n", "")
            if args[1:3] == ["project", "remove"]:
                return subprocess.CompletedProcess(args, 0, json.dumps({"ok": True}), "")
            return subprocess.CompletedProcess(
                args, 1,
                json.dumps({"ok": False, "error": {"code": "KB_PROJECT_UNKNOWN", "message": "Project is not registered.", "hint": "Register it with kb project add <path>."}}),
                "",
            )

        journey = Journey("local", "worktree-fixture")
        run_worktree_steps(journey, "/installed/kb", platform_root="/installed/platform", env={}, runner=fake_run)

        result = journey.result()
        self.assertEqual(result.status, "PASS", result.first_failure)
        self.assertEqual({claim["claimId"] for claim in result.case_run["claims"]},
                         {"S-198.5", "S-198.6", "S-198.7"})
        worktree_commands = [item for item in commands if item[1].name == "registered-worktree"]
        self.assertEqual(worktree_commands[0][0][1:3], ["workflow", "lint"])
        self.assertEqual(worktree_commands[0][2]["KB_PLATFORM_ROOT"], "/installed/platform")
        self.assertEqual(worktree_commands[0][2]["KB_PROJECT_ID"], "prj_fixture")
        self.assertIn("KB_WORKSPACE_ROOT", worktree_commands[0][2])
        self.assertEqual(worktree_commands[1][0][1:3], ["workflow", "run"])
        self.assertEqual(worktree_commands[2][0][1:3], ["workflow", "runs"])


if __name__ == "__main__":
    unittest.main()
