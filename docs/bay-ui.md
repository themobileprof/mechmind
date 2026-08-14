# MechMind bay UI — test with your car

The bay UI is a **local** app. A website on the internet cannot read OBD-II. This process runs on the laptop that has the USB dongle; the browser is only the screen.

```
Vehicle ──USB──► mechmind-bayui (this PC) ──HTTP──► browser (display)
                         │
                         └──HTTPS──► MechMind API
```

It listens on **localhost only** (`127.0.0.1:8787`).

## 1. Install the bay app (Ubuntu)

On the machine that will hold the ELM327 (this one, for your Avensis test):

```bash
./scripts/install-ubuntu.sh
```

Run it as your user — the script asks for sudo only for `dpkg`. Prefixing the whole command with `sudo` hides Go from PATH.

That builds `dist/mechmind-bay_*.deb` if needed and installs it with `dpkg` (not `apt`, so you will not see the `_apt` sandbox warning). Afterward:

- App menu: **MechMind Bay**
- Or terminal: `mechmind-bayui`

You do **not** need Go on a technician laptop — only the `.deb`. If you were added to the `dialout` group for the first time, **log out and back in** before a live scan.

If you already installed 0.1.0, quit **MechMind Bay** fully (not just the browser tab), then:

```bash
./scripts/install-ubuntu.sh
```

Then open **MechMind Bay** again.

The MechMind **API must already be running** (default `http://localhost:8080`). Keep using `make api` on this machine until a server installer exists.

## 2. Scan your car

1. Ignition **ON**. Plug the ELM327 into the OBD port and USB into the PC.
2. Open **MechMind Bay** (browser should open `http://127.0.0.1:8787/`).
3. **New shop** (first time) — bootstrap `super_admin` cannot upload scans. Create a shop, or sign in as an existing technician.
4. Adapter: **Live USB (car)**. Refresh ports. Pick `/dev/ttyUSB0`, `/dev/ttyACM0`, or `/dev/obd0`.
5. **Scan vehicle**. The right pane lists each step (open port, baud, ATI, VIN, codes). Cheap USB clones hang on **ATZ**, so MechMind skips it and tries 38400 / 115200 / 9600. The scan stops itself after **35 seconds**. Use **Cancel scan** to stop sooner. Do not type a VIN.

Success: stamped VIN plate shows a 17-character ECU VIN (not `MOCK…`). Empty DTCs on a healthy car is still a good read.

Mock mode is software-only (`MOCKTESTVIN000001`) — never treat it as hardware proof.

## Windows

There is no Windows installer yet. Cross-compile the GUI:

```bash
make bayui-windows
# → bin/mechmind-bayui.exe
```

Copy the `.exe` to the Windows PC. Install the ELM327 USB serial driver (CH340 / FTDI / CP210x). Run the exe; it opens `http://127.0.0.1:8787/`. Point **API** at the MechMind server (`http://<linux-ip>:8080` if the API is not on that PC). Pick a `COMx` port.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8787` | Listen address |
| `-api` | `API_URL` or `http://localhost:8080` | MechMind API |
| `-open` | `true` | Open a browser |

Closing the browser tab does **not** stop the scanner. Use **Quit Bay** in the top-right of the GUI, or `pkill mechmind-bayui`.

## Developers only

```bash
make installer          # dist/mechmind-bay_<ver>_<arch>.deb
make bayui              # run from source, no install
```
