#!/usr/bin/env bash
set -euo pipefail

: "${GATEWAY_URL:?Set GATEWAY_URL to the fresh host gateway URL}"
: "${KB_STUDIO_PROJECT_ID:?Set KB_STUDIO_PROJECT_ID to the registered project id}"
: "${KB_STUDIO_PROJECT_NAME:?Set KB_STUDIO_PROJECT_NAME to the registered project name}"
: "${KB_ACCESS_MODE:?Set KB_ACCESS_MODE to local or secured}"

export KB_STUDIO_FRESH_HOST_JOURNEY=1
pnpm exec playwright test scenarios/default/cases/fresh-host.spec.ts
