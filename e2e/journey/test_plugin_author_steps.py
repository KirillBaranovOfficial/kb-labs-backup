import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).parent))
from plugin_author_steps import run_plugin_author_flow
from service_steps import Journey


FAKE_KB = r'''#!/usr/bin/env python3
import json
import os
import sys
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
    core.parent.mkdir(parents=True, exist_ok=True)
    (entry / "package.json").write_text('{"name":"demo-entry","version":"0.1.0"}')
    core.write_text('return { greeting: `Hello, ${who} from demo!` };\n')
elif args[:3] == ["marketplace", "link", "--link"]:
    if not (root / args[3] / "package.json").is_file():
        raise SystemExit("entry package is missing")
    lock_path.write_text(json.dumps({"plugins": {"demo": {
        "source": "local", "resolvedPath": args[3]
    }}}))
elif args[:4] == ["marketplace", "plugins", "list", "--json"]:
    plugins = json.loads(lock_path.read_text()).get("plugins", {}) if lock_path.exists() else {}
    entries = [{"id": key, "source": value["source"], "scope": "project"}
               for key, value in plugins.items()]
    print(json.dumps({"entries": entries, "total": len(entries)}))
elif args[:4] == ["marketplace", "plugins", "unlink", "demo"]:
    lock_path.write_text(json.dumps({"plugins": {}}))
elif args == ["--help"]:
    print("Commands: demo")
elif args == ["demo", "hello"]:
    built = plugin / ".built-output"
    if not built.exists():
        raise SystemExit("plugin has not been built")
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
        print("simulated build error")
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

    # case: S-003.1
    def test_scaffold_link_edit_invoke_and_unlink_records_all_case_steps(self):
        result = self.run_flow()

        self.assertEqual(result.status, "PASS")
        self.assertEqual(
            [step["cases"] for step in result.steps],
            [[f"S-003.{step}"] for step in range(1, 7)],
        )
        self.assertEqual(
            result.case_run["claims"],
            [{"claimId": f"S-003.{step}", "kind": "journey", "status": "PASS"}
             for step in range(1, 7)],
        )
        self.assertEqual(self.log.read_text().splitlines(), [
            "kb scaffold run plugin demo --mode standalone --yes",
            "pnpm install",
            "pnpm -w build",
            "kb marketplace link --link .kb/plugins/demo/packages/demo-entry",
            "kb marketplace plugins list --json",
            "kb --help",
            "kb demo hello",
            "pnpm -w build",
            "kb demo hello",
            "kb marketplace plugins unlink demo",
            "kb marketplace plugins list --json",
        ])
        self.assertFalse((self.project / ".kb/plugins/demo").exists())
        self.assertEqual(json.loads((self.project / ".kb/marketplace.lock").read_text())["plugins"], {})

    def test_secured_profile_runs_the_same_author_flow(self):
        result = self.run_flow(mode="secured")

        self.assertEqual(result.status, "PASS")
        self.assertTrue(all(step["mode"] == "secured" for step in result.steps))
        self.assertEqual(len(result.case_run["claims"]), 6)

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


if __name__ == "__main__":
    unittest.main()
