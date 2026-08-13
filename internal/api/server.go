package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"

	"github.com/autoservice/autoservice/internal/auth"
	"github.com/autoservice/autoservice/internal/diagnosis"
	"github.com/autoservice/autoservice/internal/enrichment"
	"github.com/autoservice/autoservice/internal/explain"
	"github.com/autoservice/autoservice/internal/store"
	"github.com/autoservice/autoservice/pkg/obd"
)

type ctxKey int

const claimsKey ctxKey = 1

type Server struct {
	Store    *store.Store
	Auth     *auth.Manager
	Narrator *explain.Narrator
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PATCH", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/v1", func(r chi.Router) {
		r.Post("/auth/register", s.register)
		r.Post("/auth/login", s.login)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.me)

			r.Post("/scans", s.createScan)
			r.Get("/scans/{id}", s.getScan)
			r.Get("/vehicles/{vin}/scans", s.listVehicleScans)
			r.Get("/codes/{code}/explain", s.explainCode)

			r.Route("/admin", func(r chi.Router) {
				r.With(s.requireRole(auth.RoleSuperAdmin)).Get("/organizations", s.listOrgs)
				r.With(s.requireRole(auth.RoleSuperAdmin)).Post("/organizations", s.createOrg)
				r.With(s.requireRole(auth.RoleSuperAdmin)).Get("/technicians", s.listAllTechnicians)
				r.With(s.requireRole(auth.RoleSuperAdmin)).Patch("/settings/{key}", s.patchSetting)
				r.With(s.requireRole(auth.RoleSuperAdmin)).Get("/enrichment/stats", s.enrichmentStats)
				r.With(s.requireRole(auth.RoleSuperAdmin)).Post("/enrichment/seed", s.enrichmentSeed)
				r.With(s.requireRole(auth.RoleSuperAdmin, auth.RoleOrgAdmin)).Patch("/technicians/{id}/active", s.setActive)
				r.With(s.requireRole(auth.RoleOrgAdmin)).Get("/org/technicians", s.listOrgTechnicians)
			})
		})
	})

	return r
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := s.Auth.Parse(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := claimsFrom(r)
			if c == nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if _, ok := allowed[c.Role]; !ok {
				writeErr(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func claimsFrom(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(claimsKey).(*auth.Claims)
	return c
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	ShopName    string `json:"shop_name"`
	InviteCode  string `json:"invite_code"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, org, err := s.Store.RegisterTechnician(r.Context(), store.RegisterInput{
		Email:       strings.TrimSpace(strings.ToLower(req.Email)),
		Password:    req.Password,
		DisplayName: req.DisplayName,
		ShopName:    req.ShopName,
		InviteCode:  strings.TrimSpace(req.InviteCode),
	})
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, store.ErrRegisterClosed):
			status = http.StatusForbidden
		case errors.Is(err, store.ErrEmailTaken):
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	token, exp, err := s.issue(t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": exp,
		"technician": publicTech(t),
		"organization": org,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.Store.Authenticate(r.Context(), strings.TrimSpace(strings.ToLower(req.Email)), req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	token, exp, err := s.issue(t)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_at": exp,
		"technician": publicTech(t),
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	id, _ := uuid.Parse(c.TechnicianID)
	t, err := s.Store.GetTechnician(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicTech(t))
}

func (s *Server) issue(t *store.Technician) (string, any, error) {
	org := ""
	if t.OrgID != nil {
		org = t.OrgID.String()
	}
	return s.Auth.Issue(t.ID.String(), org, t.Email, t.Role)
}

func publicTech(t *store.Technician) map[string]any {
	m := map[string]any{
		"id":           t.ID,
		"email":        t.Email,
		"display_name": t.DisplayName,
		"role":         t.Role,
		"active":       t.Active,
	}
	if t.OrgID != nil {
		m["org_id"] = t.OrgID
	}
	return m
}

type createScanRequest struct {
	VIN          string            `json:"vin"`
	LinkType     string            `json:"link_type"`
	AdapterName  string            `json:"adapter_name"`
	Protocol     string            `json:"protocol"`
	Notes        string            `json:"notes"`
	DTCs         []obd.DTC         `json:"dtcs"`
	Observations *obd.Observations `json:"observations"`
}

func (s *Server) createScan(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	if c.OrgID == "" {
		writeErr(w, http.StatusForbidden, "super admins cannot create scans without an org context; use a shop technician account")
		return
	}
	var req createScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.LinkType == "" {
		req.LinkType = string(obd.LinkUSB)
	}
	rec, err := s.Store.CreateScan(r.Context(), store.CreateScanInput{
		TechnicianID: c.TechnicianID,
		OrgID:        c.OrgID,
		VIN:          req.VIN,
		LinkType:     req.LinkType,
		AdapterName:  req.AdapterName,
		Protocol:     req.Protocol,
		Notes:        req.Notes,
		DTCs:         req.DTCs,
		Observations: req.Observations,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := enrichment.EnqueueVINDecode(r.Context(), s.Store, rec.VehicleID, rec.VIN); err != nil {
		fmt.Printf("enqueue vin decode: %v\n", err)
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) getScan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid scan id")
		return
	}
	rec, err := s.Store.GetScan(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	c := claimsFrom(r)
	if c.Role != auth.RoleSuperAdmin && rec.OrgID.String() != c.OrgID {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) listVehicleScans(w http.ResponseWriter, r *http.Request) {
	vin := chi.URLParam(r, "vin")
	c := claimsFrom(r)
	var orgFilter *uuid.UUID
	if c.Role != auth.RoleSuperAdmin {
		id, err := uuid.Parse(c.OrgID)
		if err != nil {
			writeErr(w, http.StatusForbidden, "missing org")
			return
		}
		orgFilter = &id
	}
	recs, err := s.Store.ListScansByVIN(r.Context(), vin, orgFilter, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vin": vin, "scans": recs})
}

func (s *Server) explainCode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	vin := r.URL.Query().Get("vin")
	wantNarrative := r.URL.Query().Get("narrative") == "1" || r.URL.Query().Get("narrative") == "true"
	forceNarrative := r.URL.Query().Get("force_narrative") == "1" || r.URL.Query().Get("force_narrative") == "true"

	c := claimsFrom(r)
	var orgFilter *uuid.UUID
	if c.Role != auth.RoleSuperAdmin && c.OrgID != "" {
		id, _ := uuid.Parse(c.OrgID)
		orgFilter = &id
	}
	exp, err := s.Store.ExplainCode(r.Context(), code, vin, orgFilter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var dtcs []obd.DTC
	if vin != "" {
		if latest, lerr := s.Store.LatestDTCsForVIN(r.Context(), vin, orgFilter); lerr == nil {
			dtcs = latest
		}
	}
	co := make([]diagnosis.CoOccurStat, 0, len(exp.CoOccurrence))
	for _, row := range exp.CoOccurrence {
		co = append(co, diagnosis.CoOccurStat{
			Code: row.Code, WithCount: row.WithCount, FocusCount: row.FocusCount, Rate: row.Rate,
		})
	}
	findings := diagnosis.Analyze(diagnosis.Input{
		FocusCode:    code,
		DTCs:         dtcs,
		Observations: exp.Observations,
		CoOccur:      co,
	})

	out := map[string]any{
		"code":                exp.Code,
		"generic_description": exp.Generic,
		"article":             exp.Article,
		"network_history":     exp.History,
		"vehicle":             exp.Vehicle,
		"open_recalls":        exp.Recalls,
		"market_note":         exp.MarketNote,
		"observations":        exp.Observations,
		"co_occurrence":       exp.CoOccurrence,
		"findings":            findings,
	}

	// Always expose the lean LLM packet so clients can see/audit what would be sent.
	packet := explain.BuildPacket(explain.BuildInput{
		Code:     code,
		Findings: findings,
		Article:  exp.Article,
		Vehicle:  exp.Vehicle,
		CoOccur:  exp.CoOccurrence,
		History:  exp.History,
	})
	out["llm_packet"] = packet

	if wantNarrative && s.Narrator != nil {
		narr := s.Narrator.Narrate(r.Context(), packet, forceNarrative)
		out["narrative"] = narr
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.Store.ListOrganizations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
}

func (s *Server) createOrg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	org, err := s.Store.CreateOrganization(r.Context(), body.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (s *Server) listAllTechnicians(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTechnicians(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, publicTech(&list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"technicians": out})
}

func (s *Server) listOrgTechnicians(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	orgID, err := uuid.Parse(c.OrgID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing org")
		return
	}
	list, err := s.Store.ListTechnicians(r.Context(), &orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, publicTech(&list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"technicians": out})
}

func (s *Server) setActive(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c := claimsFrom(r)
	if c.Role == auth.RoleOrgAdmin {
		target, err := s.Store.GetTechnician(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if target.OrgID == nil || target.OrgID.String() != c.OrgID {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	if err := s.Store.SetTechnicianActive(r.Context(), id, body.Active); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": body.Active})
}

func (s *Server) patchSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch key {
	case "allow_self_register", "require_invite_code", "enrichment_enabled":
		if body.Value != "true" && body.Value != "false" {
			writeErr(w, http.StatusBadRequest, "value must be true or false")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown setting")
		return
	}
	if err := s.Store.SetSetting(r.Context(), key, body.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": body.Value})
}

func (s *Server) enrichmentStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Store.EnrichmentStats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) enrichmentSeed(w http.ResponseWriter, r *http.Request) {
	id, err := s.Store.EnqueueEnrichmentJob(r.Context(), "seed_catalog", map[string]any{"forced": true}, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id, "kind": "seed_catalog"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
