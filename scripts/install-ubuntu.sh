#!/usr/bin/env bash
# Install MechMind Bay on this Ubuntu machine (builds the .deb if needed).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! command -v dpkg-deb >/dev/null; then
  echo "dpkg-deb is required (Ubuntu/Debian)." >&2
  exit 1
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
sudo dpkg -i "$DEB"

echo
echo "Launch: MechMind Bay in the app menu, or: mechmind-bayui"
echo "The MechMind API must already be running (http://localhost:8080 by default)."
echo "If you were just added to dialout, log out and back in before a live USB scan."
