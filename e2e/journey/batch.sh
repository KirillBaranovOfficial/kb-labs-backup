#!/bin/sh
# batch.sh <output-dir> [baseline-label] — the shared product-evidence journey batch.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
OUT=${1:?usage: e2e/journey/batch.sh <output-dir> [baseline]}
BASELINE=${2:-${BATCH_BASELINE:-j28}}
BUILD_BASE=${KB_DEVKIT_BASE_REF:-local/main...HEAD}
case "$OUT" in /*) ;; *) OUT="$ROOT/$OUT" ;; esac
mkdir -p "$OUT"
SHARED_OUT="$OUT/shared"
mkdir -p "$SHARED_OUT"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  printf 'artifact_shared=%s\nartifact_local=%s\nartifact_secured=%s\n' \
    "$SHARED_OUT" "$OUT/local" "$OUT/secured" >> "$GITHUB_OUTPUT"
fi
: > "$SHARED_OUT/build.log"
cd "$ROOT"

commit=""
interrupted=0
batch_started_at=$(date +%s)
current_stage=""
current_stage_started=$batch_started_at
reset_seconds=0
build_seconds=0
unit_seconds=0
candidate_seconds=0
local_seconds=0
secured_seconds=0
total_seconds=0

start_stage() {
  current_stage=$1
  current_stage_started=$(date +%s)
}
finish_stage() {
  [ -n "$current_stage" ] || return 0
  stage_finished_at=$(date +%s)
  stage_elapsed=$((stage_finished_at - current_stage_started))
  case "$current_stage" in
    reset) reset_seconds=$stage_elapsed ;;
    build) build_seconds=$stage_elapsed ;;
    unit) unit_seconds=$stage_elapsed ;;
    candidate) candidate_seconds=$stage_elapsed ;;
    local) local_seconds=$stage_elapsed ;;
    secured) secured_seconds=$stage_elapsed ;;
  esac
  current_stage=""
}
write_metadata() {
  result=$1
  total_seconds=$(($(date +%s) - batch_started_at))
  for metadata_mode in shared local secured; do
    metadata_file="$SHARED_OUT/batch-metadata.json"
    metadata_mode_json=""
    if [ "$metadata_mode" != shared ]; then
      [ -d "$OUT/$metadata_mode" ] || continue
      metadata_file="$OUT/$metadata_mode/batch-metadata.json"
      metadata_mode_json=",\"mode\":\"$metadata_mode\""
    fi
    printf '{"commit":"%s","baseline":"%s","buildBase":"%s","result":"%s"%s,"stageDurationsSeconds":{"reset":%s,"build":%s,"unit":%s,"candidate":%s,"local":%s,"secured":%s,"total":%s}}\n' \
      "$commit" "$BASELINE" "$BUILD_BASE" "$result" "$metadata_mode_json" \
      "$reset_seconds" "$build_seconds" "$unit_seconds" "$candidate_seconds" \
      "$local_seconds" "$secured_seconds" "$total_seconds" > "$metadata_file"
  done
}
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  finish_stage
  if [ "$interrupted" -eq 1 ]; then result=INTERRUPTED
  elif [ "$status" -eq 0 ]; then result=PASS
  else result=FAIL
  fi
  printf '\n- Result: %s\n' "$result" >> "$SHARED_OUT/summary.md"
  write_metadata "$result"
  exit "$status"
}
trap cleanup EXIT
trap 'interrupted=1; exit 129' HUP
trap 'interrupted=1; exit 130' INT
trap 'interrupted=1; exit 143' TERM

commit=$(git rev-parse HEAD)
printf '{"commit":"%s","baseline":"%s","buildBase":"%s"}\n' "$commit" "$BASELINE" "$BUILD_BASE" > "$SHARED_OUT/run.json"
printf '# Journey batch\n\n- Commit: `%s`\n- Baseline: `%s`\n- Build base: `%s`\n' \
  "$commit" "$BASELINE" "$BUILD_BASE" > "$SHARED_OUT/summary.md"
record_failure_log() {
  printf '\n- Failed stage: `%s`\n- Stage log: `%s`\n' "$1" "$2" >> "$SHARED_OUT/summary.md"
}
validate_build_base_ref() {
  build_base_ref=$1
  if ! git rev-parse --verify --quiet "$build_base_ref^{commit}" >/dev/null 2>&1; then
    record_failure_log build build.log
    echo "affected-build base ref '$build_base_ref' is missing from this checkout; next: git fetch --all --prune" >&2
    exit 1
  fi
}
case "$BUILD_BASE" in
  *...*)
    build_base_left=${BUILD_BASE%%...*}
    build_base_right=${BUILD_BASE#*...}
    validate_build_base_ref "$build_base_left"
    validate_build_base_ref "$build_base_right"
    ;;
  *) validate_build_base_ref "$BUILD_BASE" ;;
esac
rm -f .journey/reports/*.json .journey/reports/*.log .journey/reports/*.md
rm -rf .journey/reports/invocations
start_stage reset
reset_rc=0
e2e/journey/run.sh --down > "$SHARED_OUT/reset.log" 2>&1 || reset_rc=$?
finish_stage
if [ "$reset_rc" -ne 0 ]; then
  record_failure_log reset reset.log
  echo "journey stack reset failed; see $SHARED_OUT/reset.log; next: e2e/journey/run.sh --down" >&2
  exit 1
fi
preflight_rc=0
"$ROOT/e2e/journey/preflight.sh" "$SHARED_OUT" "$BUILD_BASE" || preflight_rc=$?
if [ -s "$SHARED_OUT/stage-durations.env" ]; then
  . "$SHARED_OUT/stage-durations.env"
  build_seconds=$build
  candidate_seconds=$candidate
  unit_seconds=$unit
  rm -f "$SHARED_OUT/stage-durations.env"
fi
if [ "$preflight_rc" -ne 0 ]; then
  failure_stage=${failed_stage:-build}
  record_failure_log "$failure_stage" "$failure_stage.log"
  exit 1
fi
build_seconds=$build
candidate_seconds=$candidate
unit_seconds=$unit

batch_rc=0
for mode in local secured; do
  mode_out="$OUT/$mode"
  mkdir -p "$mode_out"
  start_stage "$mode"
  cp "$SHARED_OUT/reset.log" "$SHARED_OUT/build.log" "$SHARED_OUT/unit.log" "$SHARED_OUT/candidate.log" "$mode_out/"
  scenario="service-$mode:explore"
  rc=0
  KB_JOURNEY_ONLY="$scenario" e2e/journey/run.sh --no-build > "$mode_out/batch.log" 2>&1 || rc=$?
  if [ "$rc" -ne 0 ] && grep -q '^failed to solve:' "$mode_out/batch.log"; then
    mv "$mode_out/batch.log" "$mode_out/infra-failed.log"
    rm -f .journey/reports/*.json .journey/reports/*.log .journey/reports/*.md
    rm -rf .journey/reports/invocations
    rc=0
    KB_JOURNEY_ONLY="$scenario" e2e/journey/run.sh --no-build > "$mode_out/batch.log" 2>&1 || rc=$?
    echo "INFRA: first attempt failed during docker image build; retried once" >> "$mode_out/batch.log"
  fi
  printf 'exit=%s\n' "$rc" >> "$mode_out/batch.log"
  cp -R .journey/reports/. "$mode_out/"
  rm -f .journey/reports/*.json .journey/reports/*.log .journey/reports/*.md
  rm -rf .journey/reports/invocations
  finish_stage
  if [ "$rc" -ne 0 ]; then
    batch_rc=1
    record_failure_log "$mode" "$mode/batch.log"
  fi
done
cat >> "$SHARED_OUT/summary.md" <<SUMMARY
- Modes: local and secured
- Reports and logs: one directory per mode
SUMMARY
if [ "$batch_rc" -ne 0 ]; then
  echo "one or more journey modes failed; inspect $OUT/local/batch.log and $OUT/secured/batch.log" >&2
  exit 1
fi

exit 0
