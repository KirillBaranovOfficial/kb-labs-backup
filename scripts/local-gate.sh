#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-}"
shift || true
DRY_RUN=0
PACKAGES=""
while (($#)); do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --packages) (($# >= 2)) || { echo "local-gate: --packages needs a comma-separated value" >&2; exit 2; }; PACKAGES="$2"; shift ;;
    *) echo "local-gate: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
[[ "$MODE" == quick || "$MODE" == batch ]] || { echo "Usage: scripts/local-gate.sh quick [--packages a,b] [--dry-run] | batch [--dry-run]" >&2; exit 2; }

if [[ "$MODE" == quick ]]; then
  if [[ -z "$PACKAGES" ]]; then
    git rev-parse --verify local/main >/dev/null 2>&1 || { echo "local-gate: local/main is required; update the local main ref, then rerun this command" >&2; exit 2; }
    mapfile -t changed_files < <({ git diff --name-only local/main...HEAD; git diff --name-only; git diff --name-only --cached; } | sort -u)
    declare -A found=()
    for file in "${changed_files[@]}"; do
      dir="$(dirname "$file")"
      while [[ "$dir" != . && "$dir" != / ]]; do
        if [[ -f "$dir/package.json" ]]; then found["$dir"]=1; break; fi
        dir="$(dirname "$dir")"
      done
    done
    for dir in "${!found[@]}"; do
      name="$(node -p "require('./$dir/package.json').name || ''")"
      [[ -z "$name" ]] || PACKAGES+="${PACKAGES:+,}$name"
    done
  fi
  [[ -n "$PACKAGES" ]] || { echo "local-gate: no changed packages found; use --packages a,b to select packages" >&2; exit 2; }
  IFS=, read -r -a package_list <<< "$PACKAGES"
  if ((DRY_RUN)); then
    printf 'quick packages: %s\n' "$PACKAGES"
    for package in "${package_list[@]}"; do
      echo "pnpm --filter $package run type-check (when declared)"
      echo "pnpm --filter $package run test (when declared)"
    done
    echo "go test changed tools/kb-create packages with an isolated HOME"
    exit 0
  fi
  for package in "${package_list[@]}"; do
    kb-devkit run type-check test --packages "$package"
  done
  go_dirs=()
  while IFS= read -r file; do
    [[ "$file" == tools/kb-create/*/*.go ]] || continue
    dir="$(dirname "$file")"; [[ " ${go_dirs[*]:-} " == *" $dir "* ]] || go_dirs+=("$dir")
  done < <({ git diff --name-only local/main...HEAD; git diff --name-only; git diff --name-only --cached; } | sort -u)
  if ((${#go_dirs[@]})); then
    isolated_home="$(mktemp -d)"
    trap 'chmod -R u+w "$isolated_home" 2>/dev/null || true; rm -rf "$isolated_home"' EXIT
    for dir in "${go_dirs[@]}"; do (cd "$ROOT/$dir" && env -u KB_PLATFORM_ROOT -u KB_HOME -u KB_NET_OFFSET HOME="$isolated_home" go test ./...); done
  fi
  exit 0
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$ROOT/.kb/local-gate/$STAMP"
if ((DRY_RUN)); then
  printf '%s\n' \
    '1. pnpm install --frozen-lockfile' \
    '2. kb-devkit run build --affected (diff against local/main)' \
    '3. kb-devkit run lint type-check test --affected' \
    '4. isolated-HOME Go tests for tools/kb-create' \
    '5. pnpm audit --audit-level moderate' \
    '6. gitleaks detect --source . --config .gitleaks.toml --redact --no-banner' \
    '7. colima start' \
    '8. KB_JOURNEY_ONLY=service-local:explore e2e/journey/run.sh --no-build' \
    '9. KB_JOURNEY_ONLY=service-secured:explore e2e/journey/run.sh --no-build' \
    '10. aggregate journey JSON with e2e/journey/nightly_report.py' \
    '11. colima stop and remove task-owned temporary resources'
  exit 0
fi
mkdir -p "$OUT"
REPORT="$OUT/report.md"; JSON="$OUT/report.json"
START="$(date -u +%FT%TZ)"; FIRST_FAILURE=""; STATUS=0; STAGES=()
cleanup() {
  local result=$?
  if [[ -n "${COLIMA_STARTED:-}" ]]; then colima stop || STATUS=1; fi
  if [[ -n "${TEMP_HOME:-}" ]]; then chmod -R u+w "$TEMP_HOME" 2>/dev/null || true; rm -rf "$TEMP_HOME"; fi
  python3 - "$JSON" "$START" "$FIRST_FAILURE" "$STATUS" "${STAGES[@]}" <<'PY'
import json,sys
path,start,first,status,*stages=sys.argv[1:]
json.dump({'started_at':start,'status':'PASS' if status=='0' else 'FAIL','first_failure':first or None,'stages':[{'name':x.split('|',1)[0],'status':x.split('|',1)[1]} for x in stages]},open(path,'w'),indent=2)
PY
  { echo "# Local gate report"; echo; echo "Started: $START"; echo "Status: $([[ $STATUS == 0 ]] && echo PASS || echo FAIL)"; echo "First failing step: ${FIRST_FAILURE:-none}"; echo; echo '| Stage | Status |'; echo '|---|---|'; for item in "${STAGES[@]}"; do printf '| %s | %s |\n' "${item%%|*}" "${item##*|}"; done; echo; echo "Artifacts: $OUT"; } > "$REPORT"
  exit "$result"
}
trap cleanup EXIT INT TERM
stage() { local name="$1"; shift; echo "==> $name"; if "$@"; then STAGES+=("$name|PASS"); else STAGES+=("$name|FAIL"); FIRST_FAILURE="${FIRST_FAILURE:-$name}"; STATUS=1; return 1; fi; }
stage 'frozen install' pnpm install --frozen-lockfile || exit 1
stage 'affected build' env KB_DEVKIT_DIFF_BASE=local/main kb-devkit run build --affected || exit 1
stage 'affected lint type-check tests' env KB_DEVKIT_DIFF_BASE=local/main kb-devkit run lint type-check test --affected || exit 1
TEMP_HOME="$(mktemp -d)"
stage 'kb-create Go tests' bash -c 'cd tools/kb-create && env -u KB_PLATFORM_ROOT -u KB_HOME -u KB_NET_OFFSET HOME="$1" go test ./v2/...' _ "$TEMP_HOME" || exit 1
stage 'dependency audit' pnpm audit --audit-level moderate || exit 1
stage 'secret scan' gitleaks detect --source . --config .gitleaks.toml --redact --no-banner || exit 1
stage 'Colima start' colima start || exit 1
COLIMA_STARTED=1
stage 'local install and service journey' env KB_JOURNEY_ONLY=service-local:explore e2e/journey/run.sh --no-build || exit 1
stage 'secured install and service journey' env KB_JOURNEY_ONLY=service-secured:explore e2e/journey/run.sh --no-build || exit 1
stage 'nightly journey aggregation' python3 e2e/journey/nightly_report.py --results .journey/reports --json "$OUT/capabilities.json" --summary "$OUT/capabilities.md" || exit 1
exit 0
