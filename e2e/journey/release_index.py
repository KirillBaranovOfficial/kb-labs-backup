"""Helpers for reading the sealed V2 release index."""


def stable_platform_version(index: dict) -> str:
    """Return the platform version selected by the stable channel."""
    return index["channels"]["stable"]
