package enrichment

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/autoservice/autoservice/internal/market"
	"github.com/autoservice/autoservice/internal/store"
)

type Worker struct {
	Store  *store.Store
	Client *Client
	Log    *log.Logger
}

func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	if w.Log == nil {
		w.Log = log.Default()
	}
	if w.Client == nil {
		w.Client = NewClient()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	enabled, _ := w.Store.Setting(ctx, "enrichment_enabled")
	if enabled == "false" {
		return
	}
	for i := 0; i < 5; i++ {
		job, err := w.Store.ClaimEnrichmentJob(ctx)
		if err != nil || job == nil {
			return
		}
		if err := w.Process(ctx, job); err != nil {
			w.Log.Printf("enrichment job %s failed: %v", job.ID, err)
			_ = w.Store.FailEnrichmentJob(ctx, job.ID, err.Error(), time.Now().UTC().Add(2*time.Minute))
			continue
		}
		_ = w.Store.CompleteEnrichmentJob(ctx, job.ID)
	}
}

func (w *Worker) Process(ctx context.Context, job *store.EnrichmentJob) error {
	switch job.Kind {
	case "vin_decode":
		return w.vinDecode(ctx, job.Payload)
	case "recalls_mmy":
		return w.recallsMMY(ctx, job.Payload)
	case "seed_catalog":
		return w.seedCatalog(ctx)
	default:
		return nil
	}
}

func (w *Worker) vinDecode(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		VehicleID string `json:"vehicle_id"`
		VIN       string `json:"vin"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	vid, err := uuid.Parse(p.VehicleID)
	if err != nil {
		return err
	}

	lean, err := w.Client.DecodeVIN(ctx, p.VIN)
	if err != nil {
		return err
	}
	source := "nhtsa_vpic"
	if lean.Model == "" || lean.Year == 0 {
		lean.ErrorText = "incomplete_vpic"
	}

	inScope := market.InMarketScope(lean.Make, lean.Year)
	engine := lean.DisplacementL
	if lean.Cylinders != "" {
		if engine != "" {
			engine += "L "
		}
		engine += lean.Cylinders + "cyl"
	}
	if lean.EngineNote != "" {
		if engine != "" {
			engine += " · "
		}
		engine += lean.EngineNote
	}

	if err := w.Store.UpsertVehicleIdentity(ctx, vid, lean.Make, lean.Model, lean.Year, engine); err != nil {
		return err
	}
	if err := w.Store.UpsertVehicleEnrichment(ctx, store.VehicleEnrichment{
		VehicleID:     vid,
		Make:          lean.Make,
		Model:         lean.Model,
		Year:          lean.Year,
		BodyClass:     lean.BodyClass,
		FuelType:      lean.FuelType,
		DisplacementL: lean.DisplacementL,
		Cylinders:     lean.Cylinders,
		DriveType:     lean.DriveType,
		EngineNote:    lean.EngineNote,
		InMarketScope: inScope,
		Source:        source,
	}); err != nil {
		return err
	}

	if !inScope {
		w.Log.Printf("vin %s decoded %s %s %d — outside NG 2010+ top-5 scope; skipping recalls", p.VIN, lean.Make, lean.Model, lean.Year)
		return nil
	}

	n, err := w.Store.AttachCatalogRecallsToVehicle(ctx, vid, lean.Make, lean.Model, lean.Year)
	if err != nil {
		return err
	}
	if n == 0 {
		_, _ = w.Store.EnqueueEnrichmentJob(ctx, "recalls_mmy", map[string]any{
			"vehicle_id": p.VehicleID,
			"make":       lean.Make,
			"model":      lean.Model,
			"year":       lean.Year,
		}, time.Now().UTC())
	}
	return nil
}

func (w *Worker) recallsMMY(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		VehicleID string `json:"vehicle_id"`
		Make      string `json:"make"`
		Model     string `json:"model"`
		Year      int    `json:"year"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	if !market.InMarketScope(p.Make, p.Year) {
		return nil
	}
	recalls, err := w.Client.RecallsByMMY(ctx, p.Make, p.Model, p.Year)
	if err != nil {
		return err
	}
	for _, r := range recalls {
		if err := w.Store.UpsertRecallCatalog(ctx, store.RecallCatalogRow{
			Make:           p.Make,
			Model:          p.Model,
			Year:           p.Year,
			CampaignNumber: r.CampaignNumber,
			Component:      r.Component,
			Summary:        r.Summary,
			Consequence:    r.Consequence,
			Remedy:         r.Remedy,
			ReportDate:     r.ReportDate,
			Source:         "nhtsa",
		}); err != nil {
			return err
		}
	}
	if p.VehicleID != "" {
		vid, err := uuid.Parse(p.VehicleID)
		if err == nil {
			_, _ = w.Store.AttachCatalogRecallsToVehicle(ctx, vid, p.Make, p.Model, p.Year)
		}
	}
	// Be polite to NHTSA public API.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(400 * time.Millisecond):
	}
	return nil
}

func (w *Worker) seedCatalog(ctx context.Context) error {
	models, err := w.Store.ListMarketModels(ctx)
	if err != nil {
		return err
	}
	enqueued := 0
	for _, m := range models {
		for year := market.MinModelYear; year <= market.MaxSeedYear; year += market.SeedYearStep {
			_, err := w.Store.EnqueueEnrichmentJob(ctx, "recalls_mmy", map[string]any{
				"make":  m.NHTSAName,
				"model": m.NHTSAModel,
				"year":  year,
			}, time.Now().UTC().Add(time.Duration(enqueued)*500*time.Millisecond))
			if err != nil {
				return err
			}
			enqueued++
		}
	}
	w.Log.Printf("seed_catalog enqueued %d recall jobs for NG top makes (2010+%d step)", enqueued, market.SeedYearStep)
	return nil
}

// EnqueueSeed queues a one-shot catalog seed if none pending/done recently.
func EnqueueSeed(ctx context.Context, st *store.Store) error {
	ok, err := st.HasRecentJob(ctx, "seed_catalog", 24*time.Hour)
	if err != nil || ok {
		return err
	}
	_, err = st.EnqueueEnrichmentJob(ctx, "seed_catalog", map[string]any{"region": market.RegionCode}, time.Now().UTC())
	return err
}

func EnqueueVINDecode(ctx context.Context, st *store.Store, vehicleID uuid.UUID, vin string) error {
	_, err := st.EnqueueEnrichmentJob(ctx, "vin_decode", map[string]any{
		"vehicle_id": vehicleID.String(),
		"vin":        vin,
	}, time.Now().UTC())
	return err
}
