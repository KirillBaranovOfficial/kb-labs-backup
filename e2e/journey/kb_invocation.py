"""Sole constructor for invocations of the installed ``kb`` command."""
from __future__ import annotations

from collections.abc import Sequence
from pathlib import Path
import shlex


def kb_command(arguments: Sequence[str], *, executable: str | Path = "kb") -> list[str]:
    if any(not isinstance(argument, str) for argument in arguments):
        raise TypeError("kb command arguments must be strings")
    return [str(executable), *arguments]


def kb_login_shell(arguments: Sequence[str]) -> list[str]:
    """Build the profile-aware shell form while keeping its executable spelling here."""
    if any(not isinstance(argument, str) for argument in arguments):
        raise TypeError("kb command arguments must be strings")
    return ["bash", "-c", 'if [ -f "$HOME/.profile" ]; then . "$HOME/.profile"; fi; kb "$@"',
            "kb", *arguments]


def kb_login_shell_sequence(commands: Sequence[Sequence[str]]) -> list[str]:
    """Build ordered invocations in one login shell through the shared owner."""
    if not commands:
        raise ValueError("a login shell command sequence must not be empty")
    command_text = " && ".join(shlex.join(kb_command(arguments)) for arguments in commands)
    return ["bash", "-c", f'. "$HOME/.profile"; {command_text}']


def kb_shell_text(arguments: Sequence[str]) -> str:
    return shlex.join(kb_command(arguments))
