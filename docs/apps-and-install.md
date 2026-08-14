# MechMind — apps & installation

**MechMind** is a shop network for technicians: local OBD-II scanning, shared history, knowledge, and software diagnosis findings.

This document lists **every runnable app** in the repo and how to install/run it on Linux (primary). A Windows bay UI can be cross-compiled (`make bayui-windows`).

---

## Product map

| App | Path | Who uses it | Role |
|---|---|---|---|
| **MechMind API** | `cmd/api` | All shops (server) | Auth, orgs, scans, explain, enrichment worker, diagnosis |
| **bayui** | `cmd/bayui` | Technician laptop | Local GUI (browser display; this process owns USB) |
| **obdctl** | `cmd/obdctl` | Technician laptop | Local USB/mock OBD CLI → uploads to API |
| **enrichctl** | `cmd/enrichctl` | Ops / admin | Seed/drain NHTSA lean enrichment jobs |
| **ubuntu-usb-check** | `scripts/ubuntu-usb-check.sh` | Technician | Serial permissions + live USB preflight |
| **Postgres** | host or `docker-compose.yml` | API | System of record |
| Desktop (Wails) | *planned* | Bay PC | Native window around the same local OBD stack |
| Mobile companion | *planned* | Android bay | Bluetooth-first later |

```
Vehicle ──USB──► bayui / obdctl (local) ──HTTPS──► MechMind API ──► Postgres
                                      │
                                      ├── diagnosis findings
                                      ├── knowledge + history
                                      └── enrichment worker (NHTSA)
```

---

## Prerequisites (Linux)

| Tool | Why |
|---|---|
| Go **1.22+** (1.24 OK) | Build API + CLIs |
| PostgreSQL **16+** (18 OK) | API database |
| `git`, `make`, `curl` | Dev workflow |
| USB ELM327-class adapter | Live vehicle reads (optional for mock) |
| Membership in `dialout` | Access `/dev/ttyUSB*` / `ttyACM*` |

```bash
# Example (Ubuntu/Debian)
sudo apt update
sudo apt install -y golang-go make curl postgresql-client
# Install PostgreSQL server if not already present, then:
sudo usermod -aG dialout "$USER"   # log out/in after this
```

Clone / enter the repo:

```bash
cd /path/to/autoservice   # working directory for this MechMind codebase
```

---

## 1. Database

MechMind expects Postgres. On this project the default local DB/user is still named `autoservice` (internal); the product brand is MechMind.

### Option A — Host Postgres (recommended if you already have it)

```bash
# create role + DB once (peer auth as your OS user, or as postgres admin)
psql -d postgres -c "CREATE ROLE autoservice LOGIN PASSWORD 'autoservice';" 2>/dev/null || true
psql -d postgres -c "CREATE DATABASE autoservice OWNER autoservice;" 2>/dev/null || true

export PGPASSWORD=autoservice
psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/001_init.sql
psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/002_nigeria_enrichment.sql
psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/003_avensis_primary.sql
psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/004_remove_vin_overrides.sql
psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/005_observations.sql
```

Or: `make migrate`

### Option B — Docker Postgres

```bash
docker compose up -d
# listens on localhost:5433 — set DATABASE_URL accordingly in .env
```

---

## 2. MechMind API (`cmd/api`)

Central backend. **Required** for real product use (scans upload here).

### Configure

```bash
cp .env.example .env
```

Important variables:

| Variable | Purpose |
|---|---|
| `HTTP_ADDR` | Listen address (default `:8080`) |
| `DATABASE_URL` | Postgres URL |
| `JWT_SECRET` | Required — sign tokens |
| `BOOTSTRAP_ADMIN_EMAIL` / `PASSWORD` | First `super_admin` if none exists |
| `API_URL` | Used by clients (default `http://localhost:8080`) |
| `LLM_ENABLED` / `LLM_API_KEY` / `LLM_MODEL` | Optional bay narrative (`&narrative=1`); see [llm-context.md](llm-context.md) |

### Install / run

```bash
# Dev
make api
# or
go run ./cmd/api

# Production-style binary
go build -o bin/mechmind-api ./cmd/api
./bin/mechmind-api
```

On start the API also runs the **enrichment worker** (lean NHTSA jobs).

### Health check

```bash
curl -s http://localhost:8080/healthz
```

### systemd sketch (optional)

```ini
# /etc/systemd/system/mechmind-api.service
[Unit]
Description=MechMind API
After=network.target postgresql.service

[Service]
WorkingDirectory=/opt/mechmind
EnvironmentFile=/opt/mechmind/.env
ExecStart=/opt/mechmind/bin/mechmind-api
Restart=on-failure
User=mechmind

[Install]
WantedBy=multi-user.target
```

---

## 3. Technician client — `obdctl` (`cmd/obdctl`)

Local CLI that talks to the vehicle (USB or mock) and **uploads to the API**.

### Install

```bash
go build -o bin/mechmind-obdctl ./cmd/obdctl
sudo install -m 755 bin/mechmind-obdctl /usr/local/bin/mechmind-obdctl
```

Token file (after login): `~/.config/mechmind/token`

### First-time auth

```bash
# Register a shop (API must be running) — first user becomes org_admin
curl -s http://localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{
  "email":"tech@demo.shop",
  "password":"changeme123",
  "display_name":"Bay Tech",
  "shop_name":"Demo Shop"
}' | jq

mechmind-obdctl --login --email tech@demo.shop --password changeme123
```

### Mock scan (software only)

```bash
mechmind-obdctl --mock
# VIN will be MOCKTESTVIN000001 — not a real car
```

### Live USB scan (hardware validation)

```bash
# Do NOT pass --vin when proving the OBD link
./scripts/ubuntu-usb-check.sh
mechmind-obdctl --list
mechmind-obdctl --device /dev/ttyUSB0
```

See [verifying-obd-reads.md](verifying-obd-reads.md).

### Bay GUI (same USB rules)

Technician install (no Go on the laptop):

```bash
./scripts/install-ubuntu.sh
# then open “MechMind Bay” from the app menu
```

Developer run-from-source:

```bash
make bayui
# browser: http://127.0.0.1:8787/
```

A remote website cannot read the car. `bayui` is a local process that owns the serial port; the browser is only the screen. Full steps: [bay-ui.md](bay-ui.md).

Windows: `make bayui-windows` → `bin/mechmind-bayui.exe` (no installer yet).

### Common flags

| Flag | Meaning |
|---|---|
| `--api` | API base (default `http://localhost:8080`) |
| `--mock` | Synthetic adapter |
| `--device` | Serial path |
| `--baud` | 38400 / 9600 / 115200 |
| `--login` | Auth only |
| `--no-upload` | Escape hatch (not recommended) |
| `--allow-vin-override` | Only when you knowingly replace ECU VIN |

---

## 4. Enrichment CLI — `enrichctl` (`cmd/enrichctl`)

Ops tool for catalog recall seeding and queue draining (also handled automatically by the API worker).

```bash
go build -o bin/mechmind-enrichctl ./cmd/enrichctl

./bin/mechmind-enrichctl --stats
./bin/mechmind-enrichctl --seed
./bin/mechmind-enrichctl --seed --drain --timeout 25m
```

Makefile shortcuts: `make enrich-stats`, `make enrich-seed`, `make enrich-drain`.

Details: [enrichment-sources.md](enrichment-sources.md).

---

## 5. USB helper script

```bash
chmod +x scripts/ubuntu-usb-check.sh
./scripts/ubuntu-usb-check.sh              # permissions + device list
./scripts/ubuntu-usb-check.sh /dev/ttyUSB0 # live read attempt
```

Optional udev symlink (stable `/dev/obd0`):

```bash
sudo cp deploy/udev/99-mechmind-obd.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules && sudo udevadm trigger
```

---

## 6. Build everything at once

```bash
make build
# produces:
#   bin/api
#   bin/obdctl
#   bin/enrichctl
#   bin/bayui
```

Suggested install names:

```bash
sudo install -m 755 bin/api        /usr/local/bin/mechmind-api
sudo install -m 755 bin/obdctl     /usr/local/bin/mechmind-obdctl
sudo install -m 755 bin/enrichctl  /usr/local/bin/mechmind-enrichctl
sudo install -m 755 bin/bayui      /usr/local/bin/mechmind-bayui
```

---

## Quick start (minimal path)

```bash
cp .env.example .env          # set JWT_SECRET + bootstrap admin
make migrate
make api                      # terminal 1 — API must stay running
./scripts/install-ubuntu.sh
# then open “MechMind Bay” from the app menu
```

---

## Planned apps (not in repo yet)

| App | Intent |
|---|---|
| **MechMind Desktop** (Wails) | Native window around the same local OBD stack |
| **MechMind Mobile** | Android Bluetooth companion |
| **Shop portal** | Browser history / reports (no live USB) |

Same API; only the client shell changes.

---

## Troubleshooting install

| Symptom | Fix |
|---|---|
| `JWT_SECRET is required` | Set in `.env` |
| `database: …` | Check `DATABASE_URL`, migrate applied |
| `login failed 404` | Another process on `:8080` — stop it or change `HTTP_ADDR` |
| No `/dev/ttyUSB*` | Adapter, cable, `dialout` group, ignition ON |
| Mock VIN on live device | Don’t use `--mock`; don’t override VIN |

---

## Related docs

- [verifying-obd-reads.md](verifying-obd-reads.md) — mock vs real OBD proof
- [bay-ui.md](bay-ui.md) — Ubuntu installer + live-car steps
- [enrichment-sources.md](enrichment-sources.md) — NHTSA / data sources
