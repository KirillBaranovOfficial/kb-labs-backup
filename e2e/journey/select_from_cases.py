#!/usr/bin/env python3
"""Select journey entry points from release cases claimed by executable flows.

The static dependency rail requires every `.step`/`.flow` recorder attribute in
runner-reachable modules to be the direct callee of a call. Reflection such as
`getattr(journey, name)` and `eval` is outside this AST rail. Runtime
`Journey.step` checks every declared step or flow outcome in all run modes;
`FlowGraph.flow` records flow outcomes for those checks. Thus a dependency the
selector cannot see still produces a BLOCKED row for a selected claim, never a
PASS.
"""
from __future__ import annotations

import json
import os
import ast
import sys
from functools import lru_cache
from pathlib import Path

from hermetic_cli import run_hermetic_cli
from invocations import (decode_ux_success_result, describe_cli_failure, invocation_context,
                         print_report_text, read_ux_cli_result)
from dispatch_registry import RUNNERS, RUNNER_GROUPS, runners_for_source
from journey_dependencies import Dependency


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
        runners_node = keywords.get("runners")
        runners = []
        if runners_node is not None:
            if not isinstance(runners_node, (ast.Tuple, ast.List)):
                raise ValueError(f"runners= in {source}:{call.lineno} must contain literal runner selectors")
            for item in runners_node.elts:
                if not isinstance(item, ast.Constant) or not isinstance(item.value, str):
                    raise ValueError(f"runners= in {source}:{call.lineno} must contain literal runner selectors")
                if item.value not in RUNNERS:
                    raise ValueError(f"{source}:{call.lineno}: unknown registered journey runner {item.value!r}")
                runners.append(item.value)
        function = call.func.attr if isinstance(call.func, ast.Attribute) else (
            call.func.id if isinstance(call.func, ast.Name) else "")
        if prerequisites is None:
            required = []
        elif isinstance(prerequisites, (ast.Tuple, ast.List)):
            required = []
            for item in prerequisites.elts:
                if not isinstance(item, ast.Constant) or not isinstance(item.value, str):
                    raise ValueError(f"requires= in {source}:{call.lineno} must contain literal dependency references")
                try:
                    required.append(str(Dependency.parse(item.value)))
                except ValueError as error:
                    raise ValueError(f"{source}:{call.lineno}: {error}") from error
        else:
            raise ValueError(f"step {name!r} in {source} must declare prerequisites as a literal sequence")
        role_name = role.value if isinstance(role, ast.Constant) else None
        declaration = {"name": name, "kind": "flow" if function == "flow" else "step",
                       "requires": required, "role": role_name, "runners": runners}
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
    """Index declarations reachable from the executable journey entry points."""
    entrypoints = {source for runner in RUNNERS.values() for source in runner.journey_sources}
    expanded = _imported_journey_sources(sources | entrypoints, root)
    return _source_step_declarations_cached(tuple(sorted(expanded)), root)


def _imported_journey_sources(sources: set[str], root: Path) -> set[str]:
    """Follow local imports from the claim registry to find shared step declarations."""
    return set(_imported_journey_sources_cached(tuple(sorted(sources)), root))


def _validate_recorder_attribute_references(source: str, tree: ast.Module) -> None:
    """Require recorder attributes to be called directly so dependencies stay visible."""
    parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
    for node in ast.walk(tree):
        if not isinstance(node, ast.Attribute) or node.attr not in {"step", "flow"}:
            continue
        parent = parents.get(node)
        if not isinstance(parent, ast.Call) or parent.func is not node:
            raise ValueError(
                f"{source}:{node.lineno}: recorder must be called directly so its dependencies are statically visible"
            )


@lru_cache(maxsize=None)
def _imported_journey_sources_cached(sources: tuple[str, ...], root: Path) -> frozenset[str]:
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
    return frozenset(found)


@lru_cache(maxsize=None)
def _source_step_declarations_cached(sources: tuple[str, ...], root: Path) -> dict[str, dict]:
    declarations: dict[str, dict] = {}
    trees = {source: _source_tree(source, root) for source in sources
             if (root / source).is_file() and (root / source).suffix == ".py"}
    for source, tree in trees.items():
        _validate_recorder_attribute_references(source, tree)
    forwarding: list[tuple[str, ast.FunctionDef | ast.AsyncFunctionDef,
                           ast.Call, str, str]] = []

    def enclosing_function(tree: ast.Module, target: ast.AST):
        parents: dict[ast.AST, ast.AST] = {}
        for parent in ast.walk(tree):
            for child in ast.iter_child_nodes(parent):
                parents[child] = parent
        current = parents.get(target)
        while current is not None:
            if isinstance(current, (ast.FunctionDef, ast.AsyncFunctionDef)):
                return current
            current = parents.get(current)
        return None

    def call_name(call: ast.Call) -> str:
        function = call.func
        return function.id if isinstance(function, ast.Name) else (
            function.attr if isinstance(function, ast.Attribute) else "")

    def literal_references(value: ast.AST, source: str, line: int) -> list[str]:
        if not isinstance(value, ast.Tuple):
            raise ValueError(f"{source}:{line}: dependency declaration is not statically resolvable")
        required: list[str] = []
        for item in value.elts:
            if not isinstance(item, ast.Constant) or not isinstance(item.value, str):
                raise ValueError(f"{source}:{line}: dependency declaration is not statically resolvable")
            try:
                required.append(str(Dependency.parse(item.value)))
            except ValueError as error:
                raise ValueError(f"{source}:{line}: {error}") from error
        return required

    # Find functions that forward their own `requires` parameter to a journey
    # recorder. Their call sites, rather than the wrapper body, are declarations.
    for source, tree in trees.items():
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or call_name(node) not in {"step", "flow"}:
                continue
            required = next((keyword.value for keyword in node.keywords if keyword.arg == "requires"), None)
            if not isinstance(required, ast.Name):
                continue
            function = enclosing_function(tree, node)
            if function is None:
                continue
            parameters = {argument.arg for argument in
                          [*function.args.posonlyargs, *function.args.args, *function.args.kwonlyargs]}
            if required.id in parameters:
                rebound = any(isinstance(candidate, ast.Name) and candidate.id == required.id
                              and isinstance(candidate.ctx, ast.Store)
                              for candidate in ast.walk(function))
                if rebound:
                    raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
                forwarding.append((source, function, node, required.id, call_name(node)))

    forwarding_names = {function.name for _, function, _, _, _ in forwarding}
    for name in forwarding_names:
        definitions = [(source, node) for source, tree in trees.items() for node in ast.walk(tree)
                       if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == name]
        if len(definitions) != 1:
            source, _, call, _, _ = next(item for item in forwarding if item[1].name == name)
            raise ValueError(f"{source}:{call.lineno}: dependency declaration is not statically resolvable")
    forwarding_call_sites: dict[str, list[tuple[str, ast.Call]]] = {name: [] for name in forwarding_names}
    for source, tree in trees.items():
        for node in ast.walk(tree):
            if isinstance(node, ast.Call) and call_name(node) in forwarding_names:
                forwarding_call_sites[call_name(node)].append((source, node))

    def add_declaration(source: str, line: int, kind: str, name: str | None,
                        required: list[str], role: str | None, runners: list[str]) -> None:
        # Dynamic step names can carry checked edges, but are not selector
        # producers. Only a literal name or a statically resolved wrapper
        # call site can enter the producer graph.
        if name is None:
            key = f"@dynamic:{source}:{line}"
            declarations[key] = {"name": None, "kind": kind, "requires": required,
                                 "sources": {source}, "role": role, "runners": runners,
                                 "locations": [(source, line, required)],
                                 "producer": False}
            return
        key = str(Dependency(kind, name))
        declaration = declarations.setdefault(key, {"name": name, "kind": kind, "requires": [],
                                                    "sources": set(), "role": role, "runners": [],
                                                    "locations": []})
        declaration["requires"] = sorted(set(declaration["requires"]) | set(required))
        declaration["sources"].add(source)
        declaration["locations"].append((source, line, required))
        if role is not None:
            declaration["role"] = role
        declaration["runners"] = sorted(set(declaration.get("runners", [])) | set(runners))

    for source in sources:
        tree = trees.get(source)
        if tree is None:
            continue
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or call_name(node) not in {"step", "flow", "service_check"}:
                continue
            if any(keyword.arg is None for keyword in node.keywords):
                raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
            keywords = {keyword.arg: keyword.value for keyword in node.keywords}
            required_node = keywords.get("requires")
            if required_node is None:
                required: list[str] = []
            elif isinstance(required_node, ast.Tuple):
                required = literal_references(required_node, source, node.lineno)
            elif isinstance(required_node, ast.Name):
                function = enclosing_function(tree, node)
                parameters = ({argument.arg for argument in
                               [*function.args.posonlyargs, *function.args.args, *function.args.kwonlyargs]}
                              if function is not None else set())
                if required_node.id not in parameters:
                    raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
                wrapper_name = function.name
                matches = [(owner, definition, inner, parameter, kind)
                           for owner, definition, inner, parameter, kind in forwarding
                           if owner == source and definition is function and parameter == required_node.id]
                if not matches:
                    raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
                _, _, _, parameter, kind = matches[0]
                call_sites = forwarding_call_sites.get(wrapper_name, [])
                if not call_sites:
                    raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
                for call_source, call_site in call_sites:
                    if any(keyword.arg is None for keyword in call_site.keywords):
                        raise ValueError(f"{call_source}:{call_site.lineno}: dependency declaration is not statically resolvable")
                    call_keywords = {keyword.arg: keyword.value for keyword in call_site.keywords}
                    forwarded_value = call_keywords.get(parameter)
                    if forwarded_value is None:
                        kwonly_names = [argument.arg for argument in function.args.kwonlyargs]
                        if parameter in kwonly_names:
                            forwarded_value = function.args.kw_defaults[kwonly_names.index(parameter)]
                        else:
                            parameter_args = [*function.args.posonlyargs, *function.args.args]
                            parameter_index = next(index for index, argument in enumerate(parameter_args)
                                                   if argument.arg == parameter)
                            defaults_start = len(parameter_args) - len(function.args.defaults)
                            forwarded_value = (function.args.defaults[parameter_index - defaults_start]
                                               if parameter_index >= defaults_start else None)
                        if forwarded_value is None:
                            raise ValueError(f"{call_source}:{call_site.lineno}: dependency declaration is not statically resolvable")
                    call_required = literal_references(forwarded_value, call_source, call_site.lineno)
                    inner_name = node.args[0] if node.args else None
                    call_name_node = None
                    if isinstance(inner_name, ast.Name):
                        parameter_names = [argument.arg for argument in
                                           [*function.args.posonlyargs, *function.args.args]]
                        if inner_name.id in parameter_names:
                            parameter_index = parameter_names.index(inner_name.id)
                            call_name_node = call_keywords.get(inner_name.id)
                            if call_name_node is None and parameter_index < len(call_site.args):
                                call_name_node = call_site.args[parameter_index]
                    call_name_value = (call_name_node.value if isinstance(call_name_node, ast.Constant)
                                       and isinstance(call_name_node.value, str) else None)
                    if call_name_value is None:
                        raise ValueError(f"{call_source}:{call_site.lineno}: dependency declaration is not statically resolvable")
                    add_declaration(call_source, call_site.lineno, kind, call_name_value,
                                    call_required, None, [])
                continue
            else:
                raise ValueError(f"{source}:{node.lineno}: dependency declaration is not statically resolvable")
            name_node = node.args[0] if node.args else keywords.get("name")
            name = name_node.value if isinstance(name_node, ast.Constant) and isinstance(name_node.value, str) else None
            kind = "flow" if call_name(node) == "flow" else "step"
            role_node = keywords.get("role")
            role = role_node.value if isinstance(role_node, ast.Constant) else None
            runners_node = keywords.get("runners")
            runners = [item.value for item in runners_node.elts
                       if isinstance(item, ast.Constant) and isinstance(item.value, str)] \
                if isinstance(runners_node, (ast.Tuple, ast.List)) else []
            add_declaration(source, node.lineno, kind, name, required, role, runners)
    return declarations


def validate_dependency_declarations(sources: set[str], root: Path) -> dict[str, dict]:
    """Reject unresolved edges against the declarations reachable by either journey runner."""
    return _validate_dependency_declarations_cached(tuple(sorted(sources)), root)


@lru_cache(maxsize=None)
def _validate_dependency_declarations_cached(sources: tuple[str, ...], root: Path) -> dict[str, dict]:
    entrypoints = sorted({source for runner in RUNNERS.values() for source in runner.journey_sources})
    reachable = {entry: _imported_journey_sources({entry}, root) for entry in entrypoints}
    declarations = _source_step_declarations(set(sources), root)
    graphs = {entry: _source_step_declarations_cached(tuple(sorted(graph)), root)
              for entry, graph in reachable.items()}
    validate_dependency_edges(declarations, reachable, graphs)
    reachable_sources = set().union(*reachable.values()) if reachable else set()
    validate_registered_state_dependencies(reachable_sources, root)
    validate_runner_conditions(reachable_sources, root)
    return declarations


def validate_registered_state_dependencies(sources: set[str], root: Path) -> None:
    """Require an edge when one registered callback reads mutable state another writes."""
    _validate_registered_state_dependencies_cached(tuple(sorted(sources)), root)


def validate_runner_conditions(sources: set[str], root: Path) -> None:
    """Require variant ownership on registrations nested under runner-context branches."""
    dimensions = {"FRONT", "FRONT_DOOR", "SCENARIO", "VARIANT", "mode",
                  "local_service_journey", "secured_service_journey", "BASE_ENV"}
    for source in sorted(sources):
        if not source.endswith(".py"):
            continue
        tree = _source_tree(source, root)
        parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
        for call in ast.walk(tree):
            if not isinstance(call, ast.Call):
                continue
            keywords = {keyword.arg: keyword.value for keyword in call.keywords}
            cases = keywords.get("cases")
            if not isinstance(cases, (ast.List, ast.Tuple)) or not any(
                    isinstance(item, ast.Constant) and isinstance(item.value, str) for item in cases.elts):
                continue
            if keywords.get("runners") is not None:
                continue
            current = parents.get(call)
            while current is not None:
                if isinstance(current, ast.If):
                    referenced = {node.id for node in ast.walk(current.test) if isinstance(node, ast.Name)}
                    if referenced.intersection(dimensions):
                        raise ValueError(
                            f"{source}:{call.lineno}: claim registration is conditional on runner context; "
                            "declare its executable runners= variants"
                        )
                current = parents.get(current)


@lru_cache(maxsize=None)
def _validate_registered_state_dependencies_cached(sources: tuple[str, ...], root: Path) -> None:
    registrations: list[tuple[str, str, int, str, set[str], set[str], set[str]]] = []
    callback_accesses: dict[ast.AST, tuple[set[str], set[str]]] = {}
    for source in sources:
        if not source.endswith(".py"):
            continue
        tree = _source_tree(source, root)
        parents = {child: parent for parent in ast.walk(tree) for child in ast.iter_child_nodes(parent)}
        functions = {node.name: node for node in ast.walk(tree)
                     if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))}
        for call in ast.walk(tree):
            if not isinstance(call, ast.Call):
                continue
            function_name = call.func.attr if isinstance(call.func, ast.Attribute) else (
                call.func.id if isinstance(call.func, ast.Name) else "")
            if function_name not in {"step", "flow", "service_check"}:
                continue
            callback_index = 3 if function_name == "flow" else 2
            if len(call.args) <= callback_index:
                continue
            callback = call.args[callback_index]
            if isinstance(callback, ast.Name):
                callback = functions.get(callback.id)
            if not isinstance(callback, (ast.Lambda, ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            name_node = call.args[0] if call.args else None
            name = name_node.value if isinstance(name_node, ast.Constant) and isinstance(name_node.value, str) else None
            if name is None:
                continue
            scope_node = parents.get(call)
            while scope_node is not None and not isinstance(scope_node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                scope_node = parents.get(scope_node)
            scope = scope_node.name if scope_node is not None else "<module>"
            keywords = {keyword.arg: keyword.value for keyword in call.keywords}
            requires_node = keywords.get("requires")
            requires = {item.value for item in requires_node.elts
                        if isinstance(item, ast.Constant) and isinstance(item.value, str)} \
                if isinstance(requires_node, (ast.Tuple, ast.List)) else set()
            if callback not in callback_accesses:
                reads: set[str] = set()
                writes: set[str] = set()
                callback_locals = {node.id for node in ast.walk(callback)
                                   if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Store)}
                for node in ast.walk(callback):
                    if isinstance(node, ast.Subscript) and isinstance(node.value, ast.Name):
                        key = (node.slice.value if isinstance(node.slice, ast.Constant)
                               and isinstance(node.slice.value, str) else "*")
                        target = writes if isinstance(node.ctx, ast.Store) else reads if isinstance(node.ctx, ast.Load) else None
                        if target is not None:
                            target.add(f"{node.value.id}[{key}]")
                    elif (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
                          and isinstance(node.func.value, ast.Name)):
                        state_name = node.func.value.id
                        if node.func.attr in {"get", "pop", "setdefault"}:
                            key_node = node.args[0] if node.args else None
                            key = (key_node.value if isinstance(key_node, ast.Constant)
                                   and isinstance(key_node.value, str) else "*")
                            reads.add(f"{state_name}[{key}]")
                            if node.func.attr in {"pop", "setdefault"}:
                                writes.add(f"{state_name}[{key}]")
                        elif node.func.attr == "update":
                            writes.update(f"{state_name}[{keyword.arg}]"
                                          for keyword in node.keywords if keyword.arg is not None)
                            if node.args:
                                writes.add(f"{state_name}[*]")
                reads = {state for state in reads if state.split("[", 1)[0] not in callback_locals}
                writes = {state for state in writes if state.split("[", 1)[0] not in callback_locals}
                callback_accesses[callback] = reads, writes
            reads, writes = callback_accesses[callback]
            registrations.append((source, scope, call.lineno, name, requires, reads, writes))

    writers: dict[tuple[str, str, str], list[tuple[str, str]]] = {}
    for source, scope, _, name, _, _, writes in registrations:
        for state_name in writes:
            writers.setdefault((source, scope, state_name), []).append((source, name))
    for source, scope, line, name, requires, reads, _ in registrations:
        for state_name in sorted(reads):
            variable, key = state_name[:-1].split("[", 1)
            possible_writes = [write for (write_source, write_scope, write_name), values in writers.items()
                               if write_source == source and write_scope == scope
                               and write_name.split("[", 1)[0] == variable
                               and (key == "*" or write_name.endswith("[*]")
                                    or write_name.endswith(f"[{key}]"))
                               for write in values]
            for producer_source, producer_name in possible_writes:
                if producer_source == source and producer_name == name:
                    continue
                dependency = f"step:{producer_name}"
                if dependency not in requires:
                    raise ValueError(
                        f"{source}:{line}: registered step {name!r} reads shared state {state_name!r} "
                        f"written by {producer_name!r} without requires=(\"{dependency}\",)"
                    )


def validate_dependency_edges(declarations: dict[str, dict], reachable: dict[str, set[str]],
                              graphs: dict[str, dict[str, dict]]) -> None:
    for dependency_key, declaration in declarations.items():
        for source, line, references in declaration["locations"]:
            owners = [entry for entry, source_set in reachable.items()
                      if source in source_set and (declaration.get("producer") is False
                                                   or dependency_key in graphs[entry])]
            for reference in references:
                key = str(Dependency.parse(reference))
                if not owners or not any(key in graphs[entry] for entry in owners):
                    raise ValueError(f"{source}:{line}: {reference!r} does not resolve to a declared, reachable step or flow")


def resolve_selection(selected_steps: list[str], sources_by_step: dict[str, set[str]],
                      declarations_by_step: dict[str, list[dict]], declarations_by_name: dict[str, dict],
                      owners_by_step: dict[str, dict[str, set[str]]], only: str = "",
                      ineligible_by_step: dict[str, set[str]] | None = None) -> dict:
    """Resolve selected claims to their runner and transitive declared prerequisites."""
    selected = set(selected_steps)
    names: set[str] = set()
    sources: set[str] = set()
    pending: list[str] = []
    for step_id in selected:
        sources.update(sources_by_step.get(step_id, set()))
        for declaration in declarations_by_step.get(step_id, []):
            name = declaration.get("name") or step_id
            names.add(str(Dependency(declaration.get("kind", "step"), name)))
            pending.extend(declaration.get("requires", []))
    prerequisites: set[str] = set()
    while pending:
        name = pending.pop()
        dependency = Dependency.parse(name)
        key = str(dependency)
        if key in names:
            continue
        names.add(key)
        prerequisites.add(key)
        declaration = declarations_by_name.get(key, {})
        sources.update(declaration.get("sources", set()))
        pending.extend(declaration.get("requires", []))
    owners: dict[str, set[str]] = {}
    for step_id in selected:
        for runner, claims in owners_by_step.get(step_id, {}).items():
            owners.setdefault(runner, set()).update(claims)
    ineligible_by_step = ineligible_by_step or {}
    unowned = sorted(step_id for step_id in selected
                     if not any(step_id in claims for claims in owners.values()))
    if unowned:
        routes = []
        for step_id in unowned:
            candidates = sorted(ineligible_by_step.get(step_id, set()))
            if candidates:
                routes.extend(f"{step_id} is registered on {selector} ({'secured' if RUNNERS[selector].variant.endswith('secured') else 'local'} mode): "
                              f"KB_JOURNEY_CASES={step_id} KB_JOURNEY_ONLY={selector} e2e/journey/run.sh --no-build"
                              for selector in candidates)
            else:
                routes.append(f"{step_id} has no eligible runner variant; register the claim on its executable step")
        raise ValueError("selected claims have no executable registration for the requested mode: "
                         + ", ".join(unowned) + ". Available registrations: " + "; ".join(routes))
    requested_selectors = set(filter(None, only.split(",")))
    requested_runners = {
        runner
        for selector in requested_selectors
        for runner in RUNNER_GROUPS.get(selector, (selector,))
    }
    unknown_runners = requested_runners - RUNNERS.keys()
    if unknown_runners:
        unknown_selectors = requested_selectors - RUNNERS.keys() - RUNNER_GROUPS.keys()
        raise ValueError(f"unknown KB_JOURNEY_ONLY runner(s): {', '.join(sorted(unknown_selectors))}")
    runners = set(owners)
    if requested_runners:
        runners.intersection_update(requested_runners)
        covered = {claim for runner in runners for claim in owners[runner]}
        uncovered = set(selected) - covered
        if uncovered:
            routes = "; ".join(
                f"{runner} owns {', '.join(sorted(uncovered.intersection(claims)))}: "
                f"KB_JOURNEY_CASES={','.join(sorted(uncovered.intersection(claims)))} "
                f"KB_JOURNEY_ONLY={runner} e2e/journey/run.sh --no-build"
                for runner, claims in sorted(owners.items())
                if runner not in runners and uncovered.intersection(claims)
            )
            raise ValueError(
                "KB_JOURNEY_ONLY leaves selected claims without a runner: "
                + ", ".join(sorted(uncovered)) + ". Run the owning selectors: " + routes
            )
    step_prerequisites = sorted(Dependency.parse(item).name for item in prerequisites
                                if Dependency.parse(item).kind == "step")
    flow_prerequisites = sorted(Dependency.parse(item).name for item in prerequisites
                                if Dependency.parse(item).kind == "flow")
    return {"runners": sorted(runners), "owners": {key: sorted(value) for key, value in sorted(owners.items())},
            "steps": sorted(selected),
            "prerequisiteSteps": step_prerequisites, "prerequisiteFlows": flow_prerequisites,
            "stepSources": sorted(sources)}


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
    owners_by_step: dict[str, dict[str, set[str]]] = {}
    ineligible_by_step: dict[str, set[str]] = {}
    unclaimed_by_case: dict[str, set[str]] = {}
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
                if isinstance(step_id, str) and not journey_claimed:
                    unclaimed_by_case.setdefault(case_id, set()).add(step_id)
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
                        mode = str(coverage.get("mode", ""))
                        selectors = declaration.get("runners") or runners_for_source(source, mode)
                        if declaration.get("runners"):
                            for selector in selectors:
                                if RUNNERS[selector].variant.endswith("secured") != (mode == "secured"):
                                    ineligible_by_step.setdefault(step_id, set()).add(selector)
                            selectors = [selector for selector in selectors
                                         if RUNNERS[selector].variant.endswith("secured") == (mode == "secured")]
                        for selector in selectors:
                            owners_by_step.setdefault(step_id, {}).setdefault(selector, set()).add(step_id)
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
            unclaimed = sorted(unclaimed_by_case.get(token, set()))
            if unclaimed:
                claimed = ",".join(sorted(registry[token]))
                raise ValueError(
                    f"case {token} includes unclaimed steps that no journey runner can prove: "
                    + ", ".join(unclaimed)
                    + f". To resolve its claimed rows only, run `KB_JOURNEY_CASES={claimed} e2e/journey/run.sh --resolve-only`; "
                    + "to prove the full case, add a journey claim for each unclaimed step."
                )
            selected.update(registry[token])
        elif token in step_ids:
            selected.add(token)
        else:
            raise ValueError(
                f"unknown or unclaimed journey case/step ID: {token}. "
                "Check available IDs with `node ./cli/bin/dist/bin.js qa cases list --json`, then rerun the journey."
            )
    claim_sources = set().union(*sources_by_step.values()) if sources_by_step else set()
    declarations_by_name = validate_dependency_declarations(claim_sources, root)
    resolved = resolve_selection(sorted(selected), sources_by_step, declarations_by_step, declarations_by_name,
                                 owners_by_step, os.environ.get("KB_JOURNEY_ONLY", ""), ineligible_by_step)
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
        print_report_text(json.dumps(selection_from_coverage(results, requested)), flush=True)
    else:
        print_report_text(json.dumps(selectors_from_coverage(results)), flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as error:
        print_report_text(str(error), stream=sys.stderr, flush=True)
        raise SystemExit(2)
    except Exception as error:
        print_report_text(f"Journey case selection failed: {error.__class__.__name__}: {error}",
                          stream=sys.stderr, flush=True)
        raise SystemExit(1)
