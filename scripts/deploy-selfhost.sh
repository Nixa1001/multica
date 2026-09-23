#!/usr/bin/env bash
set -Eeuo pipefail

# Deploy one already-checked-out commit to the local Docker Compose installation.
# This script intentionally builds on the deployment host: no registry push or
# deployment credential is needed. The GitHub workflow invoking it is disabled
# until MULTICA_SELFHOST_DEPLOY_ENABLED=true is set by an operator.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE_FILES=(-f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml)
COMPOSE_CMD=(docker compose)
STATE_DIR="${MULTICA_DEPLOY_STATE_DIR:-$ROOT_DIR/.multica-deploy}"
LOCK_FILE="$STATE_DIR/deploy.lock"
STATE_FILE="$STATE_DIR/current"
BACKUP_DIR="$STATE_DIR/backups"
HEALTH_TIMEOUT_SECONDS="${MULTICA_DEPLOY_HEALTH_TIMEOUT_SECONDS:-180}"

die() { echo "deploy-selfhost: ERROR: $*" >&2; exit 1; }
log() { echo "deploy-selfhost: $*"; }

usage() {
  cat <<'EOF'
Usage:
  scripts/deploy-selfhost.sh deploy <commit>
  scripts/deploy-selfhost.sh rollback <commit>

deploy builds and starts the exact checked-out commit using local immutable
image tags. rollback switches to an image already built on this host; it does
not reverse database migrations.
EOF
}

valid_commit() { [[ "$1" =~ ^[0-9a-f]{40}$ ]]; }

compose() { "${COMPOSE_CMD[@]}" "${COMPOSE_FILES[@]}" "$@"; }

published_port() {
  local service=$1 container_port=$2 port
  port="$(compose port "$service" "$container_port" 2>/dev/null | tail -n 1 || true)"
  port="${port##*:}"
  [[ "$port" =~ ^[0-9]+$ ]] || die "Could not determine published port for $service:$container_port"
  printf '%s\n' "$port"
}

wait_for_ready() {
  local backend_port=$1 expected_commit=$2 deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
  local url="http://127.0.0.1:${backend_port}/healthz"
  log "Waiting for migration-aware readiness at $url"
  while (( SECONDS < deadline )); do
    if curl --fail --silent --show-error --max-time 3 "$url" >/dev/null 2>&1; then
      local identity
      identity="$(curl --fail --silent --show-error --max-time 3 "http://127.0.0.1:${backend_port}/health" 2>/dev/null || true)"
      if [[ "$identity" == *"\"commit\":\"$expected_commit\""* ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

require_tools() {
  command -v docker >/dev/null || die "docker is required"
  command -v curl >/dev/null || die "curl is required"
  command -v flock >/dev/null || die "flock is required"
  compose version >/dev/null || die "Docker Compose is required"
}

ensure_state_dir() {
  mkdir -p "$BACKUP_DIR"
  chmod 700 "$STATE_DIR" "$BACKUP_DIR"
}

take_backup() {
  local commit=$1 backup="$BACKUP_DIR/${commit}.dump"
  [[ -e "$backup" ]] && { log "Database backup already exists: $backup"; return; }
  log "Creating database backup before migrations"
  compose exec -T postgres pg_dump \
    --format=custom \
    --file=- \
    "${POSTGRES_DB:-multica}" >"$backup"
  chmod 600 "$backup"
}

write_state() {
  local commit=$1 previous=${2:-}
  {
    printf 'commit=%s\n' "$commit"
    printf 'previous=%s\n' "$previous"
    printf 'backend_image_id=%s\n' "$(docker image inspect --format '{{.Id}}' "multica-backend:$commit")"
    printf 'web_image_id=%s\n' "$(docker image inspect --format '{{.Id}}' "multica-web:$commit")"
    printf 'deployed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } >"$STATE_FILE.tmp"
  chmod 600 "$STATE_FILE.tmp"
  mv -f "$STATE_FILE.tmp" "$STATE_FILE"
}

read_state_value() {
  local key=$1
  [[ -r "$STATE_FILE" ]] || return 1
  awk -F= -v wanted="$key" '$1 == wanted { print substr($0, index($0, "=") + 1); exit }' "$STATE_FILE"
}

commit_is_current_main() {
  local commit=$1 remote_ref remote_url
  remote_url="$(git config --get remote.origin.url || true)"
  [[ -n "$remote_url" ]] || die "origin remote is not configured"
  remote_ref="$(git ls-remote "$remote_url" refs/heads/main | awk '{print $1}')"
  [[ -n "$remote_ref" ]] || die "Could not resolve origin/main"
  [[ "$remote_ref" == "$commit" ]] || {
    log "Skipping stale commit $commit; origin/main is now $remote_ref"
    return 1
  }
}

deploy() {
  local commit=$1 previous=""
  valid_commit "$commit" || die "commit must be a 40-character SHA"
  [[ "$(git rev-parse HEAD)" == "$commit" ]] || die "checkout HEAD is not $commit"
  commit_is_current_main "$commit" || return 0

  ensure_state_dir
  exec 9>"$LOCK_FILE"
  flock -n 9 || die "another deployment is running"
  # Re-check after acquiring the lock: a queued deployment must never replace
  # a newer merge that arrived while it was waiting.
  commit_is_current_main "$commit" || return 0

  previous="$(read_state_value commit || true)"
  take_backup "$commit"
  log "Building local images tagged with $commit"
  export MULTICA_IMAGE_TAG="$commit" VERSION="$commit" COMMIT="$commit"
  compose build --pull backend frontend
  log "Starting services for $commit"
  compose up -d --no-build backend frontend

  local backend_port
  backend_port="$(published_port backend 8080)"
  if ! wait_for_ready "$backend_port" "$commit"; then
    log "Deployment $commit did not become ready; leaving services and backup intact"
    compose ps >&2 || true
    compose logs --tail=100 backend >&2 || true
    die "health check failed; no automatic image rollback was attempted because migrations may have changed the schema"
  fi

  write_state "$commit" "$previous"
  log "Deployed commit $commit (backend multica-backend:$commit, web multica-web:$commit)"
  log "Image IDs: $(docker image inspect --format '{{.Id}}' "multica-backend:$commit") / $(docker image inspect --format '{{.Id}}' "multica-web:$commit")"
  [[ -z "$previous" ]] || log "Previous image tag was $previous; rollback changes images only and never reverses migrations"
}

rollback() {
  local commit=$1
  valid_commit "$commit" || die "rollback commit must be a 40-character SHA"
  ensure_state_dir
  exec 9>"$LOCK_FILE"
  flock -n 9 || die "another deployment is running"
  docker image inspect "multica-backend:$commit" >/dev/null 2>&1 || die "backend image $commit is not present locally"
  docker image inspect "multica-web:$commit" >/dev/null 2>&1 || die "web image $commit is not present locally"
  log "Switching application images to $commit"
  export MULTICA_IMAGE_TAG="$commit"
  compose up -d --no-build backend frontend
  local backend_port
  backend_port="$(published_port backend 8080)"
  wait_for_ready "$backend_port" "$commit" || die "rollback image $commit failed readiness; database was not reverted"
  write_state "$commit" "$(read_state_value commit || true)"
  log "Rolled back application images to $commit; database schema was left unchanged"
}

main() {
  [[ $# -eq 2 ]] || { usage >&2; exit 2; }
  require_tools
  case "$1" in
    deploy) deploy "$2" ;;
    rollback) rollback "$2" ;;
    *) usage >&2; exit 2 ;;
  esac
}

main "$@"
