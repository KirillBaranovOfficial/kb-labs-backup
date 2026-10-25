"""Behavioural regression for the runner-record consumer contract (S-237.66).

The orchestration event daemon must read run records and leftovers only
through kb-runner ps --json / ps --leftovers --json (case S-237.66), which is
what lets S-237.15's daemon publish an outcome for every terminal record.
This test runs the real daemon's own reader functions — loaded from the
shipped orchestration/event-daemon.sh, not a copy of them — against a state
directory populated by real kb-runner runs, and observes that the bad outcome
is absent: a terminal record the reader cannot see, or a dirty outcome
without its leftover identity. Against the pre-contract daemon, whose
run_record read runs/<run-id>.json directly, the finished record (stored
under runs/finished/<run-id>.json since x155) is invisible and this test
fails.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
DAEMON_PATH = REPO_ROOT / "orchestration" / "event-daemon.sh"
FIXTURE_TAGS = ("x164-contract", "x164-contract-dirty")


def build_runner(destination: Path) -> Path:
    runner = destination / "bin" / "kb-runner"
    runner.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(
        ["go", "build", "-o", str(runner), "."],
        cwd=REPO_ROOT / "tools" / "kb-runner",
        env=dict(os.environ, GOTOOLCHAIN="local", GOCACHE=str(destination / "go-build")),
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        raise AssertionError(f"could not build kb-runner: {result.stderr.strip() or result.stdout.strip()}")
    return runner


def ps_json(runner: Path, state: Path) -> list[dict]:
    output = subprocess.run(
        [str(runner), "ps", "--state", str(state), "--json"],
        text=True, capture_output=True, check=False,
    )
    if output.returncode != 0:
        raise AssertionError(f"kb-runner ps --json failed: {output.stderr.strip()}")
    return json.loads(output.stdout)


def live_identity(runner: Path, pid: int) -> str | None:
    """The process's current identity, or None when it is not live."""
    result = subprocess.run(
        [str(runner), "proc", "identity", str(pid)],
        text=True, capture_output=True, check=False,
    )
    if result.returncode == 0:
        return result.stdout.strip()
    if result.returncode == 3:
        return None
    raise AssertionError(f"kb-runner proc identity {pid} failed: {result.stderr.strip()}")


def wait_for(predicate, timeout: float, message: str):
    deadline = time.time() + timeout
    while time.time() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.1)
    raise AssertionError(message)


def owned_processes_from_records(records: list[dict]) -> list[tuple[int, str]]:
    """Every (pid, start identity) the fixture's runs own in these registry
    records: keepers and their recorded tree members."""
    owned: list[tuple[int, str]] = []
    for record in records:
        if record.get("tag") in FIXTURE_TAGS:
            owned.append((record["pid"], record["started_at"]))
            for member in record.get("tree") or []:
                owned.append((member["pid"], member["identity"]))
    return owned


def terminate_owned_processes(runner: Path, owned: list[tuple[int, str]],
                              identity_reader=live_identity, signaller=os.kill,
                              gone_timeout: float = 10.0) -> list[str]:
    """Identity-checked SIGKILL for every still-live owned process, then
    verify none of them resolves to its recorded identity any more. Every
    per-process failure is collected instead of raised, so one unreadable
    identity never stops the remaining cleanup. Returns human-readable
    problems; surfacing them is the caller's decision."""
    problems: list[str] = []
    for pid, identity in {pid: identity for pid, identity in owned}.items():
        try:
            if identity_reader(pid) != identity:
                continue
            signaller(pid, 9)
        except Exception as error:
            problems.append(f"stop process {pid}: {error}")
            continue
        try:
            if not wait_for_identity_gone(runner, pid, identity, timeout=gone_timeout,
                                          identity_reader=identity_reader):
                problems.append(f"process {pid} (identity {identity}) survived SIGKILL")
        except Exception as error:
            problems.append(f"verify process {pid} gone: {error}")
    return problems


def wait_for_identity_gone(runner: Path, pid: int, identity: str, timeout: float = 10.0,
                           identity_reader=live_identity) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if identity_reader(pid) != identity:
            return True
        time.sleep(0.1)
    return False


def cleanup_fixture(runner: Path, state: Path, child: subprocess.Popen,
                    owned_processes: list[tuple[int, str]], body_error: BaseException | None,
                    identity_reader=live_identity, signaller=os.kill, registry_reader=None) -> None:
    """Best-effort, failure-collecting fixture cleanup. Every operation is
    guarded at its own boundary, so one failure never stops the remaining
    cleanup and no cleanup problem can replace the test body's own
    exception: when the body failed, all problems are attached to it as
    notes; when the body succeeded, they surface as the fixture's own
    failure."""
    if registry_reader is None:
        registry_reader = lambda: ps_json(runner, state)
    problems: list[str] = []

    def guard(step: str, operation) -> None:
        try:
            operation()
        except Exception as error:
            problems.append(f"{step}: {error}")

    def stop_launcher() -> None:
        child.kill()
        child.wait(timeout=5)

    def wait_for_owner_death_teardown() -> None:
        deadline = time.time() + 10
        while time.time() < deadline:
            live = [record for record in registry_reader() if record.get("tag") in FIXTURE_TAGS]
            if not live or live[0].get("status") in ("dead", "lost", "dirty"):
                time.sleep(1.0)
                return
            time.sleep(0.1)

    def scan_owned_processes() -> list[tuple[int, str]]:
        return owned_processes + owned_processes_from_records(registry_reader())

    guard("stop fixture launcher", stop_launcher)
    guard("wait for owner-death teardown", wait_for_owner_death_teardown)
    owned = owned_processes
    try:
        owned = scan_owned_processes()
    except Exception as error:
        problems.append(f"scan registry for owned processes: {error}")
    guard("terminate owned processes",
          lambda: problems.extend(terminate_owned_processes(runner, owned, identity_reader, signaller)))

    if problems:
        message = "fixture cleanup problems: " + "; ".join(problems)
        if body_error is not None:
            body_error.add_note(message)
        else:
            raise AssertionError(message)


def run_clean_success(runner: Path, state: Path, stdout: Path, stderr: Path) -> None:
    subprocess.run(
        [str(runner), "run", "--tag", "x164-contract", "--state", str(state),
         "--stall", "1h", "--stdout", str(stdout), "--stderr", str(stderr),
         "--", "/bin/sh", "-c", 'exit "$1"', "sh", "0"],
        check=True, stdout=subprocess.DEVNULL,
    )


def run_lost_resource_dirty(runner: Path, state: Path, stdout: Path, stderr: Path) -> str:
    """Produce a dirty terminal record by replacing a run-owned path and
    killing the keeper. Cleans up every fixture-owned process on both the
    success and the failure path without masking the test's own failure."""
    owned = state / "owned-path"
    child = subprocess.Popen(
        [str(runner), "run", "--tag", "x164-contract-dirty", "--state", str(state),
         "--stall", "1h", "--stdout", str(stdout), "--stderr", str(stderr),
         "--", "/bin/sh", "-c", '"$1" own path --create file "$2"; exec /bin/sleep 30',
         "sh", str(runner), str(owned)],
    )
    body_error: BaseException | None = None
    owned_processes: list[tuple[int, str]] = []
    try:
        running = wait_for(
            lambda: [r for r in ps_json(runner, state)
                     if r.get("tag") == "x164-contract-dirty" and r.get("status") == "running"]
            and owned.exists(),
            20, "dirty fixture never became running with its owned path",
        )
        owned_processes.append((running[0]["pid"], running[0]["started_at"]))
        owned_processes += [(member["pid"], member["identity"]) for member in running[0].get("tree") or []]
        os.remove(owned)
        owned.write_text("replacement", encoding="utf-8")
        os.kill(running[0]["pid"], 9)
        wait_for(
            lambda: next((r for r in ps_json(runner, state)
                          if r.get("tag") == "x164-contract-dirty" and r.get("status") in ("dirty", "lost")), None),
            20, "keeper loss never became a terminal dirty/lost record",
        )
        return str(owned)
    except BaseException as error:
        body_error = error
        raise
    finally:
        cleanup_fixture(runner, state, child, owned_processes, body_error)


def load_daemon_reader_functions(root: Path):
    if not DAEMON_PATH.is_file():
        raise AssertionError(f"required orchestration artifact is missing: {DAEMON_PATH}")
    lines = DAEMON_PATH.read_text(encoding="utf-8").splitlines()
    start = next(index for index, line in enumerate(lines) if "<<'PY'" in line)
    end = len(lines) - 1 - next(
        index for index, line in enumerate(reversed(lines)) if line.strip() == "PY")
    body = "\n".join(lines[start + 1:end])
    body = body[:body.index("outcome_state = read_outcome_state()")]
    namespace: dict = {"__name__": "event-daemon-contract"}
    sys.argv = ["event-daemon-contract", str(root)]
    exec(compile(body, "event-daemon.py", "exec"), namespace)
    return namespace


class RunnerRecordContractTests(unittest.TestCase):
    def test_daemon_reader_functions_see_finished_and_dirty_records_through_the_commands(self):
        with tempfile.TemporaryDirectory(prefix="runner-record-contract-") as temporary:
            root = Path(temporary)
            state = root / "logs" / "runner"
            runner = build_runner(root)
            run_clean_success(runner, state, root / "out.log", root / "err.log")
            owned = run_lost_resource_dirty(runner, state, root / "out2.log", root / "err2.log")

            namespace = load_daemon_reader_functions(root)
            records = namespace["run_records"]()
            finished = [record for record in records if record.get("tag") == "x164-contract"]
            self.assertTrue(finished, f"daemon reader saw no finished record: {records}")
            self.assertEqual(finished[0]["status"], "dead", records)
            self.assertEqual(finished[0]["exit_code"], 0, records)
            lookup = namespace["run_record"]({"run_id": finished[0]["run_id"]})
            self.assertEqual(lookup.get("run_id"), finished[0]["run_id"], lookup)
            self.assertGreater(lookup.get("stall_ms", 0), 0, lookup)
            leftovers = namespace["leftovers_by_run"]()
            line = namespace["outcome_line"](finished[0], leftovers.get(finished[0]["run_id"], set()))
            self.assertEqual(line, "OUTCOME x164-contract: SUCCESS", line)

            dirty = [record for record in records if record.get("tag") == "x164-contract-dirty"]
            self.assertTrue(dirty, f"daemon reader saw no dirty record: {records}")
            self.assertIn(dirty[0]["status"], ("dirty", "lost"), records)
            ids = leftovers.get(dirty[0]["run_id"], set())
            self.assertIn(owned, ids, (ids, dirty[0]))
            dirty_line = namespace["outcome_line"](dirty[0], ids)
            self.assertTrue(
                dirty_line.startswith("OUTCOME x164-contract-dirty: FAILURE DIRTY leftovers=")
                and 'kb-runner ps --leftovers --state "$STATE"' in dirty_line,
                dirty_line)


class FixtureCleanupTests(unittest.TestCase):
    """Injected regressions for the cleanup contract: every cleanup-stage
    failure is collected without stopping the remaining cleanup, and the
    test body's own exception stays primary."""

    def failing_after_first_identity(self, failing_pid: int):
        attempted: list[int] = []

        def identity_reader(pid: int):
            attempted.append(pid)
            if pid == failing_pid:
                raise AssertionError("identity read failed")
            return None

        return attempted, identity_reader

    def test_cleanup_stage_failure_never_masks_the_body_error_and_cleanup_continues(self):
        attempted, identity_reader = self.failing_after_first_identity(111)

        class ExplodingChild:
            def kill(self):
                raise RuntimeError("launcher kill failed")

            def wait(self, timeout=None):
                raise RuntimeError("launcher wait failed")

        def failing_registry() -> list[dict]:
            raise AssertionError("registry read failed")

        body_error = RuntimeError("original body failure")
        # The fixture's exact try/except/finally shape: the body raises, the
        # cleanup runs in the finally, and the original exception must stay
        # primary while every cleanup-stage failure is only attached to it.
        try:
            try:
                raise body_error
            except BaseException as error:
                captured_error = error
                raise
            finally:
                cleanup_fixture(
                    runner=None, state=None, child=ExplodingChild(),
                    owned_processes=[(111, "i1"), (222, "i2")],
                    body_error=captured_error,
                    identity_reader=identity_reader,
                    registry_reader=failing_registry,
                )
        except RuntimeError as caught:
            self.assertIs(caught, body_error)
            joined = "\n".join(getattr(body_error, "__notes__", []))
            self.assertIn("launcher kill failed", joined)
            self.assertIn("registry read failed", joined)
            self.assertIn("identity read failed", joined)
        else:
            self.fail("the original body exception did not propagate")
        # The second owned process was still attempted after the first
        # cleanup failure.
        self.assertEqual(attempted, [111, 222])

    def test_cleanup_problems_surface_as_a_failure_when_the_body_succeeded(self):
        attempted, identity_reader = self.failing_after_first_identity(111)

        class ExplodingChild:
            def kill(self):
                raise RuntimeError("launcher kill failed")

            def wait(self, timeout=None):
                raise RuntimeError("launcher wait failed")

        with self.assertRaises(AssertionError) as caught:
            cleanup_fixture(
                runner=None, state=None, child=ExplodingChild(),
                owned_processes=[(111, "i1"), (222, "i2")],
                body_error=None,
                identity_reader=identity_reader,
                registry_reader=lambda: [],
            )
        self.assertIn("launcher kill failed", str(caught.exception))
        self.assertIn("identity read failed", str(caught.exception))
        self.assertEqual(attempted, [111, 222])

    def test_a_process_that_survives_sigkill_is_reported_and_does_not_stop_the_rest(self):
        signalled: list[int] = []

        def survivor_identity(pid: int):
            return "still-here" if pid == 333 else None

        def record_signaller(pid, signal):
            signalled.append(pid)

        problems = terminate_owned_processes(
            None, [(333, "still-here"), (444, "gone")],
            identity_reader=survivor_identity, signaller=record_signaller,
            gone_timeout=0.3,
        )
        self.assertEqual(signalled, [333])
        self.assertEqual(len(problems), 1)
        self.assertIn("333", problems[0])
        self.assertIn("survived SIGKILL", problems[0])


if __name__ == "__main__":
    unittest.main()
