# Autoservice — Linux + wired USB

Local **client** reads the vehicle over USB OBD-II. The **API** is the system of record (auth, scans, knowledge, later RAG). A third-party **LLM** only generates prose from retrieved context.

## Why not DEV_TECHNICIAN_ID?

Those env vars were temporary scaffolding. They are gone.

Auth model now:

| Role | How you get it | Powers |
|---|---|---|
| `super_admin` | Bootstrapped once via `BOOTSTRAP_ADMIN_*` in `.env` | Orgs, all techs, toggle self-register / invite-only |
| `org_admin` | Self-register creating a new shop, or promoted later | Shop invite code, activate/deactivate shop techs |
| `technician` | Self-register with shop invite code | Scan, history, explain |

Self-register is on by default (`allow_self_register=true`). Super admin can require invite codes or disable open registration.

## Local database

This project uses **PostgreSQL 18 on this machine** (`127.0.0.1:5432`), database/user `autoservice`:

```
DATABASE_URL=postgres://autoservice:autoservice@127.0.0.1:5432/autoservice?sslmode=disable
```

`docker compose` on `:5433` is optional fallback only.

Apply schema (once):

```bash
PGPASSWORD=autoservice psql -h 127.0.0.1 -p 5432 -U autoservice -d autoservice -f migrations/001_init.sql
```

## Run API (required)

```bash
cp .env.example .env   # if needed
make api
```

On first start, if no super admin exists, `BOOTSTRAP_ADMIN_*` creates one.

## Technician self-register + scan

```bash
# Create a shop (first registrant becomes org_admin)
curl -s http://localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{
  "email":"tech@demo.shop",
  "password":"changeme123",
  "display_name":"Bay Tech",
  "shop_name":"Demo Shop"
}' | jq

# Login + save token, then mock scan (always uploads to API)
go run ./cmd/obdctl --login --email tech@demo.shop --password changeme123
go run ./cmd/obdctl --mock

# Real USB — do not pass --vin when validating hardware
./scripts/ubuntu-usb-check.sh /dev/ttyUSB0
go run ./cmd/obdctl --device /dev/ttyUSB0
```

See [docs/verifying-obd-reads.md](docs/verifying-obd-reads.md) so mock/API tests are not confused with live OBD proof.

`--api` defaults to `http://localhost:8080`. Upload is the default; `--no-upload` is an escape hatch only.

## Architecture split

| Layer | Owns |
|---|---|
| Local client (`obdctl` / future desktop) | USB/BT, ELM327, DTC decode, login UI |
| Backend API + RAG | Auth, orgs, scan history, knowledge base, retrieval, citations |
| 3rd-party LLM | Technician-facing explanation text from retrieved chunks only |

## Software diagnosis

Scans store an `observations` pack (live PIDs, freeze-frames, link metrics).  
`GET /v1/codes/{code}/explain?vin=` returns rule-based `findings` plus fleet `co_occurrence`.

See diagnosis rules in `internal/diagnosis`.


Do not hardcode or override a real car’s identity when proving the USB link. Mock uses `MOCKTESTVIN000001` only.

```bash
make mock-scan                          # software path only
go run ./cmd/obdctl --device /dev/ttyUSB0  # live ECU VIN + codes
```

Details: [docs/verifying-obd-reads.md](docs/verifying-obd-reads.md)

## Nigeria market focus (2010+)

Deployment targets **model year ≥ 2010** and these five common makes on Nigerian roads:

1. Toyota  
2. Honda  
3. Hyundai  
4. Kia  
5. Mercedes-Benz  

Background enrichment (no API keys) pulls **lean** NHTSA fields only — enough to triage faults, not full payloads:

| Job | Stores |
|---|---|
| `vin_decode` | make, model, year, body, fuel, displacement, cylinders, drive |
| `recalls_mmy` | campaign #, component, short summary/consequence/remedy |
| `seed_catalog` | queues recall fetches for catalog models × even years 2010–2024 |

```bash
make migrate          # applies 001 + 002
make enrich-seed      # enqueue catalog seed
make enrich-drain     # process queue (rate-limited)
make enrich-stats
```

The API process also runs the enrichment worker automatically and enqueues a seed on startup.



## API

Public:
- `POST /v1/auth/register`
- `POST /v1/auth/login`

Authenticated:
- `GET /v1/auth/me`
- `POST /v1/scans`
- `GET /v1/scans/{id}`
- `GET /v1/vehicles/{vin}/scans`
- `GET /v1/codes/{code}/explain?vin=`

Super admin:
- `GET/POST /v1/admin/organizations`
- `GET /v1/admin/technicians`
- `PATCH /v1/admin/settings/{key}` (`allow_self_register`, `require_invite_code`)

Org admin:
- `GET /v1/admin/org/technicians`
- `PATCH /v1/admin/technicians/{id}/active`
