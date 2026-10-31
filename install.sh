#!/usr/bin/env sh
# Installer shim for cooler-lcd.
#
# The real logic lives in the binary, because it needs to write files with two
# different owners: the system service as root, the udev rule as root. Keeping
# one implementation avoids the two drifting apart.
#
# Usage:
#   ./install.sh                  rebuild from source, then install
#   ./install.sh --prebuilt       use dist/cooler-lcd-linux-$arch
#   ./install.sh --uninstall
set -eu

here=$(cd "$(dirname "$0")" && pwd)
arch=$(uname -m)
case "$arch" in
	x86_64) goarch=amd64 ;;
	aarch64 | arm64) goarch=arm64 ;;
	*) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

bin="$here/dist/cooler-lcd-linux-$goarch"

case "${1:-}" in
--prebuilt)
	;;
--uninstall)
	shift
	exec sudo "$bin" uninstall "$@"
	;;
*)
	echo "building $bin"
	(cd "$here" && mkdir -p dist &&
		CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$bin" ./cmd/cooler-lcd)
	;;
esac

[ -x "$bin" ] || { echo "no binary at $bin" >&2; exit 1; }
exec sudo "$bin" install "$@"
