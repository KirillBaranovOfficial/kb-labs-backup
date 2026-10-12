"""Validation for state-derived values sent to journey commands and services."""
from __future__ import annotations

from collections.abc import Mapping
import os


class MissingJourneyState(RuntimeError):
    def __init__(self, key: str):
        self.key = key
        super().__init__(f"missing journey state key {key}")


def journey_state(key: str, state: Mapping[str, str] | None = None) -> str:
    """Read required journey state at the point where it is substituted into a request."""
    source = os.environ if state is None else state
    value = source.get(key)
    if not value:
        raise MissingJourneyState(key)
    return value
