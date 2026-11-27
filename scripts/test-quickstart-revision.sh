#!/usr/bin/env bash
# Exercise the real quickstart and Git transport without starting a Compose stack.
set -euo pipefail
quickstart="$(cd "$(dirname "$0")" && pwd)/quickstart.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
cd "$scratch"
export GIT_CONFIG_GLOBAL="$scratch/gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_ALLOW_PROTOCOL=file
unset RECHVIX_INSTALL_SHA RECHVIX_REPO_URL
git config --global user.name 'Quickstart fixture'
git config --global user.email 'quickstart@example.invalid'
git config --global url."file://$scratch/source".insteadOf https://github.com/Raktim94/rechvix.git
git init -q -b main source
cat > source/install.sh <<'INSTALL'
git rev-parse HEAD > "$INSTALL_RECORD"
INSTALL
git -C source add install.sh
git -C source commit -qm 'First revision'
first="$(git -C source rev-parse HEAD)"
git -C source commit -q --allow-empty -m 'Advance main'
latest="$(git -C source rev-parse HEAD)"

mkdir bin
printf '#!/bin/sh\nexit 0\n' > bin/docker
chmod +x bin/docker
export PATH="$scratch/bin:$PATH" INSTALL_RECORD="$scratch/installed-sha"

check_install() {
  local expected="$1" actual
  bash "$quickstart"
  actual="$(cat "$INSTALL_RECORD")"
  if [ "$actual" != "$expected" ]; then
    echo "FAIL $RECHVIX_INSTALL_DIR: expected $expected, installer ran $actual" >&2
    exit 1
  fi
  echo "PASS $RECHVIX_INSTALL_DIR: installer ran $actual"
  rm "$INSTALL_RECORD"
}

RECHVIX_INSTALL_DIR=default check_install "$latest"
RECHVIX_INSTALL_DIR=pinned RECHVIX_INSTALL_SHA="$first" check_install "$first"
# Only the explicitly selected source is reachable in this case (as on a fork).
GIT_CONFIG_GLOBAL=/dev/null RECHVIX_REPO_URL="file://$scratch/source" \
  RECHVIX_INSTALL_DIR=alternate RECHVIX_INSTALL_SHA="$first" check_install "$first"

git -C source tag -a release -m 'Annotated tag'
tag_object="$(git -C source rev-parse refs/tags/release)"
for revision in 0123456789012345678901234567890123456789 main "$tag_object"; do
  if RECHVIX_INSTALL_DIR="missing-$revision" RECHVIX_INSTALL_SHA="$revision" bash "$quickstart"; then
    echo "FAIL: quickstart accepted $revision" >&2
    exit 1
  fi
  test ! -e "$INSTALL_RECORD"
  echo "PASS: $revision rejected before install.sh"
done
