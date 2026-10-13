"""Static checks for calls to journey module-level functions."""
from __future__ import annotations

import ast
import unittest
from pathlib import Path


JOURNEY_ROOT = Path(__file__).resolve().parent


def _module_name(path: Path) -> str:
    relative = path.relative_to(JOURNEY_ROOT).with_suffix("")
    parts = list(relative.parts)
    if parts[-1] == "__init__":
        parts.pop()
    return ".".join(parts)


def _resolve_module(name: str, modules: dict[str, Path]) -> str | None:
    candidates = [name]
    if name.endswith(".__init__"):
        candidates.append(name.removesuffix(".__init__"))
    return next((candidate for candidate in candidates if candidate in modules), None)


def _call_targets(tree: ast.Module, module: str, modules: dict[str, Path], definitions: dict[str, dict[str, ast.FunctionDef]]):
    imports: dict[str, tuple[str, str | None]] = {}
    for node in tree.body:
        if isinstance(node, ast.ImportFrom):
            prefix = module.rpartition(".")[0]
            if node.level:
                base = prefix.split(".")[: len(prefix.split(".")) - node.level + 1]
                imported_module = ".".join([*base, node.module or ""])
            else:
                imported_module = node.module or ""
            resolved = _resolve_module(imported_module, modules)
            if resolved is None:
                continue
            for alias in node.names:
                if alias.name in definitions[resolved]:
                    imports[alias.asname or alias.name] = (resolved, alias.name)
        elif isinstance(node, ast.Import):
            for alias in node.names:
                resolved = _resolve_module(alias.name, modules)
                if resolved is not None:
                    imports[alias.asname or alias.name.split(".")[0]] = (resolved, None)

    own_names = definitions[module]
    for call in (node for node in ast.walk(tree) if isinstance(node, ast.Call)):
        if isinstance(call.func, ast.Name):
            function = own_names.get(call.func.id)
            if function is not None:
                yield call, module, function
            elif call.func.id in imports:
                target_module, target_name = imports[call.func.id]
                if target_name is not None:
                    yield call, target_module, definitions[target_module][target_name]
        elif (isinstance(call.func, ast.Attribute) and isinstance(call.func.value, ast.Name)
              and call.func.value.id in imports):
            target_module, imported_name = imports[call.func.value.id]
            if imported_name is None and call.func.attr in definitions[target_module]:
                yield call, target_module, definitions[target_module][call.func.attr]


def _arity_problem(call: ast.Call, function: ast.FunctionDef) -> str | None:
    args = function.args
    positional = [*args.posonlyargs, *args.args]
    defaults_start = len(positional) - len(args.defaults)
    required_positional = {arg.arg for arg in positional[:defaults_start]}
    accepted_keywords = {arg.arg for arg in args.args}
    accepted_keywords.update(arg.arg for arg in args.kwonlyargs)
    has_varargs = args.vararg is not None
    has_kwargs = args.kwarg is not None

    explicit_positional = [arg for arg in call.args if not isinstance(arg, ast.Starred)]
    has_starred = len(explicit_positional) != len(call.args)
    explicit_keywords = {keyword.arg for keyword in call.keywords if keyword.arg is not None}
    has_keyword_unpack = any(keyword.arg is None for keyword in call.keywords)
    if not has_varargs and len(explicit_positional) > len(positional):
        return f"too many positional arguments: {len(explicit_positional)} explicit for {len(positional)} positional parameters"
    positional_names = {arg.arg for arg in positional[: len(explicit_positional)]}
    duplicates = positional_names & explicit_keywords
    if duplicates:
        return f"multiple values for {sorted(duplicates)[0]!r}"
    invalid_keywords = explicit_keywords - accepted_keywords
    if not has_kwargs and invalid_keywords:
        return f"unexpected keyword {sorted(invalid_keywords)[0]!r}"

    supplied = positional_names | explicit_keywords
    if not has_starred and not has_keyword_unpack:
        missing = required_positional - supplied
        missing.update(arg.arg for arg, default in zip(args.kwonlyargs, args.kw_defaults)
                       if default is None and arg.arg not in explicit_keywords)
        if missing:
            return f"missing required argument {sorted(missing)[0]!r}"
    return None


class ModuleCallArityTest(unittest.TestCase):
    def test_calls_to_journey_module_functions_match_definitions(self):
        files = sorted(path for path in JOURNEY_ROOT.rglob("*.py") if "__pycache__" not in path.parts)
        modules = {_module_name(path): path for path in files}
        trees = {module: ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
                 for module, path in modules.items()}
        definitions = {
            module: {node.name: node for node in tree.body if isinstance(node, ast.FunctionDef)}
            for module, tree in trees.items()
        }

        problems = []
        for module, tree in trees.items():
            for call, target_module, function in _call_targets(tree, module, modules, definitions):
                problem = _arity_problem(call, function)
                if problem:
                    problems.append(
                        f"{modules[module]}:{call.lineno}: {function.name} defined in "
                        f"{modules[target_module]}:{function.lineno}: {problem}"
                    )
        self.assertEqual(problems, [], "\n".join(problems))
