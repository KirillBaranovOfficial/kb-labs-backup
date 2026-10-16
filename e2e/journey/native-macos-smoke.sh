#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export KB_JOURNEY_WORKSPACE_SOURCE="$ROOT"
source "$ROOT/e2e/journey/candidate-services.sh"

native_root="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/native-installer-smoke.XXXXXX")"
export HOME="$native_root/home"
mkdir -p "$HOME"
unset KB_PLATFORM_ROOT KB_HOME KB_NET_OFFSET
export NPM_CONFIG_USERCONFIG="$HOME/.npmrc"
export NPM_CONFIG_REGISTRY=http://127.0.0.1:4873
export KB_JOURNEY_NATIVE_ROOT="$native_root"

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if ! journey_stop_host_services; then
    (( status != 0 )) || status=1
  fi
  rm -rf "$native_root"
  exit "$status"
}
trap cleanup EXIT INT TERM

journey_start_host_services
node e2e/journey/native-macos-install.mjs
