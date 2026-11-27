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
cp -R "$scratch/repo" "$scratch/second"
cat > "$scratch/bin/docker" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
case "$*" in
  'ps -a '*)
    case "$*" in
      *'project.working_dir='*) printf '%s' "${TEST_LOCAL_PROJECT:-}" ;;
      *) printf '%s' "${TEST_CONTAINER_DIR:-}" ;;
    esac ;;
  'volume ls '*)
    [ "${TEST_VOLUME_LIST_EXIT:-0}" = 0 ] || exit 1
    case "$*" in
      *'label='*) printf '%s' "${TEST_LOCAL_VOLUME:-}" ;;
      *) printf '%s' "${TEST_VOLUME:-}" ;;
    esac ;;
  'volume inspect '*) printf '%s' "${TEST_VOLUME_DIR-$(pwd -P)}" ;;
  'ps --format '*) printf '%s' "${TEST_PUBLISHED:-}" ;;
  'compose config --environment')
    grep -E '^(HTTP_PORT|COMPOSE_PROJECT_NAME)=' .env || true ;;
  'compose up'*) exit "${TEST_COMPOSE_EXIT:-0}";;
  'info') exit "${TEST_DOCKER_EXIT:-0}";;
esac
MOCK
cat > "$scratch/bin/curl" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
exit "${TEST_HTTP_EXIT:-0}"
MOCK
cat > "$scratch/bin/sleep" <<'MOCK'
#!/bin/sh
exit 0
MOCK
chmod +x "$scratch/bin/"*
export PATH="$scratch/bin:$PATH" TEST_LOG="$scratch/log" RECHVIX_INSTALL_DIR=rechvix
unset COMPOSE_PROJECT_NAME HTTP_PORT
cd "$scratch/second"
if TEST_VOLUME=rechvix_postgres-data bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Fresh checkout accepted an existing PostgreSQL volume'; exit 1
fi
test ! -e deploy/compose/.env
if grep -Eq 'compose up|volume create' "$TEST_LOG"; then echo 'Collision changed Docker resources'; exit 1; fi
if TEST_CONTAINER_DIR=/original/deploy/compose bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Fresh checkout accepted an existing stopped project'; exit 1
fi
test ! -e deploy/compose/.env
if TEST_PUBLISHED='0.0.0.0:8081->8080/tcp' HTTP_PORT=8081 bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Fresh installation accepted a published host port'; exit 1
fi
if TEST_VOLUME_LIST_EXIT=1 bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Installation continued after Docker volume lookup failed'; exit 1
fi
test ! -e deploy/compose/.env
# Another checkout can claim the volume between the lookup and create.
if TEST_VOLUME_DIR=/concurrent/deploy/compose bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Concurrent installation used a volume belonging to another checkout'; exit 1
fi
test ! -e deploy/compose/.env
if grep -q 'compose up' "$TEST_LOG"; then echo 'Rejected installation started services'; exit 1; fi

COMPOSE_PROJECT_NAME=rechvix-test HTTP_PORT=8081 bash ./install.sh > "$scratch/output"
grep -qx 'COMPOSE_PROJECT_NAME=rechvix-test' deploy/compose/.env
grep -qx 'HTTP_PORT=8081' deploy/compose/.env
grep -q 'localhost:8081' "$TEST_LOG"
cp deploy/compose/.env "$scratch/second.env"
TEST_VOLUME=rechvix-test_postgres-data bash ./install.sh > "$scratch/output"
cmp deploy/compose/.env "$scratch/second.env"
grep -q 'localhost:8081' "$scratch/output"
if HTTP_PORT=9999 bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Rerun silently changed the saved port'; exit 1
fi
if TEST_VOLUME=rechvix-test_postgres-data TEST_VOLUME_DIR=/original/deploy/compose bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'A saved .env was allowed to take over a foreign volume'; exit 1
fi
cmp deploy/compose/.env "$scratch/second.env"
mv deploy/compose/.env "$scratch/missing.env"
if TEST_LOCAL_VOLUME=rechvix-test_postgres-data bash ./install.sh > "$scratch/output" 2>&1; then
  echo 'Rerun replaced the missing configuration of a named instance'; exit 1
fi
test ! -e deploy/compose/.env
cd "$scratch/repo"
bash ./install.sh > "$scratch/output"
grep -qx 'COMPOSE_PROJECT_NAME=rechvix' deploy/compose/.env
grep -qx 'HTTP_PORT=8080' deploy/compose/.env
if [ "$(uname -s)" = Darwin ]; then
  test "$(stat -f %Lp deploy/compose/.env)" = 600
else
  test "$(stat -c %a deploy/compose/.env)" = 600
fi
! grep -E 'CHANGE_ME|changeme' deploy/compose/.env
cp deploy/compose/.env "$scratch/original.env"
bash ./install.sh > /dev/null
cmp deploy/compose/.env "$scratch/original.env"
# Existing default installations predate the project setting and volume label.
sed '/^COMPOSE_PROJECT_NAME=/d' "$scratch/original.env" > deploy/compose/.env
TEST_VOLUME=rechvix_postgres-data TEST_VOLUME_DIR='' bash ./install.sh > "$scratch/output"
if TEST_COMPOSE_EXIT=1 bash ./install.sh > /dev/null 2>&1; then echo 'Failed compose must fail installer'; exit 1; fi
if TEST_HTTP_EXIT=1 bash ./install.sh > /dev/null 2>&1; then echo 'Failed readiness must fail installer'; exit 1; fi
cd "$scratch/run"
mkdir rechvix
if bash "$src/scripts/quickstart.sh" > /dev/null 2>&1; then echo 'Existing directories must be rejected'; exit 1; fi
rmdir rechvix
if TEST_DOCKER_EXIT=1 bash "$src/scripts/quickstart.sh" > /dev/null 2>&1; then echo 'Unreachable Docker must fail'; exit 1; fi
echo 'PASS: isolated projects and ports, collision refusal, missing and legacy configuration, installer prerequisites and readiness.'
