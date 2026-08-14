#!/usr/bin/env bash
# Build an Ubuntu/Debian .deb for the MechMind bay client (GUI + udev).
# Technician machines do not need Go — only this package.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Never build the staging tree as root — leftover dist/ dirs become undeletable.
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
	exec sudo -u "$SUDO_USER" -H --preserve-env=PATH "$0" "$@"
fi

find_go() {
	if command -v go >/dev/null 2>&1; then
		command -v go
		return 0
	fi
	local c home=""
	if [ -n "${SUDO_USER:-}" ] && [ "${SUDO_USER}" != "root" ]; then
		home="$(getent passwd "$SUDO_USER" | cut -d: -f6 || true)"
	fi
	for c in \
		/usr/local/go/bin/go \
		/usr/lib/go/bin/go \
		"${home:+$home/go/bin/go}" \
		"${home:+$home/.local/bin/go}" \
		"${HOME:+$HOME/go/bin/go}" \
		/usr/bin/go; do
		[ -n "$c" ] && [ -x "$c" ] && { echo "$c"; return 0; }
	done
	return 1
}

GO_BIN="$(find_go || true)"
if [ -z "$GO_BIN" ]; then
	echo "Go is required to build the .deb, but it is not on PATH." >&2
	echo "If you used sudo, run this instead (sudo is only needed for dpkg):" >&2
	echo "  ./scripts/install-ubuntu.sh" >&2
	echo "Go on this machine is typically /usr/local/go/bin/go" >&2
	exit 1
fi

VERSION="$(tr -d '[:space:]' < VERSION)"
ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
GOARCH="$ARCH"
case "$ARCH" in
  amd64) GOARCH=amd64 ;;
  arm64) GOARCH=arm64 ;;
  *)
    echo "unsupported dpkg architecture: $ARCH" >&2
    exit 1
    ;;
esac

PKG="mechmind-bay_${VERSION}_${ARCH}"
STAGE="$ROOT/dist/$PKG"

remove_tree() {
	local p="$1"
	[ -e "$p" ] || return 0
	if rm -rf "$p" 2>/dev/null; then
		return 0
	fi
	echo "== $p is root-owned leftover from a previous sudo run; removing it =="
	sudo rm -rf "$p"
}

mkdir -p "$ROOT/dist"
remove_tree "$STAGE"
mkdir -p "$STAGE/DEBIAN" \
  "$STAGE/usr/bin" \
  "$STAGE/usr/share/applications" \
  "$STAGE/usr/share/icons/hicolor/scalable/apps" \
  "$STAGE/usr/share/doc/mechmind-bay" \
  "$STAGE/lib/udev/rules.d"

echo "== building mechmind-bayui ($GOARCH) with $GO_BIN =="
GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags "-s -w" -o "$STAGE/usr/bin/mechmind-bayui" ./cmd/bayui
chmod 755 "$STAGE/usr/bin/mechmind-bayui"

install -m 644 deploy/linux/mechmind-bay.desktop "$STAGE/usr/share/applications/mechmind-bay.desktop"
install -m 644 deploy/linux/mechmind-bay.svg "$STAGE/usr/share/icons/hicolor/scalable/apps/mechmind-bay.svg"
install -m 644 deploy/udev/99-mechmind-obd.rules "$STAGE/lib/udev/rules.d/99-mechmind-obd.rules"
install -m 644 docs/bay-ui.md "$STAGE/usr/share/doc/mechmind-bay/README.md"

cat > "$STAGE/DEBIAN/control" <<EOF
Package: mechmind-bay
Version: ${VERSION}
Section: utils
Priority: optional
Architecture: ${ARCH}
Maintainer: MechMind <mechmind@localhost>
Depends: adduser
Recommends: xdg-utils
Description: MechMind bay scanner (local OBD-II GUI)
 Local USB OBD-II client. The browser is only the display; this
 package owns the serial port. Point it at a running MechMind API.
EOF

cat > "$STAGE/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if command -v udevadm >/dev/null 2>&1; then
  udevadm control --reload-rules || true
  udevadm trigger --subsystem-match=tty --action=add || true
fi
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
  if getent group dialout >/dev/null 2>&1; then
    usermod -aG dialout "$SUDO_USER" || true
  fi
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1 && [ -d /usr/share/icons/hicolor ]; then
  gtk-update-icon-cache -q /usr/share/icons/hicolor || true
fi
echo "MechMind Bay installed. Open it from the app menu, or run: mechmind-bayui"
echo "If this is your first serial-port install, log out and back in so dialout takes effect."
EOF
chmod 755 "$STAGE/DEBIAN/postinst"

cat > "$STAGE/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if command -v udevadm >/dev/null 2>&1; then
  udevadm control --reload-rules || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications || true
fi
EOF
chmod 755 "$STAGE/DEBIAN/postrm"

dpkg-deb --root-owner-group --build "$STAGE" "$ROOT/dist/${PKG}.deb"
remove_tree "$STAGE"
echo
echo "Installer: $ROOT/dist/${PKG}.deb"
echo "Install:   sudo dpkg -i $ROOT/dist/${PKG}.deb"
