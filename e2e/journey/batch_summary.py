#!/usr/bin/env python3
"""Assemble the shared journey artifact and render durations from its metadata."""
from __future__ import annotations

import json
from pathlib import Path
import shutil
import sys
from typing import Any


STAGES = ("reset", "build", "unit", "candidate", "local", "secured", "total")


def _read_metadata(directory: Path, mode: str) -> dict[str, Any]:
    path = directory / mode / "batch-metadata.json"
    value = json.loads(path.read_text(encoding="utf-8"))
    allowed_results = {"PASS", "FAIL", "PARTIAL", "INTERRUPTED"} if mode == "shared" else {"PASS", "FAIL"}
    if (not isinstance(value, dict)
            or not isinstance(value.get("stageDurationsSeconds"), dict)
            or value.get("result") not in allowed_results):
        raise ValueError(f"batch metadata is invalid: {path}")
    return value


def _read_scoreboard(directory: Path) -> dict[str, Any]:
    path = directory / "case-scoreboard.json"
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or not isinstance(value.get("partial"), bool):
        raise ValueError(f"scoreboard partial marker is invalid: {path}")
    selection = value.get("selection")
    if not isinstance(selection, list) or any(not isinstance(item, str) for item in selection):
        raise ValueError(f"scoreboard selection is invalid: {path}")
    release_gate = value.get("releaseGate")
    modes = release_gate.get("modes") if isinstance(release_gate, dict) else None
    if not isinstance(modes, list) or any(not isinstance(mode, str) for mode in modes):
        raise ValueError(f"scoreboard releaseGate is invalid: {path}")
    return value


def mode_is_gating(directory: Path, mode: str) -> bool:
    return mode in _read_scoreboard(directory)["releaseGate"]["modes"]


def _write_metadata(path: Path, value: dict[str, Any]) -> None:
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def write_run(path: Path, commit: str, baseline: str, build_base: str) -> None:
    path.write_text(json.dumps({"commit": commit, "baseline": baseline, "buildBase": build_base}) + "\n",
                    encoding="utf-8")


def verify_run_commit(path: Path, current_commit: str) -> None:
    value = json.loads(path.read_text(encoding="utf-8"))
    producer_commit = value.get("commit") if isinstance(value, dict) else None
    if not isinstance(producer_commit, str) or not producer_commit:
        raise ValueError(f"journey producer artifact has no commit identity: {path}")
    if producer_commit != current_commit:
        raise ValueError(
            f"candidate artifact is bound to {producer_commit}, checked out {current_commit}; "
            "next: rerun e2e/journey/batch.sh prepare for this commit"
        )


def write_stage_metadata(path: Path, mode: str, result: str, commit: str, baseline: str,
                         build_base: str, stage_values: list[str]) -> None:
    if mode not in {"shared", "local", "secured"} or result not in {"PASS", "FAIL", "INTERRUPTED"}:
        raise ValueError("batch metadata has an unsupported mode or result")
    if len(stage_values) != len(STAGES):
        raise ValueError("batch metadata requires one duration for every stage")
    value: dict[str, Any] = {
        "commit": commit,
        "baseline": baseline,
        "buildBase": build_base,
        "result": result,
        "stageDurationsSeconds": dict(zip(STAGES, (int(seconds) for seconds in stage_values))),
    }
    if mode != "shared":
        value["mode"] = mode
    _write_metadata(path, value)


def write_mode_metadata(source: Path, destination: Path, mode: str, result: str, elapsed: int) -> None:
    if mode not in {"local", "secured"} or result not in {"PASS", "FAIL"}:
        raise ValueError("mode metadata has an unsupported mode or result")
    value = json.loads(source.read_text(encoding="utf-8"))
    durations = dict(value["stageDurationsSeconds"])
    durations[mode] = elapsed
    durations["total"] = sum(durations[stage] for stage in STAGES if stage != "total")
    value["stageDurationsSeconds"] = durations
    value["mode"] = mode
    value["result"] = result
    _write_metadata(destination, value)


def assemble(output: Path, producer: Path, local: Path, secured: Path) -> int:
    shared_dir = producer / "shared"
    local_dir = local / "local"
    secured_dir = secured / "secured"
    output.mkdir(parents=True, exist_ok=True)
    for mode in ("shared", "local", "secured"):
        (output / mode).mkdir(parents=True, exist_ok=True)

    for source, destination, ignore in (
        (shared_dir, output / "shared", shutil.ignore_patterns("build-candidate.tar")),
        (local_dir, output / "local", None),
        (secured_dir, output / "secured", None),
    ):
        if source.is_dir() and source.resolve() != destination.resolve():
            shutil.copytree(source, destination, dirs_exist_ok=True, ignore=ignore)

    missing = [str(path) for path in (
        output / "shared" / "batch-metadata.json",
        output / "local" / "batch-metadata.json",
        output / "secured" / "batch-metadata.json",
    ) if not path.is_file()]
    if missing:
        message = "\n".join(["# Journey batch", "", "- Result: FAIL",
                               "- Missing batch metadata:", *[f"  - `{path}`" for path in missing], ""])
        (output / "shared" / "summary.md").write_text(message, encoding="utf-8")
        return 1

    producer_metadata = _read_metadata(output, "shared")
    local_metadata = _read_metadata(output, "local")
    secured_metadata = _read_metadata(output, "secured")
    scoreboards = {
        mode: _read_scoreboard(output / mode)
        for mode in ("local", "secured")
    }
    release_gates = {mode: board["releaseGate"]["modes"] for mode, board in scoreboards.items()}
    if release_gates["local"] != release_gates["secured"]:
        raise ValueError("local and secured scoreboards publish different releaseGate modes")
    partial = any(board["partial"] for board in scoreboards.values())
    local_durations = local_metadata["stageDurationsSeconds"]
    secured_durations = secured_metadata["stageDurationsSeconds"]
    shared_durations = producer_metadata["stageDurationsSeconds"]
    durations = {
        "reset": shared_durations["reset"],
        "build": shared_durations["build"],
        "unit": shared_durations["unit"],
        "candidate": shared_durations["candidate"],
        "local": local_durations["local"],
        "secured": secured_durations["secured"],
        "total": shared_durations["total"] + max(
            local_durations["local"], secured_durations["secured"]
        ),
    }
    mode_metadata = {"local": local_metadata, "secured": secured_metadata}
    gate_modes = release_gates["local"]
    mode_results = {
        mode: metadata.get("result")
        for mode, metadata in mode_metadata.items()
    }
    shared_passed = producer_metadata.get("result") == "PASS"
    gating_mode_failed = any(
        mode in gate_modes and result != "PASS"
        for mode, result in mode_results.items()
    )
    overall_result = ("PARTIAL" if partial else
                      "PASS" if shared_passed and not gating_mode_failed else "FAIL")
    run = json.loads((output / "shared" / "run.json").read_text(encoding="utf-8"))
    lines = ["# Journey batch", "", f"- Commit: `{run['commit']}`",
             f"- Baseline: `{run['baseline']}`", f"- Build base: `{run['buildBase']}`", "",
             "## Stage durations"]
    lines.extend(f"- {stage}: {durations[stage]} seconds" for stage in STAGES)
    lines.extend(["", "- Modes: local and secured", "- Reports and logs: one directory per mode", ""])
    if partial:
        selections = "; ".join(
            f"{mode}=[{', '.join(scoreboards[mode]['selection'])}]"
            for mode in ("local", "secured")
        )
        lines.extend([f"- Partial selection: {selections}",
                      "- Partial evidence is iteration-only and cannot produce a gate verdict.", ""])
    lines.extend(
        f"- {mode}: {mode_results[mode]} ({'gating' if mode in gate_modes else 'non-gating for Release 1'})"
        for mode in mode_metadata
    )
    lines.extend(["", f"- Result: {overall_result}", ""])
    (output / "shared" / "summary.md").write_text("\n".join(lines), encoding="utf-8")
    for mode, metadata in (("shared", producer_metadata), ("local", local_metadata),
                           ("secured", secured_metadata)):
        metadata["stageDurationsSeconds"] = durations
        metadata["result"] = overall_result if mode == "shared" else metadata.get("result")
        if mode != "shared":
            metadata["mode"] = mode
        _write_metadata(output / mode / "batch-metadata.json", metadata)
    return 0 if overall_result == "PASS" else 1


def main(argv: list[str]) -> int:
    if len(argv) == 4 and argv[1] == "verify-run":
        try:
            verify_run_commit(Path(argv[2]), argv[3])
            return 0
        except (OSError, ValueError, json.JSONDecodeError) as error:
            print(f"journey batch candidate identity check failed: {error}", file=sys.stderr)
            return 1
    if len(argv) == 6 and argv[1] == "run":
        try:
            write_run(Path(argv[2]), argv[3], argv[4], argv[5])
            return 0
        except OSError as error:
            print(f"journey run metadata could not be written: {error}; next: rerun e2e/journey/batch.sh prepare",
                  file=sys.stderr)
            return 1
    if len(argv) == 15 and argv[1] == "metadata":
        try:
            write_stage_metadata(Path(argv[2]), argv[3], argv[4], argv[5], argv[6], argv[7], argv[8:])
            return 0
        except (OSError, ValueError) as error:
            print(f"journey batch metadata could not be written: {error}; next: rerun e2e/journey/batch.sh prepare",
                  file=sys.stderr)
            return 1
    if len(argv) == 7 and argv[1] == "mode-metadata":
        try:
            write_mode_metadata(Path(argv[2]), Path(argv[3]), argv[4], argv[5], int(argv[6]))
            return 0
        except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
            print(f"journey mode metadata could not be written: {error}; next: rerun e2e/journey/batch.sh mode {argv[4]}",
                  file=sys.stderr)
            return 1
    if len(argv) == 4 and argv[1] == "mode-gating":
        try:
            return 0 if mode_is_gating(Path(argv[2]), argv[3]) else 1
        except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
            print(f"journey mode gate could not be read: {error}; next: rerun e2e/journey/batch.sh mode {argv[3]}",
                  file=sys.stderr)
            return 2
    if len(argv) != 5:
        print("usage: batch_summary.py <output> <producer-artifact> <local-artifact> <secured-artifact>",
              file=sys.stderr)
        return 2
    try:
        return assemble(*(Path(value) for value in argv[1:]))
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        print(f"journey batch summary failed: {error}; next: inspect downloaded journey artifacts",
              file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
