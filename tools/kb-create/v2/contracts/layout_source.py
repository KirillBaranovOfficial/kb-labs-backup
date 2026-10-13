"""Read shared path identifiers from the canonical platform-layout contract."""

import json
from pathlib import Path, PurePosixPath


def shared_runtime_path(identifier: str) -> PurePosixPath:
    contract = Path(__file__).with_name("platform-layout.json")
    layout = json.loads(contract.read_text(encoding="utf-8"))
    for entry in layout["sharedRuntimePaths"]:
        if entry.get("id") == identifier:
            return PurePosixPath(entry["platformPath"])
    raise RuntimeError(f"platform layout does not declare shared path {identifier}")


def secret_state_path() -> PurePosixPath:
    return shared_runtime_path("secretState")
