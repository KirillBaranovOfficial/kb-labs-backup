#!/usr/bin/env bash

# Shared candidate origin and package-registry lifecycle for container and
# native installer journeys. Source this file from the owning journey entry.

JOURNEY_HOST_PIDS=()
JOURNEY_HOST_STATE=""
JOURNEY_HOSTS_ADDED=0
JOURNEY_PUBLISHER_ID=""

journey_start_container_services() {
  if [[ $# -eq 0 ]]; then
    echo "Journey container services need the Compose command and files. Next: call e2e/journey/run.sh." >&2
    return 2
  fi
  local -a compose=("$@")
  "${compose[@]}" up -d --wait origin verdaccio
  if "${compose[@]}" exec -T verdaccio sh -s -- /verdaccio/storage < e2e/journey/storage_has_packages.sh >/dev/null 2>&1; then
    echo "WARNING: reusing non-empty Verdaccio storage; existing package versions must match the local tarballs."
  fi
  JOURNEY_PUBLISHER_ID=$("${compose[@]}" run -d --name "journey-publisher-$$" publisher)
}

journey_wait_container_publisher() {
  local exit_code log_path
  if [[ -z "$JOURNEY_PUBLISHER_ID" ]]; then
    echo "Journey package publisher was not started. Next: call journey_start_container_services from e2e/journey/run.sh." >&2
    return 2
  fi
  exit_code=$(docker wait "$JOURNEY_PUBLISHER_ID")
  if [[ "$exit_code" != "0" ]]; then
    if ! log_path=$(docker logs "$JOURNEY_PUBLISHER_ID" 2>&1 | python3 e2e/journey/container_outcomes.py --report-dir .journey/reports --write-log-name publisher); then
      echo "Journey package publisher container exited $exit_code; its logs could not be captured or saved. Next: docker logs $JOURNEY_PUBLISHER_ID" >&2
      return 1
    fi
    echo "Journey package publisher container exited $exit_code. Next: cat $log_path" >&2
    return 1
  fi
}

journey_stop_container_services() {
  local status=0
  if [[ -n "$JOURNEY_PUBLISHER_ID" ]]; then
    if ! docker rm -f "$JOURNEY_PUBLISHER_ID" >/dev/null; then
      echo "Could not remove journey publisher container $JOURNEY_PUBLISHER_ID. Next: docker rm -f $JOURNEY_PUBLISHER_ID" >&2
      status=1
    fi
    JOURNEY_PUBLISHER_ID=""
  fi
  [[ $# -eq 0 ]] && return "$status"
  local -a compose=("$@")
  if ! "${compose[@]}" stop publisher verdaccio origin >/dev/null; then
    echo "Could not stop journey publisher, Verdaccio, or origin services. Next: e2e/journey/run.sh --down" >&2
    status=1
  fi
  return "$status"
}

journey_start_host_services() {
  local root="${KB_JOURNEY_WORKSPACE_SOURCE:-$(pwd)}"
  local temp_root="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
  local host_line="127.0.0.1 origin verdaccio"
  JOURNEY_HOST_STATE=$(mktemp -d "$temp_root/kb-journey-host-services.XXXXXX")
  mkdir -p "$JOURNEY_HOST_STATE/storage"
  sed "s|/verdaccio/storage|$JOURNEY_HOST_STATE/storage|g" "$root/e2e/verdaccio/config.yaml" > "$JOURNEY_HOST_STATE/verdaccio.yaml"

  if ! sudo grep -Fqx "$host_line" /etc/hosts; then
    sudo python3 -c 'from pathlib import Path; p=Path("/etc/hosts"); p.write_text(p.read_text()+"127.0.0.1 origin verdaccio\n")'
    JOURNEY_HOSTS_ADDED=1
  fi

  python3 -m http.server 8080 --directory "$root/.journey/release" > "$JOURNEY_HOST_STATE/origin.log" 2>&1 &
  JOURNEY_HOST_PIDS+=("$!")
  npx --yes verdaccio@5 --config "$JOURNEY_HOST_STATE/verdaccio.yaml" --listen 127.0.0.1:4873 > "$JOURNEY_HOST_STATE/verdaccio.log" 2>&1 &
  JOURNEY_HOST_PIDS+=("$!")

  local attempt
  for attempt in {1..60}; do
    if curl --fail --silent http://127.0.0.1:8080/journey-candidate/release-index.json >/dev/null \
      && curl --fail --silent http://127.0.0.1:4873/-/ping >/dev/null; then
      PUBLISH_PACKAGES_DIR="$root/e2e/packages" VERDACCIO_URL=http://127.0.0.1:4873 sh "$root/e2e/publisher/publish.sh"
      return
    fi
    if ! kill -0 "${JOURNEY_HOST_PIDS[0]}" 2>/dev/null || ! kill -0 "${JOURNEY_HOST_PIDS[1]}" 2>/dev/null; then
      echo "Candidate origin or Verdaccio stopped before readiness." >&2
      printf 'Next: cat %q %q\n' "$JOURNEY_HOST_STATE/origin.log" "$JOURNEY_HOST_STATE/verdaccio.log" >&2
      return 1
    fi
    sleep 1
  done
  echo "Candidate origin and Verdaccio did not become ready within 60 seconds." >&2
  printf 'Next: cat %q %q\n' "$JOURNEY_HOST_STATE/origin.log" "$JOURNEY_HOST_STATE/verdaccio.log" >&2
  return 1
}

journey_stop_host_services() {
  local status=0 pid wait_status
  for pid in ${JOURNEY_HOST_PIDS[@]+"${JOURNEY_HOST_PIDS[@]}"}; do
    if kill -0 "$pid" 2>/dev/null; then
      if ! kill "$pid" 2>/dev/null; then
        printf 'Could not stop candidate service process %s. Next: kill -KILL %s\n' "$pid" "$pid" >&2
        status=1
        continue
      fi
    fi
    if wait "$pid" 2>/dev/null; then
      :
    else
      wait_status=$?
      if (( wait_status != 143 && wait_status != 127 )); then
        echo "Candidate service process $pid exited with status $wait_status." >&2
        status=1
      fi
    fi
  done
  JOURNEY_HOST_PIDS=()
  if (( JOURNEY_HOSTS_ADDED )); then
    sudo python3 -c 'from pathlib import Path; p=Path("/etc/hosts"); line="127.0.0.1 origin verdaccio"; p.write_text("".join(row for row in p.read_text().splitlines(keepends=True) if row.rstrip("\n") != line))'
    JOURNEY_HOSTS_ADDED=0
  fi
  if [[ -n "$JOURNEY_HOST_STATE" ]]; then
    rm -rf "$JOURNEY_HOST_STATE"
    JOURNEY_HOST_STATE=""
  fi
  return "$status"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  echo "Source e2e/journey/candidate-services.sh from a journey entry." >&2
  exit 2
fi
