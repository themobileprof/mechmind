package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/autoservice/autoservice/internal/auth"
	"github.com/autoservice/autoservice/pkg/obd"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrEmailTaken     = errors.New("email already registered")
	ErrInvalidCreds   = errors.New("invalid email or password")
	ErrInactive       = errors.New("account is inactive")
	ErrRegisterClosed = errors.New("self-registration is disabled")
	ErrBadInvite      = errors.New("invalid invite code")
)

type Store struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

type Technician struct {
	ID           uuid.UUID  `json:"id"`
	OrgID        *uuid.UUID `json:"org_id,omitempty"`
	Email        string     `json:"email"`
	DisplayName  string     `json:"display_name"`
	Role         string     `json:"role"`
	Active       bool       `json:"active"`
	PasswordHash string     `json:"-"`
}

type Organization struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	NetworkID  string    `json:"network_id,omitempty"`
	InviteCode string    `json:"invite_code,omitempty"`
}

type CreateScanInput struct {
	TechnicianID string
	OrgID        string
	VIN          string
	LinkType     string
	AdapterName  string
	Protocol     string
	Notes        string
	DTCs         []obd.DTC
	Observations *obd.Observations
}

type ScanRecord struct {
	ID           uuid.UUID         `json:"id"`
	VehicleID    uuid.UUID         `json:"vehicle_id"`
	VIN          string            `json:"vin"`
	TechnicianID uuid.UUID         `json:"technician_id"`
	OrgID        uuid.UUID         `json:"org_id"`
	LinkType     string            `json:"link_type"`
	AdapterName  string            `json:"adapter_name"`
	Protocol     string            `json:"protocol"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty"`
	DTCs         []obd.DTC         `json:"dtcs"`
	Observations *obd.Observations `json:"observations,omitempty"`
}

type KnowledgeArticle struct {
	Code         string   `json:"code"`
	VehicleScope string   `json:"vehicle_scope"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	LikelyCauses []string `json:"likely_causes"`
	Tests        []string `json:"tests"`
	Parts        []string `json:"parts"`
}

type Explanation struct {
	Code         string             `json:"code"`
	Generic      string             `json:"generic_description"`
	Article      *KnowledgeArticle  `json:"article,omitempty"`
	History      []HistoryHit       `json:"network_history"`
	Vehicle      *VehicleEnrichment `json:"vehicle,omitempty"`
	Recalls      []RecallCatalogRow `json:"open_recalls,omitempty"`
	MarketNote   string             `json:"market_note,omitempty"`
	Findings     []any              `json:"findings,omitempty"`
	Observations *obd.Observations  `json:"observations,omitempty"`
	CoOccurrence []CoOccurRow       `json:"co_occurrence,omitempty"`
}

type CoOccurRow struct {
	Code       string  `json:"code"`
	WithCount  int     `json:"with_count"`
	FocusCount int     `json:"focus_count"`
	Rate       float64 `json:"rate"`
}

type HistoryHit struct {
	ScanID    uuid.UUID `json:"scan_id"`
	VIN       string    `json:"vin"`
	LinkType  string    `json:"link_type"`
	StartedAt time.Time `json:"started_at"`
	Status    string    `json:"status"`
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if err == pgx.ErrNoRows {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, key, value)
	return err
}

func (s *Store) CountByRole(ctx context.Context, role string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM technicians WHERE role = $1`, role).Scan(&n)
	return n, err
}

func (s *Store) EnsureBootstrapAdmin(ctx context.Context, email, password, name string) (*Technician, error) {
	n, err := s.CountByRole(ctx, auth.RoleSuperAdmin)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	var t Technician
	err = s.pool.QueryRow(ctx, `
		INSERT INTO technicians (email, password_hash, display_name, role, org_id)
		VALUES ($1,$2,$3,$4,NULL)
		RETURNING id, org_id, email, display_name, role, active
	`, email, hash, name, auth.RoleSuperAdmin).Scan(
		&t.ID, &t.OrgID, &t.Email, &t.DisplayName, &t.Role, &t.Active,
	)
	return &t, err
}

func (s *Store) GetTechnicianByEmail(ctx context.Context, email string) (*Technician, error) {
	var t Technician
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, email, password_hash, display_name, role, active
		FROM technicians WHERE email = $1
	`, email).Scan(&t.ID, &t.OrgID, &t.Email, &t.PasswordHash, &t.DisplayName, &t.Role, &t.Active)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetTechnician(ctx context.Context, id uuid.UUID) (*Technician, error) {
	var t Technician
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, email, password_hash, display_name, role, active
		FROM technicians WHERE id = $1
	`, id).Scan(&t.ID, &t.OrgID, &t.Email, &t.PasswordHash, &t.DisplayName, &t.Role, &t.Active)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (*Technician, error) {
	t, err := s.GetTechnicianByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCreds
		}
		return nil, err
	}
	if !t.Active {
		return nil, ErrInactive
	}
	if !auth.CheckPassword(t.PasswordHash, password) {
		return nil, ErrInvalidCreds
	}
	return t, nil
}

type RegisterInput struct {
	Email       string
	Password    string
	DisplayName string
	ShopName    string
	InviteCode  string
}

func (s *Store) RegisterTechnician(ctx context.Context, in RegisterInput) (*Technician, *Organization, error) {
	allow, err := s.Setting(ctx, "allow_self_register")
	if err != nil {
		return nil, nil, err
	}
	if allow != "true" {
		return nil, nil, ErrRegisterClosed
	}

	requireInvite, _ := s.Setting(ctx, "require_invite_code")
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	var org Organization
	role := auth.RoleTechnician

	if in.InviteCode != "" {
		err = tx.QueryRow(ctx, `
			SELECT id, name, COALESCE(network_id,''), COALESCE(invite_code,'')
			FROM organizations WHERE invite_code = $1
		`, in.InviteCode).Scan(&org.ID, &org.Name, &org.NetworkID, &org.InviteCode)
		if err == pgx.ErrNoRows {
			return nil, nil, ErrBadInvite
		}
		if err != nil {
			return nil, nil, err
		}
	} else if requireInvite == "true" {
		return nil, nil, ErrBadInvite
	} else {
		if in.ShopName == "" {
			return nil, nil, fmt.Errorf("shop_name is required when not using an invite code")
		}
		org.ID = uuid.New()
		org.Name = in.ShopName
		org.InviteCode = auth.RandomInviteCode()
		org.NetworkID = org.InviteCode
		_, err = tx.Exec(ctx, `
			INSERT INTO organizations (id, name, network_id, invite_code)
			VALUES ($1,$2,$3,$4)
		`, org.ID, org.Name, org.NetworkID, org.InviteCode)
		if err != nil {
			return nil, nil, err
		}
		role = auth.RoleOrgAdmin // first member of a new shop
	}

	var t Technician
	err = tx.QueryRow(ctx, `
		INSERT INTO technicians (org_id, email, password_hash, display_name, role)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, org_id, email, display_name, role, active
	`, org.ID, in.Email, hash, in.DisplayName, role).Scan(
		&t.ID, &t.OrgID, &t.Email, &t.DisplayName, &t.Role, &t.Active,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, nil, ErrEmailTaken
		}
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &t, &org, nil
}

func (s *Store) CreateOrganization(ctx context.Context, name string) (*Organization, error) {
	org := Organization{
		ID:         uuid.New(),
		Name:       name,
		InviteCode: auth.RandomInviteCode(),
	}
	org.NetworkID = org.InviteCode
	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (id, name, network_id, invite_code)
		VALUES ($1,$2,$3,$4)
	`, org.ID, org.Name, org.NetworkID, org.InviteCode)
	return &org, err
}

func (s *Store) ListOrganizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(network_id,''), COALESCE(invite_code,'')
		FROM organizations ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.NetworkID, &o.InviteCode); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ListTechnicians(ctx context.Context, orgID *uuid.UUID) ([]Technician, error) {
	q := `
		SELECT id, org_id, email, display_name, role, active
		FROM technicians
	`
	var rows pgx.Rows
	var err error
	if orgID != nil {
		rows, err = s.pool.Query(ctx, q+` WHERE org_id = $1 ORDER BY created_at`, *orgID)
	} else {
		rows, err = s.pool.Query(ctx, q+` ORDER BY created_at`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Technician
	for rows.Next() {
		var t Technician
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Email, &t.DisplayName, &t.Role, &t.Active); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetTechnicianActive(ctx context.Context, id uuid.UUID, active bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE technicians SET active = $2 WHERE id = $1`, id, active)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateScan(ctx context.Context, in CreateScanInput) (*ScanRecord, error) {
	if in.VIN == "" {
		return nil, fmt.Errorf("vin is required")
	}
	techID, err := uuid.Parse(in.TechnicianID)
	if err != nil {
		return nil, fmt.Errorf("technician_id: %w", err)
	}
	orgID, err := uuid.Parse(in.OrgID)
	if err != nil {
		return nil, fmt.Errorf("org_id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var vehicleID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO vehicles (vin)
		VALUES ($1)
		ON CONFLICT (vin) DO UPDATE SET updated_at = now()
		RETURNING id
	`, in.VIN).Scan(&vehicleID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	scanID := uuid.New()
	var obsJSON []byte
	if in.Observations != nil {
		obsJSON, err = json.Marshal(in.Observations)
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO scan_sessions (id, vehicle_id, technician_id, org_id, link_type, adapter_name, protocol, started_at, finished_at, observations)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)
	`, scanID, vehicleID, techID, orgID, in.LinkType, in.AdapterName, in.Protocol, now, obsJSON)
	if err != nil {
		return nil, err
	}

	for _, d := range in.DTCs {
		var ff []byte
		if d.FreezeFrame != nil {
			ff, err = json.Marshal(d.FreezeFrame)
			if err != nil {
				return nil, err
			}
		}
		status := string(d.Status)
		if status == "" {
			status = string(obd.DTCConfirmed)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO dtc_observations (scan_session_id, code, status, freeze_frame)
			VALUES ($1,$2,$3,$4)
		`, scanID, d.Code, status, ff)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	finished := now
	return &ScanRecord{
		ID:           scanID,
		VehicleID:    vehicleID,
		VIN:          in.VIN,
		TechnicianID: techID,
		OrgID:        orgID,
		LinkType:     in.LinkType,
		AdapterName:  in.AdapterName,
		Protocol:     in.Protocol,
		StartedAt:    now,
		FinishedAt:   &finished,
		DTCs:         in.DTCs,
		Observations: in.Observations,
	}, nil
}

func (s *Store) GetScan(ctx context.Context, id uuid.UUID) (*ScanRecord, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT ss.id, ss.vehicle_id, v.vin, ss.technician_id, ss.org_id, ss.link_type,
		       COALESCE(ss.adapter_name,''), COALESCE(ss.protocol,''), ss.started_at, ss.finished_at, ss.observations
		FROM scan_sessions ss
		JOIN vehicles v ON v.id = ss.vehicle_id
		WHERE ss.id = $1
	`, id)

	var rec ScanRecord
	var obs []byte
	if err := row.Scan(
		&rec.ID, &rec.VehicleID, &rec.VIN, &rec.TechnicianID, &rec.OrgID, &rec.LinkType,
		&rec.AdapterName, &rec.Protocol, &rec.StartedAt, &rec.FinishedAt, &obs,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(obs) > 0 {
		var o obd.Observations
		if err := json.Unmarshal(obs, &o); err == nil {
			rec.Observations = &o
		}
	}

	dtcs, err := s.listDTCs(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	rec.DTCs = dtcs
	return &rec, nil
}

func (s *Store) ListScansByVIN(ctx context.Context, vin string, orgID *uuid.UUID, limit int) ([]ScanRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `
		SELECT ss.id, ss.vehicle_id, v.vin, ss.technician_id, ss.org_id, ss.link_type,
		       COALESCE(ss.adapter_name,''), COALESCE(ss.protocol,''), ss.started_at, ss.finished_at
		FROM scan_sessions ss
		JOIN vehicles v ON v.id = ss.vehicle_id
		WHERE v.vin = $1
	`
	args := []any{vin}
	if orgID != nil {
		q += ` AND ss.org_id = $2`
		args = append(args, *orgID)
		q += ` ORDER BY ss.started_at DESC LIMIT $3`
		args = append(args, limit)
	} else {
		q += ` ORDER BY ss.started_at DESC LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScanRecord
	for rows.Next() {
		var rec ScanRecord
		if err := rows.Scan(
			&rec.ID, &rec.VehicleID, &rec.VIN, &rec.TechnicianID, &rec.OrgID, &rec.LinkType,
			&rec.AdapterName, &rec.Protocol, &rec.StartedAt, &rec.FinishedAt,
		); err != nil {
			return nil, err
		}
		dtcs, err := s.listDTCs(ctx, rec.ID)
		if err != nil {
			return nil, err
		}
		rec.DTCs = dtcs
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) ExplainCode(ctx context.Context, code, vin string, orgID *uuid.UUID) (*Explanation, error) {
	exp := &Explanation{
		Code:    code,
		Generic: obd.DescribeCode(code),
		History: []HistoryHit{},
		Recalls: []RecallCatalogRow{},
	}

	makeHint := ""
	modelHint := ""
	if vin != "" {
		var vehicleID uuid.UUID
		var make, model string
		var year *int
		err := s.pool.QueryRow(ctx, `
			SELECT v.id, COALESCE(ve.make, v.make, ''), COALESCE(ve.model, v.model, ''), COALESCE(ve.year, v.year)
			FROM vehicles v
			LEFT JOIN vehicle_enrichment ve ON ve.vehicle_id = v.id
			WHERE v.vin = $1
		`, vin).Scan(&vehicleID, &make, &model, &year)
		if err == nil {
			makeHint = make
			modelHint = model
			if enr, eerr := s.GetVehicleEnrichment(ctx, vehicleID); eerr == nil {
				exp.Vehicle = enr
				if !enr.InMarketScope {
					exp.MarketNote = "Outside Nigeria focus scope (Toyota/Honda/Hyundai/Kia/Mercedes-Benz, model year 2010+). Lean decode stored; recall catalog skipped."
				}
			}
			if year != nil && *year > 0 && make != "" && model != "" {
				if recs, rerr := s.ListRecallsForVehicle(ctx, vehicleID); rerr == nil && len(recs) > 0 {
					exp.Recalls = recs
				} else if recs, rerr := s.ListRecallsMMY(ctx, make, model, *year); rerr == nil {
					exp.Recalls = recs
				}
			}
		}
	}

	var art KnowledgeArticle
	err := s.pool.QueryRow(ctx, `
		SELECT code, vehicle_scope, title, summary, likely_causes, tests, parts
		FROM knowledge_articles
		WHERE code = $1
		  AND (
		    vehicle_scope = '*'
		    OR upper(vehicle_scope) = upper($2)
		    OR upper(vehicle_scope) = upper($3)
		    OR ($2 = '' AND $3 = '')
		  )
		ORDER BY
		  CASE
		    WHEN $3 <> '' AND upper(vehicle_scope) = upper($3) THEN 0
		    WHEN $2 <> '' AND upper(vehicle_scope) = upper($2) THEN 1
		    WHEN vehicle_scope = '*' THEN 2
		    ELSE 3
		  END
		LIMIT 1
	`, code, makeHint, modelHint).Scan(&art.Code, &art.VehicleScope, &art.Title, &art.Summary, &art.LikelyCauses, &art.Tests, &art.Parts)
	if err == nil {
		exp.Article = &art
	} else if err != pgx.ErrNoRows {
		return nil, err
	}

	q := `
		SELECT ss.id, v.vin, ss.link_type, ss.started_at, d.status
		FROM dtc_observations d
		JOIN scan_sessions ss ON ss.id = d.scan_session_id
		JOIN vehicles v ON v.id = ss.vehicle_id
		WHERE d.code = $1
	`
	args := []any{code}
	argN := 2
	if orgID != nil {
		q += fmt.Sprintf(` AND ss.org_id = $%d`, argN)
		args = append(args, *orgID)
		argN++
	}
	if vin != "" {
		q += fmt.Sprintf(` AND (v.vin = $%d OR $%d = '')`, argN, argN)
		args = append(args, vin)
		argN++
	}
	q += ` ORDER BY ss.started_at DESC LIMIT 10`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h HistoryHit
		if err := rows.Scan(&h.ScanID, &h.VIN, &h.LinkType, &h.StartedAt, &h.Status); err != nil {
			return nil, err
		}
		exp.History = append(exp.History, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if vin != "" {
		if obs, oerr := s.LatestObservationsForVIN(ctx, vin, orgID); oerr == nil {
			exp.Observations = obs
		}
	}
	if co, cerr := s.DTCCoOccurrence(ctx, code, orgID, 5); cerr == nil {
		exp.CoOccurrence = co
	}
	return exp, nil
}

// LatestObservationsForVIN returns the newest scan observation pack for a VIN.
func (s *Store) LatestObservationsForVIN(ctx context.Context, vin string, orgID *uuid.UUID) (*obd.Observations, error) {
	q := `
		SELECT ss.observations
		FROM scan_sessions ss
		JOIN vehicles v ON v.id = ss.vehicle_id
		WHERE v.vin = $1 AND ss.observations IS NOT NULL
	`
	args := []any{vin}
	if orgID != nil {
		q += ` AND ss.org_id = $2`
		args = append(args, *orgID)
	}
	q += ` ORDER BY ss.started_at DESC LIMIT 1`
	var raw []byte
	err := s.pool.QueryRow(ctx, q, args...).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var obs obd.Observations
	if err := json.Unmarshal(raw, &obs); err != nil {
		return nil, err
	}
	return &obs, nil
}

// DTCCoOccurrence returns codes that commonly appear in the same scan session as focus.
func (s *Store) DTCCoOccurrence(ctx context.Context, focus string, orgID *uuid.UUID, limit int) ([]CoOccurRow, error) {
	if limit <= 0 {
		limit = 5
	}
	q := `
		WITH focus_scans AS (
			SELECT d.scan_session_id
			FROM dtc_observations d
			JOIN scan_sessions ss ON ss.id = d.scan_session_id
			WHERE upper(d.code) = upper($1)
	`
	args := []any{focus}
	argN := 2
	if orgID != nil {
		q += fmt.Sprintf(` AND ss.org_id = $%d`, argN)
		args = append(args, *orgID)
		argN++
	}
	q += `
		),
		focus_count AS (SELECT count(*)::float AS n FROM focus_scans)
		SELECT d.code, count(*)::int AS with_count, (SELECT n FROM focus_count)::int AS focus_count,
		       CASE WHEN (SELECT n FROM focus_count) = 0 THEN 0
		            ELSE count(*)::float / (SELECT n FROM focus_count) END AS rate
		FROM dtc_observations d
		JOIN focus_scans fs ON fs.scan_session_id = d.scan_session_id
		WHERE upper(d.code) <> upper($1)
		GROUP BY d.code
		HAVING count(*) >= 1
		ORDER BY rate DESC, with_count DESC
	`
	q += fmt.Sprintf(` LIMIT $%d`, argN)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoOccurRow
	for rows.Next() {
		var r CoOccurRow
		if err := rows.Scan(&r.Code, &r.WithCount, &r.FocusCount, &r.Rate); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LatestDTCsForVIN(ctx context.Context, vin string, orgID *uuid.UUID) ([]obd.DTC, error) {
	q := `
		SELECT ss.id
		FROM scan_sessions ss
		JOIN vehicles v ON v.id = ss.vehicle_id
		WHERE v.vin = $1
	`
	args := []any{vin}
	if orgID != nil {
		q += ` AND ss.org_id = $2`
		args = append(args, *orgID)
	}
	q += ` ORDER BY ss.started_at DESC LIMIT 1`
	var scanID uuid.UUID
	err := s.pool.QueryRow(ctx, q, args...).Scan(&scanID)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.listDTCs(ctx, scanID)
}

func (s *Store) listDTCs(ctx context.Context, scanID uuid.UUID) ([]obd.DTC, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT code, status, freeze_frame
		FROM dtc_observations
		WHERE scan_session_id = $1
		ORDER BY code
	`, scanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []obd.DTC
	for rows.Next() {
		var d obd.DTC
		var ff []byte
		if err := rows.Scan(&d.Code, &d.Status, &ff); err != nil {
			return nil, err
		}
		if len(ff) > 0 {
			_ = json.Unmarshal(ff, &d.FreezeFrame)
		}
		d.Description = obd.DescribeCode(d.Code)
		out = append(out, d)
	}
	return out, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate key"))
}
