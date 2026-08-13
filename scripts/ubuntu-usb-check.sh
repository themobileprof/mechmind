#!/usr/bin/env bash
# Ubuntu USB OBD-II preflight + optional live scan.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "== user / serial permissions =="
id
if groups | grep -qw dialout; then
  echo "OK: you are in the dialout group"
else
  echo "MISSING: dialout group — run:"
  echo "  sudo usermod -aG dialout \"$USER\""
  echo "  then log out/in (or reboot) and re-run this script"
fi

echo
echo "== kernel USB serial devices =="
ls -l /dev/ttyUSB* /dev/ttyACM* 2>/dev/null || echo "(none yet — plug adapter in, ignition ON)"

echo
echo "== recent USB serial kernel messages =="
dmesg -T 2>/dev/null | grep -Ei 'ttyUSB|ttyACM|FTDI|cp210|ch340|cdc_acm|PL2303' | tail -n 20 || true

echo
echo "== building obdctl =="
go build -o bin/obdctl ./cmd/obdctl

echo
echo "== obdctl --list =="
./bin/obdctl --list || true

DEVICE="${1:-}"
BAUD="${2:-38400}"

if [[ -z "$DEVICE" ]]; then
  echo
  echo "Usage for a live read:"
  echo "  $0 /dev/ttyUSB0 [baud]"
  echo "Common baud rates: 38400 (default), 9600, 115200"
  echo
  echo "Vehicle must be: OBD port connected, ignition ON (engine running optional)."
  exit 0
fi

echo
echo "== live scan on $DEVICE @ $BAUD =="
./bin/obdctl --device "$DEVICE" --baud "$BAUD" --timeout 30s
