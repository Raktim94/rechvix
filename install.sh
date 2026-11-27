#!/usr/bin/env bash
# Rechvix — one-command self-hosted install.
#
#   curl -fsSL https://raw.githubusercontent.com/Raktim94/rechvix/main/install.sh | bash
#
# What this does: clones the repo (if not already inside it), generates the
# secrets deploy/compose/.env needs, and starts the selected Docker project.
#
# Requires: bash, curl, git, and Docker (Engine + Compose v2 plugin, or
# Docker Desktop on macOS). Linux and macOS only — see the README/CONTRIBUTING
# for Windows (WSL2) guidance; there is no native Windows path.
set -euo pipefail

REPO_URL="https://github.com/Raktim94/rechvix.git"
INSTALL_DIR="${RECHVIX_INSTALL_DIR:-rechvix}"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not found on PATH. $2"
}

require_cmd git   "Install git: https://git-scm.com/downloads"
require_cmd curl  "Install curl (usually already present on Linux/macOS)."
require_cmd docker "Install Docker: https://docs.docker.com/get-docker/ (macOS: Docker Desktop; Linux: Docker Engine)."

if ! docker compose version >/dev/null 2>&1; then
  if command -v docker-compose >/dev/null 2>&1; then
    warn "Using legacy 'docker-compose' — consider upgrading to the 'docker compose' plugin (Compose v2)."
    die "Docker Compose v2 is required; upgrade before installing."
  else
    die "Docker Compose not found. Install Docker Desktop, or the 'docker compose' plugin: https://docs.docker.com/compose/install/"
  fi
else
  COMPOSE="docker compose"
fi

if ! docker info >/dev/null 2>&1; then
  die "Docker is installed but not running (or you lack permission). Start Docker and, on Linux, ensure your user is in the 'docker' group."
fi

# --- get the source ---
if [ -f "go.mod" ] && grep -q '^module rechvix' go.mod 2>/dev/null; then
  log "Already inside the rechvix repo — using the current directory."
  REPO_DIR="$(pwd)"
elif [ -d "$INSTALL_DIR/.git" ]; then
  die "$INSTALL_DIR already exists. Follow the backup and upgrade guide; no files were changed."
else
  log "Cloning $REPO_URL into ./$INSTALL_DIR ..."
  git clone --depth 1 "$REPO_URL" "$INSTALL_DIR"
  REPO_DIR="$(cd "$INSTALL_DIR" && pwd)"
fi

cd -P "$REPO_DIR/deploy/compose"

project="${COMPOSE_PROJECT_NAME:-rechvix}"
port="${HTTP_PORT:-8080}"
fresh=true
if [ -f .env ]; then
  fresh=false
  # Let Compose parse quotes, comments and interpolation. Older installations
  # may have supplied their project name only in the shell.
  settings=$(
    unset HTTP_PORT
    if grep -Eq '^[[:space:]]*(export[[:space:]]+)?COMPOSE_PROJECT_NAME[[:space:]]*=' .env; then unset COMPOSE_PROJECT_NAME; fi
    docker compose config --environment | awk -F= '
      $1=="COMPOSE_PROJECT_NAME" {p=$2} $1=="HTTP_PORT" {h=$2}
      END {print (p != "" ? p : "rechvix"), (h != "" ? h : "8080")}'
  )
  read -r project port <<< "$settings"
  if [[ "${COMPOSE_PROJECT_NAME:-$project}" != "$project" || "${HTTP_PORT:-$port}" != "$port" ]]; then
    die "This checkout is configured for project '$project' on port $port. Clear the conflicting shell overrides and retry."
  fi
fi
[[ "$project" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die "Invalid Compose project name '$project'. Use lowercase letters, digits, hyphens or underscores, starting with a letter or digit."
if ! [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
  die "HTTP_PORT must be between 1 and 65535."
fi
export COMPOSE_PROJECT_NAME="$project" HTTP_PORT="$port"

volume="${project}_postgres-data"
owner_label=io.rechvix.install-dir
collision() {
  die "$1 Restore the original deploy/compose/.env to resume. For a separate instance, choose a fresh checkout, a distinct COMPOSE_PROJECT_NAME and a free HTTP_PORT."
}
project_dirs=$(docker ps -a --filter "label=com.docker.compose.project=$project" --format '{{or (.Label "com.docker.compose.project.working_dir") "unknown"}}')
while IFS= read -r dir; do
  [[ -z "$dir" ]] && continue
  if "$fresh" || [[ "$dir" != "$PWD" && ! "$dir" -ef . ]]; then
    collision "Docker project '$project' already has containers from $dir."
  fi
done <<< "$project_dirs"
existing_volume=$(docker volume ls -q --filter "name=^${volume}$")
if "$fresh"; then
  [[ -z "$existing_volume" ]] || collision "PostgreSQL volume '$volume' already exists."
  # A lost .env may have held a custom project name. Look up this checkout
  # even when its containers were removed with 'docker compose down'.
  local_volumes=$(docker volume ls -q --filter "label=$owner_label=$PWD")
  local_projects=$(docker ps -a --filter "label=com.docker.compose.project.working_dir=$PWD" --format '{{.Label "com.docker.compose.project"}}')
  [[ -z "$local_volumes$local_projects" ]] || collision "Docker still has resources for this checkout, but its .env is missing."
  published=$(docker ps --format '{{.Ports}}')
  [[ "$published" != *":$port->"* ]] || die "Host port $port is already published by Docker. Choose a free HTTP_PORT for '$project'."

  log "Generating deploy/compose/.env with fresh secrets ..."
  # Hex, not base64: these two feed straight into unescaped postgres://
  # URLs in docker-compose.yml (POSTGRES_PASSWORD, BILLING_APP_PASSWORD).
  # base64's alphabet includes '/', which a URL parser reads as the end of
  # the userinfo section — a password containing one silently breaks the
  # DSN (migrate/app/worker all fail to connect). Hex has no such
  # characters. AEAD_ENCRYPTION_KEY never goes into a URL, so base64 is
  # fine there.
  gen_secret() { openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
  gen_key()    { openssl rand -base64 32 2>/dev/null || head -c 32 /dev/urandom | base64; }

  umask 077
  temp_env=$(mktemp .env.XXXXXX)
  trap 'rm -f "$temp_env"' EXIT
  db_password=$(gen_secret)
  app_password=$(gen_secret)
  encryption_key=$(gen_key)
  sed -e "s#^POSTGRES_PASSWORD=.*#POSTGRES_PASSWORD=$db_password#" \
      -e "s#^BILLING_APP_PASSWORD=.*#BILLING_APP_PASSWORD=$app_password#" \
      -e "s#^AEAD_ENCRYPTION_KEY=.*#AEAD_ENCRYPTION_KEY=$encryption_key#" \
      -e "s#^COMPOSE_PROJECT_NAME=.*#COMPOSE_PROJECT_NAME=$project#" \
      -e "s#^HTTP_PORT=.*#HTTP_PORT=$port#" .env.example > "$temp_env"
  ln "$temp_env" .env || die "Another installer created .env in this checkout. Retry from here."
fi

if [[ -z "$existing_volume" ]]; then
  # Docker creates a named volume once, even when two installations race.
  # Check the winning directory before either checkout can update services.
  docker volume create --label "com.docker.compose.project=$project" \
    --label com.docker.compose.volume=postgres-data --label "$owner_label=$PWD" "$volume" >/dev/null
fi
owner=$(docker volume inspect --format "{{with .Labels}}{{index . \"$owner_label\"}}{{end}}" "$volume")
# Existing volumes from older installations have no directory label.
if [[ -z "$existing_volume" && -z "$owner" ]] || [[ -n "$owner" && "$owner" != "$PWD" && ! "$owner" -ef . ]]; then
  if "$fresh" && [[ .env -ef "$temp_env" ]]; then rm .env; fi
  collision "PostgreSQL volume '$volume' belongs to another installation."
fi
if "$fresh"; then
  rm "$temp_env"
  trap - EXIT
fi

log "Starting Rechvix project '$project' on port $port via $COMPOSE ..."
$COMPOSE up -d --build --wait --wait-timeout 180

# Compose health and an actual HTTP response must both pass before reporting success.
ready=false
for ((attempt=0; attempt<30; attempt++)); do
  if curl --fail --silent --max-time 5 "http://localhost:${HTTP_PORT}/api/v1/auth/bootstrap" >/dev/null; then
    ready=true
    break
  fi
  sleep 2
done
if [ "$ready" != true ]; then
  die "Containers started, but the app did not become ready. Check: $COMPOSE logs app migrate worker"
fi
log "Ready. Open http://localhost:${HTTP_PORT}/setup to create your organisation."
log "Secrets and settings live in: $REPO_DIR/deploy/compose/.env"
log "Logs: cd $REPO_DIR/deploy/compose && $COMPOSE logs -f"
