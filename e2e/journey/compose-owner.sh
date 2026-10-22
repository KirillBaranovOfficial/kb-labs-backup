#!/usr/bin/env bash

JOURNEY_COMPOSE=()
JOURNEY_COMPOSE_FILES=()
COMPOSE_AVAILABLE=0
REGISTRY_COMPOSITION_ROOT=""

journey_compose_init() {
  local root="$1" rendered overlay_path
  local -a compose_command=()

  if command docker compose version >/dev/null 2>&1; then
    compose_command=(docker compose)
    COMPOSE_AVAILABLE=1
  elif command -v docker-compose >/dev/null 2>&1; then
    compose_command=(docker-compose)
    COMPOSE_AVAILABLE=1
  fi

  JOURNEY_COMPOSE_FILES=(-f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml)
  if (( ! COMPOSE_AVAILABLE )); then
    return 0
  fi

  if [[ ",${COMPOSE_PROFILES:-}," == *",marketplace-registry,"* ]]; then
    REGISTRY_COMPOSITION_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kb-journey-registry-composition.XXXXXX")"
    rendered="$(node "$root/services/marketplace-registry/app/scripts/render-journey-composition.mjs" \
      --output "$REGISTRY_COMPOSITION_ROOT" --registry-url http://marketplace-registry:5071)"
    overlay_path="$(node -e 'const value=JSON.parse(process.argv[1]); process.stdout.write(value.composeOverlayPath)' "$rendered")"
    JOURNEY_COMPOSE_FILES+=(-f "$overlay_path")
    export KB_JOURNEY_MARKETPLACE_REGISTRY_URL="http://marketplace-tamper:8081"
    export KB_JOURNEY_MARKETPLACE_PUBLIC_KEY_B64="$(node -e 'const value=JSON.parse(process.argv[1]); process.stdout.write(value.signingPublicKeyBase64)' "$rendered")"
  fi

  JOURNEY_COMPOSE=("${compose_command[@]}" "${JOURNEY_COMPOSE_FILES[@]}")
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
