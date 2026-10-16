import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


SOURCE = Path(__file__).resolve().parents[2].parent / "reports" / "x53-cutover" / "journey-batch.sh"


class RemoteJourneyBatchTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="journey-batch-remote-")
        self.root = Path(self.temporary.name)
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
if [ "$1" = rev-parse ]; then echo deadbeef; exit 0; fi
if [ "$1" = push ] && [ "${GIT_FAIL:-}" = push ]; then exit 1; fi
exit 0
''')
        self._script(self.bin / "gh", '''#!/bin/sh
printf 'gh %s\\n' "$*" >> "$CALLS"
case "$1 $2" in
  'workflow run') [ "${GH_FAIL:-}" = dispatch ] && exit 1; exit 0 ;;
  'run list') echo 98765; exit 0 ;;
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
exit 2
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
    def test_remote_dispatch_downloads_each_mode_into_summary_layout(self):
        result = self._run("--remote", "batch-test", "j28")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        calls = self.calls.read_text()
        self.assertIn("git push backup HEAD:refs/heads/batch/batch-test", calls)
        self.assertIn("gh workflow run journey-batch.yml --repo KirillBaranovOfficial/kb-labs-backup --ref batch/batch-test -f baseline=j28", calls)
        self.assertIn("gh run watch 98765 --repo KirillBaranovOfficial/kb-labs-backup --exit-status", calls)
        for mode in ("local", "secured"):
            self.assertIn(f"gh run download 98765 --repo KirillBaranovOfficial/kb-labs-backup --name journey-{mode}", calls)
            destination = self.root / "reports" / f"batch-test-{mode}"
            self.assertTrue((destination / "step.json").is_file())
            self.assertTrue((destination / "batch.log").is_file())
            self.assertTrue((destination / "invocations/.single-run").is_file())
        self.assertFalse(any("docker" in row or "colima" in row for row in calls.splitlines()))

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


if __name__ == "__main__":
    unittest.main()
