import fnmatch
import glob
import json
import shlex
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


def dockerignore_rules(path):
    rules = []
    for line in path.read_text().splitlines():
        pattern = line.strip()
        if not pattern or pattern.startswith("#"):
            continue
        negated = pattern.startswith("!")
        if negated:
            pattern = pattern[1:]
        pattern = pattern.strip("/")
        if pattern and pattern != ".":
            rules.append((pattern, negated))
    return rules


def is_dockerignored(path, rules):
    """Apply Docker's last-matching-rule behavior to a context-relative path."""
    parts = Path(path).as_posix().strip("/").split("/")
    ignored = False
    for pattern, negated in rules:
        if "/" not in pattern:
            matched = any(fnmatch.fnmatchcase(part, pattern) for part in parts)
        else:
            prefixes = ["/".join(parts[:index]) for index in range(1, len(parts) + 1)]
            matched = any(fnmatch.fnmatchcase(prefix, pattern) for prefix in prefixes)
        if matched:
            ignored = not negated
    return ignored


def copy_instructions(dockerfile):
    resources = []
    for line in dockerfile.read_text().splitlines():
        if not line.lstrip().startswith("COPY "):
            continue
        instruction = line.strip()[len("COPY "):]
        if instruction.startswith("["):
            values = json.loads(instruction)
            sources, destination = values[:-1], values[-1]
            for source in sources:
                target = f"{destination.rstrip('/')}/{Path(source).name}" if len(sources) > 1 else destination
                resources.append((source, target))
            continue
        tokens = shlex.split(instruction)
        source_tokens = []
        for token in tokens[:-1]:
            if token.startswith("--"):
                continue
            source_tokens.append(token)
        destination = tokens[-1]
        for source in source_tokens:
            target = f"{destination.rstrip('/')}/{Path(source).name}" if len(source_tokens) > 1 else destination
            resources.append((source, target))
    return resources


def copy_sources(dockerfile):
    return (source for source, _destination in copy_instructions(dockerfile))


class DockerBuildInputsTest(unittest.TestCase):
    def test_copy_sources_are_available_in_the_root_docker_context(self):
        rules = dockerignore_rules(ROOT / ".dockerignore")
        dockerfile = ROOT / "e2e/journey/Dockerfile.runner"
        excluded = []

        for source in copy_sources(dockerfile):
            matches = glob.glob(str(ROOT / source), recursive=True)
            candidates = [Path(match).relative_to(ROOT).as_posix() for match in matches]
            if not candidates:
                candidates = [source]
            for candidate in candidates:
                path = ROOT / candidate
                paths = [candidate]
                if path.is_dir():
                    paths.extend(item.relative_to(ROOT).as_posix() for item in path.rglob("*"))
                excluded.extend(item for item in paths if is_dockerignored(item, rules))

        self.assertEqual([], excluded,
                         f"Dockerfile COPY sources are excluded by .dockerignore: {sorted(set(excluded))}")


if __name__ == "__main__":
    unittest.main()
