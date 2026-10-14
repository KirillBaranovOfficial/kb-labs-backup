#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export KB_JOURNEY_WORKSPACE_SOURCE="$ROOT"
export KB_JOURNEY_GIT_REVISION="$(git rev-parse HEAD)"

no_build=0
resolve_only=0
prepare_only=0
compose_action=""
for option in "$@"; do
  case "$option" in
    --no-build) no_build=1 ;;
    --resolve-only) resolve_only=1 ;;
    --prepare-only) prepare_only=1 ;;
    --down|--render-compose-config)
      if [[ -n "$compose_action" ]]; then
        echo "Journey compose actions --down and --render-compose-config cannot be combined." >&2
        exit 2
      fi
      compose_action="$option"
      ;;
    *) echo "Unknown journey option: $option" >&2; exit 2 ;;
  esac
done
if (( resolve_only && prepare_only )); then
  echo "Journey options --resolve-only and --prepare-only cannot be combined." >&2
  exit 2
fi
if [[ -n "$compose_action" ]] && (( resolve_only || prepare_only )); then
  echo "Journey compose actions cannot be combined with --resolve-only or --prepare-only." >&2
  exit 2
fi

if (( resolve_only )); then
  if [[ -n "${KB_JOURNEY_CASES:-}" ]]; then
    python3 e2e/journey/select_from_cases.py
  elif [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
    python3 e2e/journey/dispatch_registry.py --dispatch "$KB_JOURNEY_ONLY"
  else
    python3 e2e/journey/select_from_cases.py
  fi
  exit
fi

COMPOSE=()
COMPOSE_AVAILABLE=0
ACTIVE_MONITORS=()
MONITORED_CONTAINER_IDS=()
if command docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
  COMPOSE_AVAILABLE=1
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
  COMPOSE_AVAILABLE=1
fi
if [[ "${KB_JOURNEY_CI:-0}" == "1" ]]; then
  COMPOSE_FILES=(-f e2e/docker-compose.yml -f e2e/docker-compose.ci.yml -f e2e/journey/docker-compose.yml)
else
  COMPOSE_FILES=(-f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml)
fi
if (( COMPOSE_AVAILABLE )); then
  COMPOSE+=("${COMPOSE_FILES[@]}")
fi

if [[ -n "$compose_action" ]]; then
  if (( ! COMPOSE_AVAILABLE )); then
    echo "Docker Compose is required for journey stack operations; install Docker Compose v2 (the 'docker compose' plugin) or the standalone 'docker-compose' command." >&2
    exit 127
  fi
  if [[ "$compose_action" == "--down" ]]; then
    "${COMPOSE[@]}" down --volumes --remove-orphans
  else
    "${COMPOSE[@]}" config --format json
  fi
  exit $?
fi

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  local monitor_pid monitor_status
  for monitor_pid in "${ACTIVE_MONITORS[@]}"; do
    if kill -0 "$monitor_pid" 2>/dev/null; then
      if kill "$monitor_pid" 2>/dev/null; then
        monitor_status=143
      else
        monitor_status=0
      fi
      if wait "$monitor_pid"; then
        :
      else
        local wait_status=$?
        if (( wait_status != 143 || monitor_status == 0 )); then
          echo "Journey container metrics monitor $monitor_pid failed with exit $wait_status. Next: cat .journey/reports/container-metrics.jsonl" >&2
          (( status != 0 )) || status=1
        fi
      fi
    elif ! wait "$monitor_pid"; then
      echo "Journey container metrics monitor $monitor_pid failed. Next: cat .journey/reports/container-metrics.jsonl" >&2
      (( status != 0 )) || status=1
    fi
  done
  if (( COMPOSE_AVAILABLE )) && [[ -n "${RUN_STARTED_AT:-}" ]]; then
    local container_ids=() container_id
    local container_listing
    if container_listing=$("${COMPOSE[@]}" ps -aq --all); then
      while IFS= read -r container_id; do
        [[ -z "$container_id" ]] || container_ids+=(--container "$container_id")
      done <<< "$container_listing"
      if ! python3 e2e/journey/container_outcomes.py --report-dir .journey/reports \
        --since "$RUN_STARTED_AT" "${container_ids[@]}"; then
        echo "Journey container outcome capture failed. Next: cat .journey/reports/container-outcomes.json" >&2
        (( status != 0 )) || status=1
      fi
    else
      echo "Journey container outcome capture failed: Compose could not list containers before teardown. Next: docker compose -f e2e/docker-compose.yml -f e2e/journey/docker-compose.yml ps --all" >&2
      (( status != 0 )) || status=1
    fi
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" && -f .journey/reports/journey-summary.md ]]; then
      cat .journey/reports/journey-summary.md >> "$GITHUB_STEP_SUMMARY"
    fi
  fi
  if (( COMPOSE_AVAILABLE )) && ! "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null; then
    echo "Journey Compose cleanup failed; run 'e2e/journey/run.sh --down' to remove the journey stack." >&2
    (( status != 0 )) || status=1
  fi
  exit "$status"
}

mkdir -p .journey/reports
export KB_JOURNEY_INVOCATIONS="$ROOT/.journey/reports"

if (( prepare_only )); then
  e2e/journey/prepare-candidate.sh
  exit
fi

if (( ! COMPOSE_AVAILABLE )); then
  echo "Docker Compose is required for journey runs; install Docker Compose v2 (the 'docker compose' plugin) or the standalone 'docker-compose' command." >&2
  exit 127
fi

if [[ -z "${KB_JOURNEY_ONLY:-}" ]]; then
  python3 e2e/journey/hermetic_cli.py -- node ./cli/bin/dist/bin.js qa cases validate
fi

if [[ -n "${KB_JOURNEY_CASES:-}" ]]; then
  selection_json="$(python3 e2e/journey/select_from_cases.py)"
  export KB_JOURNEY_SELECTED_STEPS="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["steps"]))' <<< "$selection_json")"
  export KB_JOURNEY_PREREQUISITE_STEPS="$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["prerequisiteSteps"]))' <<< "$selection_json")"
  export KB_JOURNEY_PREREQUISITE_FLOWS="$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["prerequisiteFlows"]))' <<< "$selection_json")"
  export KB_JOURNEY_SELECTED_OWNING_STEPS="$(python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["selectedOwningSteps"], separators=(",",":")))' <<< "$selection_json")"
  export KB_JOURNEY_SELECTED_CAPABILITIES="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["capabilities"]))' <<< "$selection_json")"
  selected_selectors="$(python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)["runners"]))' <<< "$selection_json")"
else
  unset KB_JOURNEY_SELECTED_STEPS
  unset KB_JOURNEY_PREREQUISITE_STEPS
  unset KB_JOURNEY_PREREQUISITE_FLOWS
  unset KB_JOURNEY_SELECTED_OWNING_STEPS
  unset KB_JOURNEY_SELECTED_CAPABILITIES
  unset KB_JOURNEY_NEEDED_STATES
fi

dispatch_selector() {
  local selector="$1"
  python3 e2e/journey/dispatch_registry.py --dispatch "$selector"
}

run_selector() {
  local selectors="$1" selector runner_json had_profile=0 saved_profile="${KB_JOURNEY_SERVICE_PROFILE:-}"
  [[ "${KB_JOURNEY_SERVICE_PROFILE+x}" == "x" ]] && had_profile=1
  while IFS= read -r selector; do
    [[ -z "$selector" ]] && continue
    runner_json="$(dispatch_selector "$selector")"
    while IFS=$'\t' read -r runner_selector front scenario variant profile; do
      unset KB_JOURNEY_SERVICE_PROFILE
      if [[ -n "$profile" ]]; then export KB_JOURNEY_SERVICE_PROFILE="$profile"; fi
      run_journey "$runner_selector" "$front" "$scenario" "$variant"
    done < <(python3 -c 'import json,sys; print("\n".join("\t".join((d["selector"],d["front_door"],d["scenario"],d["variant"],d["service_profile"])) for d in json.load(sys.stdin)))' <<< "$runner_json")
  done <<< "$(printf '%s' "$selectors" | tr ',' '\n')"
  if (( had_profile )); then export KB_JOURNEY_SERVICE_PROFILE="$saved_profile"; else unset KB_JOURNEY_SERVICE_PROFILE; fi
}

trap cleanup EXIT INT TERM

RUN_STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if (( ! no_build )); then
  pnpm install --frozen-lockfile
  kb-devkit run build --affected
  e2e/journey/prepare-candidate.sh
fi

start_container_monitor() {
  python3 e2e/journey/container_outcomes.py --monitor "$1" --report-dir .journey/reports &
  ACTIVE_MONITORS+=("$!")
  MONITORED_CONTAINER_IDS+=("$1")
}

start_project_container_monitors() {
  local container_id container_listing
  container_listing=$("${COMPOSE[@]}" ps -aq --all)
  while IFS= read -r container_id; do
    [[ -z "$container_id" ]] && continue
    case " ${MONITORED_CONTAINER_IDS[*]} " in
      *" $container_id "*) ;;
      *) start_container_monitor "$container_id" ;;
    esac
  done <<< "$container_listing"
}

mkdir -p .journey/reports
rm -f .journey/reports/journey-summary.md
find .journey/reports -maxdepth 1 -type d -name 'platform-state-*' -exec rm -rf {} +
"${COMPOSE[@]}" build publisher runner idp-stub
"${COMPOSE[@]}" up -d --wait verdaccio
if "${COMPOSE[@]}" exec -T verdaccio sh -s -- /verdaccio/storage < e2e/journey/storage_has_packages.sh >/dev/null 2>&1; then
  echo "WARNING: reusing non-empty Verdaccio storage; existing package versions must match the local tarballs."
fi
publisher_id=$("${COMPOSE[@]}" run -d --name "journey-publisher-$$" publisher)
start_project_container_monitors
publisher_exit=$(docker wait "$publisher_id")
if [[ "$publisher_exit" != "0" ]]; then
  echo "Journey package publisher container exited $publisher_exit. Next: cat .journey/reports/container-outcomes.json" >&2
  exit 1
fi

run_journey() {
  local runner_selector="$1" front_door="$2" scenario="$3" variant="$4"
  local container_name="journey-runner-$$-${RUN_INDEX}" container_id container_exit
  RUN_INDEX=$((RUN_INDEX + 1))
  echo "==> $front_door / $scenario / $variant"
  if ! container_id=$(KB_JOURNEY_RUNNER_SELECTOR="$runner_selector" KB_JOURNEY_SCENARIO="$scenario" KB_JOURNEY_FRONT_DOOR="$front_door" KB_JOURNEY_VARIANT="$variant" \
    "${COMPOSE[@]}" run -d --name "$container_name" -e KB_JOURNEY_SCENARIO -e KB_JOURNEY_FRONT_DOOR -e KB_JOURNEY_VARIANT \
    -e KB_JOURNEY_RUNNER_SELECTOR -e KB_JOURNEY_SERVICE_PROFILE -e KB_JOURNEY_CASES -e KB_JOURNEY_SELECTED_STEPS \
    -e KB_JOURNEY_PREREQUISITE_STEPS -e KB_JOURNEY_PREREQUISITE_FLOWS \
    -e KB_JOURNEY_SELECTED_OWNING_STEPS -e KB_JOURNEY_SELECTED_CAPABILITIES runner < /dev/null); then
    status=1
    return
  fi
  start_container_monitor "$container_id"
  start_project_container_monitors
  if ! container_exit=$(docker wait "$container_id"); then
    echo "Could not wait for journey runner container $container_id. Next: cat .journey/reports/container-outcomes.json" >&2
    status=1
  elif [[ "$container_exit" != "0" ]]; then
    echo "Journey runner container $container_id exited $container_exit. Next: cat .journey/reports/container-outcomes.json" >&2
    status=1
  fi
}

RUN_INDEX=0
status=0
if [[ -n "${KB_JOURNEY_CASES:-}" ]]; then
  run_selector "$selected_selectors"
elif [[ -n "${KB_JOURNEY_ONLY:-}" ]]; then
  run_selector "$KB_JOURNEY_ONLY"
else
  selectors_json="$(python3 e2e/journey/select_from_cases.py)"
  selectors="$(python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)))' <<< "$selectors_json")"
  if [[ -n "$selectors" ]]; then while IFS= read -r selector; do
    if ! run_selector "$selector"; then status=1; fi
  done <<< "$selectors"; fi
fi

echo "Journey matrix complete (exit $status)"
exit "$status"
