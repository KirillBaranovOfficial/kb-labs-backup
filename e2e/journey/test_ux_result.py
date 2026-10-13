from __future__ import annotations

import ast
import unittest
from pathlib import Path


def _process_creation_sites(root: Path) -> dict[str, list[int]]:
    """Inventory runtime journey process-creation calls by their imported API."""
    subprocess_sinks = {"Popen", "run", "call", "check_call", "check_output", "getoutput", "getstatusoutput"}
    pty_sinks = {"spawn", "fork"}
    sites: dict[str, list[int]] = {}
    for source_path in (root / "e2e/journey").rglob("*.py"):
        if source_path.name.startswith("test_") or "__pycache__" in source_path.parts:
            continue
        tree = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(source_path))
        modules: dict[str, str] = {}
        functions: dict[str, tuple[str, str]] = {}
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    if alias.name in {"subprocess", "os", "pty", "asyncio"}:
                        modules[alias.asname or alias.name] = alias.name
            elif isinstance(node, ast.ImportFrom) and node.module in {"subprocess", "os", "pty", "asyncio"}:
                for alias in node.names:
                    functions[alias.asname or alias.name] = (node.module, alias.name)

        relative = source_path.relative_to(root).as_posix()
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            module, name = None, None
            if isinstance(node.func, ast.Name) and node.func.id in functions:
                module, name = functions[node.func.id]
            elif (isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Name)
                  and node.func.value.id in modules):
                module, name = modules[node.func.value.id], node.func.attr
            if (module == "subprocess" and name in subprocess_sinks
                    or module == "pty" and name in pty_sinks
                    or module == "os" and name.startswith(("exec", "spawn"))
                    or module == "asyncio" and name in {"create_subprocess_exec", "create_subprocess_shell"}):
                sites.setdefault(relative, []).append(node.lineno)
    return sites

from invocations import (decode_ux_error_result, decode_ux_success_result, describe_cli_failure,
                         read_ux_cli_result, read_ux_cli_stream)


class UxResultDecodeTests(unittest.TestCase):
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
        output = '{"error":{"code":"KB_DOCTOR_INVALID","message":"Manifest scan failed.","hint":"Run `kb-create doctor --fix` and retry."}}'
        detail = describe_cli_failure("kb-create doctor", output, "", 2)
        self.assertEqual(detail, "kb-create doctor exited 2: KB_DOCTOR_INVALID; Manifest scan failed.; hint: Run `kb-create doctor --fix` and retry.")


class JourneyProcessOwnershipTests(unittest.TestCase):
    def test_runtime_process_creation_is_confined_to_invocations_owner(self):
        root = Path(__file__).resolve().parents[2]
        sites = _process_creation_sites(root)
        self.assertEqual(set(sites), {"e2e/journey/invocations.py"}, sites)

    def test_runtime_process_creation_rail_detects_imported_aliases_outside_owner(self):
        import tempfile

        with tempfile.TemporaryDirectory(prefix="journey-process-owner-") as temporary:
            root = Path(temporary)
            source = root / "e2e/journey/outside.py"
            source.parent.mkdir(parents=True)
            source.write_text(
                "from subprocess import Popen as launch\n"
                "def start(command):\n"
                "    return launch(command)\n"
            )
            self.assertEqual(_process_creation_sites(root), {"e2e/journey/outside.py": [3]})


if __name__ == "__main__":
    unittest.main()
