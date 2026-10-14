from __future__ import annotations

import unittest
from pathlib import Path
import re
import subprocess
import ast
import fnmatch
import tempfile

from invocations import (decode_ux_error_result, decode_ux_success_result, describe_cli_failure,
                         KB_CREATE_JSON_STREAM_COMMANDS, read_kb_create_cli_result,
                         read_ux_cli_result, read_ux_cli_stream)
from test_catalog_exit_status import catalogued_exit

PROCESS_OWNER = "invocations.py"
PROCESS_MODULE_FAMILIES = {
    "subprocess": "launches processes and creates completed results",
    "pty": "creates terminal-backed child processes",
    "multiprocessing": "creates worker processes",
    "asyncio.subprocess": "creates asynchronous child processes",
}
ASYNCIO_PROCESS_FUNCTION_FAMILIES = {
    "create_subprocess_exec": "creates an asynchronous child process",
    "create_subprocess_shell": "creates a shell child process asynchronously",
}
OS_PROCESS_FUNCTION_FAMILIES = {
    "system": "runs a shell command",
    "popen": "opens a child process pipe",
    "fork": "creates a child process",
    "startfile": "launches a file through the operating system",
    "spawn*": "creates a child process",
    "exec*": "replaces the current process",
    "posix_spawn*": "creates a child process",
}


def kb_create_stdout_decoder_violations(source: str, filename: str) -> list[str]:
    """Track ordinary kb-create result dataflow; the process import rail fixes the owner.

    This flow check intentionally does not model deliberate dynamic imports,
    importlib, getattr, or ctypes routes; those are code-review concerns.
    """
    tree = ast.parse(source, filename=filename)
    violations = []
    for function in (node for node in ast.walk(tree) if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))):
        executables: set[str] = {argument.arg for argument in (*function.args.posonlyargs, *function.args.args,
                                                                *function.args.kwonlyargs)
                                 if argument.arg == "launcher"}
        commands: set[str] = set()
        results: set[str] = set()
        raw_stdout: set[str] = set()
        nodes = sorted(ast.walk(function), key=lambda node: (getattr(node, "lineno", 0), getattr(node, "col_offset", 0)))

        def referenced_names(node: ast.AST) -> set[str]:
            return {child.id for child in ast.walk(node) if isinstance(child, ast.Name)}

        def is_command_vector(node: ast.AST) -> bool:
            if not isinstance(node, (ast.List, ast.Tuple)) or not node.elts:
                return False
            first = node.elts[0]
            if isinstance(first, ast.Constant) and isinstance(first.value, str):
                return first.value == "kb-create"
            return ((isinstance(first, ast.Name) and first.id in executables)
                    or (isinstance(first, ast.Attribute) and first.attr == "launcher"))

        def contains_command(node: ast.AST) -> bool:
            return any(is_command_vector(child) or (isinstance(child, ast.Name) and child.id in commands)
                       for child in ast.walk(node))

        def is_result(node: ast.AST) -> bool:
            return ((isinstance(node, ast.Name) and node.id in results)
                    or (isinstance(node, ast.Subscript) and is_result(node.value)))

        def is_stdout(node: ast.AST) -> bool:
            return ((isinstance(node, ast.Attribute) and node.attr == "stdout" and is_result(node.value))
                    or (isinstance(node, ast.Name) and node.id in raw_stdout))

        for node in nodes:
            if isinstance(node, (ast.Assign, ast.AnnAssign)):
                value = node.value
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                target_names = {child.id for target in targets for child in ast.walk(target) if isinstance(child, ast.Name)}
                commands.difference_update(target_names)
                results.difference_update(target_names)
                raw_stdout.difference_update(target_names)
                is_launcher_lookup = (isinstance(value, ast.Call)
                                      and isinstance(value.func, ast.Attribute)
                                      and value.func.attr == "which"
                                      and any(isinstance(child, ast.Constant) and child.value == "kb-create"
                                              for child in ast.walk(value)))
                is_launcher_path = (isinstance(value, ast.Call) and isinstance(value.func, ast.Name)
                                    and value.func.id == "str"
                                    and any(isinstance(child, ast.Constant) and child.value == "kb-create"
                                            for child in ast.walk(value)))
                if value and (is_launcher_lookup or is_launcher_path):
                    executables.update(target_names)
                if value and is_command_vector(value):
                    commands.update(target_names)
                elif value and isinstance(value, ast.Name) and value.id in commands:
                    commands.update(target_names)
                elif value and is_stdout(value):
                    raw_stdout.update(target_names)
                elif value and isinstance(value, ast.Name) and value.id in raw_stdout:
                    raw_stdout.update(target_names)
                elif value and is_result(value):
                    results.update(target_names)
                elif value and isinstance(value, ast.Call) and any(contains_command(argument) for argument in value.args):
                    for target in targets:
                        if isinstance(target, (ast.Tuple, ast.List)):
                            results.update(child.id for child in target.elts[:1] if isinstance(child, ast.Name))
                        elif isinstance(target, ast.Name):
                            results.add(target.id)
            if isinstance(node, ast.Call):
                method = node.func.id if isinstance(node.func, ast.Name) else (
                    node.func.attr if isinstance(node.func, ast.Attribute) else "")
                if any(contains_command(argument) for argument in node.args):
                    for keyword in node.keywords:
                        if keyword.arg == "result_sink":
                            results.update(referenced_names(keyword.value))
                if method == "loads" and node.args:
                    raw = node.args[0]
                    if is_stdout(raw):
                        violations.append(f"{filename}:{node.lineno} parses kb-create stdout with json.loads")
                if method in {"read_ux_cli_result", "read_ux_cli_stream"} and node.args:
                    raw = node.args[0]
                    if is_stdout(raw):
                        violations.append(f"{filename}:{node.lineno} sends kb-create stdout to {method}")
    return violations


def process_import_violations(source: str, filename: str) -> list[str]:
    """Reject imports and resolved attributes that can start a process.

    The single owner keeps subprocess/pty/multiprocessing/asyncio.subprocess
    because it must launch commands and preserve their process results. The
    runtime result invariant in invocations checks every process it starts;
    steps cannot create another route because ordinary imports are rejected.
    The rail covers ordinary Python imports and resolved os attributes. Deliberate
    dynamic imports, importlib, computed getattr, and ctypes are code-review
    concerns, not patterns this static rail attempts to recognize.
    """
    tree = ast.parse(source, filename=filename)
    module_aliases: dict[str, str] = {}
    violations: list[str] = []

    def is_os_process_function(name: str) -> bool:
        return any(fnmatch.fnmatchcase(name, pattern) for pattern in OS_PROCESS_FUNCTION_FAMILIES)

    def attribute_path(node: ast.AST) -> str | None:
        if isinstance(node, ast.Name):
            return node.id
        if isinstance(node, ast.Attribute):
            parent = attribute_path(node.value)
            return f"{parent}.{node.attr}" if parent else None
        return None

    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name in PROCESS_MODULE_FAMILIES:
                    violations.append(f"{filename}:{node.lineno} imports {alias.name}: {PROCESS_MODULE_FAMILIES[alias.name]}")
                local_name = alias.asname or alias.name.split(".", 1)[0]
                module_aliases[local_name] = alias.name
        elif isinstance(node, ast.ImportFrom):
            module = node.module or ""
            if module in PROCESS_MODULE_FAMILIES:
                violations.append(f"{filename}:{node.lineno} imports {module}: {PROCESS_MODULE_FAMILIES[module]}")
            elif module == "os":
                blocked = [alias.name for alias in node.names
                           if alias.name == "*" or is_os_process_function(alias.name)]
                if blocked:
                    names = ", ".join(blocked)
                    violations.append(f"{filename}:{node.lineno} imports process functions from os: {names}")
            elif module == "asyncio":
                blocked = [alias.name for alias in node.names
                           if alias.name == "subprocess" or alias.name in ASYNCIO_PROCESS_FUNCTION_FAMILIES]
                if blocked:
                    violations.append(f"{filename}:{node.lineno} imports asyncio process APIs: {', '.join(blocked)}")
    for node in ast.walk(tree):
        if isinstance(node, ast.Attribute):
            path = attribute_path(node)
            if not path:
                continue
            root, separator, suffix = path.partition(".")
            imported = module_aliases.get(root)
            if not imported or not separator:
                continue
            resolved = f"{imported}.{suffix}"
            if imported == "os" and is_os_process_function(suffix.split(".", 1)[0]):
                violations.append(f"{filename}:{node.lineno} accesses os process function {resolved}")
            elif imported == "asyncio" and (
                    suffix == "subprocess" or suffix.startswith("subprocess.")
                    or suffix in ASYNCIO_PROCESS_FUNCTION_FAMILIES):
                violations.append(f"{filename}:{node.lineno} accesses asyncio process API {resolved}")
    return violations


def journey_modules_for_process_import_rail(journey_root: Path) -> list[Path]:
    """Use the source directory and unittest's test_*.py pattern for coverage."""
    owner = journey_root / PROCESS_OWNER
    return sorted(path for path in journey_root.rglob("*.py")
                  if path.is_file() and not fnmatch.fnmatchcase(path.name, "test_*.py")
                  and path != owner and "__pycache__" not in path.parts)


class UxResultDecodeTests(unittest.TestCase):
    def test_journey_kb_create_stdout_has_one_json_decoder_owner(self):
        journey_root = Path(__file__).parent
        modules = sorted(path for path in journey_root.rglob("*.py") if "__pycache__" not in path.parts)
        violations = [violation for path in modules
                      for violation in kb_create_stdout_decoder_violations(path.read_text(), str(path))]
        self.assertEqual(violations, [])

    def test_shell_and_typescript_helpers_do_not_launch_kb_create_json(self):
        journey_root = Path(__file__).parent
        for path in sorted(journey_root.parent.rglob("*")):
            if path.suffix not in {".sh", ".ts", ".js", ".mjs"} or not path.is_file():
                continue
            source = path.read_text(errors="replace")
            direct_command = re.search(
                r"(?:run|spawn|exec|execFile|execFileSync)\s*\([^;]{0,1000}kb-create[^;]{0,1000}--json",
                source, re.DOTALL)
            shell_source = re.sub(r"\\\s*\n", " ", source)
            shell_command = re.search(r"(?m)^\s*[^#\n]*\bkb-create\b[^\n]*--json", shell_source)
            self.assertFalse(direct_command or shell_command,
                             f"{path} runs kb-create JSON outside the Python decoder owner")

    def test_python_journey_process_launches_use_the_shared_invocation_boundary(self):
        """Only invocations.py imports process owners; its outputs enforce status classes.

        This makes every ordinary step process go through the runtime catalog
        invariant before a result reaches a step. Deliberately evasive imports
        or calls (dynamic import, getattr, ctypes) are code-review concerns.
        Test modules matching unittest's `test_*.py` discovery pattern are
        excluded because their fixtures intentionally build fake results.
        """
        journey_root = Path(__file__).parent
        modules = journey_modules_for_process_import_rail(journey_root)
        violations = [violation for path in modules
                      for violation in process_import_violations(path.read_text(), str(path))]
        self.assertEqual(violations, [])

    def test_process_import_mutations_are_rejected_by_import_structure(self):
        mutations = {
            "from subprocess import run as launch": "from subprocess import run as launch\nlaunch(['tool'])\n",
            "import subprocess as sp": "import subprocess as sp\nsp.run(['tool'])\n",
            "from os import system": "from os import system\nsystem('tool')\n",
            "os.popen": "import os\nos.popen('tool')\n",
        }
        for label, source in mutations.items():
            with self.subTest(mutation=label):
                self.assertTrue(process_import_violations(source, "steps/mutation.py"))

        with tempfile.TemporaryDirectory() as temporary:
            nested = Path(temporary) / "steps" / "new_group"
            nested.mkdir(parents=True)
            module = nested / "terminal.py"
            module.write_text("import pty\n")
            disguised_owner = nested / PROCESS_OWNER
            disguised_owner.write_text("import pty\n")
            violations = [violation for path in journey_modules_for_process_import_rail(Path(temporary))
                          for violation in process_import_violations(path.read_text(), str(path))]
            self.assertEqual(len(violations), 2)
            self.assertTrue(all("pty" in violation for violation in violations))

    def test_raw_json_parser_mutation_on_kb_create_result_is_rejected(self):
        mutated = '''
def bad_step():
    command = ["kb-create", "apply", "--json"]
    result = run_command(command)
    json.loads(result.stdout)

def bad_sink():
    command = ["kb-create", "doctor", "--json"]
    results = []
    record(command, result_sink=results)
    json.loads(results[-1].stdout)
'''
        violations = kb_create_stdout_decoder_violations(mutated, "mutation.py")
        self.assertEqual(len(violations), 2)
        self.assertIn("parses kb-create stdout with json.loads", violations[0])

    # case: S-203.1
    def test_decodes_success_payload_from_valid_envelope(self):
        self.assertEqual(
            decode_ux_success_result({"ok": True, "data": {"status": "healthy"}}),
            {"status": "healthy"},
        )

    def test_rejects_raw_and_failure_bodies(self):
        for body in ({"status": "healthy"}, {"ok": False, "error": {}}):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, "valid UX success envelope"):
                decode_ux_success_result(body)

    def test_decodes_error_payload_from_valid_envelope(self):
        self.assertEqual(
            decode_ux_error_result({"ok": False, "error": {"code": "KB_AUTH_REQUIRED", "message": "Sign in."}}),
            {"code": "KB_AUTH_REQUIRED", "message": "Sign in."},
        )

    def test_rejects_non_envelope_and_success_body_as_error(self):
        for body in ({"code": "KB_AUTH_REQUIRED"}, {"ok": True, "data": {}}):
            with self.subTest(body=body), self.assertRaisesRegex(ValueError, "valid UX error envelope"):
                decode_ux_error_result(body)

    def test_cli_exit_code_must_match_success_envelope(self):
        with self.assertRaisesRegex(ValueError, "exit code does not match"):
            read_ux_cli_result(subprocess.CompletedProcess(["kb"], 1, '{"ok":true,"data":{}}', ""))

    def test_cli_exit_code_must_match_error_envelope(self):
        with self.assertRaisesRegex(ValueError, "exit code does not match"):
            read_ux_cli_result(subprocess.CompletedProcess(["kb"], 0, '{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', ""))

    def test_cli_stream_validates_terminal_exit_and_preserves_result_events(self):
        stream = ('{"ok":true,"data":{"phase":"Apply","state":"completed"}}\n'
                  '{"ok":true,"data":{"operation":"apply","receipt":{}},"meta":{"stream":{"result":true}}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":0}}}\n')
        records = read_ux_cli_stream(subprocess.CompletedProcess(["kb-create", "apply"], 0, stream, ""))
        self.assertEqual(records[-2]["data"]["operation"], "apply")

    def test_cli_stream_rejects_terminal_exit_code_mismatch(self):
        stream = ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":1}}}\n')
        with self.assertRaisesRegex(ValueError, "terminal record does not match"):
            read_ux_cli_stream(subprocess.CompletedProcess(["kb-create", "apply"], 0, stream, ""))

    def test_catalogued_error_status_is_checked_at_the_shared_boundary(self):
        failure = '{"ok":false,"error":{"code":"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE","message":"index unavailable"}}'
        result = subprocess.CompletedProcess(["kb-create", "plan"], 2, failure, "")
        with self.assertRaisesRegex(ValueError, r"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE \(operation-failure\).*process status 2.*terminal status None"):
            read_kb_create_cli_result("plan", result)

    def test_result_owner_rejects_non_process_shapes(self):
        for result in ({"stdout": "{}", "returncode": 0},
                       type("CustomResult", (), {"stdout": "{}", "returncode": 0})()):
            with self.subTest(result=type(result).__name__), self.assertRaisesRegex(ValueError, "requires a subprocess.CompletedProcess"):
                read_ux_cli_result(result)

    def test_catalogued_stream_reports_process_and_terminal_statuses(self):
        stream = ('{"ok":false,"error":{"code":"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE","message":"failed"}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":2}}}\n')
        result = subprocess.CompletedProcess(["kb-create", "apply"], 1, stream, "")
        with self.assertRaisesRegex(ValueError, r"KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE \(operation-failure\).*process status 1.*terminal status 2"):
            read_kb_create_cli_result("apply", result)

    def test_setup_failure_description_preserves_typed_error_and_hint(self):
        output = '{"ok":false,"error":{"code":"KB_DOCTOR_INVALID","message":"Manifest scan failed.","hint":"Run `kb-create doctor --fix` and retry."}}'
        detail = describe_cli_failure("kb-create doctor", output, "", 2)
        self.assertEqual(detail, "kb-create doctor exited 2: KB_DOCTOR_INVALID; Manifest scan failed.; hint: Run `kb-create doctor --fix` and retry.")

    def test_kb_create_output_forms_match_registered_command_dispatch(self):
        root = Path(__file__).resolve().parents[2]
        registry = (root / "tools/kb-create/v2/cmd/kb-create-v2/root.go").read_text()
        descriptors = re.findall(
            r'(?ms)^\s*\{Name: "([^"]+)"(.*?)(?=^\s*\{Name: "|^\s*\})', registry
        )
        stream_commands = {
            name for name, body in descriptors
            if re.search(r"if jsonMode\(cmd\)\s*\{\s*return execute\w+Stream\(", body)
        }
        self.assertTrue(descriptors, "could not read kb-create operation descriptors from root.go")
        self.assertEqual(stream_commands, KB_CREATE_JSON_STREAM_COMMANDS)

    def test_command_reader_enforces_document_and_stream_forms(self):
        fixtures = Path(__file__).parent / "testdata"
        document = (fixtures / "gateway-index-plan-refusal.json").read_text()
        stream = (fixtures / "gateway-index-apply-refusal.jsonl").read_text()
        plan_status = int((fixtures / "gateway-index-plan-refusal.exit").read_text().strip())
        apply_status = int((fixtures / "gateway-index-apply-refusal.exit").read_text().strip())
        plan_result = subprocess.CompletedProcess(["kb-create", "plan"], plan_status, document, "")
        apply_result = subprocess.CompletedProcess(["kb-create", "apply"], apply_status, stream, "")
        self.assertEqual(read_kb_create_cli_result("plan", plan_result)["ok"], False)
        self.assertEqual(read_kb_create_cli_result("apply", apply_result)["ok"], False)
        with self.assertRaisesRegex(ValueError, "not JSON"):
            read_kb_create_cli_result("plan", apply_result)
        with self.assertRaisesRegex(ValueError, "non-JSON record"):
            read_kb_create_cli_result("apply", plan_result)
        for status in (plan_status, apply_status):
            self.assertEqual(status, catalogued_exit("KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE"))


if __name__ == "__main__":
    unittest.main()
