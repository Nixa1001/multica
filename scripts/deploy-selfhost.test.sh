#!/usr/bin/env bash
set -Eeuo pipefail

# Static safety checks for the deployment entry point. Live deployment tests
# belong on the operator's isolated host and are intentionally not run in CI.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/deploy-selfhost.sh"

grep -q 'MULTICA_SELFHOST_DEPLOY_ENABLED' "$ROOT_DIR/.github/workflows/deploy-selfhost.yml"
grep -q 'cancel-in-progress: false' "$ROOT_DIR/.github/workflows/deploy-selfhost.yml"
grep -q 'github.sha' "$ROOT_DIR/.github/workflows/deploy-selfhost.yml"
grep -q 'flock' "$SCRIPT"
grep -q 'git ls-remote' "$SCRIPT"
grep -q -- '--format=custom' "$SCRIPT"
grep -q '/healthz' "$SCRIPT"
grep -q 'expected_commit' "$SCRIPT"
grep -q 'commit' "$SCRIPT"
grep -q 'no automatic image rollback' "$SCRIPT"
grep -q 'migrations may have changed the schema' "$SCRIPT"

if grep -qE 'docker (login|push)|ghcr.io' "$SCRIPT"; then
  echo "deployment script must not publish images or authenticate to a registry" >&2
  exit 1
fi

if grep -qE 'docker compose[^\n]* down|docker compose[^\n]* destroy' "$SCRIPT"; then
  echo "deployment script must preserve the existing Compose volumes" >&2
  exit 1
fi

echo "deploy-selfhost safety checks passed"
