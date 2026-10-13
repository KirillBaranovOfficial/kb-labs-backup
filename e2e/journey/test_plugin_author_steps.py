import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).parent))
from plugin_author_steps import run_commit_case_steps, run_plugin_author_flow
from service_steps import Journey


FAKE_KB = r'''#!/usr/bin/env python3
import json
import os
import signal
import subprocess
import sys
import tty
from pathlib import Path

args = sys.argv[1:]
root = Path.cwd()
log = Path(os.environ["FAKE_JOURNEY_LOG"])
with log.open("a", encoding="utf-8") as stream:
    stream.write("kb " + " ".join(args) + "\n")
plugin = root / ".kb/plugins/demo"
lock_path = root / ".kb/marketplace.lock"
if args[:6] == ["scaffold", "run", "plugin", "demo", "--mode", "standalone"] and args[6:] == ["--yes"]:
    entry = plugin / "packages/demo-entry"
    core = plugin / "packages/demo-core/src/hello.ts"
    entry.mkdir(parents=True)
    (entry / "src/commands").mkdir(parents=True)
    core.parent.mkdir(parents=True, exist_ok=True)
    (entry / "package.json").write_text('{"name":"demo-entry","version":"0.1.0"}')
    (entry / "src/commands/hello.ts").write_text(
        "const { greeting } = hello({ who: input.flags.who ?? 'World' });\n")
    core.write_text('return { greeting: `Hello, ${who} from demo!` };\n')
elif args[:3] == ["marketplace", "plugins", "add"]:
    if os.environ.get("FAIL_MARKETPLACE_ADD"):
        print("simulated marketplace plugins add failure")
        raise SystemExit(1)
    link_path = args[args.index("--link") + 1]
    if not (root / link_path / "package.json").is_file():
        raise SystemExit("entry package is missing")
    lock_path.write_text(json.dumps({"schema": "kb.marketplace/3", "plugins": {"demo": {
        "source": "local", "link": link_path, "enabled": True
    }}}))
elif args[:4] == ["marketplace", "plugins", "list", "--json"]:
    plugins = json.loads(lock_path.read_text()).get("plugins", {}) if lock_path.exists() else {}
    entries = [{"id": key, "source": value["source"], "scope": "project"}
               for key, value in plugins.items()]
    print(json.dumps({"ok": True, "data": {"entries": entries, "total": len(entries)}}))
elif args[:3] == ["marketplace", "plugins", "remove"] and args[3] == "demo":
    lock_path.write_text(json.dumps({"schema": "kb.marketplace/3", "plugins": {}}))
elif args[:2] == ["marketplace", "install"]:
    print(json.dumps({"ok": True, "data": {"scope": "project"}}))
elif args[:2] == ["marketplace", "uninstall"]:
    print(json.dumps({"ok": True, "data": {}}))
elif args[:2] == ["adapter", "list"]:
    print(json.dumps({"ok": True, "data": {"adapters": [{
        "slot": "llm", "implementation": "@kb-labs/adapters-openai"
    }]}}))
elif args[:3] == ["config", "get", "platform.adapterOptions.llm.baseURL"]:
    print(json.dumps({"ok": True, "data": {"value": "http://mock-llm:8317/v1"}}))
elif args[:3] == ["adapter", "unbind", "llm"]:
    (root / ".kb/llm-unbound").write_text("true")
    print(json.dumps({"ok": True, "data": {}}))
elif args[:3] == ["adapter", "bind", "llm"]:
    (root / ".kb/llm-unbound").unlink(missing_ok=True)
    print(json.dumps({"ok": True, "data": {}}))
elif args[:3] == ["commit", "commit", "--dry-run"]:
    staged = subprocess.run(["git", "diff", "--cached", "--name-only"], cwd=root,
                            capture_output=True, text=True, check=True).stdout
    if (root / ".kb/llm-unbound").exists():
        print(json.dumps({"ok": False, "error": {"code": "KB_ADAPTER_UNAVAILABLE",
            "message": "Adapter llm is not configured.", "hint": "kb adapter bind llm @kb-labs/adapters-openai"}}))
        raise SystemExit(1)
    if "s030-cancel.ts" in staged:
        tty.setraw(sys.stdin.fileno())
        print("⠋ Analyzing changes...", flush=True)
        print("⠙ Analyzing with native tools...", flush=True)
        if sys.stdin.read(1) == "\x03":
            print("Interrupted")
            raise SystemExit(130)
    if "s030-progress.ts" in staged:
        print("⠋ Analyzing changes...", flush=True)
        print("⠙ Analyzing with native tools...", flush=True)
        print("Re-analyzing with diff context (Phase 2)...", flush=True)
        print("Plan generated", flush=True)
        raise SystemExit(0)
    print(json.dumps({"ok": True, "data": {"plan": {"metadata": {"llmUsed": True}}, "applied": False}}))
elif args[:2] == ["commit", "commit"]:
    changes = subprocess.run(["git", "status", "--porcelain=v1", "--untracked-files=all"],
                             cwd=root, capture_output=True, text=True, check=True).stdout
    if not changes:
        print(json.dumps({"ok": True, "data": {"plan": {"commits": []}, "applied": False, "pushed": False, "commits": []}}))
        raise SystemExit(0)
    subprocess.run(["git", "commit", "-m", "test(journey): verify deterministic commit fixture response"],
                   cwd=root, capture_output=True, text=True, check=True)
    if "--json" in args:
        print(json.dumps({"ok": True, "data": {"plan": {"metadata": {"llmUsed": True}}, "applied": True,
                          "pushed": False, "commits": [{"message": "test(journey): verify deterministic commit fixture response"}]}}))
    else:
        print("Commits created successfully")
elif args[:3] == ["review", "run", "--mode=heuristic"] and "--json" not in args:
    print("Files: 1\nFindings: 0\n")
elif args[:3] == ["review", "run", "--mode=heuristic"]:
    print(json.dumps({"ok": True, "data": {"findings": [{"file": ".kb/journey-commit-cases/s015-review.ts", "line": 1}], "metadata": {"mode": "heuristic", "analyzedFiles": 1}}}))
elif args[:2] == ["review", "run"] and "--mode=llm" in args:
    if (root / ".kb/llm-unbound").exists():
        print(json.dumps({"ok": False, "error": {"code": "KB_ADAPTER_UNAVAILABLE",
            "message": "Adapter llm is not configured.", "hint": "kb adapter bind llm @kb-labs/adapters-openai"}}))
        raise SystemExit(1)
    print(json.dumps({"ok": True, "data": {"findings": []}}))
elif args[:2] == ["review", "run"]:
    print(json.dumps({"ok": True, "data": {"findings": []}}))
elif args == ["--help"]:
    print("Commands: demo")
elif args == ["demo", "hello"]:
    built = plugin / ".built-output"
    if not built.exists():
        raise SystemExit("plugin has not been built")
    probe = root / ".kb/output/runtime-temp-probe.txt"
    probe.parent.mkdir(parents=True, exist_ok=True)
    probe.write_text("plugin temp fs ok")
    print(built.read_text())
else:
    raise SystemExit("unexpected kb command: " + " ".join(args))
'''


FAKE_PNPM = r'''#!/usr/bin/env python3
import os
import sys
from pathlib import Path

args = sys.argv[1:]
with Path(os.environ["FAKE_JOURNEY_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write("pnpm " + " ".join(args) + "\n")
if args == ["install"]:
    raise SystemExit(0)
if args == ["-w", "build"]:
    if os.environ.get("FAIL_PNPM_BUILD"):
        detail = os.environ.get("LONG_BUILD_ERROR", "simulated build error")
        print(detail)
        raise SystemExit(2)
    plugin = Path.cwd()
    source = (plugin / "packages/demo-core/src/hello.ts").read_text()
    output = "Edited demo greeting for World!" if "Edited demo greeting" in source else "Hello, World from demo!"
    (plugin / "packages/demo-entry/dist").mkdir(parents=True, exist_ok=True)
    (plugin / "packages/demo-entry/dist/manifest.js").write_text("export default {};\n")
    (plugin / ".built-output").write_text(output)
    raise SystemExit(0)
raise SystemExit("unexpected pnpm command: " + " ".join(args))
'''


class PluginAuthorJourneyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="kb-plugin-author-")
        self.root = Path(self.temp.name)
        self.home = self.root / "home"
        self.project = self.home / "project"
        self.bin = self.home / ".local/bin"
        self.project.mkdir(parents=True)
        (self.project / ".kb").mkdir()
        (self.project / ".kb/kb.config.json").write_text("{}")
        self.bin.mkdir(parents=True)
        self.kb = self.bin / "kb"
        self.pnpm = self.bin / "pnpm"
        self.kb.write_text(FAKE_KB)
        self.pnpm.write_text(FAKE_PNPM)
        self.kb.chmod(0o755)
        self.pnpm.chmod(0o755)
        self.log = self.root / "commands.log"
        self.env = os.environ.copy()
        self.env.update({
            "HOME": str(self.home),
            "PATH": f"{self.bin}:{self.env.get('PATH', '')}",
            "FAKE_JOURNEY_LOG": str(self.log),
        })

    def tearDown(self):
        self.temp.cleanup()

    def run_flow(self, *, mode="local", fail_build=False):
        if fail_build:
            self.env["FAIL_PNPM_BUILD"] = "1"
        journey = Journey(mode, "plugin-author-fixture")
        run_plugin_author_flow(journey, self.project, self.kb, "http://verdaccio:4873",
                               command_runner=subprocess.run, base_env=self.env)
        return journey.result()

    # case: S-014.1
    def test_commit_and_review_case_steps_are_claimed_from_fake_cli_responses(self):
        (self.project / ".kb/marketplace.lock").write_text(json.dumps({
            "schema": "kb.marketplace/3",
            "plugins": {"@kb-labs/commit": {
                "enabled": True,
                "version": "2.120.1",
                "integrity": "sha256-fixture",
                "source": "marketplace",
                "package": "@kb-labs/commit-entry",
            }},
        }))
        journey = Journey("local", "commit-case-fixture")
        run_commit_case_steps(journey, self.project, self.kb, "http://verdaccio:4873",
                              subprocess.run, self.env)
        result = journey.result()
        expected = {
            *(f"S-014.{step}" for step in range(1, 4)),
            *(f"S-015.{step}" for step in range(1, 5)),
            *(f"S-030.{step}" for step in range(1, 5)),
            *(f"S-160.{step}" for step in range(1, 5)),
        }
        claims = {claim["claimId"]: claim["status"] for claim in result.case_run["claims"]}
        self.assertTrue(expected.issubset(claims))
        self.assertEqual({step: claims[step] for step in ("S-014.1", "S-014.2", "S-014.3")}, {
            "S-014.1": "PASS", "S-014.2": "PASS", "S-014.3": "PASS",
        })
        self.assertEqual({step: claims[step] for step in ("S-015.1", "S-015.2", "S-015.3", "S-015.4")}, {
            "S-015.1": "PASS", "S-015.2": "PASS", "S-015.3": "PASS", "S-015.4": "PASS",
        })
        self.assertEqual({step: claims[step] for step in ("S-160.1", "S-160.2", "S-160.3", "S-160.4")}, {
            "S-160.1": "PASS", "S-160.2": "PASS", "S-160.3": "PASS", "S-160.4": "PASS",
        }, result.steps)
        self.assertEqual({step: claims[step] for step in ("S-030.1", "S-030.2", "S-030.3", "S-030.4")}, {
            "S-030.1": "PASS", "S-030.2": "PASS", "S-030.3": "PASS", "S-030.4": "PASS",
        })

    # case: S-003.1
    # case: S-210.1
    def test_scaffold_add_edit_invoke_and_remove_records_all_case_steps(self):
        result = self.run_flow()

        self.assertEqual(result.status, "PASS")
        self.assertEqual(
            [step["cases"] for step in result.steps],
            [["S-003.1"], ["S-003.2"], ["S-003.3"], ["S-003.4", "S-210.1"],
             ["S-003.5"], ["S-003.6"]],
        )
        self.assertEqual(
            result.case_run["claims"],
            [{"claimId": f"S-003.{step}", "kind": "journey", "status": "PASS"}
             for step in range(1, 7)] +
            [{"claimId": "S-210.1", "kind": "journey", "status": "PASS"}],
        )
        self.assertEqual(self.log.read_text().splitlines(), [
            "kb scaffold run plugin demo --mode standalone --yes",
            "pnpm install",
            "pnpm -w build",
            "kb marketplace plugins add --link .kb/plugins/demo/packages/demo-entry",
            "kb marketplace plugins list --json",
            "kb --help",
            "kb demo hello",
            "pnpm -w build",
            "kb demo hello",
            "kb marketplace plugins remove demo",
            "kb marketplace plugins list --json",
        ])
        self.assertFalse((self.project / ".kb/plugins/demo").exists())
        self.assertEqual(json.loads((self.project / ".kb/marketplace.lock").read_text())["plugins"], {})

    def test_secured_profile_runs_the_same_author_flow(self):
        result = self.run_flow(mode="secured")

        self.assertEqual(result.status, "PASS")
        self.assertTrue(all(step["mode"] == "secured" for step in result.steps))
        self.assertEqual(len(result.case_run["claims"]), 7)

    def test_build_failure_is_reported_and_generated_files_are_cleaned(self):
        result = self.run_flow(fail_build=True)

        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.first_failure["name"], "build generated plugin")
        self.assertIn("pnpm -w build exited 2; simulated build error", result.first_failure["message"])
        self.assertEqual(result.case_run["claims"], [
            {"claimId": "S-003.1", "kind": "journey", "status": "PASS"},
            {"claimId": "S-003.2", "kind": "journey", "status": "FAIL"},
        ])
        self.assertFalse((self.project / ".kb/plugins/demo").exists())

    def test_build_failure_keeps_the_complete_pnpm_policy_diagnostic(self):
        self.env["FAIL_PNPM_BUILD"] = "1"
        self.env["LONG_BUILD_ERROR"] = "ERR_PNPM_MINIMUM_RELEASE_AGE" + "x" * 800

        result = self.run_flow()

        self.assertIn("ERR_PNPM_MINIMUM_RELEASE_AGE", result.first_failure["message"])

    def test_add_failure_is_recorded_without_masking_it_with_a_blocked_step_error(self):
        self.env["FAIL_MARKETPLACE_ADD"] = "1"

        result = self.run_flow()

        self.assertEqual(result.status, "FAIL")
        self.assertEqual(result.first_failure["name"], "add plugin to project")
        self.assertIn("simulated marketplace plugins add failure", result.first_failure["message"])
        self.assertEqual(result.steps[-1]["status"], "BLOCKED")
        self.assertEqual(result.steps[-1]["name"], "remove plugin registration")
        self.assertIn("plugin was not added", result.steps[-1]["message"])


if __name__ == "__main__":
    unittest.main()
