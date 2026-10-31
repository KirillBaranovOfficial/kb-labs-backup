from __future__ import annotations

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from test_docker_build_inputs import copy_instructions
from test_environment import isolated_environment, require_compose_command


JOURNEY_DIR = Path(__file__).resolve().parent
REPOSITORY_ROOT = JOURNEY_DIR.parents[1]
RUNNER_DOCKERFILE = JOURNEY_DIR / "Dockerfile.runner"
TOOLCHAIN_SCRIPT = JOURNEY_DIR / "runner-toolchain.sh"
TOOLCHAIN_IMAGE_REPOSITORY = "kb-journey-runner-toolchain"
STUDIO_PLAYWRIGHT_DECLARATION = "^1.52.0"


def scratch_dir() -> Path:
    """Return the gitignored .journey scratch area for sandboxed temp files."""
    scratch = REPOSITORY_ROOT / ".journey" / "test-scratch"
    scratch.mkdir(parents=True, exist_ok=True)
    return scratch


def temporary_directory(prefix: str):
    """Create a temp directory under the gitignored .journey scratch area."""
    return tempfile.TemporaryDirectory(prefix=prefix, dir=scratch_dir())


def runner_toolchain_image(*, dockerfile: Path | None = None,
                           studio_manifest_input: Path | None = None,
                           package_manifest_input: Path | None = None) -> str:
    sys.path.insert(0, str(JOURNEY_DIR))
    from journey_resources import runner_toolchain_image as derive
    return derive(dockerfile=dockerfile, studio_manifest_input=studio_manifest_input,
                  package_manifest_input=package_manifest_input)


def runner_toolchain_contract(*, dockerfile: Path | None = None,
                              studio_manifest_input: Path | None = None):
    sys.path.insert(0, str(JOURNEY_DIR))
    from journey_resources import runner_toolchain_contract as derive
    return derive(dockerfile=dockerfile, studio_manifest_input=studio_manifest_input)



def mutated_dockerfile(old: str, new: str) -> Path:
    """Copy the real Dockerfile with one input replaced."""
    source = RUNNER_DOCKERFILE.read_text(encoding="utf-8")
    assert old in source, f"mutation target missing from Dockerfile.runner: {old}"
    handle = tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix="-dockerfile",
                                         delete=False, dir=scratch_dir())
    with handle:
        handle.write(source.replace(old, new))
    return Path(handle.name)


class RunnerToolchainTagTests(unittest.TestCase):
    def setUp(self):
        temporary = temporary_directory(prefix="journey-toolchain-tag-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.studio_manifest = root / "studio-package.json"
        self.package_manifest = root / "workspace-package.json"
        self.package_manifest.write_text(json.dumps({"packageManager": "pnpm@11.4.0"}), encoding="utf-8")
        self.studio_manifest.write_text(json.dumps({"devDependencies": {
            "@playwright/test": STUDIO_PLAYWRIGHT_DECLARATION,
        }}), encoding="utf-8")

    def test_tag_is_derived_from_the_declared_stage_and_source_manifests(self):
        reference = runner_toolchain_image(studio_manifest_input=self.studio_manifest,
                                           package_manifest_input=self.package_manifest)
        self.assertRegex(reference, rf"^{TOOLCHAIN_IMAGE_REPOSITORY}:[0-9a-f]{{16}}$")
        self.assertEqual(reference, runner_toolchain_image(studio_manifest_input=self.studio_manifest,
                                                           package_manifest_input=self.package_manifest),
                         "identical toolchain inputs must derive the same tag")

    def test_package_manager_change_invalidates_the_toolchain_image_tag(self):
        from journey_resources import runner_toolchain_image as derive
        original = json.loads((REPOSITORY_ROOT / "package.json").read_text(encoding="utf-8"))
        package_manifest = self.package_manifest
        package_manifest.write_text(json.dumps(original), encoding="utf-8")
        baseline = derive(studio_manifest_input=self.studio_manifest,
                          package_manifest_input=package_manifest)
        original["packageManager"] = original["packageManager"].rsplit("@", 1)[0] + "@11.5.0"
        package_manifest.write_text(json.dumps(original), encoding="utf-8")
        changed = derive(studio_manifest_input=self.studio_manifest,
                         package_manifest_input=package_manifest)
        self.assertNotEqual(changed, baseline)

    def test_toolchain_stage_text_is_extracted_from_the_real_dockerfile(self):
        from journey_resources import runner_toolchain_stage_text
        stage = runner_toolchain_stage_text()
        self.assertIn("AS toolchain", stage)
        self.assertIn("apt-get install", stage)
        self.assertIn("playwright install --with-deps chromium", stage)
        self.assertNotIn("COPY docs/qa/scenarios", stage,
                         "per-run runner COPYs must stay outside the toolchain stage")

    def test_runner_from_consumes_the_derived_toolchain_contract(self):
        contract = runner_toolchain_contract(studio_manifest_input=self.studio_manifest)
        instructions = [line.split() for line in RUNNER_DOCKERFILE.read_text(encoding="utf-8").splitlines()
                        if line.startswith("FROM ")]
        runner_base = instructions[-1][1]
        self.assertIn(runner_base, (f"${{{contract['imageArgument']}}}", f"${contract['imageArgument']}"))
        self.assertEqual(contract["imageReference"],
                         runner_toolchain_image(studio_manifest_input=self.studio_manifest))

    def test_each_toolchain_input_changes_the_tag(self):
        baseline = runner_toolchain_image(studio_manifest_input=self.studio_manifest,
                                          package_manifest_input=self.package_manifest)
        mutations = {
            "base image": mutated_dockerfile("FROM node:24-bookworm AS toolchain",
                                             "FROM node:25-bookworm AS toolchain"),
            "apt package list": mutated_dockerfile("procps iproute2 lsof",
                                                   "procps iproute2"),
            "playwright install command": mutated_dockerfile(
                "playwright install --with-deps chromium",
                "playwright install --with-deps chromium-headless-shell"),
        }
        try:
            for label, dockerfile in mutations.items():
                with self.subTest(input=label):
                    self.assertNotEqual(
                        runner_toolchain_image(dockerfile=dockerfile,
                                               studio_manifest_input=self.studio_manifest,
                                               package_manifest_input=self.package_manifest),
                        baseline)
            with self.subTest(input="Playwright source declaration"):
                changed = self.studio_manifest.with_name("changed-studio-package.json")
                changed.write_text(json.dumps({"devDependencies": {
                    "@playwright/test": "^1.53.0",
                }}), encoding="utf-8")
                self.assertNotEqual(
                    runner_toolchain_image(studio_manifest_input=changed,
                                           package_manifest_input=self.package_manifest), baseline)
        finally:
            for dockerfile in mutations.values():
                dockerfile.unlink()

    def test_per_run_stage_changes_do_not_change_the_toolchain_tag(self):
        baseline = runner_toolchain_image(studio_manifest_input=self.studio_manifest)
        for old, new in (
                ("COPY docs/qa/scenarios /opt/kb-journey/docs/qa/scenarios",
                 "COPY docs/qa /opt/kb-journey/docs/qa/scenarios"),
                ("COPY e2e/journey/entrypoint.py /usr/local/bin/kb-journey-runner",
                 "COPY e2e/journey/entrypoint.py /usr/local/bin/kb-journey-entrypoint")):
            with self.subTest(per_run_copy=old):
                dockerfile = mutated_dockerfile(old, new)
                self.addCleanup(dockerfile.unlink)
                self.assertEqual(
                    runner_toolchain_image(dockerfile=dockerfile,
                                           studio_manifest_input=self.studio_manifest),
                    baseline)

    def test_missing_studio_source_manifest_is_reported(self):
        with temporary_directory(prefix="journey-toolchain-missing-") as temporary:
            missing = Path(temporary) / "studio-package.json"
            with self.assertRaises(FileNotFoundError) as context:
                runner_toolchain_image(studio_manifest_input=missing)
        self.assertIn("Studio's source package.json", str(context.exception))

    def test_dockerfile_without_a_toolchain_stage_is_refused(self):
        source = RUNNER_DOCKERFILE.read_text(encoding="utf-8").replace(
            "FROM node:24-bookworm AS toolchain", "FROM node:24-bookworm")
        handle = tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix="-dockerfile", delete=False)
        with handle:
            handle.write(source)
        dockerfile = Path(handle.name)
        self.addCleanup(dockerfile.unlink)
        with self.assertRaises(ValueError) as context:
            runner_toolchain_image(dockerfile=dockerfile)
        self.assertIn("AS toolchain", str(context.exception))


class RunnerToolchainImageContractTests(unittest.TestCase):
    def test_toolchain_stage_copies_exactly_the_declared_toolchain_inputs(self):
        from journey_resources import TOOLCHAIN_STAGE_INPUTS, runner_toolchain_stage_text
        handle = tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix="-dockerfile",
                                             delete=False, dir=scratch_dir())
        with handle:
            handle.write(runner_toolchain_stage_text())
        stage_file = Path(handle.name)
        self.addCleanup(stage_file.unlink)
        copied = set(copy_instructions(stage_file))
        declared = {(resource.source, resource.destination)
                    for resource in TOOLCHAIN_STAGE_INPUTS}
        self.assertEqual(copied, declared,
                         f"toolchain stage COPY resources differ from journey_resources.py; "
                         f"undeclared={sorted(copied - declared)}, "
                         f"not_copied={sorted(declared - copied)}")

    def test_run_sh_builds_the_toolchain_before_the_compose_runner_build(self):
        lines = (JOURNEY_DIR / "run.sh").read_text(encoding="utf-8").splitlines()
        source_line = next((index for index, line in enumerate(lines)
                            if "runner-toolchain.sh" in line), None)
        ensure_line = next((index for index, line in enumerate(lines)
                            if line.startswith("journey_toolchain_ensure ")), None)
        compose_build_line = next((index for index, line in enumerate(lines)
                                   if line.startswith("journey_compose build publisher runner idp-stub")),
                                  None)
        self.assertIsNotNone(source_line, "run.sh must source e2e/journey/runner-toolchain.sh")
        self.assertIsNotNone(ensure_line, "run.sh must call journey_toolchain_ensure")
        self.assertIsNotNone(compose_build_line)
        self.assertLess(source_line, ensure_line)
        self.assertLess(ensure_line, compose_build_line)

    def test_compose_build_arg_carries_the_derived_toolchain_image(self):
        command = require_compose_command(self)
        with temporary_directory(prefix="journey-toolchain-compose-") as temporary:
            root = Path(temporary)
            manifest = root / "studio-package.json"
            manifest.write_text(json.dumps({"devDependencies": {
                "@playwright/test": STUDIO_PLAYWRIGHT_DECLARATION,
            }}), encoding="utf-8")
            contract = runner_toolchain_contract(studio_manifest_input=manifest)
            expected = contract["imageReference"]
            env = self._compose_environment(root, command, {contract["imageArgument"]: expected})
            compose = subprocess.run(
                [*command, "-f", "e2e/docker-compose.yml", "-f",
                 "e2e/journey/docker-compose.yml", "config", "--format", "json"],
                cwd=REPOSITORY_ROOT, env=env,
                text=True, capture_output=True, check=False,
            )
        self.assertEqual(compose.returncode, 0, compose.stderr)
        rendered = json.loads(compose.stdout)
        self.assertEqual(rendered["services"]["runner"]["build"]["args"][contract["imageArgument"]],
                         expected)

    def test_compose_without_the_derivation_falls_back_to_a_named_sentinel(self):
        command = require_compose_command(self)
        with temporary_directory(prefix="journey-toolchain-compose-") as temporary:
            env = self._compose_environment(Path(temporary), command, {})
            compose = subprocess.run(
                [*command, "-f", "e2e/docker-compose.yml", "-f",
                 "e2e/journey/docker-compose.yml", "config", "--format", "json"],
                cwd=REPOSITORY_ROOT, env=env,
                text=True, capture_output=True, check=False,
            )
        self.assertEqual(compose.returncode, 0, compose.stderr)
        rendered = json.loads(compose.stdout)
        self.assertEqual(rendered["services"]["runner"]["build"]["args"]["KB_JOURNEY_TOOLCHAIN_IMAGE"],
                         f"{TOOLCHAIN_IMAGE_REPOSITORY}:requires-e2e-journey-run-sh")

    def _compose_environment(self, root: Path, command: list[str], overrides: dict[str, str]) -> dict[str, str]:
        return isolated_environment(
            root / "home", root / "bin",
            required_tools=(command[0],),
            overrides={
                "KB_JOURNEY_WORKSPACE_SOURCE": str(REPOSITORY_ROOT),
                "KB_JOURNEY_GIT_REVISION": "test-revision",
                "KB_JOURNEY_RUNNER_UID": str(os.getuid()),
                "KB_JOURNEY_RUNNER_GID": str(os.getgid()),
                **overrides,
            })


class RunnerToolchainEnsureTests(unittest.TestCase):
    """Exercise the ensure/reclaim lifecycle against a stateful fake Docker CLI.

    The fixture root is built from the real journey files (Dockerfile.runner,
    journey_resources.py, journey_values.py) with source package manifests,
    so the derivation and the shell script run unmodified.
    """

    def _fixture_root(self, root: Path) -> Path:
        fixture = root / "workspace"
        (fixture / "e2e" / "journey").mkdir(parents=True)
        shutil.copy2(RUNNER_DOCKERFILE, fixture / "e2e" / "journey" / "Dockerfile.runner")
        for name in ("journey_resources.py", "journey_values.py"):
            shutil.copy2(JOURNEY_DIR / name, fixture / "e2e" / "journey" / name)
        (fixture / "package.json").write_text(json.dumps({"packageManager": "pnpm@11.4.0"}), encoding="utf-8")
        studio = fixture / "e2e" / "studio"
        studio.mkdir(parents=True)
        (studio / "package.json").write_text(json.dumps({"devDependencies": {
            "@playwright/test": STUDIO_PLAYWRIGHT_DECLARATION,
        }}), encoding="utf-8")
        return fixture

    def _fake_docker(self, bin_dir: Path, state_path: Path) -> Path:
        bin_dir.mkdir(parents=True, exist_ok=True)
        docker = bin_dir / "docker"
        docker.write_text(
            "#!/usr/bin/env python3\n"
            "import json, pathlib, sys\n"
            f"state_path = pathlib.Path({str(state_path)!r})\n"
            "state = json.loads(state_path.read_text()) if state_path.exists() else {'images': []}\n"
            "state.setdefault('calls', [])\n"
            "args = sys.argv[1:]\n"
            "state['calls'].append(args)\n"
            "if args[:2] == ['images', '-q']:\n"
            "    print('\\n'.join(image['id'] for image in state['images'] if image['ref'] == args[2]))\n"
            "elif args[0] == 'build':\n"
            "    target = args[args.index('--target') + 1]\n"
            "    tag = args[args.index('-t') + 1]\n"
            "    label = args[args.index('--label') + 1]\n"
            "    assert target == 'toolchain', f'unmodeled build target {target}'\n"
            "    if not any(image['ref'] == tag for image in state['images']):\n"
            "        state['images'].append({'id': 'id-' + tag, 'ref': tag, 'label': label})\n"
            "elif args[:2] == ['images', '--filter']:\n"
            "    wanted = args[2].removeprefix('label=')\n"
            "    for image in state['images']:\n"
            "        if image['label'].split('=', 1)[0] == wanted:\n"
            "            print(image['id'], image['ref'])\n"
            "elif args[:3] == ['image', 'rm', '-f']:\n"
            "    state['images'] = [image for image in state['images'] if image['id'] != args[3]]\n"
            "else:\n"
            "    raise SystemExit(f'unmodeled Docker command: {args!r}')\n"
            "state_path.write_text(json.dumps(state))\n",
            encoding="utf-8",
        )
        docker.chmod(docker.stat().st_mode | stat.S_IXUSR)
        return docker

    def test_ensure_builds_once_reuses_then_reclaims_previous_labels(self):
        from journey_resources import TOOLCHAIN_IMAGE_LABEL
        with temporary_directory(prefix="journey-toolchain-ensure-") as temporary:
            fixture = self._fixture_root(Path(temporary))
            expected = runner_toolchain_image(
                studio_manifest_input=fixture / "e2e" / "studio" / "package.json",
                package_manifest_input=fixture / "package.json")
            bin_dir = Path(temporary) / "bin"
            state_path = Path(temporary) / "docker-state.json"
            state_path.write_text(json.dumps({"images": [
                {"id": "id-previous", "ref": f"{TOOLCHAIN_IMAGE_REPOSITORY}:0000000000000000",
                 "label": f"{TOOLCHAIN_IMAGE_LABEL}=0000000000000000"},
            ]}), encoding="utf-8")
            self._fake_docker(bin_dir, state_path)
            env = isolated_environment(
                Path(temporary) / "home", bin_dir,
                required_tools=("bash", "python3"),
                overrides={"PYTHONDONTWRITEBYTECODE": "1"})
            driver = (
                "set -e\n"
                f"source {TOOLCHAIN_SCRIPT}\n"
                f"journey_toolchain_ensure {fixture}\n"
                f"journey_toolchain_ensure {fixture}\n"
                'printf "exported=%s\\n" "$KB_JOURNEY_TOOLCHAIN_IMAGE"\n'
            )
            result = subprocess.run(["bash", "-c", driver], cwd=fixture, env=env,
                                    text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            state = json.loads(state_path.read_text())

        self.assertIn(f"==> journey runner toolchain {expected} built", result.stdout)
        self.assertIn(f"==> journey runner toolchain {expected} reused", result.stdout)
        self.assertIn(f"exported={expected}", result.stdout)
        builds = [call for call in state["calls"] if call[0] == "build"]
        self.assertEqual(builds, [["build", "--target", "toolchain", "-t", expected,
                                   "--label", f"{TOOLCHAIN_IMAGE_LABEL}={expected.split(':', 1)[1]}",
                                   "-f", "e2e/journey/Dockerfile.runner", "."]])
        self.assertNotIn("id-previous", [image["id"] for image in state["images"]],
                         "the previous toolchain image must be reclaimed by its label")
        self.assertIn({"id": f"id-{expected}", "ref": expected,
                       "label": f"{TOOLCHAIN_IMAGE_LABEL}={expected.split(':', 1)[1]}"},
                      state["images"], "the current toolchain image must be kept")

    def test_ensure_reports_a_failed_reclaim_without_failing_the_run(self):
        from journey_resources import TOOLCHAIN_IMAGE_LABEL
        with temporary_directory(prefix="journey-toolchain-reclaim-") as temporary:
            fixture = self._fixture_root(Path(temporary))
            bin_dir = Path(temporary) / "bin"
            state_path = Path(temporary) / "docker-state.json"
            state_path.write_text(json.dumps({"images": [
                {"id": "id-stuck", "ref": f"{TOOLCHAIN_IMAGE_REPOSITORY}:0000000000000000",
                 "label": f"{TOOLCHAIN_IMAGE_LABEL}=0000000000000000"},
            ]}), encoding="utf-8")
            docker = self._fake_docker(bin_dir, state_path)
            docker.write_text(docker.read_text(encoding="utf-8").replace(
                "elif args[:3] == ['image', 'rm', '-f']:\n"
                "    state['images'] = [image for image in state['images'] if image['id'] != args[3]]\n",
                "elif args[:3] == ['image', 'rm', '-f']:\n"
                "    if args[3] == 'id-stuck':\n"
                "        state_path.write_text(json.dumps(state))\n"
                "        sys.exit(1)\n"
                "    state['images'] = [image for image in state['images'] if image['id'] != args[3]]\n",
            ))
            env = isolated_environment(
                Path(temporary) / "home", bin_dir,
                required_tools=("bash", "python3"),
                overrides={"PYTHONDONTWRITEBYTECODE": "1"})
            driver = (
                "set -e\n"
                f"source {TOOLCHAIN_SCRIPT}\n"
                f"journey_toolchain_ensure {fixture}\n"
                "echo ensure-completed\n"
            )
            result = subprocess.run(["bash", "-c", driver], cwd=fixture, env=env,
                                    text=True, capture_output=True, check=False)
            state = json.loads(state_path.read_text())

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("ensure-completed", result.stdout)
        self.assertIn("could not remove previous journey runner toolchain image", result.stderr)
        self.assertIn("id-stuck", [image["id"] for image in state["images"]])


if __name__ == "__main__":
    unittest.main()
