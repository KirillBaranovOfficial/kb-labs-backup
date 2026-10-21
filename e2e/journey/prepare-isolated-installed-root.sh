#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_BASE="${TMPDIR:-/tmp}"
RUN_HOME="$(mktemp -d "$TMP_BASE/kb-journey-home.XXXXXX")"
PLATFORM_ROOT="$(mktemp -d "$TMP_BASE/kb-journey-platform.XXXXXX")"
REGISTRY_PIPE="$RUN_HOME/registry.pipe"
PLAN_FILE=""
KEEP_ROOT=0
REGISTRY_PID=""
INSTALL_ATTEMPTED=0
KB_CREATE=""
chosen_port=""
BASELINE_4000="$(lsof -nP -iTCP:4000 -sTCP:LISTEN -Fp 2>/dev/null || true)"

cleanup() {
  if [[ "$INSTALL_ATTEMPTED" == "1" && -x "$KB_CREATE" ]]; then
    local stop_output
    stop_output="$(mktemp "$TMP_BASE/kb-stop.XXXXXX")"
    if ! "$KB_CREATE" stop --json --platform-root "$PLATFORM_ROOT" >"$stop_output"; then
      echo "Cleanup could not stop the host from its platform-root record: $PLATFORM_ROOT" >&2
      printf 'Retry with: %q stop --json --platform-root %q\n' "$KB_CREATE" "$PLATFORM_ROOT" >&2
      KEEP_ROOT=1
    fi
    if [[ -n "$chosen_port" ]] && lsof -nP -iTCP:"$chosen_port" -sTCP:LISTEN -t >/dev/null 2>&1; then
      echo "Cleanup found a listener still bound to $chosen_port; preserving root and HOME for diagnosis." >&2
      printf 'Inspect with: %q doctor --json --platform-root %q\n' "$KB_CREATE" "$PLATFORM_ROOT" >&2
      KEEP_ROOT=1
    fi
    rm -f "$stop_output"
  fi
  if [[ -n "$REGISTRY_PID" ]]; then
    if kill -0 "$REGISTRY_PID" 2>/dev/null; then
      kill "$REGISTRY_PID"
      wait "$REGISTRY_PID"
    fi
  fi
  if [[ -n "$PLAN_FILE" ]]; then rm -f "$PLAN_FILE"; fi
  if [[ -p "$REGISTRY_PIPE" ]]; then rm -f "$REGISTRY_PIPE"; fi
  if [[ "$KEEP_ROOT" != "1" ]]; then
    chmod -R u+w "$RUN_HOME" "$PLATFORM_ROOT" 2>/dev/null || true
    rm -rf "$RUN_HOME" "$PLATFORM_ROOT"
  fi
}
trap cleanup EXIT

unset KB_PLATFORM_ROOT KB_HOME KB_NET_OFFSET GOFLAGS
export KB_JOURNEY_PACKAGES_READY="${KB_JOURNEY_PACKAGES_READY:-0}"
export PATH="/Users/kirillbaranov/.nvm/versions/node/v24.18.1/bin:$PATH"
export GOMODCACHE="$(go env GOMODCACHE)"
export HOME="$RUN_HOME"

mkfifo "$REGISTRY_PIPE"
node "$ROOT/e2e/journey/candidate-package-registry.mjs" "$ROOT/e2e/packages" "$ROOT/.journey/release/journey-candidate" >"$REGISTRY_PIPE" &
REGISTRY_PID=$!
if ! IFS= read -r REGISTRY_URL <"$REGISTRY_PIPE"; then
  echo "Candidate package registry exited before reporting its URL." >&2
  exit 1
fi

KB_JOURNEY_NATIVE_BINARY=1 \
KB_JOURNEY_REGISTRY_URL="$REGISTRY_URL" \
KB_JOURNEY_BINARY_BASE_URL="$REGISTRY_URL/journey-candidate" \
bash "$ROOT/e2e/journey/prepare-candidate.sh"
CANDIDATE="$ROOT/.journey/release/journey-candidate"
KB_CREATE="$RUN_HOME/kb-create"
(cd "$ROOT/tools/kb-create" && go build -trimpath -o "$KB_CREATE" .)

PLAN_FILE="$(mktemp "$TMP_BASE/kb-plan.XXXXXX")"
for port in $(seq 4500 100 9900); do
  gateway="http://127.0.0.1:$port"
  if ! "$KB_CREATE" plan --json --index "$CANDIDATE/release-index.json" --platform-root "$PLATFORM_ROOT" --gateway-url "$gateway" --registry "$REGISTRY_URL" >"$PLAN_FILE"; then
    error_code="$(jq -r '.error.code // .data.error.code // empty' "$PLAN_FILE")"
    if [[ "$error_code" == "KB_INSTALL_PORT_IN_USE" ]]; then
      continue
    fi
    error_message="$(jq -r '.error.message // .data.error.message // "candidate plan failed"' "$PLAN_FILE")"
    error_hint="$(jq -r '.error.hint // .data.error.hint // empty' "$PLAN_FILE")"
    printf 'Candidate plan failed (%s): %s\n' "${error_code:-unknown}" "$error_message" >&2
    [[ -z "$error_hint" ]] || printf '%s\n' "$error_hint" >&2
    printf 'Next: bash %q\n' "$ROOT/e2e/journey/prepare-isolated-installed-root.sh" >&2
    exit 1
  fi
  jq -e --arg gateway "$gateway" '
    .data.plan.request.gatewayUrl == $gateway and
    .data.plan.host.healthUrl == ($gateway + "/health") and
    ([.data.plan.serviceGraph.services[] | select(.port != null) | .port] | length > 0 and all(. >= 1 and . <= 65535 and . != 4000))
  ' "$PLAN_FILE" >/dev/null
  planned_ports=()
  while IFS= read -r planned_port; do planned_ports+=("$planned_port"); done < <(jq -r '.data.plan.serviceGraph.services[] | select(.port != null) | .port' "$PLAN_FILE")
  ports_free=1
  for planned_port in "${planned_ports[@]}"; do
    if lsof -nP -iTCP:"$planned_port" -sTCP:LISTEN -t >/dev/null 2>&1; then
      ports_free=0
      break
    fi
  done
  if [[ "$ports_free" == "1" ]]; then
    chosen_port="$port"
    break
  fi
done
rm -f "$PLAN_FILE"
PLAN_FILE=""
if [[ -z "$chosen_port" ]]; then
  echo "No free Gateway port in 4500-9900 (steps of 100) has a free planned service graph." >&2
  printf 'Free one of the planned ports, then rerun: bash %q\n' "$ROOT/e2e/journey/prepare-isolated-installed-root.sh" >&2
  exit 1
fi

GATEWAY_URL="http://127.0.0.1:$chosen_port"
INSTALL_ATTEMPTED=1
"$KB_CREATE" install --json --index "$CANDIDATE/release-index.json" --platform-root "$PLATFORM_ROOT" --gateway-url "$GATEWAY_URL" --registry "$REGISTRY_URL"
after_install_4000="$(lsof -nP -iTCP:4000 -sTCP:LISTEN -Fp 2>/dev/null || true)"
if [[ "$after_install_4000" != "$BASELINE_4000" ]]; then
  "$KB_CREATE" stop --json --platform-root "$PLATFORM_ROOT"
  INSTALL_ATTEMPTED=0
  echo "The install changed the listener set on :4000 despite --gateway-url $GATEWAY_URL." >&2
  exit 1
fi
"$KB_CREATE" doctor --json --platform-root "$PLATFORM_ROOT"
curl --fail --silent --show-error "$GATEWAY_URL/health" >/dev/null
"$KB_CREATE" stop --json --platform-root "$PLATFORM_ROOT"
INSTALL_ATTEMPTED=0
if lsof -nP -iTCP:"$chosen_port" -sTCP:LISTEN -t >/dev/null 2>&1; then
  echo "Gateway still listens on $chosen_port after kb-create stop." >&2
  printf 'Retry the root-record stop with: %q stop --json --platform-root %q\n' "$KB_CREATE" "$PLATFORM_ROOT" >&2
  exit 1
fi
after_stop_4000="$(lsof -nP -iTCP:4000 -sTCP:LISTEN -Fp 2>/dev/null || true)"
if [[ "$after_stop_4000" != "$BASELINE_4000" ]]; then
  echo "The listener set on :4000 changed during isolated candidate install and stop." >&2
  exit 1
fi

KEEP_ROOT=1
printf 'Platform root: %s\nHOME: %s\nGateway URL: %s\nStop: %q stop --json --platform-root %q\nCleanup: rm -rf %q %q\n' \
  "$PLATFORM_ROOT" "$RUN_HOME" "$GATEWAY_URL" "$KB_CREATE" "$PLATFORM_ROOT" "$PLATFORM_ROOT" "$RUN_HOME"
