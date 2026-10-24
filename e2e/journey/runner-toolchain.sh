#!/usr/bin/env bash

# Journey runner toolchain image lifecycle. The tag is derived by
# e2e/journey/journey_resources.py from the `AS toolchain` stage of
# e2e/journey/Dockerfile.runner and the staged Playwright version; this script
# builds that tag once, reclaims older tags it created (identified by the
# journey toolchain image label, never by name matching), and exports the
# reference for the compose runner build. Source this file from the owning
# journey entry (e2e/journey/run.sh).

journey_toolchain_ensure() {
  local root="$1" derivation toolchain_image toolchain_label
  if ! derivation="$(python3 "$root/e2e/journey/journey_resources.py" --toolchain-image)"; then
    return 1
  fi
  # Read by line: bash 3.2 (macOS host bash) does not honor $'\n' inside
  # parameter-expansion patterns, which would empty the image reference.
  {
    read -r toolchain_image
    read -r toolchain_label
  } <<< "$derivation"
  if [[ -z "$(docker images -q "$toolchain_image")" ]]; then
    if ! (cd "$root" && docker build --target toolchain \
        -t "$toolchain_image" \
        --label "${toolchain_label}=${toolchain_image#*:}" \
        -f e2e/journey/Dockerfile.runner .); then
      echo "Journey runner toolchain image $toolchain_image could not be built. Next: docker build --target toolchain -t $toolchain_image -f e2e/journey/Dockerfile.runner ." >&2
      return 1
    fi
    echo "==> journey runner toolchain $toolchain_image built"
  else
    echo "==> journey runner toolchain $toolchain_image reused"
  fi
  journey_toolchain_reclaim "$toolchain_label" "$toolchain_image"
  export KB_JOURNEY_TOOLCHAIN_IMAGE="$toolchain_image"
}

journey_toolchain_reclaim() {
  local label="$1" keep_image="$2" keep_id image_id reference
  keep_id="$(docker images -q "$keep_image" || true)"
  while read -r image_id reference; do
    [[ -n "$image_id" && "$image_id" != "$keep_id" ]] || continue
    if ! docker image rm -f "$image_id"; then
      echo "WARNING: could not remove previous journey runner toolchain image $reference. Next: docker image rm -f $image_id" >&2
    fi
  done < <(docker images --filter "label=$label" --format '{{.ID}} {{.Repository}}:{{.Tag}}')
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  echo "Source e2e/journey/runner-toolchain.sh from a journey entry." >&2
  exit 2
fi
