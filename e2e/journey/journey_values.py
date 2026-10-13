"""Validation for state-derived values sent to journey commands and services."""
from __future__ import annotations

import os


class MissingJourneyState(RuntimeError):
    def __init__(self, key: str, reason: str | None = None):
        self.key = key
        self.reason = reason or "state probe has not run"
        super().__init__(f"missing journey state key {key}: {self.reason}")


def journey_value(key: str, default: str | None = None, *, required: bool = False,
                  state: dict[str, str] | None = None) -> str | None:
    """Read a harness input, preserving optional values and typing required omissions."""
    source = os.environ if state is None else state
    if key in source:
        value = source[key]
        if value or not required:
            return value
    if required:
        raise MissingJourneyState(key)
    return default
