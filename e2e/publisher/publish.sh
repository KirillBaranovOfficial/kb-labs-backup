#!/bin/sh
# Publish all @kb-labs/*.tgz tarballs to Verdaccio using npm.
# The input is already a packed tarball. npm publish reads the manifest from
# inside that tarball; pnpm publish <tarball> can resolve the package from its
# current working directory and republish workspace:* metadata.
#
# Existing package versions are immutable conflicts, even when bytes match.
# A successful run publishes every package before Docker Compose starts platform.
set -e

KB_ERROR_CATALOG_PATH="${KB_ERROR_CATALOG_PATH:-/errors.catalog.json}"
export KB_ERROR_CATALOG_PATH

REGISTRY="${VERDACCIO_URL:-http://verdaccio:4873}"
REGISTRY_HOST="${REGISTRY#http://}"
REGISTRY_HOST="${REGISTRY_HOST#https://}"

USER="e2e"
PASS="e2e123"

echo "==> Waiting for Verdaccio at $REGISTRY ..."
ATTEMPTS=0
until curl -sf "$REGISTRY/-/ping" > /dev/null 2>&1; do
  ATTEMPTS=$((ATTEMPTS + 1))
  if [ "$ATTEMPTS" -ge 60 ]; then
    echo "ERROR: Verdaccio did not become ready after 60s"
    exit 1
  fi
  sleep 1
done
echo "    Verdaccio ready after ${ATTEMPTS}s"

# Get auth token — try creating user first, fall back to basic-auth login.
get_token() {
  # 1. Try to create user (succeeds on fresh Verdaccio)
  CREATE_RESP=$(curl -sf -X PUT \
    -H "Content-Type: application/json" \
    -d "{\"_id\":\"org.couchdb.user:${USER}\",\"name\":\"${USER}\",\"password\":\"${PASS}\",\"type\":\"user\",\"roles\":[],\"date\":\"2024-01-01T00:00:00.000Z\"}" \
    "$REGISTRY/-/user/org.couchdb.user:${USER}" 2>/dev/null || echo "{}")
  TOKEN=$(echo "$CREATE_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('token',''))" 2>/dev/null || echo "")
  [ -n "$TOKEN" ] && echo "$TOKEN" && return

  # 2. User already exists — re-login to get a token
  LOGIN_RESP=$(curl -sf -u "${USER}:${PASS}" \
    -X PUT \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"${USER}\",\"password\":\"${PASS}\"}" \
    "$REGISTRY/-/user/org.couchdb.user:${USER}" 2>/dev/null || echo "{}")
  TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('token',''))" 2>/dev/null || echo "")
  echo "$TOKEN"
}

TOKEN=$(get_token)

if [ -n "$TOKEN" ]; then
  npm config set "//${REGISTRY_HOST}/:_authToken" "$TOKEN" 2>/dev/null || true
  echo "    Auth token acquired"
else
  # Last resort: use basic-auth (base64-encoded user:pass) in npm config
  B64=$(echo -n "${USER}:${PASS}" | base64)
  npm config set "//${REGISTRY_HOST}/:_auth" "$B64" 2>/dev/null || true
  npm config set "//${REGISTRY_HOST}/:username" "$USER" 2>/dev/null || true
  echo "    Using basic-auth (no token available)"
fi

PARALLELISM="${PUBLISH_PARALLELISM:-4}"
echo "==> Publishing packages (npm publish from tarballs, parallel x${PARALLELISM})..."

# Each pnpm publish handshakes with Verdaccio per package. Sequential
# loop over 160+ tarballs takes ~160s of mostly-idle waiting. Running
# a small parallel pool keeps Verdaccio's request queue full without
# overwhelming it. Tunable via PUBLISH_PARALLELISM env.
#
# xargs is the most portable parallel primitive available in /bin/sh.
# The worker is inlined into `sh -c` since POSIX sh lacks `export -f`.

publish_set() {
  set_dir="$1"
  find "$set_dir" -maxdepth 1 -type f -name '*.tgz' -print 2>/dev/null | sort
}

# Filter base tarballs whose name@version is supplied by the PR overlay.
# This is a local-set decision and does not consult the registry.
filter_superseded_tarballs() {
  base_dir="$1"
  pr_dir="$2"
  python3 - "$base_dir" "$pr_dir" <<'PY'
import glob
import json
import sys
import tarfile

base_dir, pr_dir = sys.argv[1:]

def identity(tarball):
    with tarfile.open(tarball, "r:gz") as archive:
        package = json.load(archive.extractfile("package/package.json"))
    return f"{package['name']}@{package['version']}"

pr_identities = {identity(path) for path in glob.glob(f"{pr_dir}/*.tgz")}
for path in sorted(glob.glob(f"{base_dir}/*.tgz")):
    package_version = identity(path)
    if package_version in pr_identities:
        print(f"  ~ {package_version} (superseded by PR tarball)", file=sys.stderr)
    else:
        print(path)
PY
}

# Worker: a new immutable version publishes; every existing-version conflict fails.
# REGISTRY is exported above; the worker reads it from env.
WORKER='
  tarball="$1"
  pkg=$(basename "$tarball")
  # npm refuses prerelease versions without a dist-tag; tag them by their
  # prerelease id (e.g. 2.1.0-canary.1 -> canary) so latest stays stable.
  metadata=$(tar -xOzf "$tarball" package/package.json | python3 -c "import sys,json; d=json.load(sys.stdin); print(d[\"name\"]+\" \"+d[\"version\"])")
  name=${metadata%% *}
  version=${metadata#* }
  tag_args=""
  case "$version" in
    *-*) prerelease="${version#*-}"; tag_args="--tag ${prerelease%%.*}" ;;
    *)
      latest_version=$(printf "%s\n" "$LATEST_STABLE_VERSIONS" | awk -v package="$name" "\$1 == package { print \$2 }")
      if [ "$version" != "$latest_version" ]; then tag_args="--tag journey-${version}"; fi
      ;;
  esac
  if OUTPUT=$(npm publish "$tarball" --registry "$REGISTRY" $tag_args 2>&1); then
    echo "  ✓ $pkg"
    exit 0
  fi
  if remote_integrity=$(npm view "$name@$version" dist.integrity --registry "$REGISTRY" 2>/dev/null); then
    if ! remote_shasum=$(npm view "$name@$version" dist.shasum --registry "$REGISTRY" 2>/dev/null); then
      remote_shasum=""
    fi
    local_integrity=$(python3 -c "import base64,hashlib,sys; data=open(sys.argv[1],\"rb\").read(); print(\"sha512-\"+base64.b64encode(hashlib.sha512(data).digest()).decode())" "$tarball")
    local_shasum=$(python3 -c "import hashlib,sys; print(hashlib.sha1(open(sys.argv[1],\"rb\").read()).hexdigest())" "$tarball")
    registry_digest="$remote_integrity"
    local_digest="$local_integrity"
    if [ -z "$registry_digest" ]; then
      if [ -n "$remote_shasum" ]; then
        registry_digest="sha1-$remote_shasum"
        local_digest="sha1-$local_shasum"
      else
        registry_digest="unavailable (registry metadata lookup returned no digest)"
      fi
    fi
    python3 - "$KB_ERROR_CATALOG_PATH" "$name" "$version" "$registry_digest" "$local_digest" <<'CATALOG_PY'
import json
import sys

catalog_path, package, version, registry_digest, local_digest = sys.argv[1:]
catalog = json.load(open(catalog_path, encoding="utf8"))
entry = next(item for item in catalog["codes"] if item["code"] == "KB_RELEASE_VERSION_CONFLICT")
message = entry["message"].replace("{package}", package).replace("{version}", version)
print(f"ERROR KB_RELEASE_VERSION_CONFLICT: {message}")
print(entry["hint"])
print(f"Registry digest: {registry_digest}; local digest: {local_digest}.")
CATALOG_PY
    exit 1
  fi
  echo "  ✗ $pkg"
  echo "$OUTPUT" | grep -v "^npm notice" | head -3 | sed "s/^/    /"
  exit 1
'

export REGISTRY

publish_tarballs() {
  set_dir="$1"
  superseding_dir="${2:-}"
  if [ -n "$superseding_dir" ]; then
    tarballs="$(filter_superseded_tarballs "$set_dir" "$superseding_dir")"
  else
    tarballs="$(publish_set "$set_dir")"
  fi
  [ -n "$(echo "$tarballs" | tr -d '[:space:]')" ] || return 0
  total=$(echo "$tarballs" | sed '/^$/d' | wc -l | tr -d ' ')
  if echo "$tarballs" | xargs -n 1 -P "$PARALLELISM" -I {} sh -c "$WORKER" _ {}; then
    echo "==> Published $set_dir: $total  Failed: 0"
  else
    echo "==> ERROR: at least one package failed to publish from $set_dir"
    exit 1
  fi
}

latest_stable_versions() {
  python3 - "$@" <<'PY'
import json
import sys
import tarfile

versions = {}
for tarball in sys.argv[1:]:
    with tarfile.open(tarball, "r:gz") as archive:
        package = json.load(archive.extractfile("package/package.json"))
    version = package["version"]
    if "-" in version:
        continue
    name = package["name"]
    precedence = tuple(int(part) for part in version.split("+", 1)[0].split("."))
    current = versions.get(name)
    if current is None or precedence > current[0]:
        versions[name] = (precedence, version)

for name, (_, version) in sorted(versions.items()):
    print(f"{name} {version}")
PY
}

# In the PR fast lane publish the PR overlay first, then omit base tarballs
# whose name@version is already in the local PR set. Digest checks still apply
# to every registry conflict in either phase.
PACKAGES_DIR="${PUBLISH_PACKAGES_DIR:-/packages}"
if [ -d "$PACKAGES_DIR/pr" ]; then
  PR_TARBALLS="$(publish_set "$PACKAGES_DIR/pr")"
  BASE_TARBALLS="$(filter_superseded_tarballs "$PACKAGES_DIR/base" "$PACKAGES_DIR/pr")"
  LATEST_STABLE_VERSIONS="$(latest_stable_versions $(printf '%s\n%s\n' "$PR_TARBALLS" "$BASE_TARBALLS" | sed '/^$/d'))"
  export LATEST_STABLE_VERSIONS
  publish_tarballs "$PACKAGES_DIR/pr"
  publish_tarballs "$PACKAGES_DIR/base" "$PACKAGES_DIR/pr"
else
  ALL_TARBALLS="$(publish_set "$PACKAGES_DIR")"
  LATEST_STABLE_VERSIONS="$(latest_stable_versions $ALL_TARBALLS)"
  export LATEST_STABLE_VERSIONS
  publish_tarballs "$PACKAGES_DIR"
fi

echo "==> All packages ready in Verdaccio"
