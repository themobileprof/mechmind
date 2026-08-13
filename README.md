# MechMind

**MechMind** helps shop technicians scan vehicles over OBD-II, share history across a network, and get software diagnosis findings that go beyond a generic code dictionary.

- **Local client** reads the car (USB today; Bluetooth later).
- **MechMind API** is the system of record (auth, scans, knowledge, enrichment, findings).
- **LLM (optional later)** only writes prose from retrieved context — never invents bus data.

## Apps in this repo

| App | Binary (suggested) | Purpose |
|---|---|---|
| API | `mechmind-api` | Backend + enrichment worker |
| obdctl | `mechmind-obdctl` | Technician USB / mock scanner |
| enrichctl | `mechmind-enrichctl` | Enrichment seed / drain |

**Full install guide:** [docs/apps-and-install.md](docs/apps-and-install.md)

## Quick start

```bash
cp .env.example .env    # set JWT_SECRET and BOOTSTRAP_ADMIN_*
make migrate
make api                # http://localhost:8080

# other terminal
go run ./cmd/obdctl --login --email tech@demo.shop --password changeme123
go run ./cmd/obdctl --mock
```

Register a shop first if needed:

```bash
curl -s http://localhost:8080/v1/auth/register -H 'Content-Type: application/json' -d '{
  "email":"tech@demo.shop",
  "password":"changeme123",
  "display_name":"Bay Tech",
  "shop_name":"Demo Shop"
}' | jq
```

## Auth roles

| Role | How you get it | Powers |
|---|---|---|
| `super_admin` | `BOOTSTRAP_ADMIN_*` on first API start | Orgs, techs, registration settings |
| `org_admin` | Self-register a new shop | Shop invite, activate/deactivate techs |
| `technician` | Register with invite code | Scan, history, explain |

## Architecture

| Layer | Owns |
|---|---|
| Local client (`obdctl` / future desktop) | USB/BT, ELM327, DTC decode, PID snapshot |
| MechMind API | Auth, orgs, scans, KB, enrichment, diagnosis findings |
| 3rd-party LLM (later) | Grounded explanation text only |

## Software diagnosis

Scans store an `observations` pack (live PIDs, freeze-frames, link metrics).  
`GET /v1/codes/{code}/explain?vin=` returns rule-based `findings` plus fleet `co_occurrence`, and always includes a lean `llm_packet`.

Optional prose: `&narrative=1` (requires `LLM_ENABLED=true`). See [docs/llm-context.md](docs/llm-context.md).

## Verifying OBD hardware

Do not override a real car’s VIN when proving the USB link. Mock always uses `MOCKTESTVIN000001`.

```bash
make mock-scan                             # software only
go run ./cmd/obdctl --device /dev/ttyUSB0  # live ECU VIN + codes
```

Details: [docs/verifying-obd-reads.md](docs/verifying-obd-reads.md)

## Market focus

Nigeria · model year **≥ 2010** · Toyota, Honda, Hyundai, Kia, Mercedes-Benz.  
Lean NHTSA enrichment (no API keys): [docs/enrichment-sources.md](docs/enrichment-sources.md)

```bash
make enrich-stats
make enrich-seed && make enrich-drain
```

## API surface

Public: `POST /v1/auth/register`, `POST /v1/auth/login`  
Auth: `GET /v1/auth/me`, `POST /v1/scans`, `GET /v1/scans/{id}`, `GET /v1/vehicles/{vin}/scans`, `GET /v1/codes/{code}/explain?vin=`  
Admin: orgs, technicians, settings, enrichment seed/stats
