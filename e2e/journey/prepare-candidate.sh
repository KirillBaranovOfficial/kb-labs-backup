#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/.journey"
RUNNER_INPUTS="$OUT/runner-inputs"
PACKAGES="$ROOT/e2e/packages"
TAG="journey-candidate"
TARGET="$OUT/release/$TAG"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD)"

if [[ ! -f "$ROOT/core/plugin-contracts/dist/project-service-path.js" ]]; then
  echo 'The journey runner needs the shared project service URL builder. Run `corepack pnpm --filter @kb-labs/plugin-contracts... --workspace-concurrency=2 build` first.' >&2
  exit 2
fi

# The repository Docker context excludes dist/ globally. Stage the runner's
# shared builder under .journey, which is inside the context and not ignored by
# .dockerignore; Dockerfile.runner restores it at the path used by host runs.
mkdir -p "$RUNNER_INPUTS"
cp "$ROOT/core/plugin-contracts/dist/project-service-path.js" "$RUNNER_INPUTS/project-service-path.js"

mkdir -p "$PACKAGES" "$TARGET"
if [[ -f "$TARGET/update-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/update-packages/generated-packages.txt"
fi
if [[ -f "$TARGET/canary-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/canary-packages/generated-packages.txt"
fi
if [[ -f "$TARGET/jump-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/jump-packages/generated-packages.txt"
fi
if [[ -f "$TARGET/minor-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/minor-packages/generated-packages.txt"
fi
if [[ -f "$TARGET/failed-host-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/failed-host-packages/generated-packages.txt"
fi
if [[ -f "$TARGET/healthy-host-packages/generated-packages.txt" ]]; then
  while IFS= read -r filename; do
    [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
  done < "$TARGET/healthy-host-packages/generated-packages.txt"
fi
for candidate in config doctor; do
  package_list="$TARGET/staged-$candidate-packages/generated-packages.txt"
  if [[ -f "$package_list" ]]; then
    while IFS= read -r filename; do
      [[ -z "$filename" ]] || rm -f "$PACKAGES/$filename"
    done < "$package_list"
  fi
done
rm -rf "$TARGET"
mkdir -p "$TARGET"
if [[ "${KB_JOURNEY_PACKAGES_READY:-0}" != "1" ]]; then
  rm -f "$PACKAGES"/*.tgz
  "$ROOT/e2e/scripts/pack-all.sh" "$PACKAGES"
fi

node "$ROOT/e2e/scripts/validate-package-outputs.mjs" "$PACKAGES" "$ROOT"

(
  cd "$ROOT/tools/kb-create"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT" \
    -o "$TARGET/kb-create-linux-amd64" .
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.commit=$COMMIT" \
    -o "$TARGET/kb-create-linux-arm64" .
  # The installer artifact targets the Linux runner; the sealer runs on the
  # machine preparing the candidate (macOS locally, Linux in CI).
  CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
    -o "$OUT/kb-create-release-index" ./v2/cmd/kb-create-release-index
)

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TARGET" && sha256sum kb-create-linux-amd64 kb-create-linux-arm64 > binary-checksums.txt)
else
  (cd "$TARGET" && shasum -a 256 kb-create-linux-amd64 kb-create-linux-arm64 > binary-checksums.txt)
fi
BINARY_VERSION="$(node -p "require('$ROOT/core/runtime/package.json').version")"
node "$ROOT/tools/kb-create/scripts/prepare-binary-manifest.mjs" \
  --checksums "$TARGET/binary-checksums.txt" \
  --repository kb-labs-team/kb-labs \
  --release-tag "$TAG" \
  --version "$BINARY_VERSION" \
  --asset-base-url http://origin:8080/journey-candidate \
  --output "$TARGET/binary-manifest.json"

node "$ROOT/e2e/platform/prepare-release-index.mjs" \
  --packages-dir "$PACKAGES" \
  --registry-manifest "$ROOT/e2e/platform/registry-manifest.json" \
  --sealer "$OUT/kb-create-release-index" \
  --binary-dir "$TARGET" \
  --binary-manifest "$TARGET/binary-manifest.json" \
  --binary-manifest-tool "$ROOT/tools/kb-create/scripts/prepare-binary-manifest.mjs" \
  --binary-checksums "$TARGET/binary-checksums.txt" \
  --binary-version "$BINARY_VERSION" \
  --prepare-script "$ROOT/tools/kb-create/scripts/prepare-release-index.mjs" \
  --output "$TARGET/release-index.json"

(
  cd "$TARGET"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum kb-create-linux-amd64 kb-create-linux-arm64 release-index.json release-index-update.json release-index-update-jump.json release-index-update-minor.json release-index-update-failed-host.json release-index-update-healthy-host.json release-index-update-bad-checksum.json release-index-update-config-failure.json release-index-update-doctor-failure.json release-index-canary.json > checksums.txt
  else
    shasum -a 256 kb-create-linux-amd64 kb-create-linux-arm64 release-index.json release-index-update.json release-index-update-jump.json release-index-update-minor.json release-index-update-failed-host.json release-index-update-healthy-host.json release-index-update-bad-checksum.json release-index-update-config-failure.json release-index-update-doctor-failure.json release-index-canary.json > checksums.txt
  fi
)
echo "Prepared $TARGET"
