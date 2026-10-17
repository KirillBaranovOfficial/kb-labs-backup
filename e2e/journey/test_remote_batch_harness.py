import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SOURCE = Path(__file__).resolve().parents[2].parent / "reports" / "x53-cutover" / "journey-batch.sh"
WORKFLOW_PATH = ".github/workflows/journey-batch.yml"


class RemoteJourneyBatchTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="journey-batch-remote-")
        self.root = Path(self.temporary.name)
        self.workflow_path = WORKFLOW_PATH
        self.bin = self.root / "stub-bin"
        self.bin.mkdir()
        self.logs = self.root / "logs"
        self.logs.mkdir()
        (self.root / "bin").mkdir()
        (self.root / "reports").mkdir()
        self.tree = self.root / "checkout"
        (self.tree / "e2e/journey").mkdir(parents=True)
        (self.root / "prompts").mkdir()
        shutil.copy2(SOURCE, self.root / "prompts/journey-batch.sh")
        self.calls = self.root / "calls.log"
        self._script(self.root / "bin/kb-runner", '''#!/bin/sh
exit 0
''')
        self._script(self.root / "prompts/batch-summary.sh", '''#!/bin/sh
printf 'summary\\n' >> "$CALLS"
exit 0
''')
        self._script(self.root / "prompts/docker-trim.sh", '''#!/bin/sh
printf 'docker-trim\\n' >> "$CALLS"
exit 0
''')
        self._script(self.root / "prompts/gem-triage.sh", '''#!/bin/sh
exit 0
''')
        self._script(self.root / "prompts/disk-guard.sh", '''#!/bin/sh
exit 0
''')
        self._script(self.bin / "git", '''#!/bin/sh
printf 'git %s\\n' "$*" >> "$CALLS"
if [ "$1" = rev-parse ]; then echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef; exit 0; fi
if [ "$1" = ls-remote ]; then
  if [ -n "${REMOTE_REF_SHA:-}" ]; then printf '%s\\trefs/heads/batch/batch-test\\n' "$REMOTE_REF_SHA"; fi
  exit 0
fi
if [ "$1" = push ] && [ "${GIT_FAIL:-}" = push ]; then exit 1; fi
exit 0
''')
        self._script(self.bin / "gh", '''#!/bin/sh
printf 'gh %s\\n' "$*" >> "$CALLS"
case "$1 $2" in
  'run watch') [ "${GH_FAIL:-}" = watch ] && exit 1; exit 0 ;;
  'run download')
    name=; destination=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --name) shift; name="$1" ;;
        --dir) shift; destination="$1" ;;
      esac
      shift
    done
    mkdir -p "$destination"
    printf '%s report\\n' "$name" > "$destination/step.json"
    printf 'exit=0\\n' > "$destination/batch.log"
    exit 0
    ;;
esac
if [ "$1" = api ]; then
  endpoint="$2"
  shift 2
  jq_filter=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --jq ]; then shift; jq_filter="$1"; fi
    shift
  done
  # Serve the REST Actions runs payload shape and apply the harness filter with real jq.
  if [ "${GH_FAIL:-}" = appearance ]; then
    payload='{"total_count":0,"workflow_runs":[]}'
  else
    payload="{\\"total_count\\":2,\\"workflow_runs\\":[{\\"id\\":11111,\\"path\\":\\".github/workflows/ci.yml\\"},{\\"id\\":98765,\\"path\\":\\"$EXPECTED_WORKFLOW_PATH\\"}]}"
  fi
  printf '%s\\n' "$payload" | jq -r "$jq_filter"
  exit 0
fi
exit 2
''')
        self._script(self.bin / "sleep", '''#!/bin/sh
printf 'sleep %s\\n' "$*" >> "$CALLS"
exit 0
''')
        self._script(self.bin / "colima", '''#!/bin/sh
printf 'colima %s\\n' "$*" >> "$CALLS"
exit 0
''')
        self._script(self.bin / "docker", '''#!/bin/sh
printf 'docker %s\\n' "$*" >> "$CALLS"
exit 0
''')
        self.env = {
            **os.environ,
            "PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
            "HOME": str(self.root / "home"),
            "CALLS": str(self.calls),
            "EXPECTED_WORKFLOW_PATH": self.workflow_path,
            "KBLABS_WORKTREES_ROOT": str(self.root),
            "BATCH_TREE": "checkout",
            "KB_BATCH_SUPERVISED": "1",
        }
        Path(self.env["HOME"]).mkdir()

    def tearDown(self):
        self.temporary.cleanup()

    def _script(self, path, contents):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        path.chmod(0o755)

    def _run(self, *args, failure=None):
        env = dict(self.env)
        if failure:
            env["GH_FAIL"] = failure
        return subprocess.run(
            ["sh", str(self.root / "prompts/journey-batch.sh"), *args],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    # case: S-262.3
    def test_remote_push_finds_run_by_branch_and_sha_then_downloads_modes(self):
        result = self._run("--remote", "batch-test", "j28")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        calls = self.calls.read_text()
        self.assertIn("git push --force-with-lease=refs/heads/batch/batch-test: backup HEAD:refs/heads/batch/batch-test", calls)
        self.assertIn("gh api repos/KirillBaranovOfficial/kb-labs-backup/actions/runs?branch=batch/batch-test&head_sha=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef&event=push", calls)
        self.assertIn(f'--jq .workflow_runs[] | select(.path == "{self.workflow_path}") | .id', calls)
        self.assertNotIn("--workflow", calls)
        self.assertNotIn("Journey batch", calls)
        self.assertIn("gh run watch 98765 --repo KirillBaranovOfficial/kb-labs-backup --exit-status", calls)
        for mode in ("local", "secured"):
            self.assertIn(f"gh run download 98765 --repo KirillBaranovOfficial/kb-labs-backup --name journey-{mode}", calls)
            destination = self.root / "reports" / f"batch-test-{mode}"
            self.assertTrue((destination / "step.json").is_file())
            self.assertTrue((destination / "batch.log").is_file())
            self.assertTrue((destination / "invocations/.single-run").is_file())
        self.assertFalse(any("docker" in row or "colima" in row for row in calls.splitlines()))

    # case: S-262.3
    def test_remote_push_uses_force_with_lease_for_existing_tag_ref(self):
        env = dict(self.env)
        env["REMOTE_REF_SHA"] = "0123456789abcdef0123456789abcdef01234567"
        result = subprocess.run(
            ["sh", str(self.root / "prompts/journey-batch.sh"), "--remote", "batch-test", "j28"],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn(
            "git push --force-with-lease=refs/heads/batch/batch-test:0123456789abcdef0123456789abcdef01234567 backup HEAD:refs/heads/batch/batch-test",
            self.calls.read_text(),
        )

    # case: S-262.3
    def test_remote_workflow_failure_names_run_and_recovery_command(self):
        result = self._run("--remote", "batch-timeout", "j28", failure="watch")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("remote journey workflow run 98765 failed or timed out", result.stderr)
        self.assertIn("gh run view 98765 --repo KirillBaranovOfficial/kb-labs-backup", result.stderr)
        calls = self.calls.read_text().splitlines()
        for mode in ("local", "secured"):
            self.assertTrue(any(f"gh run download 98765 --repo KirillBaranovOfficial/kb-labs-backup --name journey-{mode}" in row for row in calls))
            destination = self.root / "reports" / f"batch-timeout-{mode}"
            self.assertTrue((destination / "step.json").is_file())
            self.assertTrue((destination / "batch.log").is_file())
        self.assertFalse(any("docker" in row or "colima" in row for row in calls))

    # case: S-262.3
    def test_remote_run_appearance_timeout_names_commit_scoped_lookup(self):
        result = self._run("--remote", "batch-missing", "j28", failure="appearance")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no journey workflow run appeared for batch/batch-missing at deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", result.stderr)
        self.assertIn('gh api "repos/KirillBaranovOfficial/kb-labs-backup/actions/runs?branch=batch/batch-missing&head_sha=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef&event=push"', result.stderr)
        calls = self.calls.read_text().splitlines()
        self.assertEqual(sum(row.startswith("gh api ") for row in calls), 12)
        self.assertEqual(sum(row.startswith("sleep 5") for row in calls), 11)
        self.assertFalse(any("gh workflow run" in row for row in calls))


if __name__ == "__main__":
    unittest.main()
