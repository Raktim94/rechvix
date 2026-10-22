#!/usr/bin/env bash
# Install, use and remove check for the Debian/Ubuntu package. Run as root on a Debian or Ubuntu machine with systemd.
#   sudo DEB=dist/rechvix_0.15.2_amd64.deb scripts/test-deb.sh
# Steps: install, services up, create a business and sign in, read the CRM and dashboard, reinstall (upgrade keeps
# data), remove (services and programs gone, data kept), purge, then clean up the database it created.
# Set RELAX_SANDBOX=1 inside a container, where systemd cannot apply the unit's sandboxing.
set -euo pipefail
DEB="${DEB:?path to the .deb}"; PORT=8090; BASE="http://127.0.0.1:$PORT"; JAR="$(mktemp)"
ok() { printf '\033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$1" >&2; journalctl -u rechvix -u rechvix-worker -n 30 --no-pager >&2 || true; exit 1; }
req() { curl -sS -o /tmp/rv.out -w '%{http_code}' "$@"; }
wait_ready() { for _ in $(seq 1 40); do [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/health/ready" || true)" = 200 ] && return 0; sleep 1; done; fail "server did not become ready"; }
relax() { [ "${RELAX_SANDBOX:-}" = 1 ] || return 0; for u in rechvix rechvix-worker; do mkdir -p /etc/systemd/system/$u.service.d; printf '[Service]\nNoNewPrivileges=no\nPrivateTmp=no\nProtectSystem=no\nProtectHome=no\nProtectKernelTunables=no\nProtectControlGroups=no\nRestrictSUIDSGID=no\nLockPersonality=no\nStateDirectory=\n' > /etc/systemd/system/$u.service.d/container.conf; done; systemctl daemon-reload; systemctl reset-failed rechvix rechvix-worker 2>/dev/null || true; systemctl restart rechvix rechvix-worker; }

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null; apt-get install -y -qq curl >/dev/null
chmod 0644 "$DEB"; cp "$DEB" /var/tmp/rechvix-test.deb; chmod 0644 /var/tmp/rechvix-test.deb

# 1. install
apt-get install -y -qq /var/tmp/rechvix-test.deb >/dev/null || fail "apt install failed"
relax; wait_ready
[ "$(systemctl is-active rechvix)" = active ] && [ "$(systemctl is-active rechvix-worker)" = active ] || fail "services are not active"
[ "$(systemctl is-enabled rechvix)" = enabled ] || fail "rechvix is not enabled at boot"
ok "install: server and worker active, enabled at boot"
[ "$(stat -c %a /etc/rechvix/rechvix.env)" = 640 ] || fail "configuration is not mode 640"
ok "install: configuration is private (640)"
[ "$(req "$BASE/")" = 200 ] && grep -qi '<title>rechvix' /tmp/rv.out || fail "web app is not served"
ok "install: web application is served"

# 2. use it
[ "$(req "$BASE/api/v1/auth/bootstrap")" = 200 ] && grep -q '"available":true' /tmp/rv.out || fail "first-run setup is not available"
code=$(req -X POST "$BASE/api/v1/auth/bootstrap" -H 'Content-Type: application/json' -d '{"organisation_name":"Deb Test","default_currency_code":"EUR","default_timezone":"Europe/Madrid","legal_entity_name":"Deb Test SL","country_code":"ES","branch_code":"HO","branch_name":"Head office","warehouse_code":"WH","warehouse_name":"Main","owner_full_name":"Test Owner","owner_email":"owner@example.test","owner_password":"correct-horse-battery-9"}')
[ "$code" = 201 ] || [ "$code" = 200 ] || fail "bootstrap returned $code: $(cat /tmp/rv.out)"
ok "use: business created through /setup"
[ "$(req -c "$JAR" -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' -d '{"email":"owner@example.test","password":"correct-horse-battery-9","mfa_code":""}')" = 200 ] || fail "login failed: $(cat /tmp/rv.out)"
for path in /api/v1/reports/dashboard /api/v1/crm/stages /api/v1/organisation /api/v1/legal-entities; do
  [ "$(req -b "$JAR" "$BASE$path")" = 200 ] || fail "GET $path failed"
done
ok "use: signed in; dashboard, CRM, organisation and companies respond"
[ "$(req -b "$JAR" -X POST "$BASE/api/v1/crm/opportunities" -H 'Content-Type: application/json' -d '{"title":"Package test lead","company_name":"Acme","value":"100","currency":"EUR"}')" = 201 ] || fail "creating a CRM lead failed: $(cat /tmp/rv.out)"
ok "use: data can be written (CRM lead)"

# 3. upgrade over the same version keeps data and configuration
KEY_BEFORE=$(sha256sum /etc/rechvix/rechvix.env | cut -d' ' -f1)
apt-get install -y -qq --reinstall /var/tmp/rechvix-test.deb >/dev/null || fail "reinstall failed"
relax; wait_ready
[ "$KEY_BEFORE" = "$(sha256sum /etc/rechvix/rechvix.env | cut -d' ' -f1)" ] || fail "reinstall changed the configuration or its secrets"
[ "$(req -c "$JAR" -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' -d '{"email":"owner@example.test","password":"correct-horse-battery-9","mfa_code":""}')" = 200 ] || fail "owner cannot sign in after reinstall"
ok "upgrade: configuration and data survive a reinstall"

# 4. remove
apt-get remove -y -qq rechvix >/dev/null || fail "apt remove failed"
[ ! -e /usr/lib/rechvix/server ] && [ ! -e /usr/bin/rechvix ] && [ ! -d /usr/share/rechvix ] || fail "program files remain after remove"
[ "$(systemctl is-active rechvix 2>/dev/null || true)" != active ] || fail "server still running after remove"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/health/live" || true)" != 200 ] || fail "port still answering after remove"
runuser -u postgres -- psql -d billing -Atc "select count(*) from organisations" | grep -qx 1 || fail "data was lost on remove"
ok "remove: services stopped, programs gone, database kept"

# 5. purge
apt-get purge -y -qq rechvix >/dev/null || fail "apt purge failed"
[ ! -e /lib/systemd/system/rechvix.service ] || fail "unit file remains after purge"
[ -f /etc/rechvix/rechvix.env ] || fail "purge removed the encryption key (it must be kept)"
ok "purge: units removed; database and encryption key deliberately kept"

# 6. reinstall after purge finds the same database and key, then clean up what this test created
apt-get install -y -qq /var/tmp/rechvix-test.deb >/dev/null || fail "install after purge failed"
relax; wait_ready
[ "$(req -c "$JAR" -X POST "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' -d '{"email":"owner@example.test","password":"correct-horse-battery-9","mfa_code":""}')" = 200 ] || fail "data not found after install-after-purge"
ok "reinstall after purge: previous business is still there"
apt-get purge -y -qq rechvix >/dev/null
runuser -u postgres -- psql -qc "DROP DATABASE IF EXISTS billing" -c "DROP ROLE IF EXISTS billing_app" -c "DROP ROLE IF EXISTS billing_migrator" >/dev/null 2>&1 || true
rm -rf /etc/rechvix /var/lib/rechvix /var/tmp/rechvix-test.deb "$JAR"
ok "cleanup: test database, roles and configuration deleted"
echo "All package checks passed."
