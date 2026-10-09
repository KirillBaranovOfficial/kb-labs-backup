#!/usr/bin/env python3
"""Select journey entry points from release cases claimed by executable flows."""
from __future__ import annotations

import json
import os
import ast
import sys
from functools import lru_cache
from pathlib import Path

from hermetic_cli import run_hermetic_cli
from invocations import decode_ux_success_result, describe_cli_failure, invocation_context, read_ux_cli_result
from dispatch_registry import runners_for_source


@lru_cache(maxsize=None)
def _source_tree(source: str, root: Path) -> ast.Module:
    path = root / source
    try:
        return ast.parse(path.read_text(encoding="utf-8"), filename=source)
    except (OSError, SyntaxError) as error:
        raise ValueError(f"cannot read journey step declarations in {source}: {error}") from error


@lru_cache(maxsize=None)
def _case_declarations(source: str, root: Path) -> dict[int, dict]:
    """Index step declarations by their source lines once per claim module."""
    path = root / source
    if not path.is_file():
        raise ValueError(f"journey claim source does not exist: {source}")
    if path.suffix != ".py":
        return {}
    tree = _source_tree(source, root)
    calls = [node for node in ast.walk(tree)
             if isinstance(node, ast.Call)
             and any(keyword.arg == "cases" for keyword in node.keywords)]
    declarations: dict[int, dict] = {}
    for call in sorted(calls, key=lambda item: item.end_lineno - item.lineno):
        name = call.args[0].value if call.args and isinstance(call.args[0], ast.Constant) else None
        if not isinstance(name, str):
            continue
        keywords = {keyword.arg: keyword.value for keyword in call.keywords}
        prerequisites = keywords.get("requires")
        role = keywords.get("role")
        if prerequisites is None:
            required = []
        elif isinstance(prerequisites, (ast.Tuple, ast.List)):
            required = [item.value for item in prerequisites.elts if isinstance(item, ast.Constant)]
        else:
            raise ValueError(f"step {name!r} in {source} must declare prerequisites as a literal sequence")
        role_name = role.value if isinstance(role, ast.Constant) else None
        declaration = {"name": name, "requires": required, "role": role_name}
        for line in range(call.lineno, call.end_lineno + 1):
            declarations.setdefault(line, declaration)
    return declarations


def _step_declaration(source: str, line: int, root: Path) -> dict:
    """Read a step's static declaration from its actual claim callsite."""
    if (root / source).suffix != ".py":
        return {}
    declaration = _case_declarations(source, root).get(line)
    if declaration is None:
        raise ValueError(f"journey claim at {source}:{line} has no declared step call with cases")
    return declaration


def _source_step_declarations(sources: set[str], root: Path) -> dict[str, dict]:
    """Index statically named step calls from claim-registered modules."""
    expanded = _imported_journey_sources(sources, root)
    return _source_step_declarations_cached(tuple(sorted(expanded)), root)


def _imported_journey_sources(sources: set[str], root: Path) -> set[str]:
    """Follow local imports from the claim registry to find shared step declarations."""
    found = set(sources)
    pending = list(sources)
    while pending:
        source = pending.pop()
        path = root / source
        if not path.is_file() or path.suffix != ".py":
            continue
        tree = _source_tree(source, root)
        imports: set[str] = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imports.add(node.module)
        for module in imports:
            parts = module.split(".")
            candidates = [path.parent.joinpath(*parts).with_suffix(".py"),
                          root.joinpath("e2e/journey", *parts).with_suffix(".py")]
            for candidate in candidates:
                if candidate.is_file():
                    discovered = candidate.relative_to(root).as_posix()
                    if discovered not in found:
                        found.add(discovered)
                        pending.append(discovered)
                    break
    return found


@lru_cache(maxsize=None)
def _source_step_declarations_cached(sources: tuple[str, ...], root: Path) -> dict[str, dict]:
    declarations: dict[str, dict] = {}
    for source in sources:
        path = root / source
        if not path.is_file() or path.suffix != ".py":
            continue
        tree = _source_tree(source, root)
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or not node.args or not isinstance(node.args[0], ast.Constant):
                continue
            name = node.args[0].value
            if not isinstance(name, str):
                continue
            keywords = {keyword.arg: keyword.value for keyword in node.keywords}
            function = node.func.attr if isinstance(node.func, ast.Attribute) else (
                node.func.id if isinstance(node.func, ast.Name) else "")
            if not ("cases" in keywords or "requires" in keywords or "role" in keywords
                    or function in {"step", "flow"}):
                continue
            required_node = keywords.get("requires")
            if required_node is None:
                required = []
            elif isinstance(required_node, (ast.Tuple, ast.List)):
                required = [item.value for item in required_node.elts if isinstance(item, ast.Constant)]
            else:
                raise ValueError(f"step {name!r} in {source} must declare prerequisites as a literal sequence")
            role_node = keywords.get("role")
            role = role_node.value if isinstance(role_node, ast.Constant) else None
            declarations.setdefault(name, {"requires": [], "sources": set(), "role": role})
            declarations[name]["requires"] = sorted(set(declarations[name]["requires"]) | set(required))
            declarations[name]["sources"].add(source)
            if role is not None:
                declarations[name]["role"] = role
    return declarations


def resolve_selection(selected_steps: list[str], sources_by_step: dict[str, set[str]],
                      declarations_by_step: dict[str, list[dict]], declarations_by_name: dict[str, dict]) -> dict:
    """Resolve selected claims to their runner and transitive declared prerequisites."""
    selected = set(selected_steps)
    names: set[str] = set()
    sources: set[str] = set()
    pending: list[str] = []
    for step_id in selected:
        sources.update(sources_by_step.get(step_id, set()))
        for declaration in declarations_by_step.get(step_id, []):
            name = declaration.get("name") or step_id
            names.add(name)
            pending.extend(declaration.get("requires", []))
    prerequisites: set[str] = set()
    while pending:
        name = pending.pop()
        if name in names:
            continue
        names.add(name)
        prerequisites.add(name)
        declaration = declarations_by_name.get(name, {})
        sources.update(declaration.get("sources", set()))
        pending.extend(declaration.get("requires", []))
    runners = {selector for source in sources for mode in ("local", "secured")
               for selector in runners_for_source(source, mode)}
    return {"runners": sorted(runners), "steps": sorted(selected),
            "prerequisiteSteps": sorted(prerequisites), "stepSources": sorted(sources)}


def read_coverage_result(stdout: str, returncode: int) -> dict:
    payload = decode_ux_success_result(read_ux_cli_result(stdout, returncode))
    if not isinstance(payload, dict):
        raise ValueError("QA coverage result data is not an object")
    return payload


def selectors_from_coverage(results: list[dict]) -> list[str]:
    selected: set[str] = set()
    for coverage in results:
        mode = coverage.get("mode")
        if mode not in {"local", "secured"}:
            raise ValueError(f"unsupported QA coverage mode: {mode!r}")
        for case in coverage.get("cases", []):
            if case.get("status") != "active" or case.get("release") is not True:
                continue
            for step in case.get("steps", []):
                for claim in step.get("claims", []):
                    if claim.get("kind") not in {"journey", "e2e"}:
                        continue
                    selected.update(runners_for_source(str(claim.get("source", "")), mode))
    return sorted(selected)


def selection_from_coverage(results: list[dict], requested: str) -> dict:
    """Resolve requested cases/steps against the QA coverage claim registry."""
    registry: dict[str, set[str]] = {}
    sources_by_step: dict[str, set[str]] = {}
    declarations_by_step: dict[str, dict] = {}
    root = Path(__file__).resolve().parents[2]
    for coverage in results:
        for case in coverage.get("cases", []):
            if case.get("status") != "active":
                continue
            case_id = case.get("id")
            if not isinstance(case_id, str):
                continue
            for step in case.get("steps", []):
                step_id = step.get("stepId")
                journey_claimed = any(
                    claim.get("kind") in {"journey", "e2e"}
                    and str(claim.get("source", "")).startswith(("e2e/journey/", "e2e/studio/", "e2e/"))
                    for claim in step.get("claims", [])
                )
                if journey_claimed and isinstance(step_id, str):
                    registry.setdefault(case_id, set()).add(step_id)
                    for claim in step.get("claims", []):
                        if claim.get("kind") not in {"journey", "e2e"}:
                            continue
                        source = str(claim.get("source", ""))
                        if not source.startswith(("e2e/journey/", "e2e/studio/", "e2e/")):
                            continue
                        sources_by_step.setdefault(step_id, set()).add(source)
                        declaration = _step_declaration(source, int(claim.get("line", 0)), root)
                        if declaration:
                            declarations = declarations_by_step.setdefault(step_id, [])
                            if declaration not in declarations:
                                declarations.append(declaration)

    case_ids = set(registry)
    step_ids = set().union(*registry.values()) if registry else set()
    tokens = [item.strip() for item in requested.split(",") if item.strip()]
    if not tokens:
        raise ValueError("KB_JOURNEY_CASES must contain at least one case or step ID")
    selected: set[str] = set()
    for token in tokens:
        if token in case_ids:
            selected.update(registry[token])
        elif token in step_ids:
            selected.add(token)
        else:
            raise ValueError(
                f"unknown or unclaimed journey case/step ID: {token}. "
                "Check available IDs with `node ./cli/bin/dist/bin.js qa cases list --json`, then rerun the journey."
            )
    claim_sources = set().union(*sources_by_step.values()) if sources_by_step else set()
    declarations_by_name = _source_step_declarations(claim_sources, root)
    resolved = resolve_selection(sorted(selected), sources_by_step, declarations_by_step, declarations_by_name)
    return {"selection": sorted(set(tokens)), **resolved}


def main() -> int:
    results = []
    for mode in ("local", "secured"):
        command = ["pnpm", "-s", "kb", "qa", "cases", "coverage", "--level", "all", "--mode", mode, "--json"]
        with invocation_context(step_id="journey.case-selection"):
            result = run_hermetic_cli(command, cwd=Path.cwd(), env=os.environ, timeout=120)
        if result.returncode != 0:
            raise ValueError(describe_cli_failure(" ".join(command), result.stdout, result.stderr, result.returncode))
        results.append(read_coverage_result(result.stdout, result.returncode))
    requested = os.environ.get("KB_JOURNEY_CASES", "")
    if requested:
        print(json.dumps(selection_from_coverage(results, requested)))
    else:
        print(json.dumps(selectors_from_coverage(results)))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(2)
