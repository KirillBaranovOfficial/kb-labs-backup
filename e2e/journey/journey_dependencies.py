"""Typed references shared by journey selection and runtime dependency checks."""
from __future__ import annotations

from dataclasses import dataclass


# FlowGraph state producers are the shared runtime contract used by the installer
# runner and the source resolver. A step dependency must never stand in for one
# of these values when its producing flow varies by runner.
FLOW_GRAPH_STATE_PRODUCERS = {
    "host-running": "host",
    "project-registered": "project",
    "session": "auth",
}


@dataclass(frozen=True, order=True)
class Dependency:
    kind: str
    name: str

    @classmethod
    def parse(cls, value: str) -> "Dependency":
        kind, separator, name = value.partition(":")
        if not separator or kind not in {"step", "flow", "state"} or not name.strip():
            raise ValueError(
                f"invalid journey dependency {value!r}; use 'step:<name>', 'flow:<name>', or 'state:<name>'")
        return cls(kind, name.strip())

    def __str__(self) -> str:
        return f"{self.kind}:{self.name}"
