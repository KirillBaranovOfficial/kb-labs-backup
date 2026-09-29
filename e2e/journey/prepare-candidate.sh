#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/.journey"
PACKAGES="$ROOT/e2e/packages"
TAG="journey-candidate"
TARGET="$OUT/release/$TAG"

mkdir -p "$PACKAGES" "$TARGET"
rm -f "$TARGET"/*
if [[ "${KB_JOURNEY_PACKAGES_READY:-0}" != "1" ]]; then
  rm -f "$PACKAGES"/*.tgz
  "$ROOT/e2e/scripts/pack-all.sh" "$PACKAGES"
fi

(
  cd "$ROOT/tools/kb-create"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
    -o "$TARGET/kb-create-linux-amd64" .
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' \
    -o "$OUT/kb-create-release-index" ./v2/cmd/kb-create-release-index
)

node "$ROOT/e2e/platform/prepare-release-index.mjs" \
  --packages-dir "$PACKAGES" \
  --registry-manifest "$ROOT/e2e/platform/registry-manifest.json" \
  --sealer "$OUT/kb-create-release-index" \
  --prepare-script "$ROOT/tools/kb-create/scripts/prepare-release-index.mjs" \
  --output "$TARGET/release-index.json"

(
  cd "$TARGET"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum kb-create-linux-amd64 release-index.json > checksums.txt
  else
    shasum -a 256 kb-create-linux-amd64 release-index.json > checksums.txt
  fi
)
echo "Prepared $TARGET"
