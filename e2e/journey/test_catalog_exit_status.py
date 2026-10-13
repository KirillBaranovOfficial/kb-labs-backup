from __future__ import annotations

import ast
import json
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
JOURNEY = ROOT / "e2e" / "journey"
CATALOG = ROOT / "core" / "platform" / "src" / "error-envelope" / "errors.catalog.json"
CATALOG_ENTRIES: dict[str, dict] = {}
UNKNOWN = object()


class Alternatives:
    def __init__(self, values):
        self.values = values


def expected_exit(code: str, catalog: dict[str, dict]) -> int:
    entry = catalog.get(code)
    if entry is None:
        raise AssertionError(f"error code {code} is absent from the shared catalog")
    return 2 if entry["processExitClass"] == "input-refusal" else 1


def catalogued_exit(code: str) -> int:
    if not CATALOG_ENTRIES:
        raw = json.loads(CATALOG.read_text())
        CATALOG_ENTRIES.update({entry["code"]: entry for entry in raw["codes"]})
    return expected_exit(code, CATALOG_ENTRIES)


def error_codes(value):
    if isinstance(value, Alternatives):
        for alternative in value.values:
            yield from error_codes(alternative)
        return
    if isinstance(value, dict):
        error = value.get("error")
        if isinstance(error, dict):
            code = error.get("code")
            if isinstance(code, str) and code in CATALOG_ENTRIES:
                yield code
            yield from error_codes(error)
    elif isinstance(value, list):
        for nested in value:
            yield from error_codes(nested)


def recorded_results(catalog):
    checked = []
    for fixture in sorted((JOURNEY / "testdata").rglob("*")):
        if not fixture.is_file() or fixture.suffix not in {".json", ".jsonl"}:
            continue
        text = fixture.read_text()
        try:
            documents = [json.loads(line) for line in text.splitlines() if line.strip()] if fixture.suffix == ".jsonl" else [json.loads(text)]
        except json.JSONDecodeError:
            continue
        codes = [code for document in documents for code in error_codes(document)]
        terminal_codes = [document.get("meta", {}).get("stream", {}).get("exitCode")
                          for document in documents if isinstance(document, dict)
                          and document.get("meta", {}).get("stream", {}).get("terminal") is True]
        status_file = fixture.with_suffix(".exit")
        recorded_status = int(status_file.read_text().strip()) if status_file.exists() else None
        for code in codes:
            status = recorded_status if recorded_status is not None else (
                terminal_codes[-1] if terminal_codes else None)
            checked.append((fixture, code, status, expected_exit(code, catalog)))
            if recorded_status is not None:
                for terminal_status in terminal_codes:
                    checked.append((fixture, code, terminal_status, recorded_status))
    return checked


def go_kb_create_codes(catalog):
    """Derive kb-create's diagnostic scope from its Go source and shared boundary."""
    codes = set()
    for source_root in (ROOT / "tools" / "kb-create", ROOT / "tools" / "clikit"):
        for source in source_root.rglob("*.go"):
            if source.name.endswith("_test.go") or source.name == "catalog_gen.go":
                continue
            codes.update(code for code in re.findall(r"\bKB_[A-Z0-9_]+\b", source.read_text())
                         if code in catalog)
    return codes


def literal(node, env):
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        left, right = literal(node.left, env), literal(node.right, env)
        if isinstance(left, str) and isinstance(right, str):
            return left + right
    if isinstance(node, ast.Name):
        return env.get(node.id, UNKNOWN)
    if isinstance(node, ast.Dict):
        keys = [literal(key, env) for key in node.keys]
        values = [literal(value, env) for value in node.values]
        if all(key is not UNKNOWN for key in keys) and all(value is not UNKNOWN for value in values):
            return dict(zip(keys, values))
    if isinstance(node, (ast.List, ast.Tuple)):
        values = [literal(item, env) for item in node.elts]
        if all(value is not UNKNOWN for value in values):
            return values
    if isinstance(node, ast.IfExp):
        return Alternatives((literal(node.body, env), literal(node.orelse, env)))
    if isinstance(node, ast.Call):
        name = node.func.id if isinstance(node.func, ast.Name) else (
            node.func.attr if isinstance(node.func, ast.Attribute) else "")
        values = [literal(arg, env) for arg in node.args]
        if name == "join" and isinstance(node.func, ast.Attribute) and len(values) == 1:
            separator = literal(node.func.value, env)
            if isinstance(separator, str) and isinstance(values[0], list) and all(
                    isinstance(item, str) for item in values[0]):
                return separator.join(values[0])
        if name == "dumps" and values and values[0] is not UNKNOWN:
            if isinstance(values[0], Alternatives):
                return Alternatives(json.dumps(value) for value in values[0].values)
            return json.dumps(values[0], default=lambda value: value.values[0]
                              if isinstance(value, Alternatives) else str(value))
        if name == "loads" and values and isinstance(values[0], str):
            try:
                return json.loads(values[0])
            except json.JSONDecodeError:
                return None
        if name in {"kb_create_stream", "apply_stream"} and len(values) == 2 and values[0] is not UNKNOWN:
            return {"stream_record": values[0], "terminal_exit": values[1]}
    return UNKNOWN


def synthesized_results(catalog):
    checked = []
    failures = []
    go_codes = go_kb_create_codes(catalog)
    for source in sorted(JOURNEY.glob("test_*.py")):
        text = source.read_text()
        tree = ast.parse(text, filename=str(source))
        parents = {}
        for parent in ast.walk(tree):
            for child in ast.iter_child_nodes(parent):
                parents[child] = parent
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or not (isinstance(node.func, ast.Attribute) and node.func.attr == "CompletedProcess"):
                continue
            scope = node
            while scope in parents and not isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef)):
                scope = parents[scope]
            if not isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef)):
                continue
            env = {}
            for item in ast.walk(scope):
                if isinstance(item, (ast.Assign, ast.AnnAssign)):
                    value_node = item.value
                    value = literal(value_node, env)
                    targets = item.targets if isinstance(item, ast.Assign) else [item.target]
                    for target in targets:
                        if isinstance(target, ast.Name) and value is not UNKNOWN:
                            env[target.id] = value
            for item in ast.walk(scope):
                if (isinstance(item, ast.Call) and isinstance(item.func, ast.Attribute)
                        and item.func.attr == "assertEqual" and len(item.args) >= 2
                        and isinstance(item.args[0], ast.Name)):
                    asserted_argv = literal(item.args[1], env)
                    if isinstance(asserted_argv, (list, tuple)):
                        env[item.args[0].id] = asserted_argv
            if len(node.args) < 3:
                stdout_node = next((keyword.value for keyword in node.keywords if keyword.arg == "stdout"), None)
                status_node = node.args[1] if len(node.args) > 1 else next(
                    (keyword.value for keyword in node.keywords if keyword.arg == "returncode"), None)
                if stdout_node is None or status_node is None:
                    continue
            else:
                stdout_node = node.args[2]
                status_node = node.args[1]
            status = literal(status_node, env)
            argv = literal(node.args[0], env) if node.args else UNKNOWN
            if isinstance(argv, (list, tuple)) and argv and argv[0] != "kb-create":
                continue
            stdout = literal(stdout_node, env)
            if isinstance(stdout, str):
                try:
                    stdout = json.loads(stdout)
                except json.JSONDecodeError:
                    try:
                        stdout = [json.loads(line) for line in stdout.splitlines() if line.strip()]
                    except json.JSONDecodeError:
                        stdout = None
            if isinstance(stdout, Alternatives):
                alternatives = []
                for stdout_option in stdout.values:
                    if isinstance(stdout_option, str):
                        try:
                            stdout_option = json.loads(stdout_option)
                        except json.JSONDecodeError:
                            stdout_option = None
                    alternatives.append(stdout_option)
            else:
                alternatives = [stdout]
            terminal_statuses = []
            expanded = []
            for value in alternatives:
                if isinstance(value, dict) and "stream_record" in value:
                    terminal_statuses.append(value["terminal_exit"])
                    value = value["stream_record"]
                if isinstance(value, list):
                    terminal_statuses.extend(
                        document.get("meta", {}).get("stream", {}).get("exitCode")
                        for document in value if isinstance(document, dict)
                        and document.get("meta", {}).get("stream", {}).get("terminal") is True)
                expanded.append(value)
            codes = [code for value in expanded for code in error_codes(value)]
            codes = [code for code in codes if code in go_codes]
            for code in codes:
                expected = expected_exit(code, catalog)
                checked.append((source, node.lineno, code, status, tuple(terminal_statuses), expected))
                if status != expected or any(terminal_status != expected for terminal_status in terminal_statuses):
                    failures.append(f"{source.relative_to(ROOT)}:{node.lineno} {code}: process={status}, terminal={terminal_statuses}, catalog={expected}")
    return checked, failures


class CatalogExitStatusTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        raw = json.loads(CATALOG.read_text())
        cls.catalog = {entry["code"]: entry for entry in raw["codes"]}
        CATALOG_ENTRIES.update(cls.catalog)

    def test_recorded_kb_create_results_match_catalogued_status(self):
        results = recorded_results(self.catalog)
        mismatches = [f"{path.relative_to(ROOT)} {code}: terminal={status}, catalog={expected}"
                      for path, code, status, expected in results if status != expected or status is None]
        self.assertGreater(len(results), 0, "no recorded kb-create result statuses were checked")
        self.assertEqual(mismatches, [])

    def test_synthesized_kb_create_results_match_catalogued_status(self):
        results, mismatches = synthesized_results(self.catalog)
        self.assertGreater(len(results), 0, "no synthesized kb-create error results were checked")
        self.assertEqual(mismatches, [])


if __name__ == "__main__":
    unittest.main()
