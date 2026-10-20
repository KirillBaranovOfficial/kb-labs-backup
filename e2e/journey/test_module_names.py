"""Every global a journey function reads must exist; a merge that drops a helper fails here, not in a batch."""
from __future__ import annotations

import builtins
import symtable
import unittest
from pathlib import Path


MODULE_DUNDERS = {"__file__", "__name__", "__doc__", "__spec__", "__package__"}


def _undefined_globals(source: Path) -> list[str]:
    table = symtable.symtable(source.read_text(), str(source), "exec")
    module_names = {symbol.get_name() for symbol in table.get_symbols()
                    if symbol.is_assigned() or symbol.is_imported() or symbol.is_namespace()}
    missing: set[str] = set()

    def walk(scope: symtable.SymbolTable) -> None:
        for symbol in scope.get_symbols():
            name = symbol.get_name()
            if (symbol.is_global() and symbol.is_referenced() and name not in module_names
                    and not hasattr(builtins, name) and name not in MODULE_DUNDERS):
                missing.add(name)
        for child in scope.get_children():
            walk(child)

    for child in table.get_children():
        walk(child)
    star_import = "import *" in source.read_text()
    return [] if star_import else sorted(missing)


class JourneyModuleNamesTest(unittest.TestCase):
    def test_journey_modules_reference_only_defined_globals(self):
        journey_dir = Path(__file__).resolve().parent
        problems = {}
        for source in sorted(journey_dir.rglob("*.py")):
            if source.name.startswith("test_") or "__pycache__" in source.parts:
                continue
            missing = _undefined_globals(source)
            if missing:
                problems[str(source.relative_to(journey_dir))] = missing
        self.assertEqual(problems, {})
