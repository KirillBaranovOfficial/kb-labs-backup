#!/usr/bin/env bash
set -euo pipefail

if ! compose_bin="$(command -v docker-compose)"; then
  printf 'Required tool docker-compose was not found on PATH; install Docker Compose, then run e2e/delivery/scripts/run.sh from the repository root.\n' >&2
  exit 127
fi

printf '%s\n' "$compose_bin"
