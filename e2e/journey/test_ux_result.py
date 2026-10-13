from __future__ import annotations

import unittest
from pathlib import Path
import re
import ast

from invocations import (decode_ux_error_result, decode_ux_success_result, describe_cli_failure,
                         KB_CREATE_JSON_STREAM_COMMANDS, read_kb_create_cli_result,
                         read_ux_cli_result, read_ux_cli_stream)


def kb_create_stdout_decoder_violations(source: str, filename: str) -> list[str]:
    """Track kb-create command vectors through subprocess results to JSON readers."""
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
        journey_root = Path(__file__).parent
        launch_methods = {"run", "Popen", "call", "check_call", "check_output", "system", "popen"}
        violations = []
        for path in journey_root.rglob("*.py"):
            if path.name.startswith("test_") or path == journey_root / "invocations.py":
                continue
            tree = ast.parse(path.read_text(), filename=str(path))
            for node in ast.walk(tree):
                if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
                    continue
                owner = node.func.value
                if (isinstance(owner, ast.Name) and owner.id in {"subprocess", "os"}
                        and node.func.attr in launch_methods):
                    violations.append(f"{path}:{node.lineno} launches a process outside invocations.py")
        self.assertEqual(violations, [])

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
            read_ux_cli_result('{"ok":true,"data":{}}', 1)

    def test_cli_exit_code_must_match_error_envelope(self):
        with self.assertRaisesRegex(ValueError, "exit code does not match"):
            read_ux_cli_result('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}', 0)

    def test_cli_stream_validates_terminal_exit_and_preserves_result_events(self):
        stream = ('{"ok":true,"data":{"phase":"Apply","state":"completed"}}\n'
                  '{"ok":true,"data":{"operation":"apply","receipt":{}},"meta":{"stream":{"result":true}}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":0}}}\n')
        records = read_ux_cli_stream(stream, 0)
        self.assertEqual(records[-2]["data"]["operation"], "apply")

    def test_cli_stream_rejects_terminal_exit_code_mismatch(self):
        stream = ('{"ok":false,"error":{"code":"KB_TEST","message":"failed"}}\n'
                  '{"ok":true,"data":null,"meta":{"stream":{"terminal":true,"exitCode":1}}}\n')
        with self.assertRaisesRegex(ValueError, "terminal record does not match"):
            read_ux_cli_stream(stream, 0)

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
        self.assertEqual(read_kb_create_cli_result("plan", document, 2)["ok"], False)
        self.assertEqual(read_kb_create_cli_result("apply", stream, 2)["ok"], False)
        with self.assertRaisesRegex(ValueError, "not JSON"):
            read_kb_create_cli_result("plan", stream, 2)
        with self.assertRaisesRegex(ValueError, "non-JSON record"):
            read_kb_create_cli_result("apply", document, 2)


if __name__ == "__main__":
    unittest.main()
