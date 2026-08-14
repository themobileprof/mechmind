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

## Adapter classes

MechMind’s USB path speaks **ELM327 AT commands** (the QBD `0918:7104` that answered `ELM327 v1.5`). Init tries **ATI first**, then a warm start / **ATZ** only if there is no ELM banner. After a scan it **parks** the chip (`ATWS`) so the next scan is not wedged. Vehicle talk tries **CAN 11/500, then 29/500, 11/250, 29/250**, and only then ELM automatic — cheap v1.5 clones often return `SEARCHING... UNABLE TO CONNECT` on `ATSP0` even when the ECU is present.

A **Tactrix OpenPort 2.0** (including Revision E clones) is a **J2534** pass-through. It often shows up as `/dev/ttyACM0` too, but it will not answer `ATI`/`ATZ`. MechMind **detects** typical OpenPort USB IDs (`0403:cc4d` or a Tactrix/OpenPort product string) and refuses the ELM scan path instead of hanging.

| Dongle | Speaks | Use with MechMind today |
|---|---|---|
| ELM327 / STN11xx USB | AT commands + Mode 01/03/09 | Yes |
| QBD `0918:7104` | CDC-ACM virtual COM; ELM only if firmware answers `ATI` | Only if it prints `ELM327` |
| OpenPort 2.0 / J2534 | PassThru API (not ELM) | Not yet |
| Bluetooth ELM | Same as USB ELM | Later |

Keep the ELM327-class adapter for Phase 1 live scans. OpenPort is a better *future* OEM/J2534 path, not a drop-in for the current client.

## Mock is for software only

```bash
make mock-scan
# → VIN MOCKTESTVIN000001, link_type mock
```

Never treat mock output as proof the dongle or car bus works.

## Enrichment is separate from OBD

After a real scan, the API may call NHTSA vPIC for make/model.  
EU-market VINs often decode incompletely there. That does **not** mean OBD failed — OBD succeeded if the ECU VIN and codes were read. Incomplete vPIC just means weaker secondary enrichment until another source is added.
