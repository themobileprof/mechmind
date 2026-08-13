# Vehicle enrichment sources — how to get them

This guide covers **how to obtain** the data that makes Autoservice more informational than a generic OBD code reader, and **where it should live** in our architecture.

## Deployment focus (Nigeria)

| Constraint | Value |
|---|---|
| Region | Nigeria (`NG`) |
| Model years | **2010 and newer** (electronics-heavy fleet) |
| Makes | **Toyota, Honda, Hyundai, Kia, Mercedes-Benz** |

Background workers populate **lean triage fields only** (no full upstream JSON blobs). See `make enrich-seed` / `make enrich-drain`.

**Rule of thumb:** store shared enrichment on the **API / Postgres**. Cache on the local client only for offline UI. Never send raw enrichment dumps to a third-party LLM without redaction policy; RAG should retrieve *your* curated chunks.


| Priority | Source | Cost | Signup | Store on |
|---|---|---|---|---|
| P0 | NHTSA vPIC (VIN decode) | Free | None | API `vehicles` row |
| P0 | NHTSA recalls | Free | None | API (per VIN / per scan) |
| P1 | EPA fueleconomy.gov | Free | None | API vehicle context |
| P1 | Your `knowledge_articles` | Internal | — | API (moat) |
| P2 | CarAPI / Auto.dev / Vincario | Paid | Account + key | API |
| P2 | Parts catalogs | Paid | Dealer/shop account | API links |
| P3 | ALLDATA / Mitchell1 / Identifix | Paid + ToS | Sales / license | Curated KB only |

---

## 1. NHTSA vPIC — VIN → make / model / year / engine (free)

**What you get:** Official US VIN decode: make, model, model year, body class, engine, drive type, plant, etc.

**Docs:** https://vpic.nhtsa.dot.gov/api/

**How to get access:** No API key. Public HTTPS GET.

### Try it now

```bash
VIN=1HGCM82633A004352
curl -s "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVinValues/${VIN}?format=json" | jq '.Results[0] | {Make,Model,ModelYear,DisplacementL,EngineCylinders,FuelTypePrimary,BodyClass,ErrorCode,ErrorText}'
```

Optional model year hint (improves some edge cases):

```bash
curl -s "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVinValues/${VIN}?format=json&modelyear=2003" | jq '.Results[0]'
```

Batch (max 50 VINs), form POST:

```bash
curl -s -X POST "https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVINValuesBatch/" \
  -d "format=json" \
  -d "data=${VIN};5YJSA1E11FF000000;"
```

### Map into Autoservice

On `POST /v1/scans` (after VIN is known), call vPIC from the **API**, then update:

```text
vehicles.make, vehicles.model, vehicles.year, vehicles.engine
```

Also keep a JSONB column (recommended next migration) such as `vehicles.vpic_raw` or `vehicles.enrichment` for full Results[0].

Check `ErrorCode` / `ErrorText` in the response; non-zero often means partial decode.

### Rate / policy notes

- Intended for interactive / modest volume use.
- Cache by VIN on the API (decode once; refresh infrequently).
- Do **not** hammer from every bay client — one server-side enricher.

---

## 2. NHTSA recalls — open campaigns (free)

**What you get:** Safety recall campaigns for a vehicle.

**Portal / policy:** https://api.nhtsa.gov/  
**Important:** NHTSA states this API is **not** for bulk VIN harvesting and applies rate limiting.

### Endpoints to use

By make / model / year (prefer after vPIC decode so names match NHTSA’s catalog):

```bash
MAKE=HONDA
MODEL=ACCORD
YEAR=2003
curl -s "https://api.nhtsa.gov/recalls/recallsByVehicle?make=${MAKE}&model=${MODEL}&modelYear=${YEAR}" | jq .
```

By VIN (when available on their vehicle-id style routes — verify against current docs; naming has varied):

```bash
# Common pattern used by integrators — confirm in live docs if this 404s:
curl -s "https://api.nhtsa.gov/recalls/recallsByVehicleId?vin=${VIN}" | jq .
```

Complaints (optional context, same family of APIs):

```bash
curl -s "https://api.nhtsa.gov/complaints/complaintsByVehicle?make=${MAKE}&model=${MODEL}&modelYear=${YEAR}" | jq .
```

### Map into Autoservice

- Table idea: `vehicle_recalls (vehicle_id, campaign_number, component, summary, remedy, report_received, raw jsonb, fetched_at)`
- Attach a `recalls[]` section on `GET /v1/codes/{code}/explain?vin=` and on vehicle history UI.
- Refresh on scan or nightly job; respect rate limits (queue + cache).

### How to “get” it for product use

1. No signup.
2. Implement server-side client with timeouts, caching, and backoff.
3. Read https://api.nhtsa.gov/ use policy before scaling.

---

## 3. EPA fueleconomy.gov web services (free)

**What you get:** Fuel economy, vehicle menu IDs, some vehicle descriptors — useful context, not DTC diagnosis.

**Docs:** https://www.fueleconomy.gov/feg/ws/

### Example

```bash
# Menu of makes for a year
curl -s "https://www.fueleconomy.gov/ws/rest/vehicle/menu/make?year=2012" -H 'Accept: application/json'

# After you have EPA vehicle id:
# curl -s "https://www.fueleconomy.gov/ws/rest/vehicle/VEHICLE_ID" -H 'Accept: application/json'
```

### Map into Autoservice

Optional fields on `vehicles` or `vehicles.enrichment`: `epa_id`, `city_mpg`, `hwy_mpg`.  
Low priority vs NHTSA VIN + recalls.

---

## 4. Your proprietary knowledge base (internal — highest product value)

**What you get:** Causes, tests, parts, OEM notes grounded in *your* shops’ history — this is the moat.

**How to get / grow it:**

1. Seed rows in `knowledge_articles` (already in `migrations/001_init.sql`).
2. After each job, senior tech marks “what fixed it” → promote into KB.
3. Later: RAG embeddings (`pgvector`) over articles + anonymized scan notes.
4. LLM only sees retrieved KB + history snippets.

No external vendor required for P1 differentiation.

---

## 5. Commercial VIN / vehicle data APIs (paid)

Use when you need richer option packages, market specs, or non-US coverage beyond vPIC.

| Vendor | Typical offer | How to get started |
|---|---|---|
| [CarAPI](https://carapi.app/) | Year/make/model, VIN decode, trims | Create account → API key → REST |
| [Auto.dev](https://www.auto.dev/) | Vehicle data APIs | Sign up → key in dashboard |
| [Vincario](https://vincario.com/) | VIN decode / vehicle info | Register → API credentials |
| OEM / dealer data feeds | Build sheets, options | Usually B2B contracts; not self-serve |

**Integration pattern (API-side):**

```text
.env
  CARAPI_KEY=...
internal/enrichment/carapi.go  → EnrichVIN(ctx, vin) → upsert vehicles
```

Store normalized fields + `provider` + `fetched_at`. Never put the vendor API key in the desktop client.

---

## 6. Repair information systems (paid, license-sensitive)

| Product | Typical content | How to get access |
|---|---|---|
| [ALLDATA](https://www.alldata.com/) | OEM procedures, wiring, TSBs | Shop subscription / sales |
| [Mitchell1](https://mitchell1.com/) | ProDemand repair info | Shop subscription |
| [Identifix](https://www.identifix.com/) | Direct-hit repairs from techs | Shop subscription |
| Motor Information Systems | Parts / labor / repair data | Commercial license |

**Critical:** most ToS **forbid** dumping their manuals into your DB or feeding them wholesale to an LLM.

**Allowed pattern we should use:**

1. Tech uses vendor tool as licensed.
2. Tech (or integrator under contract) writes a **short derived note** into `knowledge_articles` (symptoms, tests that worked, parts).
3. Autoservice cites *your* article + scan history — not “ALLDATA page 12.”

If you want automated ingest, talk to their partnership / API teams first and get written redistribution terms.

---

## 7. Parts fitment catalogs (paid)

| Source | How to get | Use in product |
|---|---|---|
| PartsTech, WorldPac, NextPart, etc. | Shop account with local jobber / WD | “Likely parts” links on explain |
| Manufacturer EPC portals | Dealer/shop login | Manual part numbers into KB |

Prefer deep-links or SKU references on the API explain payload over scraping.

---

## 8. Community / PID / protocol references (mostly free)

| Source | URL / notes | Use |
|---|---|---|
| SAE J2012 summaries | Public DTC title lists | Already partially in `pkg/obd` |
| OpenXC | http://openxcplatform.com/ | Extra signal ideas |
| ELM327 AT command set | Adapter vendor docs | Client USB layer |

These improve the **local client** decode quality; still persist final scan results on the API.

---

## Recommended implementation order for this repo

1. **API enricher job** on scan create:
   - vPIC → update `vehicles`
   - recalls by make/model/year → store + attach to explain
2. **Admin KB editor** for `knowledge_articles`
3. **Cache layer** (VIN → enrichment) with TTL (e.g. 30–90 days)
4. Paid VIN API only if vPIC gaps hurt (non-US, incomplete options)
5. Repair DBs only via license-safe curation into KB + RAG

### Suggested next code touchpoints

```text
internal/enrichment/nhtsa.go     # DecodeVIN, RecallsByVehicle
internal/store/vehicles.go       # UpsertEnrichment
POST /v1/scans                   # after insert, enqueue enrich
GET  /v1/vehicles/{vin}          # return enrichment + recalls
```

### Env vars (when implemented)

```bash
# No key needed for NHTSA / EPA
NHTSA_VPIC_BASE=https://vpic.nhtsa.dot.gov/api
NHTSA_API_BASE=https://api.nhtsa.gov
ENRICHMENT_ENABLED=true

# Optional paid
# CARAPI_KEY=
# AUTODEV_API_KEY=
```

---

## Quick verification checklist

- [ ] `curl` vPIC for a known VIN returns Make/Model/ModelYear
- [ ] Recalling that make/model/year returns campaigns (or empty list)
- [ ] Enrichment runs on API, not from `obdctl` directly
- [ ] Results visible on explain / vehicle history for another logged-in tech in the same org
- [ ] Vendor keys (if any) only in server `.env`

---

## Links index

- NHTSA vPIC: https://vpic.nhtsa.dot.gov/api/
- NHTSA API policy: https://api.nhtsa.gov/
- EPA fuel economy WS: https://www.fueleconomy.gov/feg/ws/
- CarAPI: https://carapi.app/
- Auto.dev: https://www.auto.dev/
- Vincario: https://vincario.com/
- ALLDATA: https://www.alldata.com/
- Mitchell1: https://mitchell1.com/
- Identifix: https://www.identifix.com/
