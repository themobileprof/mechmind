package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type EnrichmentJob struct {
	ID      uuid.UUID
	Kind    string
	Payload json.RawMessage
}

type VehicleEnrichment struct {
	VehicleID     uuid.UUID `json:"vehicle_id"`
	Make          string    `json:"make"`
	Model         string    `json:"model"`
	Year          int       `json:"year"`
	BodyClass     string    `json:"body_class,omitempty"`
	FuelType      string    `json:"fuel_type,omitempty"`
	DisplacementL string    `json:"displacement_l,omitempty"`
	Cylinders     string    `json:"cylinders,omitempty"`
	DriveType     string    `json:"drive_type,omitempty"`
	EngineNote    string    `json:"engine_note,omitempty"`
	InMarketScope bool      `json:"in_market_scope"`
	Source        string    `json:"source"`
	EnrichedAt    time.Time `json:"enriched_at"`
}

type RecallCatalogRow struct {
	Make           string `json:"make"`
	Model          string `json:"model"`
	Year           int    `json:"year"`
	CampaignNumber string `json:"campaign_number"`
	Component      string `json:"component,omitempty"`
	Summary        string `json:"summary,omitempty"`
	Consequence    string `json:"consequence,omitempty"`
	Remedy         string `json:"remedy,omitempty"`
	ReportDate     string `json:"report_date,omitempty"`
	Source         string `json:"source"`
}

type MarketModel struct {
	Make       string
	NHTSAName  string
	Model      string
	NHTSAModel string
	YearFrom   int
	YearTo     *int
}

func (s *Store) EnqueueEnrichmentJob(ctx context.Context, kind string, payload any, runAfter time.Time) (uuid.UUID, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO enrichment_jobs (kind, payload, run_after)
		VALUES ($1, $2, $3)
		RETURNING id
	`, kind, b, runAfter).Scan(&id)
	return id, err
}

func (s *Store) HasRecentJob(ctx context.Context, kind string, within time.Duration) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM enrichment_jobs
		WHERE kind = $1 AND created_at > $2
		  AND status IN ('pending','running','done')
	`, kind, time.Now().UTC().Add(-within)).Scan(&n)
	return n > 0, err
}

func (s *Store) ClaimEnrichmentJob(ctx context.Context) (*EnrichmentJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var job EnrichmentJob
	err = tx.QueryRow(ctx, `
		SELECT id, kind, payload
		FROM enrichment_jobs
		WHERE status = 'pending' AND run_after <= now()
		ORDER BY run_after
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&job.ID, &job.Kind, &job.Payload)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE enrichment_jobs
		SET status = 'running', attempts = attempts + 1, updated_at = now()
		WHERE id = $1
	`, job.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) CompleteEnrichmentJob(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE enrichment_jobs SET status = 'done', updated_at = now(), last_error = NULL
		WHERE id = $1
	`, id)
	return err
}

func (s *Store) FailEnrichmentJob(ctx context.Context, id uuid.UUID, msg string, retryAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE enrichment_jobs
		SET status = CASE WHEN attempts >= 5 THEN 'failed' ELSE 'pending' END,
		    last_error = $2,
		    run_after = $3,
		    updated_at = now()
		WHERE id = $1
	`, id, truncateErr(msg), retryAt)
	return err
}

func (s *Store) UpsertVehicleIdentity(ctx context.Context, vehicleID uuid.UUID, make, model string, year int, engine string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE vehicles
		SET make = COALESCE(NULLIF($2,''), make),
		    model = COALESCE(NULLIF($3,''), model),
		    year = COALESCE(NULLIF($4,0), year),
		    engine = COALESCE(NULLIF($5,''), engine),
		    updated_at = now()
		WHERE id = $1
	`, vehicleID, make, model, year, engine)
	return err
}

func (s *Store) UpsertVehicleEnrichment(ctx context.Context, e VehicleEnrichment) error {
	if e.Source == "" {
		e.Source = "nhtsa_vpic"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO vehicle_enrichment (
			vehicle_id, make, model, year, body_class, fuel_type, displacement_l, cylinders,
			drive_type, engine_note, in_market_scope, source, enriched_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
		ON CONFLICT (vehicle_id) DO UPDATE SET
			make = EXCLUDED.make,
			model = EXCLUDED.model,
			year = EXCLUDED.year,
			body_class = EXCLUDED.body_class,
			fuel_type = EXCLUDED.fuel_type,
			displacement_l = EXCLUDED.displacement_l,
			cylinders = EXCLUDED.cylinders,
			drive_type = EXCLUDED.drive_type,
			engine_note = EXCLUDED.engine_note,
			in_market_scope = EXCLUDED.in_market_scope,
			source = EXCLUDED.source,
			enriched_at = now()
	`, e.VehicleID, e.Make, e.Model, e.Year, e.BodyClass, e.FuelType, e.DisplacementL, e.Cylinders,
		e.DriveType, e.EngineNote, e.InMarketScope, e.Source)
	return err
}

func (s *Store) GetVehicleEnrichment(ctx context.Context, vehicleID uuid.UUID) (*VehicleEnrichment, error) {
	var e VehicleEnrichment
	err := s.pool.QueryRow(ctx, `
		SELECT vehicle_id, COALESCE(make,''), COALESCE(model,''), COALESCE(year,0),
		       COALESCE(body_class,''), COALESCE(fuel_type,''), COALESCE(displacement_l,''),
		       COALESCE(cylinders,''), COALESCE(drive_type,''), COALESCE(engine_note,''),
		       in_market_scope, source, enriched_at
		FROM vehicle_enrichment WHERE vehicle_id = $1
	`, vehicleID).Scan(
		&e.VehicleID, &e.Make, &e.Model, &e.Year, &e.BodyClass, &e.FuelType, &e.DisplacementL,
		&e.Cylinders, &e.DriveType, &e.EngineNote, &e.InMarketScope, &e.Source, &e.EnrichedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return &e, err
}

func (s *Store) UpsertRecallCatalog(ctx context.Context, r RecallCatalogRow) error {
	if r.Source == "" {
		r.Source = "nhtsa"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO recall_catalog (
			make, model, year, campaign_number, component, summary, consequence, remedy, report_date, source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (make, model, year, campaign_number) DO UPDATE SET
			component = EXCLUDED.component,
			summary = EXCLUDED.summary,
			consequence = EXCLUDED.consequence,
			remedy = EXCLUDED.remedy,
			report_date = EXCLUDED.report_date,
			fetched_at = now()
	`, r.Make, r.Model, r.Year, r.CampaignNumber, r.Component, r.Summary, r.Consequence, r.Remedy, r.ReportDate, r.Source)
	return err
}

func (s *Store) AttachCatalogRecallsToVehicle(ctx context.Context, vehicleID uuid.UUID, make, model string, year int) (int, error) {
	ct, err := s.pool.Exec(ctx, `
		INSERT INTO vehicle_recalls (
			vehicle_id, make, model, year, campaign_number, component, summary, consequence, remedy, report_date, source
		)
		SELECT $1, make, model, year, campaign_number, component, summary, consequence, remedy, report_date, source
		FROM recall_catalog
		WHERE lower(make) = lower($2) AND lower(model) = lower($3) AND year = $4
		ON CONFLICT (vehicle_id, campaign_number) DO NOTHING
	`, vehicleID, make, model, year)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

func (s *Store) ListRecallsForVehicle(ctx context.Context, vehicleID uuid.UUID) ([]RecallCatalogRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT make, model, year, campaign_number, COALESCE(component,''), COALESCE(summary,''),
		       COALESCE(consequence,''), COALESCE(remedy,''), COALESCE(report_date,''), source
		FROM vehicle_recalls
		WHERE vehicle_id = $1
		ORDER BY report_date DESC NULLS LAST
		LIMIT 20
	`, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecallCatalogRow
	for rows.Next() {
		var r RecallCatalogRow
		if err := rows.Scan(&r.Make, &r.Model, &r.Year, &r.CampaignNumber, &r.Component, &r.Summary, &r.Consequence, &r.Remedy, &r.ReportDate, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListRecallsMMY(ctx context.Context, make, model string, year int) ([]RecallCatalogRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT make, model, year, campaign_number, COALESCE(component,''), COALESCE(summary,''),
		       COALESCE(consequence,''), COALESCE(remedy,''), COALESCE(report_date,''), source
		FROM recall_catalog
		WHERE lower(make) = lower($1) AND lower(model) = lower($2) AND year = $3
		ORDER BY report_date DESC NULLS LAST
		LIMIT 20
	`, make, model, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecallCatalogRow
	for rows.Next() {
		var r RecallCatalogRow
		if err := rows.Scan(&r.Make, &r.Model, &r.Year, &r.CampaignNumber, &r.Component, &r.Summary, &r.Consequence, &r.Remedy, &r.ReportDate, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListMarketModels(ctx context.Context) ([]MarketModel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT mk.name, mk.nhtsa_name, md.name, md.nhtsa_name, md.year_from, md.year_to
		FROM market_models md
		JOIN market_makes mk ON mk.id = md.make_id
		ORDER BY mk.sort_order, md.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MarketModel
	for rows.Next() {
		var m MarketModel
		if err := rows.Scan(&m.Make, &m.NHTSAName, &m.Model, &m.NHTSAModel, &m.YearFrom, &m.YearTo); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) EnrichmentStats(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.pool.Query(ctx, `
		SELECT status, count(*) FROM enrichment_jobs GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	var recalls, enriched int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM recall_catalog`).Scan(&recalls)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM vehicle_enrichment`).Scan(&enriched)
	out["recall_catalog"] = recalls
	out["vehicles_enriched"] = enriched
	return out, rows.Err()
}

func truncateErr(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
