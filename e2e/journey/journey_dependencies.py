"""Typed references shared by journey selection and runtime dependency checks."""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True, order=True)
class Dependency:
    kind: str
    name: str

    @classmethod
    def parse(cls, value: str) -> "Dependency":
        kind, separator, name = value.partition(":")
        if not separator or kind not in {"step", "flow"} or not name.strip():
            raise ValueError(f"invalid journey dependency {value!r}; use 'step:<name>' or 'flow:<name>'")
        return cls(kind, name.strip())

    def __str__(self) -> str:
        return f"{self.kind}:{self.name}"
