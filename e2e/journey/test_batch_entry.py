import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[2]


class JourneyBatchEntryTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="journey-batch-entry-")
        self.root = Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.root / "e2e/journey").mkdir(parents=True)
        (self.root / "prompts").mkdir()
        (self.root / "tools/kb-devkit").mkdir(parents=True)
        shutil.copy2(REPO / "e2e/journey/batch.sh", self.root / "e2e/journey/batch.sh")
        self.calls = self.root / "calls.log"
        self._script(self.root / "e2e/journey/run.sh", '''#!/bin/sh
printf 'journey %s %s\\n' "$KB_JOURNEY_ONLY" "$*" >> "$CALLS"
if [ "$*" = "--down" ] && [ "${BLOCK_RESET:-}" = 1 ]; then
  printf 'ready\\n' >&"$READY_FD"
  cat "$BLOCK_FIFO"
fi
case "$KB_JOURNEY_ONLY" in
  service-local:explore) mode=local ;;
  service-secured:explore) mode=secured ;;
  *) mode=reset ;;
esac
mkdir -p "$REPORTS"
printf '%s report\\n' "$mode" > "$REPORTS/$mode.json"
if [ "$mode" = "${FAIL_MODE:-}" ] && [ ! -f "$ATTEMPT_MARKER" ]; then
  : > "$ATTEMPT_MARKER"
  if [ "${FAIL_KIND:-infra}" = infra ]; then
    printf 'failed to solve: fixture image failure\\n'
  else
    printf 'journey assertion failed\\n'
  fi
  exit 1
fi
exit 0
''')
        self._script(self.root / "e2e/journey/prepare-candidate.sh", '''#!/bin/sh
printf 'candidate\\n' >> "$CALLS"
exit 0
''')
        self._script(self.bin / "python3", '''#!/bin/sh
printf 'unit\\n' >> "$CALLS"
exit 0
''')
        self._script(self.bin / "go", '''#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    cat > "$1" <<'SCRIPT'
#!/bin/sh
printf 'build\\n' >> "$CALLS"
exit 0
SCRIPT
    chmod +x "$1"
    exit 0
  fi
  shift
done
exit 2
''')
        self._script(self.bin / "git", '''#!/bin/sh
[ "$1 $2" = "rev-parse HEAD" ] && { echo deadbeef; exit 0; }
exit 2
''')
        self.env = {
            **os.environ,
            "PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
            "CALLS": str(self.calls),
            "REPORTS": str(self.root / ".journey/reports"),
            "ATTEMPT_MARKER": str(self.root / "first-attempt"),
            "BLOCK_FIFO": str(self.root / "reset.fifo"),
            "HOME": str(self.root / "home"),
        }
        Path(self.env["REPORTS"]).mkdir(parents=True)
        Path(self.env["HOME"]).mkdir()

    def tearDown(self):
        self.temporary.cleanup()

    def _script(self, path, contents):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        path.chmod(0o755)

    def _run(self, mode="", failure_kind="infra"):
        env = dict(self.env)
        if mode:
            env["FAIL_MODE"] = mode
            env["FAIL_KIND"] = failure_kind
        return subprocess.run(
            ["sh", str(self.root / "e2e/journey/batch.sh"), str(self.root / "out")],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    # case: S-262.1
    def test_entry_runs_product_steps_in_order_and_exports_both_modes(self):
        result = self._run()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        calls = self.calls.read_text().splitlines()
        self.assertEqual(calls, [
            "journey  --down",
            "build",
            "unit",
            "candidate",
            "journey service-local:explore --no-build",
            "journey service-secured:explore --no-build",
        ])
        self.assertTrue((self.root / "out/local/local.json").is_file())
        self.assertTrue((self.root / "out/secured/secured.json").is_file())
        self.assertIn("deadbeef", (self.root / "out/run.json").read_text())
        summary = (self.root / "out/summary.md").read_text()
        self.assertEqual([line for line in summary.splitlines() if line.startswith("- Result:")], ["- Result: PASS"])
        import json
        for mode in ("local", "secured"):
            metadata = json.loads((self.root / f"out/{mode}/batch-metadata.json").read_text())
            self.assertEqual(metadata["mode"], mode)
            self.assertEqual(metadata["result"], "PASS")
            self.assertEqual(set(metadata["stageDurationsSeconds"]), {
                "reset", "build", "unit", "candidate", "local", "secured", "total",
            })
            self.assertTrue(all(isinstance(value, int) and value >= 0
                                for value in metadata["stageDurationsSeconds"].values()))

    # case: S-262.1
    def test_entry_retries_only_a_docker_image_build_failure_once(self):
        result = self._run("local")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        local_runs = [row for row in self.calls.read_text().splitlines()
                      if row == "journey service-local:explore --no-build"]
        self.assertEqual(len(local_runs), 2)
        self.assertTrue((self.root / "out/local/infra-failed.log").is_file())
        self.assertEqual(
            sum(row == "journey service-secured:explore --no-build"
                for row in self.calls.read_text().splitlines()),
            1,
        )

    # case: S-262.1
    def test_entry_does_not_retry_a_non_image_failure(self):
        result = self._run("local", "ordinary")
        self.assertNotEqual(result.returncode, 0)
        local_runs = [row for row in self.calls.read_text().splitlines()
                      if row == "journey service-local:explore --no-build"]
        self.assertEqual(len(local_runs), 1)
        self.assertFalse((self.root / "out/local/infra-failed.log").exists())
        summary = (self.root / "out/summary.md").read_text()
        self.assertEqual([line for line in summary.splitlines() if line.startswith("- Result:")], ["- Result: FAIL"])

    # case: S-262.1
    def test_interrupted_entry_finalizes_one_result_and_timing_metadata(self):
        import select
        import signal

        fifo = Path(self.env["BLOCK_FIFO"])
        os.mkfifo(fifo)
        ready_read, ready_write = os.pipe()
        env = {**self.env, "BLOCK_RESET": "1", "READY_FD": str(ready_write)}
        process = subprocess.Popen(
            ["sh", str(self.root / "e2e/journey/batch.sh"), str(self.root / "out")],
            cwd=self.root,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=True,
            pass_fds=(ready_write,),
        )
        os.close(ready_write)
        try:
            ready, _, _ = select.select([ready_read], [], [], 10)
            self.assertTrue(ready, "batch did not reach the blocking reset fixture")
            self.assertEqual(os.read(ready_read, 6), b"ready\n")
            os.killpg(process.pid, signal.SIGTERM)
            _, stderr = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 143, stderr)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate(timeout=10)
            os.close(ready_read)
        summary = (self.root / "out/summary.md").read_text()
        self.assertEqual([line for line in summary.splitlines() if line.startswith("- Result:")], ["- Result: INTERRUPTED"])
        import json
        metadata = json.loads((self.root / "out/batch-metadata.json").read_text())
        self.assertGreaterEqual(metadata["stageDurationsSeconds"]["reset"], 0)
        self.assertEqual(metadata["result"], "INTERRUPTED")

    # case: S-262.2
    def test_workflow_uses_only_batch_entry_and_uploads_each_mode(self):
        workflow = REPO / ".github/workflows/journey-batch.yml"
        parsed = subprocess.run(
            ["node", "-e", "const fs=require('fs');const y=require('js-yaml').load(fs.readFileSync(process.argv[1],'utf8'));process.stdout.write(JSON.stringify(y))", str(workflow)],
            cwd=REPO,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(parsed.returncode, 0, parsed.stderr)
        import json
        spec = json.loads(parsed.stdout)
        self.assertIn("workflow_dispatch", spec["on"])
        self.assertIn("pull_request", spec["on"])
        self.assertIn("baseline", spec["on"]["workflow_dispatch"]["inputs"])
        self.assertNotIn("docs/**", spec["on"]["pull_request"]["paths"])
        self.assertEqual(spec["jobs"]["journey"]["timeout-minutes"], 180)
        steps = spec["jobs"]["journey"]["steps"]
        entries = [step for step in steps if "e2e/journey/batch.sh" in step.get("run", "")]
        self.assertEqual(len(entries), 1)
        self.assertEqual(steps[2]["with"]["go-version-file"], "tools/kb-devkit/go.mod")
        self.assertEqual(sum("journey-local" in step.get("with", {}).get("name", "") for step in steps), 1)
        self.assertEqual(sum("journey-secured" in step.get("with", {}).get("name", "") for step in steps), 1)
        artifact_steps = [step for step in steps if step.get("uses", "").startswith("actions/upload-artifact@")]
        self.assertEqual([step["with"]["path"].rsplit("/", 2)[-2] for step in artifact_steps], ["local", "secured"])
        self.assertEqual(steps[1]["with"]["node-version-file"], "package.json")
        commands = [step.get("run", "") for step in steps]
        self.assertFalse(any("e2e/journey/run.sh" in command for command in commands))
        self.assertFalse(any("prepare-candidate.sh" in command for command in commands))
        self.assertEqual(len(entries), sum("e2e/journey/batch.sh" in command for command in commands))


if __name__ == "__main__":
    unittest.main()
