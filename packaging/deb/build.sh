#!/usr/bin/env bash
# Builds rechvix_<version>_<arch>.deb.   Usage: packaging/deb/build.sh <version> <amd64|arm64>
# Needs Go, a built web app in apps/web/dist (npm ci && npm run build in apps/web), and dpkg-deb.
set -euo pipefail
VERSION="${1:?version, e.g. 0.17.1}"; ARCH="${2:?amd64 or arm64}"
HERE="$(cd "$(dirname "$0")" && pwd)"; ROOT="$(cd "$HERE/../.." && pwd)"
OUT="${OUT_DIR:-$ROOT/dist}"; STAGE="$(mktemp -d)"; trap 'rm -rf "$STAGE"' EXIT
[ -f "$ROOT/apps/web/dist/index.html" ] || { echo "apps/web/dist is missing: build the web app first" >&2; exit 1; }

install -d "$STAGE/usr/lib/rechvix" "$STAGE/usr/share/rechvix" "$STAGE/usr/bin" "$STAGE/lib/systemd/system" "$STAGE/usr/share/doc/rechvix" "$STAGE/DEBIAN" "$OUT"
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags="-s -w" -o "$STAGE/usr/lib/rechvix/server" ./apps/server \
  && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags="-s -w" -o "$STAGE/usr/lib/rechvix/worker" ./apps/worker )
cp -a "$ROOT/apps/web/dist" "$STAGE/usr/share/rechvix/web"
install -m 0755 "$HERE/files/rechvix" "$STAGE/usr/bin/rechvix"
install -m 0644 "$HERE/files/rechvix.service" "$HERE/files/rechvix-worker.service" "$STAGE/lib/systemd/system/"
install -m 0644 "$HERE/debian/copyright" "$STAGE/usr/share/doc/rechvix/copyright"
gzip -9n -c "$ROOT/CHANGELOG.md" > "$STAGE/usr/share/doc/rechvix/changelog.gz"
install -m 0755 "$HERE/debian/postinst" "$HERE/debian/prerm" "$HERE/debian/postrm" "$STAGE/DEBIAN/"
chmod 0755 "$STAGE" "$STAGE/usr/lib/rechvix/server" "$STAGE/usr/lib/rechvix/worker"; chmod 0644 "$STAGE/usr/share/doc/rechvix/changelog.gz"; find "$STAGE" -type d -exec chmod 0755 {} +; find "$STAGE/usr/share/rechvix/web" -type f -exec chmod 0644 {} +
SIZE=$(du -sk "$STAGE" | cut -f1)
sed -e "s/@VERSION@/$VERSION/" -e "s/@ARCH@/$ARCH/" -e "s/@SIZE@/$SIZE/" "$HERE/debian/control.in" > "$STAGE/DEBIAN/control"
FILE="$OUT/rechvix_${VERSION}_${ARCH}.deb"
dpkg-deb --root-owner-group -Zxz --build "$STAGE" "$FILE" >/dev/null
( cd "$OUT" && sha256sum "$(basename "$FILE")" > "$(basename "$FILE").sha256" )
echo "$FILE"
