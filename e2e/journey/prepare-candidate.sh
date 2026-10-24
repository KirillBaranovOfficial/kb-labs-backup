#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/e2e/journey/compose-owner.sh"
source "$ROOT/e2e/journey/candidate-services.sh"
journey_compose_init "$ROOT"
trap journey_compose_cleanup EXIT
OUT="$ROOT/.journey"
PACKAGES="$ROOT/e2e/packages"
TAG="journey-candidate"
TARGET="$OUT/release/$TAG"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD)"

(
  cd "$ROOT"
  bash "$ROOT/e2e/scripts/check-frozen-lockfile.sh"
)

node "$ROOT/e2e/scripts/build-pack-extras.mjs" "$ROOT"
node "$ROOT/e2e/platform/validate-package-outputs.mjs" --build-outputs "$ROOT"

bash "$ROOT/e2e/journey/stage-runner-inputs.sh"

mkdir -p "$PACKAGES" "$TARGET"
node "$ROOT/e2e/platform/release-index-package-stage.mjs" --packages-dir "$PACKAGES" --output-dir "$TARGET"
rm -rf "$TARGET"
mkdir -p "$TARGET"
if [[ "${KB_JOURNEY_PACKAGES_READY:-0}" != "1" ]]; then
  rm -f "$PACKAGES"/*.tgz
  "$ROOT/e2e/scripts/pack-all.sh" "$PACKAGES"
fi

node "$ROOT/e2e/platform/validate-package-outputs.mjs" "$PACKAGES" "$ROOT"

NATIVE_GOOS=""
NATIVE_GOARCH=""
if [[ "${KB_JOURNEY_NATIVE_BINARY:-0}" == "1" ]]; then
  NATIVE_GOOS="$(cd "$ROOT/tools/kb-create" && go env GOOS)"
  NATIVE_GOARCH="$(cd "$ROOT/tools/kb-create" && go env GOARCH)"
fi
(
  cd "$ROOT/tools/kb-create"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT" \
    -o "$TARGET/kb-create-linux-amd64" .
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT" \
    -o "$TARGET/kb-create-linux-arm64" .
  if [[ -n "$NATIVE_GOOS" && "$NATIVE_GOOS/$NATIVE_GOARCH" != "linux/amd64" && "$NATIVE_GOOS/$NATIVE_GOARCH" != "linux/arm64" ]]; then
    GOOS="$NATIVE_GOOS" GOARCH="$NATIVE_GOARCH" CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT" \
      -o "$TARGET/kb-create-$NATIVE_GOOS-$NATIVE_GOARCH" .
  fi
  # The installer artifact targets the Linux runner; the sealer runs on the
  # machine preparing the candidate (macOS locally, Linux in CI).
  CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
    -o "$OUT/kb-create-release-index" ./v2/cmd/kb-create-release-index
)

BINARY_FILES=(kb-create-linux-amd64 kb-create-linux-arm64)
if [[ -n "${NATIVE_GOOS:-}" && "$NATIVE_GOOS/$NATIVE_GOARCH" != "linux/amd64" && "$NATIVE_GOOS/$NATIVE_GOARCH" != "linux/arm64" ]]; then
  BINARY_FILES+=("kb-create-$NATIVE_GOOS-$NATIVE_GOARCH")
fi
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TARGET" && sha256sum "${BINARY_FILES[@]}" > binary-checksums.txt)
else
  (cd "$TARGET" && shasum -a 256 "${BINARY_FILES[@]}" > binary-checksums.txt)
fi
BINARY_VERSION="$(node -p "require('$ROOT/core/runtime/package.json').version")"
node "$ROOT/tools/kb-create/scripts/prepare-binary-manifest.mjs" \
  --checksums "$TARGET/binary-checksums.txt" \
  --repository kb-labs-team/kb-labs \
  --release-tag "$TAG" \
  --version "$BINARY_VERSION" \
  --asset-base-url "${KB_JOURNEY_BINARY_BASE_URL:-http://origin:8080/journey-candidate}" \
  --output "$TARGET/binary-manifest.json"

node "$ROOT/e2e/platform/prepare-release-index.mjs" \
  --packages-dir "$PACKAGES" \
  --registry-manifest "$ROOT/e2e/platform/registry-manifest.json" \
  --sealer "$OUT/kb-create-release-index" \
  --binary-dir "$TARGET" \
  --binary-manifest "$TARGET/binary-manifest.json" \
  --binary-manifest-tool "$ROOT/tools/kb-create/scripts/prepare-binary-manifest.mjs" \
  --binary-checksums "$TARGET/binary-checksums.txt" \
  --release-config "$ROOT/tools/kb-create/config/platform-release.jsonc" \
  --binary-version "$BINARY_VERSION" \
  --source-revision "$COMMIT" \
  --prepare-script "$ROOT/tools/kb-create/scripts/prepare-release-index.mjs" \
  --output "$TARGET/release-index.json"

# Install the real candidate graph through kb-create's staging and package
# installer, then run the same built-entrypoint checker used by S-054.16.
RUNTIME_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kb-candidate-runtime-root.XXXXXX")"
cleanup() {
  local status=$?
  trap - EXIT
  if ! rm -rf "$RUNTIME_ROOT"; then
    echo "Could not remove candidate runtime temp root $RUNTIME_ROOT." >&2
    (( status != 0 )) || status=1
  fi
  if ! journey_compose_cleanup; then
    echo "Could not remove journey Compose overlay temp state." >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT
RUNTIME_CONTENT_ROOT="$(journey_install_candidate_runtime "$TARGET/release-index.json" "$PACKAGES" "$RUNTIME_ROOT")"
node "$ROOT/e2e/journey/sdk_runtime_import_check.mjs" "$RUNTIME_CONTENT_ROOT"

(
  cd "$TARGET"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${BINARY_FILES[@]}" release-index.json release-index-update.json release-index-update-jump.json release-index-update-minor.json release-index-update-failed-host.json release-index-update-healthy-host.json release-index-update-bad-checksum.json release-index-update-config-failure.json release-index-update-doctor-failure.json release-index-canary.json > checksums.txt
  else
    shasum -a 256 "${BINARY_FILES[@]}" release-index.json release-index-update.json release-index-update-jump.json release-index-update-minor.json release-index-update-failed-host.json release-index-update-healthy-host.json release-index-update-bad-checksum.json release-index-update-config-failure.json release-index-update-doctor-failure.json release-index-canary.json > checksums.txt
  fi
)
echo "Prepared $TARGET"
