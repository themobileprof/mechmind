-- Lean enrichment for Nigeria-focused fleet (2010+, top makes).
-- Store triage fields only — never full upstream payloads.

CREATE TABLE market_makes (
    id          SERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    nhtsa_name  TEXT NOT NULL,
    sort_order  INT NOT NULL DEFAULT 100
);

CREATE TABLE market_models (
    id          SERIAL PRIMARY KEY,
    make_id     INT NOT NULL REFERENCES market_makes(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    nhtsa_name  TEXT NOT NULL,
    year_from   INT NOT NULL DEFAULT 2010,
    year_to     INT,
    UNIQUE (make_id, name)
);

CREATE TABLE vehicle_enrichment (
    vehicle_id       UUID PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE,
    make             TEXT,
    model            TEXT,
    year             INT,
    body_class       TEXT,
    fuel_type        TEXT,
    displacement_l   TEXT,
    cylinders        TEXT,
    drive_type       TEXT,
    engine_note      TEXT,
    in_market_scope  BOOLEAN NOT NULL DEFAULT false,
    enriched_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    source           TEXT NOT NULL DEFAULT 'nhtsa_vpic'
);

CREATE TABLE vehicle_recalls (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id       UUID REFERENCES vehicles(id) ON DELETE CASCADE,
    make             TEXT NOT NULL,
    model            TEXT NOT NULL,
    year             INT NOT NULL,
    campaign_number  TEXT NOT NULL,
    component        TEXT,
    summary          TEXT,
    consequence      TEXT,
    remedy           TEXT,
    report_date      TEXT,
    source           TEXT NOT NULL DEFAULT 'nhtsa',
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vehicle_id, campaign_number)
);

CREATE INDEX vehicle_recalls_vehicle_idx ON vehicle_recalls (vehicle_id);
CREATE INDEX vehicle_recalls_mmy_idx ON vehicle_recalls (make, model, year);

-- Catalog-level recall cache (shared across VINs of same MMY)
CREATE TABLE recall_catalog (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    make             TEXT NOT NULL,
    model            TEXT NOT NULL,
    year             INT NOT NULL,
    campaign_number  TEXT NOT NULL,
    component        TEXT,
    summary          TEXT,
    consequence      TEXT,
    remedy           TEXT,
    report_date      TEXT,
    source           TEXT NOT NULL DEFAULT 'nhtsa',
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (make, model, year, campaign_number)
);

CREATE TABLE enrichment_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         TEXT NOT NULL CHECK (kind IN ('vin_decode', 'recalls_mmy', 'seed_catalog')),
    payload      JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'running', 'done', 'failed', 'skipped')),
    attempts     INT NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    run_after    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX enrichment_jobs_pending_idx
    ON enrichment_jobs (status, run_after)
    WHERE status = 'pending';

INSERT INTO app_settings (key, value) VALUES
    ('market_region', 'NG'),
    ('min_vehicle_year', '2010'),
    ('enrichment_enabled', 'true')
ON CONFLICT (key) DO NOTHING;

-- Top 5 common makes for Nigerian roads / tokunbo + recent sales mix.
INSERT INTO market_makes (name, nhtsa_name, sort_order) VALUES
    ('Toyota', 'TOYOTA', 1),
    ('Honda', 'HONDA', 2),
    ('Hyundai', 'HYUNDAI', 3),
    ('Kia', 'KIA', 4),
    ('Mercedes-Benz', 'MERCEDES-BENZ', 5);

INSERT INTO market_models (make_id, name, nhtsa_name, year_from, year_to)
SELECT m.id, x.name, x.nhtsa_name, 2010, 2026
FROM market_makes m
JOIN (VALUES
    ('Toyota', 'Corolla', 'COROLLA'),
    ('Toyota', 'Camry', 'CAMRY'),
    ('Toyota', 'RAV4', 'RAV4'),
    ('Toyota', 'Hilux', 'HILUX'),
    ('Toyota', 'Highlander', 'HIGHLANDER'),
    ('Toyota', 'Prado', 'LAND CRUISER'),
    ('Toyota', 'Sienna', 'SIENNA'),
    ('Honda', 'Accord', 'ACCORD'),
    ('Honda', 'Civic', 'CIVIC'),
    ('Honda', 'CR-V', 'CR-V'),
    ('Honda', 'Pilot', 'PILOT'),
    ('Hyundai', 'Elantra', 'ELANTRA'),
    ('Hyundai', 'Sonata', 'SONATA'),
    ('Hyundai', 'Tucson', 'TUCSON'),
    ('Hyundai', 'Santa Fe', 'SANTA FE'),
    ('Kia', 'Sportage', 'SPORTAGE'),
    ('Kia', 'Sorento', 'SORENTO'),
    ('Kia', 'Rio', 'RIO'),
    ('Kia', 'Optima', 'OPTIMA'),
    ('Kia', 'Forte', 'FORTE'),
    ('Mercedes-Benz', 'C-Class', 'C-CLASS'),
    ('Mercedes-Benz', 'E-Class', 'E-CLASS'),
    ('Mercedes-Benz', 'GLC', 'GLC'),
    ('Mercedes-Benz', 'GLE', 'GLE'),
    ('Mercedes-Benz', 'GLK', 'GLK-CLASS')
) AS x(make, name, nhtsa_name) ON x.make = m.name;

-- Electronics / network / emissions triage articles (2010+ relevant).
INSERT INTO knowledge_articles (code, vehicle_scope, title, summary, likely_causes, tests, parts) VALUES
(
    'U0100', '*',
    'Lost Communication With ECM/PCM',
    'CAN/network fault: scan tool cannot talk to the engine controller, or modules report ECM offline. Common on 2010+ vehicles with multiple gateways.',
    ARRAY['Blown ECM/PCM fuse or relay','Open/short on CAN-H/CAN-L','Failed ECM power/ground','Gateway module fault','Corroded OBD or harness connector'],
    ARRAY['Verify battery voltage >12.2V key on','Check ECM fuses/relays under load','Measure CAN termination ~60Ω with battery disconnected','Scope CAN for silence vs bus-off','Attempt module ID list — note which modules respond'],
    ARRAY['ECM fuse/relay','CAN wiring repair','ECM/PCM (after power/network confirmed)','Gateway module']
),
(
    'U0140', '*',
    'Lost Communication With Body Control Module',
    'Body electronics offline on the network. Often affects lighting, locks, wipers, and can block other module diagnostics on 2010+ platforms.',
    ARRAY['BCM power/ground fault','CAN/LIN wiring damage','Water intrusion in BCM','Incorrect coding after replacement'],
    ARRAY['Confirm BCM powers and grounds','Check related fuses','Network topology — which bus is down','Look for water in kick-panel / under-dash BCM'],
    ARRAY['BCM','Related fuses','Wiring repair']
),
(
    'P2610', '*',
    'ECM/PCM Engine Off Timer Performance',
    'Keep-alive / engine-off timer fault; often battery, power supply, or ECM internal issue on modern ECMs.',
    ARRAY['Weak battery / poor parasitic draw','ECM keep-alive (B+) circuit open','Recent battery disconnect without proper idle relearn','Failing ECM'],
    ARRAY['Battery/charging system test','Measure ECM B+ and ignition feeds','Check for DTCs after battery replaced','Verify no aftermarket alarm tapping keep-alive circuits'],
    ARRAY['Battery','ECM power circuit repair','ECM']
),
(
    'C0035', '*',
    'Left Front Wheel Speed Sensor Circuit',
    'ABS/ESC wheel-speed input fault — frequent on Nigerian roads due to hub corrosion, damaged tone rings, and harness chafe.',
    ARRAY['Failed wheel speed sensor','Damaged reluctor/tone ring','Harness open/short at knuckle','ABS module input fault','Debris packed in sensor gap'],
    ARRAY['Live-data wheel speeds at walk speed — compare LF vs others','Ohm/check sensor vs OEM range','Inspect connector for green corrosion','Visual tone ring cracks/missing teeth'],
    ARRAY['Wheel speed sensor','ABS tone ring / hub assembly','ABS harness']
),
(
    'B0100', '*',
    'Driver Airbag Circuit / Squib Fault (generic family)',
    'SRS squib or circuit resistance out of range. Do not probe with a standard ohmmeter across the airbag; use an SRS-safe load tool.',
    ARRAY['Clock spring wear','Airbag connector not locked after service','Damaged yellow SRS harness','Seat occupancy / buckle switch faults (related codes)'],
    ARRAY['Check SRS connectors after seat/column work','Inspect clock spring with steering wheel centered','Use scan tool SRS freeze frame','Never clear SRS codes without verifying repair'],
    ARRAY['Clock spring','Airbag module (OEM)','SRS connector repair']
),
(
    'P0300', 'TOYOTA',
    'Toyota Random Misfire — electronics-era triage',
    'On 2010+ Toyotas, also consider coil-on-plug failures, VVT oil control, and ECM ground integrity — not only plugs.',
    ARRAY['Ignition coil(s)','Spark plugs irregular wear','VVT oil control valve / dirty oil','Intake gasket unmetered air','Low fuel pressure'],
    ARRAY['Identify which cylinders misfire with Mode 06 / per-cylinder counters','Swap coils cylinder-to-cylinder','Check oil level/condition for VVT','Fuel pressure and long-term trims'],
    ARRAY['Ignition coils','Spark plugs','VVT oil control valve','Intake gasket']
),
(
    'P0300', 'HONDA',
    'Honda Random Misfire — electronics-era triage',
    '2010+ Hondas often show coil or injector imbalance; check PGM-FI relay and ground points before parts cannon.',
    ARRAY['Coils/plugs','Injector imbalance','PGM-FI relay','Vacuum leak at intake'],
    ARRAY['Per-cylinder misfire counts','Swap test coils','Listen/smoke for intake leaks','Check fuel pressure'],
    ARRAY['Ignition coils','Spark plugs','PGM-FI relay','Injectors']
),
(
    'P0171', 'HYUNDAI',
    'Hyundai/Kia Lean Bank 1 — triage',
    'Common on 2010+ Hyundai/Kia with cracked PCV valves/hoses and contaminated MAF after dusty operation.',
    ARRAY['PCV valve/hose crack','MAF contamination','Intake boot tear','Low fuel pressure'],
    ARRAY['Smoke test PCV/intake','MAF grams/sec vs expected','STFT/LTFT idle vs 2500 rpm','Fuel pressure'],
    ARRAY['PCV valve','Intake boot','MAF sensor','Fuel filter/pump']
),
(
    'P0171', 'KIA',
    'Kia Lean Bank 1 — triage',
    'Treat like Hyundai platform siblings: PCV, MAF, and intake leaks before replacing O2 sensors.',
    ARRAY['PCV/hose leak','MAF dirt','Intake leak','Fuel delivery'],
    ARRAY['Smoke test','Fuel trims','MAF data','Fuel pressure'],
    ARRAY['PCV valve','MAF','Intake hose','Fuel filter']
),
(
    'U0121', 'MERCEDES-BENZ',
    'Mercedes Lost Communication With ABS',
    'Frequent on 2010+ Mercedes when ABS module power, ground, or chassis CAN is compromised; often paired with ESP lamps.',
    ARRAY['ABS module power/ground','Chassis CAN fault','Failed ABS hydraulic module','Corroded underbody connectors'],
    ARRAY['Check ABS fuses/relays','Battery voltage stability during ABS self-test','Network topology with OEM-capable tool','Inspect connectors under battery tray / module'],
    ARRAY['ABS fuse/relay','ABS module','Wiring repair']
)
ON CONFLICT (code, vehicle_scope) DO NOTHING;
