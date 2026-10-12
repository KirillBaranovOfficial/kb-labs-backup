"""Validation for state-derived values sent to journey commands and services."""
from __future__ import annotations

from collections.abc import Iterable
import re
from typing import overload


_UNRESOLVED = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)\}")


class MissingJourneyState(RuntimeError):
    def __init__(self, key: str):
        self.key = key
        super().__init__(f"missing journey state key {key}")


@overload
def require_journey_value(value: str, *, state_key: str | None = None) -> str: ...


@overload
def require_journey_value(value: Iterable[str], *, state_key: str | None = None) -> Iterable[str]: ...


def require_journey_value(value: str | Iterable[str], *, state_key: str | None = None) -> str | Iterable[str]:
    values = (value,) if isinstance(value, str) else value
    for item in values:
        if not item and state_key:
            raise MissingJourneyState(state_key)
        unresolved = _UNRESOLVED.search(item)
        if unresolved:
            key = state_key or unresolved.group(1)
            raise MissingJourneyState(key)
    return value
