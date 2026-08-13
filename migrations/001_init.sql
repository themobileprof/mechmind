# Autoservice/MechMind schema: orgs, auth, vehicles, scans, knowledge

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    network_id  TEXT,
    invite_code TEXT UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE technicians (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID REFERENCES organizations(id),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'technician'
                  CHECK (role IN ('super_admin', 'org_admin', 'technician')),
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Super admins may have NULL org_id; technicians/org_admins must belong to an org.
ALTER TABLE technicians
    ADD CONSTRAINT technicians_org_required
    CHECK (role = 'super_admin' OR org_id IS NOT NULL);

CREATE TABLE app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO app_settings (key, value) VALUES
    ('allow_self_register', 'true'),
    ('require_invite_code', 'false');

CREATE TABLE vehicles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vin         TEXT NOT NULL UNIQUE,
    make        TEXT,
    model       TEXT,
    year        INT,
    engine      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE scan_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id      UUID NOT NULL REFERENCES vehicles(id),
    technician_id   UUID NOT NULL REFERENCES technicians(id),
    org_id          UUID NOT NULL REFERENCES organizations(id),
    link_type       TEXT NOT NULL CHECK (link_type IN ('usb', 'bluetooth', 'mock')),
    adapter_name    TEXT,
    protocol        TEXT,
    mileage_km      INT,
    notes           TEXT,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE dtc_observations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_session_id   UUID NOT NULL REFERENCES scan_sessions(id) ON DELETE CASCADE,
    code              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'confirmed'
                      CHECK (status IN ('pending', 'confirmed', 'permanent', 'cleared')),
    freeze_frame      JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX dtc_observations_code_idx ON dtc_observations (code);
CREATE INDEX scan_sessions_vehicle_idx ON scan_sessions (vehicle_id);
CREATE INDEX scan_sessions_org_idx ON scan_sessions (org_id);

CREATE TABLE knowledge_articles (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code          TEXT NOT NULL,
    vehicle_scope TEXT NOT NULL DEFAULT '*',
    title         TEXT NOT NULL,
    summary       TEXT NOT NULL,
    likely_causes TEXT[] NOT NULL DEFAULT '{}',
    tests         TEXT[] NOT NULL DEFAULT '{}',
    parts         TEXT[] NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX knowledge_articles_code_scope_idx
    ON knowledge_articles (code, vehicle_scope);

INSERT INTO knowledge_articles (code, vehicle_scope, title, summary, likely_causes, tests, parts) VALUES
(
    'P0300',
    '*',
    'Random/Multiple Cylinder Misfire Detected',
    'The ECU detected misfires across multiple cylinders or could not isolate a single cylinder. Treat as a drivability and catalyst-risk fault until confirmed otherwise.',
    ARRAY['Ignition coils or plugs worn','Fuel delivery / low pressure','Vacuum leak','Crank/cam correlation','Contaminated fuel'],
    ARRAY['Pull freeze-frame (RPM, load, STFT/LTFT)','Relative compression or power balance','Fuel pressure & injector contribution','Smoke-test intake','Inspect plugs/coils for uneven wear'],
    ARRAY['Spark plugs','Ignition coils','Fuel filter','Intake gaskets']
),
(
    'P0171',
    '*',
    'System Too Lean (Bank 1)',
    'Fuel trim adaptation exceeded lean limits on bank 1. Often intake air metering, vacuum leak, or fuel delivery — not automatically an O2 sensor.',
    ARRAY['Vacuum / unmetered air leak','MAF contamination or failure','Low fuel pressure','Exhaust leak upstream of O2','Weak injectors'],
    ARRAY['Graph STFT/LTFT at idle and 2500 RPM','Smoke-test intake tract','MAF grams/sec vs expected','Fuel pressure under load','Inspect upstream exhaust leaks'],
    ARRAY['MAF sensor','Intake boots / PCV hoses','Fuel pump / filter','O2 sensor (only after root cause)']
),
(
    'P0420',
    '*',
    'Catalyst System Efficiency Below Threshold (Bank 1)',
    'Downstream O2 activity suggests the catalytic converter is not storing oxygen as expected. Confirm it is not a false trigger from exhaust leaks or aging sensors before condemning the cat.',
    ARRAY['Aged / damaged catalytic converter','Downstream O2 sensor slow','Exhaust leak before downstream O2','Upstream misfire poisoning cat','Incorrect fuel trim history'],
    ARRAY['Compare upstream vs downstream O2 waveforms','Check for exhaust leaks','Review misfire & fuel trim history','Temperature delta across cat if accessible'],
    ARRAY['Catalytic converter','Downstream O2 sensor','Exhaust gaskets']
);
