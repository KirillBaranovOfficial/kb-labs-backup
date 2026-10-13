"""Resolve shared files through the platform layout contract."""

import importlib.util
from pathlib import Path, PurePosixPath

_repository = Path(__file__).resolve().parents[2]
_owner_path = _repository / "tools/kb-create/v2/contracts/layout_source.py"
_owner_spec = importlib.util.spec_from_file_location("kb_platform_layout_owner", _owner_path)
if _owner_spec is None or _owner_spec.loader is None:
    raise RuntimeError("cannot load the platform layout owner")
_owner = importlib.util.module_from_spec(_owner_spec)
_owner_spec.loader.exec_module(_owner)

def secret_state_relative_path() -> PurePosixPath:
    return _owner.secret_state_path()


def secret_state_name() -> str:
    return secret_state_relative_path().name
