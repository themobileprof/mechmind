# Verifying real OBD-II reads (MechMind)

Use this checklist so you can tell **hardware truth** from **mock / overrides**.

## Golden rule

When validating the USB OBD link:

1. Do **not** pass `--vin`
2. Do **not** use `--mock`
3. Do **not** use `--allow-vin-override`
4. Trust only what the ECU returns (Mode 09 VIN, Mode 03/07/0A codes)

If the adapter cannot read the VIN, that is a real failure to fix — not something to paper over.

## What each mode proves

| Command | Proves |
|---|---|
| `obdctl --mock` | API upload + explain path only. VIN will be `MOCKTESTVIN000001`. |
| `obdctl --device /dev/ttyUSB0` | Real serial/ELM327 + ECU communication. |
| `obdctl --device … --vin …` | **Blocked** unless `--allow-vin-override` (escape hatch only). |

## Live USB validation

```bash
./scripts/ubuntu-usb-check.sh
go run ./cmd/obdctl --list
go run ./cmd/obdctl --device /dev/ttyUSB0
```

Or install **MechMind Bay** (`./scripts/install-ubuntu.sh`) and choose **Live USB** — same rules: no VIN typing, no mock.

Success looks like:

- `link_type` is `usb` (not `mock`)
- `vehicle.vin` is a 17-character value **from the ECU** (not starting with `MOCK`)
- DTC list may be empty on a healthy car — empty can still be a successful read
- Upload to API uses that same ECU VIN

Failure modes that are **honest** (good signals):

- No `/dev/ttyUSB*` / permission denied
- ELM327 timeout / `UNABLE TO CONNECT`
- VIN missing from Mode 09 (some ECUs need ignition ON, protocol set, or multi-frame support)

## Mock is for software only

```bash
make mock-scan
# → VIN MOCKTESTVIN000001, link_type mock
```

Never treat mock output as proof the dongle or car bus works.

## Enrichment is separate from OBD

After a real scan, the API may call NHTSA vPIC for make/model.  
EU-market VINs often decode incompletely there. That does **not** mean OBD failed — OBD succeeded if the ECU VIN and codes were read. Incomplete vPIC just means weaker secondary enrichment until another source is added.
