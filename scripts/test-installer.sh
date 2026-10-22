#!/usr/bin/env bash
# Isolated command-boundary tests: never starts Docker or installs host packages.
set -euo pipefail
src="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/bin" "$scratch/repo/deploy/compose" "$scratch/run"
cp "$src/install.sh" "$scratch/repo/"
cp "$src/go.mod" "$scratch/repo/"
cp "$src/deploy/compose/.env.example" "$scratch/repo/deploy/compose/"
cat > "$scratch/bin/docker" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
case "$*" in
  'compose up'*) exit "${TEST_COMPOSE_EXIT:-0}";;
  'info') exit "${TEST_DOCKER_EXIT:-0}";;
esac
MOCK
cat > "$scratch/bin/curl" <<'MOCK'
#!/bin/sh
exit "${TEST_HTTP_EXIT:-0}"
MOCK
cat > "$scratch/bin/sleep" <<'MOCK'
#!/bin/sh
exit 0
MOCK
chmod +x "$scratch/bin/"*
export PATH="$scratch/bin:$PATH" TEST_LOG="$scratch/log" RECHVIX_INSTALL_DIR=rechvix
cd "$scratch/repo"
bash ./install.sh > "$scratch/output"
if [ "$(uname -s)" = Darwin ]; then
  test "$(stat -f %Lp deploy/compose/.env)" = 600
else
  test "$(stat -c %a deploy/compose/.env)" = 600
fi
! grep -E 'CHANGE_ME|changeme' deploy/compose/.env
cp deploy/compose/.env "$scratch/original.env"
bash ./install.sh > /dev/null
cmp deploy/compose/.env "$scratch/original.env"
if TEST_COMPOSE_EXIT=1 bash ./install.sh > /dev/null 2>&1; then echo 'Failed compose must fail installer'; exit 1; fi
if TEST_HTTP_EXIT=1 bash ./install.sh > /dev/null 2>&1; then echo 'Failed readiness must fail installer'; exit 1; fi
cd "$scratch/run"
mkdir rechvix
if bash "$src/scripts/quickstart.sh" > /dev/null 2>&1; then echo 'Existing directories must be rejected'; exit 1; fi
rmdir rechvix
if TEST_DOCKER_EXIT=1 bash "$src/scripts/quickstart.sh" > /dev/null 2>&1; then echo 'Unreachable Docker must fail'; exit 1; fi
echo 'PASS: private secrets, preserved configuration, compose failure, HTTP readiness failure, existing directory refusal, Docker prerequisite.'
