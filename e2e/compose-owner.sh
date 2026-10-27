#!/usr/bin/env bash

COMPOSE_OWNER_COMMAND=()
COMPOSE_OWNER_MISSING_MESSAGE="Docker Compose is required; install Docker Compose v2 (\`docker compose\`) or the standalone \`docker-compose\` command, then rerun the command."

compose_owner_init() {
  COMPOSE_OWNER_COMMAND=()
  if command docker compose version >/dev/null 2>&1; then
    COMPOSE_OWNER_COMMAND=(docker compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE_OWNER_COMMAND=(docker-compose)
  else
    printf '%s\n' "$COMPOSE_OWNER_MISSING_MESSAGE" >&2
    return 127
  fi
}

compose_owner_json() {
  printf '["%s"' "${COMPOSE_OWNER_COMMAND[0]}"
  for argument in "${COMPOSE_OWNER_COMMAND[@]:1}"; do
    printf ',"%s"' "$argument"
  done
  printf ']\n'
}

JOURNEY_COMPOSE=()
JOURNEY_COMPOSE_FILES=()
COMPOSE_AVAILABLE=0
REGISTRY_COMPOSITION_ROOT=""

journey_compose_init() {
  local root="$1" rendered overlay_path

  JOURNEY_COMPOSE_FILES=(-f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml)
  if ! compose_owner_init; then
    COMPOSE_AVAILABLE=0
    return 0
  fi
  COMPOSE_AVAILABLE=1

  if [[ ",${COMPOSE_PROFILES:-}," == *",marketplace-registry,"* ]]; then
    REGISTRY_COMPOSITION_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kb-journey-registry-composition.XXXXXX")"
    rendered="$(node "$root/services/marketplace-registry/app/scripts/render-journey-composition.mjs" \
      --output "$REGISTRY_COMPOSITION_ROOT" --registry-url http://marketplace-registry:5071)"
    overlay_path="$(node -e 'const value=JSON.parse(process.argv[1]); process.stdout.write(value.composeOverlayPath)' "$rendered")"
    JOURNEY_COMPOSE_FILES+=(-f "$overlay_path")
    export KB_JOURNEY_MARKETPLACE_REGISTRY_URL="http://marketplace-tamper:8081"
    export KB_JOURNEY_MARKETPLACE_PUBLIC_KEY_B64="$(node -e 'const value=JSON.parse(process.argv[1]); process.stdout.write(value.signingPublicKeyBase64)' "$rendered")"
  fi

  JOURNEY_COMPOSE=("${COMPOSE_OWNER_COMMAND[@]}" "${JOURNEY_COMPOSE_FILES[@]}")
}

journey_compose() {
  "${JOURNEY_COMPOSE[@]}" "$@"
}

journey_compose_cleanup() {
  if [[ -n "$REGISTRY_COMPOSITION_ROOT" ]]; then
    rm -rf "$REGISTRY_COMPOSITION_ROOT"
    REGISTRY_COMPOSITION_ROOT=""
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  if [[ "${1:-}" == "resolve" ]]; then
    compose_owner_init && compose_owner_json
  else
    printf 'Usage: %s resolve\n' "${0##*/}" >&2
    exit 2
  fi
fi
