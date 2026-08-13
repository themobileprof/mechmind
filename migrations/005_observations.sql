-- Software diagnosis: persist observation packs with each scan.

ALTER TABLE scan_sessions
    ADD COLUMN IF NOT EXISTS observations JSONB;

CREATE INDEX IF NOT EXISTS scan_sessions_observations_gin
    ON scan_sessions USING GIN (observations);

-- Helpful for co-occurrence queries already covered by dtc_observations indexes.
