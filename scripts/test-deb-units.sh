#!/usr/bin/env bash
# Run immediately after installing the package, before applying any test overrides.
set -euo pipefail

unit_dir=/lib/systemd/system
verify_units() {
  systemd-analyze verify "$@"
}

verify_units "$unit_dir/rechvix.service" "$unit_dir/rechvix-worker.service"

# Give the fixture its own unit name so systemd cannot resolve the installed worker in its place.
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
invalid_unit="$scratch/rechvix-worker-invalid.service"
sed '/^ExecStart=/d' "$unit_dir/rechvix-worker.service" > "$invalid_unit"
if LC_ALL=C verify_units "$invalid_unit" > "$scratch/verify.log" 2>&1; then
  echo "FAIL: systemd verification accepted the worker without ExecStart" >&2
  exit 1
fi
if ! grep -Fq 'ExecStart=' "$scratch/verify.log"; then
  cat "$scratch/verify.log" >&2
  echo "FAIL: expected a diagnostic about the missing ExecStart" >&2
  exit 1
fi
echo "PASS: systemd verified both installed units and rejected the worker without ExecStart"
