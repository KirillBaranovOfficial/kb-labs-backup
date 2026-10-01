#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
docker() {
  if [[ "${1:-}" == "compose" ]]; then
    shift
    if command docker compose version >/dev/null 2>&1; then
      command docker compose "$@"
    else
      command docker-compose "$@"
    fi
  else
    command docker "$@"
  fi
}
if [[ "${KB_JOURNEY_CI:-0}" == "1" ]]; then
  COMPOSE=(docker compose -f e2e/docker-compose.yml -f e2e/docker-compose.ci.yml -f e2e/journey/docker-compose.yml)
else
  COMPOSE=(docker compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml)
fi
cleanup() { "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

if [[ "${1:-}" == "--prepare-only" ]]; then
  e2e/journey/prepare-candidate.sh
  exit
fi

if [[ -z "${KB_JOURNEY_ONLY:-}" ]]; then
  pnpm -s kb qa cases validate
fi

if [[ "${1:-}" != "--no-build" ]]; then
  pnpm install --frozen-lockfile
  kb-devkit run build --affected
  e2e/journey/prepare-candidate.sh
fi

mkdir -p .journey/reports
rm -f .journey/reports/journey-summary.md
find .journey/reports -maxdepth 1 -type d -name 'platform-state-*' -exec rm -rf {} +
"${COMPOSE[@]}" build publisher runner
"${COMPOSE[@]}" up -d --wait verdaccio
if "${COMPOSE[@]}" exec -T verdaccio sh -c 'find /verdaccio/storage -type f -name "*.json" -print -quit | grep -q .' >/dev/null 2>&1; then
  echo "WARNING: reusing non-empty Verdaccio storage; existing package versions must match the local tarballs."
fi
"${COMPOSE[@]}" run --rm publisher

run_journey() {
  local front_door="$1" scenario="$2" variant="$3"
  echo "==> $front_door / $scenario / $variant"
  if ! KB_JOURNEY_SCENARIO="$scenario" KB_JOURNEY_FRONT_DOOR="$front_door" KB_JOURNEY_VARIANT="$variant" \
    "${COMPOSE[@]}" run --rm -e KB_JOURNEY_SCENARIO -e KB_JOURNEY_FRONT_DOOR -e KB_JOURNEY_VARIANT \
    -e KB_JOURNEY_SERVICE_PROFILE runner < /dev/null; then
    status=1
  fi
}

status=0
if [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
  case "$KB_JOURNEY_ONLY" in
    agent:*) run_journey agent "${KB_JOURNEY_ONLY#agent:}" default ;;
    agent-input:*) run_journey agent "${KB_JOURNEY_ONLY#agent-input:}" input ;;
    human:default) run_journey human explore human-default ;;
    human:nondefault) run_journey human commit human-nondefault ;;
    negative:*) run_journey agent commit "${KB_JOURNEY_ONLY#negative:}" ;;
    service-local:*) KB_JOURNEY_SERVICE_PROFILE=nightly run_journey agent "${KB_JOURNEY_ONLY#service-local:}" default ;;
    service-secured:*) KB_JOURNEY_SERVICE_PROFILE=nightly run_journey human "${KB_JOURNEY_ONLY#service-secured:}" secured ;;
    service-modes)
      run_journey agent commit default
      run_journey human explore secured
      ;;
    *) echo "Unknown KB_JOURNEY_ONLY value: $KB_JOURNEY_ONLY" >&2; exit 2 ;;
  esac
else
  selectors_json="$(python3 e2e/journey/select_from_cases.py)"
  selectors="$(python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)))' <<< "$selectors_json")"
  if [[ -n "$selectors" ]]; then while IFS= read -r selector; do
    case "$selector" in
      agent:*) run_journey agent "${selector#agent:}" default ;;
      agent-input:*) run_journey agent commit input ;;
      human:default) run_journey human explore human-default ;;
      human:nondefault) run_journey human commit human-nondefault ;;
      negative:*) run_journey agent commit "${selector#negative:}" ;;
      service-local:*) KB_JOURNEY_SERVICE_PROFILE=nightly run_journey agent "${selector#service-local:}" default ;;
      service-secured:*) KB_JOURNEY_SERVICE_PROFILE=nightly run_journey human "${selector#service-secured:}" secured ;;
      *) echo "Unknown case-derived journey selector: $selector" >&2; status=1 ;;
    esac
  done <<< "$selectors"; fi
fi

echo "Journey matrix complete (exit $status)"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" && -f .journey/reports/journey-summary.md ]]; then
  cat .journey/reports/journey-summary.md >> "$GITHUB_STEP_SUMMARY"
fi
exit "$status"
