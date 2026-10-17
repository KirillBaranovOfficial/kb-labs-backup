import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from test_environment import isolated_environment

REPO = Path(__file__).resolve().parents[2]
ENTRY_SHELL = shutil.which("dash") or "sh"


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
if [ "${FAIL_UNIT:-}" = 1 ]; then
  printf 'unit failure fixture\\n'
  exit 1
fi
exit 0
''')
        self._script(self.bin / "go", '''#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    cat > "$1" <<'SCRIPT'
#!/bin/sh
printf 'build\\n' >> "$CALLS"
if [ "${FAIL_AFFECTED_BUILD:-}" = 1 ]; then
  printf 'affected build fixture failure\\n'
  exit 1
fi
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
if [ "$1 $2 $3" = "rev-parse --verify --quiet" ]; then
  [ "${MISSING_BASE_REF:-}" = "$4" ] && exit 1
  echo deadbeef
  exit 0
fi
exit 2
''')
        self.env = {
            **isolated_environment(
                Path(self.root / "home"), self.bin,
                required_tools=("git", "dirname", "mkdir", "mktemp", "date", "rm", "cat", "cp", "grep", "env", "chmod", "mv"),
            ),
            "CALLS": str(self.calls),
            "REPORTS": str(self.root / ".journey/reports"),
            "ATTEMPT_MARKER": str(self.root / "first-attempt"),
            "BLOCK_FIFO": str(self.root / "reset.fifo"),
            "KB_DEVKIT_BASE_REF": "origin/master",
        }
        Path(self.env["REPORTS"]).mkdir(parents=True)

    def tearDown(self):
        self.temporary.cleanup()

    def _script(self, path, contents):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        path.chmod(0o755)

    def _run(self, mode="", failure_kind="infra", missing_base_ref="", fail_build=False):
        env = dict(self.env)
        if mode:
            env["FAIL_MODE"] = mode
            env["FAIL_KIND"] = failure_kind
        if missing_base_ref:
            env["KB_DEVKIT_BASE_REF"] = missing_base_ref
            env["MISSING_BASE_REF"] = missing_base_ref + "^{commit}"
        if fail_build:
            env["FAIL_AFFECTED_BUILD"] = "1"
        return subprocess.run(
            [ENTRY_SHELL, str(self.root / "e2e/journey/batch.sh"), str(self.root / "out")],
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
        self.assertNotIn("not found", result.stderr)
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
        for stage_log in ("reset.log", "build.log", "unit.log", "candidate.log"):
            self.assertTrue((self.root / "out" / stage_log).is_file(), stage_log)
        for mode in ("local", "secured"):
            self.assertTrue((self.root / f"out/{mode}/batch.log").is_file())
        self.assertIn("deadbeef", (self.root / "out/run.json").read_text())
        summary = (self.root / "out/summary.md").read_text()
        self.assertIn("- Commit: `deadbeef`", summary)
        self.assertIn("- Baseline: `j28`", summary)
        self.assertIn("- Build base: `origin/master`", summary)
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
            [ENTRY_SHELL, str(self.root / "e2e/journey/batch.sh"), str(self.root / "out")],
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

    # case: S-262.1
    def test_entry_names_missing_base_ref_before_affected_build(self):
        result = self._run(missing_base_ref="origin/master")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("affected-build base ref 'origin/master' is missing", result.stderr)
        self.assertIn("next: git fetch --all --prune", result.stderr)
        self.assertNotIn("not found", result.stderr)
        self.assertFalse(self.calls.exists())
        self.assertTrue((self.root / "out/summary.md").is_file())
        self.assertTrue((self.root / "out/batch-metadata.json").is_file())
        self.assertTrue((self.root / "out/build.log").is_file())

    # case: S-262.2
    def test_unit_failure_summary_points_to_relative_stage_log(self):
        env = {**self.env, "FAIL_UNIT": "1"}
        result = subprocess.run(
            [ENTRY_SHELL, str(self.root / "e2e/journey/batch.sh"), str(self.root / "out")],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        summary = (self.root / "out/summary.md").read_text()
        self.assertIn("- Failed stage: `unit`", summary)
        self.assertIn("- Stage log: `unit.log`", summary)
        self.assertEqual([line for line in summary.splitlines() if line.startswith("- Result:")], ["- Result: FAIL"])
        self.assertEqual((self.root / "out/unit.log").read_text(), "unit failure fixture\n")

    # case: S-262.1
    def test_entry_preserves_root_outputs_when_build_fails_before_modes(self):
        result = self._run(fail_build=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("devkit affected build failed", result.stderr)
        self.assertNotIn("not found", result.stderr)
        self.assertTrue((self.root / "out/summary.md").is_file())
        self.assertTrue((self.root / "out/batch-metadata.json").is_file())
        self.assertTrue((self.root / "out/build.log").is_file())
        self.assertFalse((self.root / "out/local").exists())
        self.assertFalse((self.root / "out/secured").exists())

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
        self.assertEqual(spec["on"]["push"]["branches"], ["batch/**"])
        self.assertIn("pull_request", spec["on"])
        self.assertIn("baseline", spec["on"]["workflow_dispatch"]["inputs"])
        self.assertIn("recorded in batch metadata", spec["on"]["workflow_dispatch"]["inputs"]["baseline"]["description"])
        self.assertNotIn("docs/**", spec["on"]["pull_request"]["paths"])
        self.assertEqual(spec["jobs"]["journey"]["timeout-minutes"], 180)
        steps = spec["jobs"]["journey"]["steps"]
        job_env = spec["jobs"]["journey"]["env"]
        self.assertEqual(job_env["JOURNEY_DEFAULT_BRANCH"], "${{ github.event.repository.default_branch }}")
        entries = [step for step in steps if "e2e/journey/batch.sh" in step.get("run", "")]
        self.assertEqual(len(entries), 1)
        self.assertEqual(job_env["BATCH_OUTPUT"], ".journey/batch/${{ github.run_id }}")
        self.assertEqual(entries[0]["run"], 'e2e/journey/batch.sh "$BATCH_OUTPUT" "$BATCH_BASELINE"')
        self.assertEqual(entries[0]["env"]["BATCH_BASELINE"], "${{ inputs.baseline || env.JOURNEY_DEFAULT_BRANCH }}")
        self.assertEqual(entries[0]["env"]["KB_DEVKIT_BASE_REF"], "${{ github.event.pull_request.base.sha || format('origin/{0}', env.JOURNEY_DEFAULT_BRANCH) }}")
        self.assertEqual(job_env["GOTOOLCHAIN"], "local")
        checkout_step = next(step for step in steps if step.get("uses", "").startswith("actions/checkout@"))
        self.assertEqual(checkout_step["with"]["fetch-depth"], 0)
        pnpm_steps = [step for step in steps if step.get("uses", "").startswith("pnpm/action-setup@")]
        self.assertEqual(len(pnpm_steps), 1)
        pnpm_step = pnpm_steps[0]
        node_step = next(step for step in steps if step.get("uses", "").startswith("actions/setup-node@"))
        self.assertLess(steps.index(pnpm_step), steps.index(node_step))
        self.assertNotIn("version", pnpm_step.get("with", {}))
        package = json.loads((REPO / "package.json").read_text())
        self.assertIn("packageManager", package)
        go_step = next(step for step in steps if step.get("uses", "").startswith("actions/setup-go@"))
        self.assertEqual(go_step["with"]["go-version"], "1.26.8")
        module = subprocess.run(["go", "mod", "edit", "-json"], cwd=REPO / "tools/kb-devkit", text=True, capture_output=True, check=False)
        self.assertEqual(module.returncode, 0, module.stderr)
        module_info = json.loads(module.stdout)
        selected_go = tuple(int(part) for part in go_step["with"]["go-version"].split("."))
        for required in (module_info["Go"], module_info["Toolchain"].removeprefix("go")):
            self.assertGreaterEqual(selected_go, tuple(int(part) for part in required.split(".")))
        self.assertEqual(sum("journey-local" in step.get("with", {}).get("name", "") for step in steps), 1)
        self.assertEqual(sum("journey-secured" in step.get("with", {}).get("name", "") for step in steps), 1)
        artifact_steps = [step for step in steps if step.get("uses", "").startswith("actions/upload-artifact@")]
        self.assertEqual([step["with"]["path"] for step in artifact_steps], [
            "${{ env.BATCH_OUTPUT }}/", "${{ env.BATCH_OUTPUT }}/",
        ])
        self.assertEqual([step["with"]["if-no-files-found"] for step in artifact_steps], ["warn", "warn"])
        self.assertEqual(node_step["with"]["node-version-file"], "package.json")
        summary_step = next(step for step in steps if step.get("name") == "Add batch summary")
        self.assertEqual(summary_step["if"], "always()")
        failed_step_expression = summary_step["env"]["FAILED_STEP"]
        self.assertIn("steps.node.outcome == 'failure' && 'Set up Node'", failed_step_expression)
        self.assertIn("steps.pnpm.outcome == 'failure' && 'Set up pnpm'", failed_step_expression)
        summary_result = self.root / "github-step-summary.md"
        env = {
            **os.environ,
            "GITHUB_STEP_SUMMARY": str(summary_result),
            "FAILED_STEP": "Set up pnpm",
            "BATCH_OUTPUT": ".journey/batch/test-run",
        }
        summary_command = summary_step["run"]
        missing_summary = subprocess.run(
            ["sh", "-c", summary_command],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(missing_summary.returncode, 0, missing_summary.stderr)
        self.assertEqual(missing_summary.stderr, "")
        self.assertEqual(summary_result.read_text(), "batch did not start: Set up pnpm\n")
        commands = [step.get("run", "") for step in steps]
        self.assertFalse(any("e2e/journey/run.sh" in command for command in commands))
        self.assertFalse(any("prepare-candidate.sh" in command for command in commands))
        self.assertEqual(len(entries), sum("e2e/journey/batch.sh" in command for command in commands))


if __name__ == "__main__":
    unittest.main()
