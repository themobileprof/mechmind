#!/usr/bin/env bash
# Install MechMind Bay on this Ubuntu machine (builds the .deb if needed).
# Run as your user:  ./scripts/install-ubuntu.sh
# Do not prefix the whole script with sudo — that hides Go from PATH.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ "$(id -u)" -eq 0 && -n "${SUDO_USER:-}" && "$SUDO_USER" != "root" ]]; then
  echo "Building the .deb as $SUDO_USER (not root)."
  sudo -u "$SUDO_USER" -H "$ROOT/scripts/install-ubuntu.sh"
  exit $?
fi

ARCH="$(dpkg --print-architecture)"
VERSION="$(tr -d '[:space:]' < VERSION)"
DEB="$ROOT/dist/mechmind-bay_${VERSION}_${ARCH}.deb"

if [[ ! -f "$DEB" ]]; then
  echo "== packaging $DEB =="
  "$ROOT/scripts/package-deb.sh"
fi

echo "== installing $DEB =="
# dpkg -i only. Do not run apt-get -f — that can change unrelated packages.
if [[ "$(id -u)" -eq 0 ]]; then
  dpkg -i "$DEB"
else
  sudo dpkg -i "$DEB"
fi

echo
echo "Launch: MechMind Bay in the app menu, or: mechmind-bayui"
echo "The MechMind API must already be running (http://localhost:8080 by default)."
echo "If you were just added to dialout, log out and back in before a live USB scan."
