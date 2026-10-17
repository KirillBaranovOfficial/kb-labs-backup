#!/bin/sh
# batch.sh <output-dir> [baseline-label] — the shared product-evidence journey batch.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
OUT=${1:?usage: e2e/journey/batch.sh <output-dir> [baseline]}
BASELINE=${2:-${BATCH_BASELINE:-j28}}
BUILD_BASE=${KB_DEVKIT_BASE_REF:-local/main...HEAD}
case "$OUT" in /*) ;; *) OUT="$ROOT/$OUT" ;; esac
mkdir -p "$OUT"
: > "$OUT/build.log"
cd "$ROOT"

work=$(mktemp -d "${TMPDIR:-/tmp}/kb-journey-batch.XXXXXX")
unit_home=""
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
  for metadata_mode in batch local secured; do
    metadata_file="$OUT/batch-metadata.json"
    metadata_mode_json=""
    if [ "$metadata_mode" != batch ]; then
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
  printf '\n- Result: %s\n' "$result" >> "$OUT/summary.md"
  write_metadata "$result"
  if [ -n "$unit_home" ] && [ -d "$unit_home" ]; then
    if ! chmod -R u+w "$unit_home"; then echo "could not make temporary HOME writable for cleanup: $unit_home" >&2; fi
    if ! rm -rf "$unit_home"; then echo "could not remove temporary HOME: $unit_home" >&2; fi
  fi
  if ! chmod -R u+w "$work"; then echo "could not make batch temp directory writable for cleanup: $work" >&2; fi
  if ! rm -rf "$work"; then echo "could not remove batch temp directory: $work" >&2; fi
  exit "$status"
}
trap cleanup EXIT
trap 'interrupted=1; exit 129' HUP
trap 'interrupted=1; exit 130' INT
trap 'interrupted=1; exit 143' TERM

commit=$(git rev-parse HEAD)
printf '{"commit":"%s","baseline":"%s","buildBase":"%s"}\n' "$commit" "$BASELINE" "$BUILD_BASE" > "$OUT/run.json"
printf '# Journey batch\n\n- Commit: `%s`\n- Baseline: `%s`\n- Build base: `%s`\n' \
  "$commit" "$BASELINE" "$BUILD_BASE" > "$OUT/summary.md"
record_failure_log() {
  printf '\n- Failed stage: `%s`\n- Stage log: `%s`\n' "$1" "$2" >> "$OUT/summary.md"
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
e2e/journey/run.sh --down > "$OUT/reset.log" 2>&1 || reset_rc=$?
finish_stage
if [ "$reset_rc" -ne 0 ]; then
  record_failure_log reset reset.log
  echo "journey stack reset failed; see $OUT/reset.log; next: e2e/journey/run.sh --down" >&2
  exit 1
fi
start_stage build
build_rc=0
(cd tools/kb-devkit && go build -o "$work/kb-devkit" .) > "$OUT/build.log" 2>&1 || build_rc=$?
if [ "$build_rc" -ne 0 ]; then
  finish_stage
  record_failure_log build build.log
  echo "could not build kb-devkit from this checkout; see $OUT/build.log; next: (cd tools/kb-devkit && go build .)" >&2
  exit 1
fi
if ! env KB_DEVKIT_BASE_REF="$BUILD_BASE" "$work/kb-devkit" run build --affected >> "$OUT/build.log" 2>&1; then
  finish_stage
  record_failure_log build build.log
  echo "devkit affected build failed; see $OUT/build.log; next: (cd tools/kb-devkit && KB_DEVKIT_BASE_REF='$BUILD_BASE' go run . run build --affected)" >&2
  exit 1
fi
output_check_rc=0
node "$ROOT/e2e/scripts/validate-package-outputs.mjs" --build-outputs "$ROOT" > "$OUT/release-build-output-check.log" 2>&1 || output_check_rc=$?
cat "$OUT/release-build-output-check.log" >> "$OUT/build.log"
if [ "$output_check_rc" -ne 0 ]; then
  finish_stage
  record_failure_log build build.log
  cat "$OUT/release-build-output-check.log" >&2
  echo "release package build outputs are missing; see $OUT/build.log" >&2
  exit 1
fi
finish_stage
unit_home=$(mktemp -d "${TMPDIR:-/tmp}/kb-batch-unit.XXXXXX")
unit_rc=0
start_stage unit
HOME="$unit_home" env -u KB_PLATFORM_ROOT -u KB_HOME -u KB_NET_OFFSET python3 -m unittest discover -s e2e/journey -p 'test_*.py' > "$OUT/unit.log" 2>&1 || unit_rc=$?
if ! chmod -R u+w "$unit_home"; then echo "could not make temporary HOME writable for cleanup: $unit_home" >&2; fi
if ! rm -rf "$unit_home"; then echo "could not remove temporary HOME: $unit_home" >&2; fi
unit_home=""
finish_stage
if [ "$unit_rc" -ne 0 ]; then
  record_failure_log unit unit.log
  echo "journey unit suite failed; see $OUT/unit.log; next: python3 -m unittest discover -s e2e/journey -p 'test_*.py'" >&2
  exit 1
fi
start_stage candidate
candidate_rc=0
e2e/journey/prepare-candidate.sh > "$OUT/candidate.log" 2>&1 || candidate_rc=$?
finish_stage
if [ "$candidate_rc" -ne 0 ]; then
  record_failure_log candidate candidate.log
  echo "journey candidate preparation failed; see $OUT/candidate.log; next: e2e/journey/prepare-candidate.sh" >&2
  exit 1
fi

batch_rc=0
for mode in local secured; do
  mode_out="$OUT/$mode"
  mkdir -p "$mode_out"
  start_stage "$mode"
  cp "$OUT/reset.log" "$OUT/build.log" "$OUT/unit.log" "$OUT/candidate.log" "$mode_out/"
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
cat >> "$OUT/summary.md" <<SUMMARY
- Modes: local and secured
- Reports and logs: one directory per mode
SUMMARY
if [ "$batch_rc" -ne 0 ]; then
  echo "one or more journey modes failed; inspect $OUT/local/batch.log and $OUT/secured/batch.log" >&2
  exit 1
fi

exit 0
