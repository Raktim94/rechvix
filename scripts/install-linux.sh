#!/usr/bin/env bash
# One-command install of Rechvix on Ubuntu or Debian (amd64 or arm64).
#
#   curl -fsSL https://rechvix.nodedr.com/install-linux.sh | sudo bash
#
# Downloads the .deb for the latest release from GitHub, checks its SHA-256, and installs it with apt. The package
# installs PostgreSQL if needed, creates the database, and starts Rechvix on port 8090.
# Pin a version:  curl -fsSL https://rechvix.nodedr.com/install-linux.sh | sudo RECHVIX_VERSION=0.17.1 bash
set -euo pipefail

REPO="Raktim94/rechvix"
say() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
die() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Run as root: curl -fsSL https://rechvix.nodedr.com/install-linux.sh | sudo bash"
[ -r /etc/os-release ] && . /etc/os-release
case "${ID:-}:${ID_LIKE:-}" in debian:*|ubuntu:*|*:*debian*|*:*ubuntu*) ;; *) die "This installer is for Ubuntu and Debian. For other systems use Docker: https://rechvix.nodedr.com/install" ;; esac
command -v apt-get >/dev/null || die "apt-get was not found."
command -v systemctl >/dev/null && [ -d /run/systemd/system ] || die "systemd is required. In a container or WSL without systemd, use Docker instead: https://rechvix.nodedr.com/install"

ARCH="$(dpkg --print-architecture)"
case "$ARCH" in amd64|arm64) ;; *) die "Unsupported CPU architecture '$ARCH' (amd64 and arm64 are published)." ;; esac

export DEBIAN_FRONTEND=noninteractive
say "Installing prerequisites"
apt-get update -qq
apt-get install -y -qq curl ca-certificates >/dev/null

VERSION="${RECHVIX_VERSION:-}"
if [ -z "$VERSION" ]; then
  say "Finding the latest release"
  # The /releases/latest redirect has no API rate limit (shared networks get HTTP 403 from api.github.com); the API is the fallback.
  VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null | sed -n 's|.*/releases/tag/v\{0,1\}||p' || true)"
  [ -n "$VERSION" ] || VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || die "Could not determine the latest version. Set RECHVIX_VERSION, for example RECHVIX_VERSION=0.17.1."
fi
VERSION="${VERSION#v}"
FILE="rechvix_${VERSION}_${ARCH}.deb"
BASE="https://github.com/$REPO/releases/download/v${VERSION}"

WORK="$(mktemp -d /var/tmp/rechvix-install.XXXXXX)"; chmod 0755 "$WORK"; trap 'rm -rf "$WORK"' EXIT
say "Downloading Rechvix $VERSION ($ARCH)"
curl -fSL --retry 3 -o "$WORK/$FILE" "$BASE/$FILE" || die "Could not download $BASE/$FILE"
curl -fsSL -o "$WORK/$FILE.sha256" "$BASE/$FILE.sha256" || die "Could not download the checksum file."
( cd "$WORK" && sha256sum -c "$FILE.sha256" >/dev/null ) || die "Checksum mismatch: the download is damaged. Nothing was installed."
chmod 0644 "$WORK/$FILE"

say "Installing (this also sets up PostgreSQL if it is missing)"
apt-get install -y "$WORK/$FILE"
