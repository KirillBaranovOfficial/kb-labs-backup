#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export KB_JOURNEY_GIT_REVISION="$(git rev-parse HEAD)"
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

mkdir -p .journey/reports
export KB_JOURNEY_INVOCATIONS="$ROOT/.journey/reports"

if [[ "${1:-}" == "--prepare-only" ]]; then
  e2e/journey/prepare-candidate.sh
  exit
fi

if [[ -z "${KB_JOURNEY_ONLY:-}" ]]; then
  python3 e2e/journey/hermetic_cli.py -- pnpm -s kb qa cases validate
fi

if [[ -n "${KB_JOURNEY_CASES:-}" ]]; then
  if [[ -z "${KB_JOURNEY_ONLY:-}" ]]; then
    echo "KB_JOURNEY_CASES requires KB_JOURNEY_ONLY to select a journey mode." >&2
    exit 2
  fi
  selection_json="$(python3 e2e/journey/select_from_cases.py)"
  export KB_JOURNEY_SELECTED_STEPS="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["steps"]))' <<< "$selection_json")"
  export KB_JOURNEY_PREREQUISITE_STEPS="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["prerequisiteSteps"]))' <<< "$selection_json")"
  export KB_JOURNEY_PREREQUISITE_FLOWS="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["prerequisiteFlows"]))' <<< "$selection_json")"
  export KB_JOURNEY_SELECTED_SOURCES="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["stepSources"]))' <<< "$selection_json")"
  if [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
    python3 e2e/journey/dispatch_registry.py --supports "$KB_JOURNEY_ONLY" <<< "$selection_json" || {
        echo "Selected runner $KB_JOURNEY_ONLY is not registered for the selected claim sources." >&2
        exit 2
      }
  fi
else
  unset KB_JOURNEY_SELECTED_STEPS
  unset KB_JOURNEY_PREREQUISITE_STEPS
  unset KB_JOURNEY_PREREQUISITE_FLOWS
  unset KB_JOURNEY_SELECTED_SOURCES
fi

dispatch_selector() {
  local selector="$1"
  python3 e2e/journey/dispatch_registry.py --dispatch "$selector"
}

run_selector() {
  local selector="$1"
  local runner_json had_profile=0 saved_profile="${KB_JOURNEY_SERVICE_PROFILE:-}"
  [[ "${KB_JOURNEY_SERVICE_PROFILE+x}" == "x" ]] && had_profile=1
  runner_json="$(dispatch_selector "$selector")"
  while IFS=$'\t' read -r front scenario variant profile; do
    unset KB_JOURNEY_SERVICE_PROFILE
    if [[ -n "$profile" ]]; then export KB_JOURNEY_SERVICE_PROFILE="$profile"; fi
    run_journey "$front" "$scenario" "$variant"
  done < <(python3 -c 'import json,sys; print("\n".join("\t".join((d["front_door"],d["scenario"],d["variant"],d["service_profile"])) for d in json.load(sys.stdin)))' <<< "$runner_json")
  if (( had_profile )); then export KB_JOURNEY_SERVICE_PROFILE="$saved_profile"; else unset KB_JOURNEY_SERVICE_PROFILE; fi
}

if [[ "${1:-}" == "--resolve-only" ]]; then
  if [[ -n "${KB_JOURNEY_CASES:-}" ]]; then
    python3 -c 'import json,sys; d=json.load(sys.stdin); print(json.dumps({"runners":d["runners"],"steps":d["steps"],"prerequisiteSteps":d["prerequisiteSteps"],"prerequisiteFlows":d["prerequisiteFlows"],"stepSources":d["stepSources"]},indent=2))' <<< "$selection_json"
  elif [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
    dispatch_selector "$KB_JOURNEY_ONLY"
  else
    python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin),indent=2))' \
      <<< "$(python3 e2e/journey/select_from_cases.py)"
  fi
  exit 0
fi

trap cleanup EXIT INT TERM

if [[ "${1:-}" != "--no-build" ]]; then
  pnpm install --frozen-lockfile
  kb-devkit run build --affected
  e2e/journey/prepare-candidate.sh
fi

mkdir -p .journey/reports
rm -f .journey/reports/journey-summary.md
find .journey/reports -maxdepth 1 -type d -name 'platform-state-*' -exec rm -rf {} +
"${COMPOSE[@]}" build publisher runner idp-stub
"${COMPOSE[@]}" up -d --wait verdaccio
if "${COMPOSE[@]}" exec -T verdaccio sh -s -- /verdaccio/storage < e2e/journey/storage_has_packages.sh >/dev/null 2>&1; then
  echo "WARNING: reusing non-empty Verdaccio storage; existing package versions must match the local tarballs."
fi
"${COMPOSE[@]}" run --rm publisher

run_journey() {
  local front_door="$1" scenario="$2" variant="$3"
  echo "==> $front_door / $scenario / $variant"
  if ! KB_JOURNEY_SCENARIO="$scenario" KB_JOURNEY_FRONT_DOOR="$front_door" KB_JOURNEY_VARIANT="$variant" \
    "${COMPOSE[@]}" run --rm -e KB_JOURNEY_SCENARIO -e KB_JOURNEY_FRONT_DOOR -e KB_JOURNEY_VARIANT \
    -e KB_JOURNEY_SERVICE_PROFILE -e KB_JOURNEY_CASES -e KB_JOURNEY_SELECTED_STEPS \
    -e KB_JOURNEY_PREREQUISITE_STEPS -e KB_JOURNEY_PREREQUISITE_FLOWS \
    -e KB_JOURNEY_SELECTED_SOURCES runner < /dev/null; then
    status=1
  fi
}

status=0
if [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
  run_selector "$KB_JOURNEY_ONLY"
else
  selectors_json="$(python3 e2e/journey/select_from_cases.py)"
  selectors="$(python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)))' <<< "$selectors_json")"
  if [[ -n "$selectors" ]]; then while IFS= read -r selector; do
    if ! run_selector "$selector"; then status=1; fi
  done <<< "$selectors"; fi
fi

echo "Journey matrix complete (exit $status)"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" && -f .journey/reports/journey-summary.md ]]; then
  cat .journey/reports/journey-summary.md >> "$GITHUB_STEP_SUMMARY"
fi
exit "$status"
