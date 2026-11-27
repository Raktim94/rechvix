#!/usr/bin/env bash
# Bootstrap a fresh checkout without installing privileged host packages.
# Requires Docker Engine + Compose v2 (or Docker Desktop), git and curl.
set -euo pipefail
REPO_URL="${RECHVIX_REPO_URL:-https://github.com/Raktim94/rechvix.git}"
INSTALL_DIR="${RECHVIX_INSTALL_DIR:-rechvix}"
for cmd in git curl docker; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "$cmd is required. See https://rechvix.nodedr.com/install" >&2; exit 1; }
done
docker compose version >/dev/null 2>&1 || { echo 'Docker Compose v2 is required.' >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo 'Start Docker and check your Docker permissions, then retry.' >&2; exit 1; }
if [ -e "$INSTALL_DIR" ]; then
  echo "$INSTALL_DIR already exists. No files or database were changed." >&2
  echo 'For an existing installation, follow the backup and upgrade guide; this command creates fresh installations only.' >&2
  exit 1
fi
clone_args=(--depth 1)
if [[ -n "${RECHVIX_INSTALL_SHA:-}" ]]; then
  if [[ ! "$RECHVIX_INSTALL_SHA" =~ ^[[:xdigit:]]{40}$ ]]; then
    echo 'RECHVIX_INSTALL_SHA must be a full 40-character commit SHA.' >&2
    exit 1
  fi
  clone_args+=("--revision=$RECHVIX_INSTALL_SHA")
fi
git clone "${clone_args[@]}" -- "$REPO_URL" "$INSTALL_DIR"
cd "$INSTALL_DIR"
# Git also accepts annotated tag objects for --revision and peels them to commits.
if [[ -n "${RECHVIX_INSTALL_SHA:-}" && "$(git cat-file -t "$RECHVIX_INSTALL_SHA")" != commit ]]; then
  echo 'RECHVIX_INSTALL_SHA must identify a commit.' >&2
  exit 1
fi
exec bash ./install.sh
